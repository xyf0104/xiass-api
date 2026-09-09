package repository

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

var ErrRefreshTransitionUnsafe = errors.New("refresh token transition safety gate rejected")

const (
	refreshTransitionMaxTokens          = 10000
	refreshTransitionBodyLimit          = 4096
	refreshTransitionMaxScanPages       = 4096
	refreshTransitionMaxMixedKeys       = 250000
	refreshTransitionLockID       int64 = 6364720318240
)

// LegacyRefreshTransitionOptions is an offline operator capability, not runtime
// configuration. Source supplies a fixed standalone address and credentials;
// custom dialers are discarded and credential providers are rejected.
// RecoverySecret is 32 newly generated random bytes,
// retained securely by the operator for crash/retry recovery. It must not be a
// legacy password. ExpectedRunID pins the live source process inspected by the
// operator, never a Sentinel address or a promoted/restored replica snapshot.
// Run ID and INFO cannot prove historical source freshness: an old RDB loaded
// into a new standalone process may pass them. Provenance is an external gate;
// Group enables explicitly inventoried mixed/replicated sources. Inventory
// provenance and maintenance admission remain external operator obligations.
// The operation disables ALL other Redis ACL users, across ALL Redis databases.
type LegacyRefreshTransitionOptions struct {
	Source                *redis.Client
	ExpectedRunID         string
	RecoverySecret        []byte
	Group                 *LegacyRefreshTransitionGroup
	PreserveRuntimeAccess *LegacyRefreshRuntimeAccess
}

// LegacyRefreshTransitionGroup pins the ORIGINAL primary's replication identity
// and every replica. Do not construct this inventory from a failover endpoint.
// ReplicaAddress is the address advertised in the primary's INFO replication;
// PrimaryAddress is the master_host:master_port configured on every replica.
// ACLUsers and Modules are exact inventories, not permission to trust any user
// or arbitrary module. Clients carry only direct transport/bootstrap credentials.
type LegacyRefreshTransitionGroup struct {
	PrimaryReplicationID string
	PrimaryAddress       string
	PrimaryACLUsers      []string
	PrimaryModules       []LegacyRefreshTransitionModule
	Replicas             []LegacyRefreshTransitionReplica
}

type LegacyRefreshTransitionModule struct {
	Name    string
	Version int64
}

type LegacyRefreshTransitionReplica struct {
	Client         *redis.Client
	ExpectedRunID  string
	ReplicaAddress string
	ACLUsers       []string
	Modules        []LegacyRefreshTransitionModule
}

type refreshTransitionNodeManifest struct {
	RunID, Address, ReplicaAddress string
	DB                             int
	ACLUsers                       []string
	Modules                        []LegacyRefreshTransitionModule
	RuntimeACLProof                []refreshRuntimeUserProof `json:",omitempty"`
}

type refreshTransitionGroupManifest struct {
	ReplicationID, PrimaryAddress string
	Nodes                         []refreshTransitionNodeManifest // primary first
	RuntimeFence                  *refreshRuntimeFencePolicy      `json:",omitempty"`
}

type refreshTransitionGroupNode struct {
	pin          refreshTransitionNodeManifest
	bootstrap    *redis.Client
	fenced       *redis.Client
	aclHash      string
	runtimeRules []string
}

type refreshTransitionGroupRuntime struct {
	manifest refreshTransitionGroupManifest
	nodes    []*refreshTransitionGroupNode
}

type LegacyRefreshTransitionResult struct {
	TransitionID   string
	Imported       int64
	Expired        int64
	ActivatedAt    time.Time
	SnapshotSHA256 string
}

func NewLegacyRefreshTransitionRecoverySecret() ([]byte, error) {
	secret := make([]byte, 32)
	_, err := rand.Read(secret)
	return secret, err
}

type refreshTransitionRecord struct {
	id, runID, replID, address, passwordHash, state, aclHash string
	db                                                       int
	result                                                   LegacyRefreshTransitionResult
	groupManifest                                            []byte
}

type refreshLegacyEntry struct {
	hash       string
	data       service.RefreshTokenData
	validUntil time.Time
}

func refreshTransitionDigest(value string) string {
	h := sha256.Sum256([]byte(value))
	return hex.EncodeToString(h[:])
}

func refreshTransitionReject(reason string) error {
	return fmt.Errorf("%w: %s", ErrRefreshTransitionUnsafe, reason)
}

func refreshTransitionSameJSON(a, b []byte) bool {
	if len(a) == 0 || len(b) == 0 {
		return len(a) == len(b)
	}
	var left, right any
	if json.Unmarshal(a, &left) != nil || json.Unmarshal(b, &right) != nil {
		return false
	}
	x, _ := json.Marshal(left)
	y, _ := json.Marshal(right)
	return string(x) == string(y)
}

func refreshTransitionBuildGroup(o LegacyRefreshTransitionOptions) (*refreshTransitionGroupRuntime, []byte, error) {
	if o.Group == nil {
		if o.PreserveRuntimeAccess != nil {
			return nil, nil, refreshTransitionReject("preserving runtime access requires an explicit group inventory")
		}
		return nil, nil, nil
	}
	p := o.Group
	if !persistentRefreshHex(p.PrimaryReplicationID, 20) || !refreshTransitionAddress(p.PrimaryAddress) || len(p.Replicas) > 8 {
		return nil, nil, refreshTransitionReject("invalid bounded primary/replica inventory")
	}
	g := &refreshTransitionGroupRuntime{manifest: refreshTransitionGroupManifest{ReplicationID: p.PrimaryReplicationID, PrimaryAddress: p.PrimaryAddress}}
	if access := o.PreserveRuntimeAccess; access != nil {
		policy, err := refreshRuntimePolicy(*access)
		if err != nil {
			return nil, nil, err
		}
		for _, hash := range policy.Access.ReservedPasswordSHA256 {
			if hash == refreshTransitionDigest(hex.EncodeToString(o.RecoverySecret)) {
				return nil, nil, refreshTransitionReject("runtime and recovery credentials must be independent")
			}
		}
		g.manifest.RuntimeFence = policy
	}
	clients := []*redis.Client{o.Source}
	pins := []refreshTransitionNodeManifest{{RunID: o.ExpectedRunID, ACLUsers: p.PrimaryACLUsers, Modules: p.PrimaryModules}}
	for _, replica := range p.Replicas {
		if !refreshTransitionAddress(replica.ReplicaAddress) {
			return nil, nil, refreshTransitionReject("invalid advertised replica address")
		}
		clients = append(clients, replica.Client)
		pins = append(pins, refreshTransitionNodeManifest{RunID: replica.ExpectedRunID, ReplicaAddress: replica.ReplicaAddress, ACLUsers: replica.ACLUsers, Modules: replica.Modules})
	}
	seenRuns, seenAddresses, seenPeers := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for i, pin := range pins {
		if clients[i] == nil || !persistentRefreshHex(pin.RunID, 20) || seenRuns[pin.RunID] {
			g.close()
			return nil, nil, refreshTransitionReject("missing or duplicate node identity")
		}
		opts := *clients[i].Options()
		if !refreshTransitionAddress(opts.Addr) || opts.Network != "tcp" || opts.DB < 0 || opts.OnConnect != nil || opts.CredentialsProvider != nil || opts.CredentialsProviderContext != nil || opts.StreamingCredentialsProvider != nil || seenAddresses[opts.Addr] || (i > 0 && seenPeers[pin.ReplicaAddress]) {
			g.close()
			return nil, nil, refreshTransitionReject("node must have a unique fixed direct TCP endpoint")
		}
		seenRuns[pin.RunID], seenAddresses[opts.Addr], seenPeers[pin.ReplicaAddress] = true, true, true
		pin.Address, pin.DB = opts.Addr, opts.DB
		pin.ACLUsers = append([]string{}, pin.ACLUsers...)
		sort.Strings(pin.ACLUsers)
		if len(pin.ACLUsers) == 0 || len(pin.ACLUsers) > 64 {
			g.close()
			return nil, nil, refreshTransitionReject("exact bounded ACL user inventory required")
		}
		for j, user := range pin.ACLUsers {
			if user == "" || len(user) > 128 || strings.ContainsAny(user, " \t\r\n") || strings.HasPrefix(user, "xiass-refresh-transition-") || (j > 0 && user == pin.ACLUsers[j-1]) {
				g.close()
				return nil, nil, refreshTransitionReject("invalid ACL user inventory")
			}
		}
		pin.Modules = append([]LegacyRefreshTransitionModule{}, pin.Modules...)
		sort.Slice(pin.Modules, func(i, j int) bool { return pin.Modules[i].Name < pin.Modules[j].Name })
		if len(pin.Modules) > 5 {
			g.close()
			return nil, nil, refreshTransitionReject("unsupported module inventory")
		}
		for j, module := range pin.Modules {
			switch module.Name {
			case "ReJSON", "search", "timeseries", "bf", "vectorset":
			default:
				g.close()
				return nil, nil, refreshTransitionReject("unknown module writer boundary")
			}
			if module.Version <= 0 || (j > 0 && pin.Modules[j-1].Name == module.Name) {
				g.close()
				return nil, nil, refreshTransitionReject("invalid module version inventory")
			}
		}
		opts.Dialer, opts.MaxRetries, opts.ContextTimeoutEnabled = nil, -1, true
		opts.OnConnect = refreshTransitionPinConnection(pin.RunID)
		g.nodes = append(g.nodes, &refreshTransitionGroupNode{pin: pin, bootstrap: redis.NewClient(&opts)})
		if g.manifest.RuntimeFence != nil {
			rules, err := g.manifest.RuntimeFence.Access.ACL.Rules(pin.DB)
			if err != nil {
				g.close()
				return nil, nil, refreshTransitionReject("unsafe runtime ACL parameters")
			}
			g.nodes[len(g.nodes)-1].runtimeRules = rules
		}
	}
	sort.Slice(g.nodes[1:], func(i, j int) bool { return g.nodes[i+1].pin.RunID < g.nodes[j+1].pin.RunID })
	for _, node := range g.nodes {
		g.manifest.Nodes = append(g.manifest.Nodes, node.pin)
	}
	manifest, err := json.Marshal(g.manifest)
	return g, manifest, err
}

func refreshTransitionAddress(address string) bool {
	host, port, err := net.SplitHostPort(address)
	n, parseErr := strconv.Atoi(port)
	return err == nil && parseErr == nil && host != "" && n > 0 && n <= 65535
}

// Pin EVERY new physical connection, including reconnects between topology
// checks and ACL commands. A fixed hostname alone must not authorize a fence
// against a different process after DNS/routing or a container restart changes.
func refreshTransitionPinConnection(runID string) func(context.Context, *redis.Conn) error {
	return func(ctx context.Context, conn *redis.Conn) error {
		info, err := conn.Info(ctx, "server").Result()
		if err != nil {
			return refreshTransitionReject("cannot verify connected Redis process")
		}
		for _, line := range strings.Split(info, "\n") {
			key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
			if ok && key == "run_id" && value == runID {
				return nil
			}
		}
		return refreshTransitionReject("connected Redis process does not match inventory")
	}
}

func (g *refreshTransitionGroupRuntime) close() {
	for _, node := range g.nodes {
		_ = node.bootstrap.Close()
		if node.fenced != nil {
			_ = node.fenced.Close()
		}
	}
}

func refreshTransitionInfo(ctx context.Context, client *redis.Client) (map[string]string, error) {
	value, err := client.Info(ctx, "server", "replication", "cluster", "persistence").Result()
	if err != nil {
		return nil, refreshTransitionReject("cannot inspect inventoried node")
	}
	info := map[string]string{}
	for _, line := range strings.Split(value, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if ok {
			info[key] = value
		}
	}
	return info, nil
}

// Initial admission requires the entire direct star topology online. Once the
// witness exists, fenced replication credentials may cause links to go down;
// an in-progress full sync may settle within the caller's existing migration
// deadline. Every sample must retain the pinned roles, identities and upstreams.
func (g *refreshTransitionGroupRuntime) topology(ctx context.Context, clients []*redis.Client, initial bool) error {
	peers := map[string]bool{}
	for _, node := range g.nodes[1:] {
		peers[node.pin.ReplicaAddress] = true
	}
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("%w: topology observation canceled: %w", ErrRefreshTransitionUnsafe, err)
		}
		var pending error
		for i, node := range g.nodes {
			info, err := refreshTransitionInfo(ctx, clients[i])
			if err != nil {
				return err
			}
			if info["run_id"] != node.pin.RunID || info["redis_mode"] != "standalone" || info["cluster_enabled"] != "0" || info["master_replid"] != g.manifest.ReplicationID {
				return refreshTransitionReject("inventoried process or replication identity changed")
			}
			acl, err := clients[i].ConfigGet(ctx, "aclfile").Result()
			if err != nil || acl["aclfile"] == "" {
				return refreshTransitionReject("every node requires a persistent ACL file")
			}
			if i == 0 {
				if info["role"] != "master" || info["master_replid2"] != strings.Repeat("0", 40) || info["loading"] != "0" {
					return refreshTransitionReject("source is not the pinned original primary")
				}
				n, err := strconv.Atoi(info["connected_slaves"])
				if err != nil || n < 0 || n > len(peers) || (initial && n != len(peers)) {
					return refreshTransitionReject("primary replica inventory is incomplete")
				}
				seen := map[string]bool{}
				for j := 0; j < n; j++ {
					fields := map[string]string{}
					for _, field := range strings.Split(info[fmt.Sprintf("slave%d", j)], ",") {
						k, v, _ := strings.Cut(field, "=")
						fields[k] = v
					}
					peer := net.JoinHostPort(fields["ip"], fields["port"])
					if !peers[peer] || seen[peer] {
						return refreshTransitionReject("unknown or unsynchronized replica on primary")
					}
					if fields["state"] != "online" {
						if initial || (fields["state"] != "wait_bgsave" && fields["state"] != "send_bulk") {
							return refreshTransitionReject("unknown or unsynchronized replica on primary")
						}
						pending = refreshTransitionReject("inventoried replica full sync has not settled")
					}
					seen[peer] = true
				}
			} else {
				if info["role"] != "slave" || net.JoinHostPort(info["master_host"], info["master_port"]) != g.manifest.PrimaryAddress || info["connected_slaves"] != "0" ||
					(info["master_link_status"] != "up" && info["master_link_status"] != "down") || (initial && info["master_link_status"] != "up") {
					return g.replicaTopologyError(node, info, initial)
				}
				if info["master_sync_in_progress"] == "1" && !initial && (info["loading"] == "0" || info["loading"] == "1") {
					pending = g.replicaTopologyError(node, info, initial)
				} else if info["master_sync_in_progress"] != "0" || info["loading"] != "0" {
					return g.replicaTopologyError(node, info, initial)
				}
			}
		}
		if pending == nil {
			return nil
		}
		// Inspect the whole group on every pass, including nodes after a syncing
		// replica. A concurrent promotion or new downstream must reject immediately.
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("%w: convergence deadline reached: %w", pending, ctx.Err())
		case <-timer.C:
		}
	}
}

func (g *refreshTransitionGroupRuntime) replicaTopologyError(node *refreshTransitionGroupNode, info map[string]string, initial bool) error {
	phase := "pre-fence"
	if node.aclHash != "" {
		phase = "post-fence"
	}
	state := func(key string, values ...string) string {
		for _, value := range values {
			if info[key] == value {
				return value
			}
		}
		return "unrecognized"
	}
	downstream := "unrecognized"
	if count, err := strconv.Atoi(info["connected_slaves"]); err == nil && count >= 0 {
		downstream = strconv.Itoa(count)
	}
	upstream := net.JoinHostPort(info["master_host"], info["master_port"])
	return refreshTransitionReject(fmt.Sprintf("replica role, direct upstream or synchronization does not match inventory (phase=%s initial=%t role=%s upstream_match=%t upstream_sha256=%s sync_in_progress=%s loading=%s link=%s connected_slaves=%s run_id_match=%t)",
		phase, initial, state("role", "master", "slave"), upstream == g.manifest.PrimaryAddress, refreshTransitionDigest(upstream)[:16], state("master_sync_in_progress", "0", "1"), state("loading", "0", "1"), state("master_link_status", "up", "down"), downstream, info["run_id"] == node.pin.RunID))
}

func refreshTransitionModules(ctx context.Context, client *redis.Client) ([]LegacyRefreshTransitionModule, error) {
	rows, err := client.Do(ctx, "MODULE", "LIST").Slice()
	if err != nil || len(rows) > 5 {
		return nil, refreshTransitionReject("cannot verify bounded module inventory")
	}
	modules := []LegacyRefreshTransitionModule{}
	for _, row := range rows {
		fields := map[any]any{}
		switch row := row.(type) {
		case []any:
			if len(row)%2 != 0 {
				return nil, refreshTransitionReject("unrecognized module description")
			}
			for i := 0; i < len(row); i += 2 {
				key, ok := row[i].(string)
				if !ok {
					return nil, refreshTransitionReject("unrecognized module field")
				}
				fields[key] = row[i+1]
			}
		case map[any]any:
			fields = row
		case map[string]any:
			for key, value := range row {
				fields[key] = value
			}
		default:
			return nil, refreshTransitionReject("unrecognized module description")
		}
		module := LegacyRefreshTransitionModule{}
		module.Name, _ = fields["name"].(string)
		module.Version, _ = fields["ver"].(int64)
		if module.Name == "" || module.Version <= 0 {
			return nil, refreshTransitionReject("missing module identity")
		}
		modules = append(modules, module)
	}
	sort.Slice(modules, func(i, j int) bool { return modules[i].Name < modules[j].Name })
	return modules, nil
}

func refreshTransitionNodeInventory(ctx context.Context, node *refreshTransitionGroupNode, client *redis.Client, username, passwordHash string) error {
	users, err := client.ACLUsers(ctx).Result()
	if err != nil || len(users) > 65 {
		return refreshTransitionReject("cannot verify ACL user inventory")
	}
	old := []string{}
	for _, user := range users {
		if user != username {
			old = append(old, user)
		}
	}
	sort.Strings(old)
	if strings.Join(old, "\n") != strings.Join(node.pin.ACLUsers, "\n") {
		return refreshTransitionReject("ACL user inventory changed")
	}
	lines, err := client.ACLList(ctx).Result()
	if err != nil {
		return refreshTransitionReject("cannot inspect inventoried ACLs")
	}
	for _, line := range lines {
		if !strings.HasPrefix(line, "user "+username+" ") && strings.Contains(line, "#"+passwordHash) {
			return refreshTransitionReject("recovery secret is an existing Redis password")
		}
	}
	modules, err := refreshTransitionModules(ctx, client)
	if err != nil {
		return err
	}
	x, _ := json.Marshal(modules)
	y, _ := json.Marshal(node.pin.Modules)
	if string(x) != string(y) {
		return refreshTransitionReject("module inventory changed")
	}
	return nil
}

// Scan names only: preserve every non-auth value/type/TTL and reject auth data in
// an unselected DB. DBSIZE, returned keys, cursor pages and DB count all have
// independent limits; MATCH alone would not bound a mostly unrelated keyspace.
func refreshTransitionMixedBoundary(ctx context.Context, source *redis.Client, runtimeRules ...[]string) error {
	var runtimeKeys *regexp.Regexp
	if len(runtimeRules) > 0 && runtimeRules[0] != nil {
		var err error
		runtimeKeys, err = refreshRuntimeKeyMatcher(runtimeRules[0])
		if err != nil {
			return err
		}
	}
	info, err := source.Info(ctx, "keyspace").Result()
	if err != nil {
		return refreshTransitionReject("cannot inventory mixed databases")
	}
	dbs := []int{source.Options().DB}
	for _, line := range strings.Split(info, "\n") {
		name, _, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok || !strings.HasPrefix(name, "db") {
			continue
		}
		db, err := strconv.Atoi(strings.TrimPrefix(name, "db"))
		if err != nil || db < 0 {
			return refreshTransitionReject("unrecognized mixed database")
		}
		if db != source.Options().DB {
			dbs = append(dbs, db)
		}
	}
	if len(dbs) > 16 {
		return refreshTransitionReject("mixed database count exceeds budget")
	}
	pages, keysRead := 0, 0
	for _, db := range dbs {
		opts := *source.Options()
		opts.DB, opts.Dialer = db, nil
		client := redis.NewClient(&opts)
		err := func() error {
			defer func() { _ = client.Close() }()
			size, err := client.DBSize(ctx).Result()
			if err != nil || size > refreshTransitionMaxMixedKeys {
				return refreshTransitionReject("mixed keyspace exceeds bounded adoption size")
			}
			var cursor uint64
			for {
				pages++
				if pages > refreshTransitionMaxScanPages {
					return refreshTransitionReject("mixed keyspace scan page budget exhausted")
				}
				keys, next, err := client.Scan(ctx, cursor, "*", 256).Result()
				if err != nil {
					return refreshTransitionReject("cannot scan bounded mixed keyspace")
				}
				keysRead += len(keys)
				if keysRead > refreshTransitionMaxMixedKeys {
					return refreshTransitionReject("mixed keyspace returned-key budget exhausted")
				}
				for _, key := range keys {
					if len(key) > 1024 {
						return refreshTransitionReject("mixed key name exceeds size budget")
					}
					if db != source.Options().DB && (strings.HasPrefix(key, refreshTokenKeyPrefix) || strings.HasPrefix(key, tokenFamilyPrefix) || strings.HasPrefix(key, userRefreshTokensPrefix)) {
						return refreshTransitionReject("auth namespace found outside selected session database")
					}
					if runtimeKeys != nil && !strings.HasPrefix(key, refreshTokenKeyPrefix) && !strings.HasPrefix(key, tokenFamilyPrefix) && !strings.HasPrefix(key, userRefreshTokensPrefix) && !runtimeKeys.MatchString(key) {
						return refreshTransitionReject(fmt.Sprintf("unknown runtime namespace in DB %d (key SHA256 %s); inspect the fixed runtime ACL inventory", db, refreshTransitionDigest(key)))
					}
				}
				cursor = next
				if cursor == 0 {
					return nil
				}
			}
		}()
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *PersistentRefreshTokenStore) refreshTransitionAdoptGroup(ctx context.Context, conn *sql.Conn, g *refreshTransitionGroupRuntime, manifest []byte, record *refreshTransitionRecord, password, passwordHash string) (*LegacyRefreshTransitionResult, error) {
	clients := make([]*redis.Client, len(g.nodes))
	for i, node := range g.nodes {
		clients[i] = node.bootstrap
	}
	if record == nil {
		if err := g.topology(ctx, clients, true); err != nil {
			return nil, err
		}
		// ALL nodes must pass durable-ACL preflight before ANY old user is disabled.
		for _, node := range g.nodes {
			if err := refreshTransitionNodeInventory(ctx, node, node.bootstrap, "", passwordHash); err != nil {
				return nil, err
			}
			if err := node.bootstrap.Do(ctx, "ACL", "SAVE").Err(); err != nil {
				return nil, refreshTransitionReject("original ACL persistence preflight failed on group node")
			}
		}
		if err := g.prepareRuntimeProof(ctx); err != nil {
			return nil, err
		}
		if g.manifest.RuntimeFence != nil {
			var err error
			manifest, err = json.Marshal(g.manifest)
			if err != nil {
				return nil, err
			}
		}
		if err := refreshTransitionMixedBoundary(ctx, clients[0], g.nodes[0].runtimeRules); err != nil {
			return nil, err
		}
		if _, err := refreshTransitionSnapshot(ctx, clients[0]); err != nil {
			return nil, err
		}
		if err := s.transaction(ctx, func(tx *sql.Tx) error {
			if err := refreshTransitionPristine(ctx, tx); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, `INSERT INTO refresh_token_legacy_transition
				(singleton, source_run_id, source_repl_id, source_address, source_db, fence_password_sha256, group_manifest)
				VALUES(TRUE,$1,$2,$3,$4,$5,$6::jsonb)`, g.nodes[0].pin.RunID, g.manifest.ReplicationID, g.nodes[0].pin.Address, g.nodes[0].pin.DB, passwordHash, string(manifest))
			return err
		}); err != nil {
			return nil, err
		}
		var err error
		record, _, err = refreshTransitionLoad(ctx, conn)
		if err != nil {
			return nil, err
		}
	}
	username := "xiass-refresh-transition-" + record.id
	for i, node := range g.nodes {
		opts := *node.bootstrap.Options()
		opts.Dialer, opts.Username, opts.Password = nil, username, password
		node.fenced = redis.NewClient(&opts)
		if err := node.fenced.Ping(ctx).Err(); err == nil {
			clients[i] = node.fenced
		} else if record.state != "preparing" {
			return nil, refreshTransitionReject("persisted group fence credential unavailable")
		}
	}
	if err := g.topology(ctx, clients, false); err != nil {
		return nil, err
	}
	for i, node := range g.nodes {
		if err := refreshTransitionNodeInventory(ctx, node, clients[i], username, passwordHash); err != nil {
			return nil, err
		}
		if err := clients[i].Do(ctx, "ACL", "SAVE").Err(); err != nil {
			return nil, refreshTransitionReject("group retry ACL persistence preflight failed")
		}
		if clients[i] != node.fenced {
			users, err := clients[i].ACLUsers(ctx).Result()
			if err != nil {
				return nil, refreshTransitionReject("cannot establish group recovery principal")
			}
			for _, user := range users {
				if user == username {
					return nil, refreshTransitionReject("existing group recovery principal has different credentials")
				}
			}
			if err := clients[i].ACLSetUser(ctx, username, "reset", "on", "#"+passwordHash, "~*", "&*", "+@all").Err(); err != nil {
				return nil, refreshTransitionReject("cannot create group recovery principal")
			}
			clients[i] = node.fenced
		}
		if err := clients[i].Do(ctx, "ACL", "SAVE").Err(); err != nil {
			return nil, refreshTransitionReject("cannot persist group recovery principal")
		}
		if err := conn.QueryRowContext(ctx, `SELECT acl_sha256 FROM refresh_token_transition_nodes WHERE transition_id=$1 AND run_id=$2`, record.id, node.pin.RunID).Scan(&node.aclHash); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}
	// Replicas first, original primary last. ACLs are local, not replicated.
	order := append(append([]*refreshTransitionGroupNode{}, g.nodes[1:]...), g.nodes[0])
	for _, node := range order {
		if node.aclHash == "" {
			if record.state != "preparing" {
				return nil, refreshTransitionReject("group fence proof missing")
			}
			if err := refreshTransitionNodeInventory(ctx, node, node.fenced, username, passwordHash); err != nil {
				return nil, err
			}
			// EXEC serializes all per-node permission resets with old clients. No
			// error path re-enables a user, including an ambiguous EXEC/SAVE/commit.
			var runtimeRules map[string][]string
			if node.runtimeRules != nil {
				var err error
				runtimeRules, err = node.runtimeFenceRules(ctx, false)
				if err != nil {
					return nil, err
				}
			}
			if _, err := node.fenced.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				for _, user := range node.pin.ACLUsers {
					if runtimeRules != nil {
						pipe.ACLSetUser(ctx, user, runtimeRules[user]...)
					} else {
						pipe.ACLSetUser(ctx, user, "reset", "off")
					}
				}
				return nil
			}); err != nil {
				return nil, refreshTransitionReject("group node fence failed; no permissions restored")
			}
			if err := node.fenced.Do(ctx, "ACL", "SAVE").Err(); err != nil {
				return nil, refreshTransitionReject("group node fence persistence failed; no permissions restored")
			}
			hash, err := node.verifyACL(ctx, username, passwordHash, "")
			if err != nil {
				return nil, err
			}
			if err := s.transaction(ctx, func(tx *sql.Tx) error {
				_, err := tx.ExecContext(ctx, `INSERT INTO refresh_token_transition_nodes(transition_id,run_id,acl_sha256) VALUES($1,$2,$3)`, record.id, node.pin.RunID, hash)
				return err
			}); err != nil {
				return nil, err
			}
			node.aclHash = hash
		}
	}
	verify := func() error {
		if err := g.topology(ctx, clients, false); err != nil {
			return err
		}
		for _, node := range g.nodes {
			if err := refreshTransitionNodeInventory(ctx, node, node.fenced, username, passwordHash); err != nil {
				return err
			}
			if _, err := node.verifyACL(ctx, username, passwordHash, node.aclHash); err != nil {
				return err
			}
		}
		return nil
	}
	if err := verify(); err != nil {
		return nil, err
	}
	if record.state == "preparing" {
		digest := sha256.New()
		for _, node := range g.nodes {
			fmt.Fprintf(digest, "%s\n%s\n", node.pin.RunID, node.aclHash)
		}
		hash := hex.EncodeToString(digest.Sum(nil))
		if err := s.transaction(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `UPDATE refresh_token_legacy_transition SET state='fenced',fenced_at=clock_timestamp(),acl_sha256=$1 WHERE singleton=TRUE AND state='preparing'`, hash)
			return err
		}); err != nil {
			return nil, err
		}
		record.state, record.aclHash = "fenced", hash
	}
	if err := refreshTransitionMixedBoundary(ctx, clients[0], g.nodes[0].runtimeRules); err != nil {
		return nil, err
	}
	return s.refreshTransitionActivate(ctx, record, clients[0], verify)
}

// AdoptLegacyRefreshTokens performs a destructive Redis ACL fence followed by a
// single atomic PostgreSQL import/activation. It never un-fences Redis on error.
// Retry with the same endpoint, run ID and recovery secret. Completed retries
// read only the permanent PG witness: they never replay token inserts.
func (s *PersistentRefreshTokenStore) AdoptLegacyRefreshTokens(ctx context.Context, options LegacyRefreshTransitionOptions) (*LegacyRefreshTransitionResult, error) {
	if s == nil || s.db == nil || options.Source == nil || len(options.RecoverySecret) != 32 || !persistentRefreshHex(options.ExpectedRunID, 20) {
		return nil, refreshTransitionReject("missing transition capability")
	}
	if s.db.Stats().MaxOpenConnections == 1 {
		return nil, refreshTransitionReject("transition requires a control connection plus a transaction connection")
	}
	opts := *options.Source.Options()
	if opts.Addr == "" || opts.Network != "tcp" || opts.OnConnect != nil ||
		opts.CredentialsProvider != nil || opts.CredentialsProviderContext != nil || opts.StreamingCredentialsProvider != nil || opts.DB < 0 {
		return nil, refreshTransitionReject("source must be a fixed direct TCP client")
	}
	// go-redis fills a default Dialer even for direct clients. Rebuild it instead
	// of retaining an injected Sentinel/failover/custom routing function.
	opts.Dialer = nil
	opts.MaxRetries, opts.ContextTimeoutEnabled = -1, true
	opts.OnConnect = refreshTransitionPinConnection(options.ExpectedRunID)
	bootstrapOpts := opts
	bootstrap := redis.NewClient(&bootstrapOpts)
	defer func() { _ = bootstrap.Close() }()
	group, manifest, err := refreshTransitionBuildGroup(options)
	if err != nil {
		return nil, err
	}
	if group != nil {
		defer group.close()
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	var locked bool
	if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, refreshTransitionLockID).Scan(&locked); err != nil {
		return nil, err
	}
	if !locked {
		return nil, refreshTransitionReject("another transition is running")
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := conn.ExecContext(cleanup, `SELECT pg_advisory_unlock($1)`, refreshTransitionLockID); err != nil {
			// Do not return a possibly still-locked session to the application pool.
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
	}()
	password := hex.EncodeToString(options.RecoverySecret)
	passwordHash := refreshTransitionDigest(password)
	record, backend, err := refreshTransitionLoad(ctx, conn)
	if err != nil {
		return nil, err
	}
	if record != nil {
		if record.runID != options.ExpectedRunID || record.address != opts.Addr || record.db != opts.DB || record.passwordHash != passwordHash {
			return nil, refreshTransitionReject("transition witness does not match this capability")
		}
		if group != nil && group.manifest.RuntimeFence != nil {
			var saved refreshTransitionGroupManifest
			if json.Unmarshal(record.groupManifest, &saved) != nil || len(saved.Nodes) != len(group.nodes) {
				return nil, refreshTransitionReject("runtime fence inventory does not match its witness")
			}
			for i, node := range group.nodes {
				node.pin.RuntimeACLProof = saved.Nodes[i].RuntimeACLProof
				group.manifest.Nodes[i] = node.pin
			}
			manifest, err = json.Marshal(group.manifest)
			if err != nil {
				return nil, err
			}
		}
		if !refreshTransitionSameJSON(manifest, record.groupManifest) {
			return nil, refreshTransitionReject("transition topology inventory does not match its witness")
		}
		if backend == "postgres" && record.state == "completed" {
			return &record.result, nil
		}
	}
	if backend != "redis" {
		return nil, refreshTransitionReject("authority is not an unfinished Redis transition")
	}
	if group != nil {
		return s.refreshTransitionAdoptGroup(ctx, conn, group, manifest, record, password, passwordHash)
	}
	if record == nil {
		info, err := refreshTransitionInspect(ctx, bootstrap, options.ExpectedRunID)
		if err != nil {
			return nil, err
		}
		acls, err := bootstrap.ACLList(ctx).Result()
		if err != nil {
			return nil, refreshTransitionReject("cannot inspect source ACL")
		}
		if len(acls) != 1 || !strings.HasPrefix(acls[0], "user default ") {
			return nil, refreshTransitionReject("unknown service boundary: only a dedicated default-user session Redis is supported")
		}
		for _, line := range acls {
			if strings.Contains(line, "#"+passwordHash) {
				return nil, refreshTransitionReject("recovery secret is an existing Redis password")
			}
		}
		if err := refreshTransitionServiceBoundary(ctx, bootstrap); err != nil {
			return nil, err
		}
		// Verify the original ACL file is actually writable before disabling even
		// one legacy principal. A configured pathname alone is not a durable fence.
		if err := bootstrap.Do(ctx, "ACL", "SAVE").Err(); err != nil {
			return nil, refreshTransitionReject("original ACL cannot be durably saved; no fence applied")
		}
		// Preflight avoids fencing obviously unimportable installations. It is
		// not the adopted snapshot; every record is read again after the fence.
		if _, err := refreshTransitionSnapshot(ctx, bootstrap); err != nil {
			return nil, err
		}
		err = s.transaction(ctx, func(tx *sql.Tx) error {
			if err := refreshTransitionPristine(ctx, tx); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, `INSERT INTO refresh_token_legacy_transition
				(singleton, source_run_id, source_repl_id, source_address, source_db, fence_password_sha256)
				VALUES (TRUE,$1,$2,$3,$4,$5)`, options.ExpectedRunID, info["master_replid"], opts.Addr, opts.DB, passwordHash)
			return err
		})
		if err != nil {
			return nil, err
		}
		record, _, err = refreshTransitionLoad(ctx, conn)
		if err != nil {
			return nil, err
		}
	}
	username := "xiass-refresh-transition-" + record.id
	opts.Username, opts.Password = username, password
	opts.MaxRetries, opts.ContextTimeoutEnabled = -1, true
	fenced := redis.NewClient(&opts)
	defer func() { _ = fenced.Close() }()
	if _, err := fenced.Ping(ctx).Result(); err != nil {
		if record.state != "preparing" {
			return nil, refreshTransitionReject("persisted fence credential is unavailable")
		}
		if _, err := refreshTransitionInspect(ctx, bootstrap, record.runID); err != nil {
			return nil, err
		}
		users, err := bootstrap.ACLUsers(ctx).Result()
		if err != nil {
			return nil, refreshTransitionReject("cannot establish new fence principal")
		}
		for _, user := range users {
			if user == username {
				return nil, refreshTransitionReject("existing fence principal has different credentials")
			}
		}
		if err := bootstrap.ACLSetUser(ctx, username, "reset", "on", "#"+passwordHash, "~*", "&*", "+@all").Err(); err != nil {
			return nil, refreshTransitionReject("cannot create exclusive fence principal")
		}
		if err := fenced.Ping(ctx).Err(); err != nil {
			return nil, refreshTransitionReject("cannot authenticate new fence principal")
		}
	}
	if record.state == "preparing" {
		if _, err := refreshTransitionInspect(ctx, fenced, record.runID); err != nil {
			return nil, err
		}
		if err := refreshTransitionServiceBoundary(ctx, fenced); err != nil {
			return nil, err
		}
		users, err := fenced.ACLUsers(ctx).Result()
		if err != nil {
			return nil, refreshTransitionReject("cannot enumerate legacy principals")
		}
		for _, user := range users {
			if user != username && user != "default" {
				return nil, refreshTransitionReject("unknown ACL principal appeared before fencing")
			}
		}
		if err := fenced.Do(ctx, "ACL", "SAVE").Err(); err != nil {
			return nil, refreshTransitionReject("ACL persistence preflight failed; no old principal disabled")
		}
		for _, user := range users {
			if user != username {
				if err := fenced.ACLSetUser(ctx, user, "reset", "off").Err(); err != nil {
					return nil, refreshTransitionReject("incomplete legacy principal fence")
				}
			}
		}
		if err := fenced.Do(ctx, "ACL", "SAVE").Err(); err != nil {
			return nil, refreshTransitionReject("ACL fence was not persisted; Redis remains fenced")
		}
		aclHash, err := refreshTransitionVerifyFence(ctx, fenced, record, username)
		if err != nil {
			return nil, err
		}
		err = s.transaction(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `UPDATE refresh_token_legacy_transition SET state='fenced', fenced_at=clock_timestamp(), acl_sha256=$1 WHERE singleton=TRUE AND state='preparing'`, aclHash)
			return err
		})
		if err != nil {
			return nil, err
		}
		record.state, record.aclHash = "fenced", aclHash
	}
	if _, err := refreshTransitionVerifyFence(ctx, fenced, record, username); err != nil {
		return nil, err
	}
	if err := refreshTransitionServiceBoundary(ctx, fenced); err != nil {
		return nil, err
	}
	return s.refreshTransitionActivate(ctx, record, fenced, func() error {
		_, err := refreshTransitionVerifyFence(ctx, fenced, record, username)
		return err
	})
}

// Both dedicated and inventoried-group transitions use this single import and
// activation transaction. No token body is ever read from a replica.
func (s *PersistentRefreshTokenStore) refreshTransitionActivate(ctx context.Context, record *refreshTransitionRecord, source *redis.Client, verify func() error) (*LegacyRefreshTransitionResult, error) {
	entries, err := refreshTransitionSnapshot(ctx, source)
	if err != nil {
		return nil, err
	}
	result := &LegacyRefreshTransitionResult{TransitionID: record.id}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		if err := refreshTransitionPristine(ctx, tx); err != nil {
			return err
		}
		// This check is inside the PG transaction. All old Redis users have
		// no session/admin commands (opt-in runtime retains only fixed non-session
		// grants). Only the new operator-held credential can
		// authenticate as the sole remaining privileged principal. The new
		// principal is held only by this explicit administrative operation.
		if err := verify(); err != nil {
			return err
		}
		var now time.Time
		if err := tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
			return err
		}
		digest := sha256.New()
		for _, e := range entries {
			if e.data.CreatedAt.After(now) {
				return refreshTransitionReject("future credential creation time")
			}
			if !e.validUntil.After(now) {
				result.Expired++
				continue
			}
			if err := refreshTransitionInsert(ctx, tx, e); err != nil {
				return err
			}
			metadata, _ := json.Marshal(e.data)
			fmt.Fprintf(digest, "%s\n%s\n%s\n", e.hash, metadata, e.validUntil.Format(time.RFC3339Nano))
			result.Imported++
		}
		result.SnapshotSHA256 = hex.EncodeToString(digest.Sum(nil))
		if err := verify(); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&result.ActivatedAt); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE refresh_token_legacy_transition SET state='completed', completed_at=$1,
			snapshot_sha256=$2, imported_count=$3, expired_count=$4 WHERE singleton=TRUE AND state='fenced'`,
			result.ActivatedAt, result.SnapshotSHA256, result.Imported, result.Expired)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE refresh_token_authority SET backend='postgres', activated_at=$1
			WHERE singleton=TRUE AND backend='redis'`, result.ActivatedAt)
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func refreshTransitionLoad(ctx context.Context, conn *sql.Conn) (*refreshTransitionRecord, string, error) {
	var backend string
	if err := conn.QueryRowContext(ctx, `SELECT backend FROM refresh_token_authority WHERE singleton=TRUE`).Scan(&backend); err != nil {
		return nil, "", err
	}
	r := &refreshTransitionRecord{}
	var completed sql.NullTime
	err := conn.QueryRowContext(ctx, `SELECT transition_id, source_run_id, source_repl_id, source_address, source_db,
		fence_password_sha256, state, COALESCE(acl_sha256,''), completed_at, imported_count, expired_count, COALESCE(snapshot_sha256,'')
		, group_manifest FROM refresh_token_legacy_transition WHERE singleton=TRUE`).Scan(&r.id, &r.runID, &r.replID, &r.address, &r.db, &r.passwordHash, &r.state, &r.aclHash,
		&completed, &r.result.Imported, &r.result.Expired, &r.result.SnapshotSHA256, &r.groupManifest)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, backend, nil
	}
	if err != nil {
		return nil, "", err
	}
	r.result.TransitionID, r.result.ActivatedAt = r.id, completed.Time
	return r, backend, nil
}

func refreshTransitionPristine(ctx context.Context, tx *sql.Tx) error {
	var backend string
	if err := tx.QueryRowContext(ctx, `SELECT backend FROM refresh_token_authority WHERE singleton=TRUE FOR UPDATE`).Scan(&backend); err != nil {
		return err
	}
	if backend != "redis" {
		return refreshTransitionReject("authority is not Redis")
	}
	var generation int64
	if err := tx.QueryRowContext(ctx, `SELECT generation FROM refresh_token_revocation_state WHERE singleton=TRUE FOR UPDATE`).Scan(&generation); err != nil {
		return err
	}
	var used bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM refresh_tokens) OR EXISTS(SELECT 1 FROM refresh_token_families)
		OR EXISTS(SELECT 1 FROM refresh_token_users) OR EXISTS(SELECT 1 FROM refresh_token_issuances)`).Scan(&used); err != nil {
		return err
	}
	if generation != 0 || used {
		return refreshTransitionReject("PG refresh authority already has issuance or revocation history")
	}
	return nil
}

func refreshTransitionInsert(ctx context.Context, tx *sql.Tx, e refreshLegacyEntry) error {
	d := e.data
	if _, err := persistentRefreshUserLock(ctx, tx, d.UserID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO refresh_token_families(family_id,user_id,family_expires_at) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, d.FamilyID, d.UserID, d.FamilyExpiresAt); err != nil {
		return err
	}
	var owner int64
	var deadline time.Time
	if err := tx.QueryRowContext(ctx, `SELECT user_id,family_expires_at FROM refresh_token_families WHERE family_id=$1 FOR UPDATE`, d.FamilyID).Scan(&owner, &deadline); err != nil {
		return err
	}
	if owner != d.UserID || !deadline.Equal(d.FamilyExpiresAt) {
		return refreshTransitionReject("inconsistent family ownership or deadline")
	}
	var ticket string
	if err := tx.QueryRowContext(ctx, `INSERT INTO refresh_token_issuances(user_id,user_generation,global_generation,used_at) VALUES($1,0,0,clock_timestamp()) RETURNING ticket_id`, d.UserID).Scan(&ticket); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO refresh_tokens(token_hash,user_id,family_id,token_version,binding_hash,created_at,expires_at,valid_until,issuance_id,user_generation,global_generation)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,0,0)`, e.hash, d.UserID, d.FamilyID, d.TokenVersion, d.BindingHash, d.CreatedAt, d.ExpiresAt, e.validUntil, ticket)
	return err
}

func refreshTransitionInspect(ctx context.Context, client *redis.Client, runID string) (map[string]string, error) {
	text, err := client.Info(ctx, "server", "replication", "cluster", "persistence").Result()
	if err != nil {
		return nil, refreshTransitionReject("cannot inspect source process")
	}
	info := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if ok {
			info[key] = value
		}
	}
	if info["run_id"] != runID || info["redis_mode"] != "standalone" || info["role"] != "master" || info["cluster_enabled"] != "0" ||
		info["loading"] != "0" || info["connected_slaves"] != "0" || info["master_repl_offset"] != "0" || info["repl_backlog_active"] != "0" ||
		info["master_replid2"] != strings.Repeat("0", 40) || !persistentRefreshHex(info["master_replid"], 20) {
		return nil, refreshTransitionReject("source is not the pinned standalone primary with no replication history")
	}
	configuration, err := client.ConfigGet(ctx, "aclfile").Result()
	if err != nil || configuration["aclfile"] == "" {
		return nil, refreshTransitionReject("source needs a persistent ACL file before transition")
	}
	return info, nil
}

func refreshTransitionServiceBoundary(ctx context.Context, client *redis.Client) error {
	info, err := client.Info(ctx, "keyspace").Result()
	if err != nil {
		return refreshTransitionReject("cannot inspect Redis database boundary")
	}
	selected := fmt.Sprintf("db%d", client.Options().DB)
	for _, line := range strings.Split(info, "\n") {
		name, _, ok := strings.Cut(strings.TrimSpace(line), ":")
		if ok && strings.HasPrefix(name, "db") && name != selected {
			return refreshTransitionReject("another Redis database is nonempty")
		}
	}
	modules, err := client.Do(ctx, "MODULE", "LIST").Slice()
	if err != nil || len(modules) != 0 {
		return refreshTransitionReject("unknown Redis module service boundary")
	}
	var cursor uint64
	seen := map[string]bool{}
	for page := 0; ; page++ {
		if page >= refreshTransitionMaxScanPages {
			return refreshTransitionReject("session keyspace scan page budget exhausted")
		}
		keys, next, err := client.Scan(ctx, cursor, "*", 256).Result()
		if err != nil {
			return refreshTransitionReject("cannot inspect session keyspace")
		}
		for _, key := range keys {
			valid := false
			switch {
			case strings.HasPrefix(key, refreshTokenKeyPrefix):
				valid = persistentRefreshHex(strings.TrimPrefix(key, refreshTokenKeyPrefix), 32)
			case strings.HasPrefix(key, tokenFamilyPrefix):
				valid = persistentRefreshHex(strings.TrimPrefix(key, tokenFamilyPrefix), 16)
			case strings.HasPrefix(key, userRefreshTokensPrefix):
				id, err := strconv.ParseInt(strings.TrimPrefix(key, userRefreshTokensPrefix), 10, 64)
				valid = err == nil && id > 0
			}
			if !valid {
				return refreshTransitionReject("unknown non-session Redis key namespace")
			}
			seen[key] = true
			if len(seen) > 3*refreshTransitionMaxTokens {
				return refreshTransitionReject("source keyspace exceeds bounded adoption size")
			}
		}
		cursor = next
		if cursor == 0 {
			return nil
		}
	}
}

func refreshTransitionVerifyFence(ctx context.Context, client *redis.Client, r *refreshTransitionRecord, username string) (string, error) {
	info, err := refreshTransitionInspect(ctx, client, r.runID)
	if err != nil {
		return "", err
	}
	if info["master_replid"] != r.replID {
		return "", refreshTransitionReject("source replication identity changed")
	}
	return refreshTransitionVerifyACL(ctx, client, username, r.passwordHash, r.aclHash)
}

func refreshTransitionVerifyACL(ctx context.Context, client *redis.Client, username, passwordHash, expectedHash string) (string, error) {
	lines, err := client.ACLList(ctx).Result()
	if err != nil {
		return "", refreshTransitionReject("cannot verify ACL fence")
	}
	found := false
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[0] != "user" {
			return "", refreshTransitionReject("unrecognized ACL state")
		}
		if fields[1] == username {
			found = true
			passwords, enabled := 0, false
			for _, field := range fields[2:] {
				if field == "on" {
					enabled = true
				}
				if strings.HasPrefix(field, "#") {
					passwords++
					if field != "#"+passwordHash {
						return "", refreshTransitionReject("exclusive principal has an unexpected password")
					}
				}
			}
			if passwords != 1 || !enabled || strings.Contains(line, " nopass") || strings.Contains(line, "(") {
				return "", refreshTransitionReject("invalid exclusive principal")
			}
			continue
		}
		// SETUSER reset off removes passwords and selectors as well as commands.
		if !strings.Contains(line, " off ") || !strings.Contains(line, " -@all") || strings.Contains(line, " +") || strings.ContainsAny(line, "(#") {
			return "", refreshTransitionReject("legacy Redis principal is not completely fenced")
		}
	}
	if !found {
		return "", refreshTransitionReject("exclusive principal missing")
	}
	sort.Strings(lines)
	hash := refreshTransitionDigest(strings.Join(lines, "\n"))
	if expectedHash != "" && expectedHash != hash {
		return "", refreshTransitionReject("persisted fence changed")
	}
	return hash, nil
}

func refreshTransitionSnapshot(ctx context.Context, client *redis.Client) ([]refreshLegacyEntry, error) {
	var cursor uint64
	entries := map[string]refreshLegacyEntry{}
	for page := 0; ; page++ {
		if page >= refreshTransitionMaxScanPages {
			return nil, refreshTransitionReject("credential scan page budget exhausted")
		}
		keys, next, err := client.Scan(ctx, cursor, refreshTokenKeyPrefix+"*", 256).Result()
		if err != nil {
			return nil, refreshTransitionReject("cannot enumerate source credentials")
		}
		for _, key := range keys {
			hash := strings.TrimPrefix(key, refreshTokenKeyPrefix)
			if !persistentRefreshHex(hash, 32) {
				return nil, refreshTransitionReject("source contains a non-hash token key")
			}
			if _, ok := entries[hash]; ok {
				continue
			}
			// GET is bounded before the body crosses the Redis protocol boundary.
			body, err := client.Eval(ctx, `local n=redis.call('STRLEN',KEYS[1]); if n>4096 then return redis.error_reply('oversized metadata') end; return redis.call('GET',KEYS[1])`, []string{key}).Text()
			if err == redis.Nil {
				continue
			}
			if err != nil {
				return nil, refreshTransitionReject("cannot read bounded source metadata")
			}
			d, err := refreshTransitionDecode(body)
			if err != nil {
				return nil, err
			}
			values, err := client.Eval(ctx, `local v=redis.call('GET',KEYS[1]); if not v then return nil end
				if v~=ARGV[1] then return redis.error_reply('metadata changed') end
				return {redis.call('PEXPIRETIME',KEYS[1]),redis.call('SISMEMBER',KEYS[2],ARGV[2]),redis.call('SISMEMBER',KEYS[3],ARGV[2])}`,
				[]string{key, userRefreshTokensKey(d.UserID), tokenFamilyKey(d.FamilyID)}, body, hash).Slice()
			if err == redis.Nil {
				continue
			}
			if err != nil || len(values) != 3 {
				return nil, refreshTransitionReject("source metadata or index validation failed")
			}
			expiry, ok := values[0].(int64)
			if !ok || expiry <= 0 || values[1] != int64(1) || values[2] != int64(1) {
				return nil, refreshTransitionReject("missing source expiry or atomic scope membership")
			}
			until := time.UnixMilli(expiry).UTC()
			for _, bound := range []time.Time{d.ExpiresAt, d.FamilyExpiresAt, d.CreatedAt.Add(7 * 24 * time.Hour)} {
				if bound.Before(until) {
					until = bound
				}
			}
			entries[hash] = refreshLegacyEntry{hash: hash, data: *d, validUntil: until}
			if len(entries) > refreshTransitionMaxTokens {
				return nil, refreshTransitionReject("source exceeds bounded adoption size")
			}
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	result := make([]refreshLegacyEntry, 0, len(entries))
	for _, e := range entries {
		result = append(result, e)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].hash < result[j].hash })
	return result, nil
}

func refreshTransitionDecode(body string) (*service.RefreshTokenData, error) {
	if len(body) == 0 || len(body) > refreshTransitionBodyLimit {
		return nil, refreshTransitionReject("invalid source metadata size")
	}
	decoder := json.NewDecoder(strings.NewReader(body))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, refreshTransitionReject("metadata is not a JSON object")
	}
	fields := map[string]json.RawMessage{}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return nil, refreshTransitionReject("malformed metadata")
		}
		name, ok := key.(string)
		if !ok {
			return nil, refreshTransitionReject("malformed metadata key")
		}
		if _, exists := fields[name]; exists {
			return nil, refreshTransitionReject("duplicate metadata key")
		}
		switch name {
		case "user_id", "token_version", "family_id", "binding_hash", "created_at", "expires_at", "family_expires_at":
		default:
			return nil, refreshTransitionReject("unknown source metadata field")
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return nil, refreshTransitionReject("malformed metadata value")
		}
		if string(raw) == "null" {
			return nil, refreshTransitionReject("null source metadata")
		}
		fields[name] = raw
	}
	if _, err := decoder.Token(); err != nil {
		return nil, refreshTransitionReject("truncated metadata")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, refreshTransitionReject("trailing metadata")
	}
	for _, key := range []string{"user_id", "token_version", "family_id", "created_at", "expires_at"} {
		if _, ok := fields[key]; !ok {
			return nil, refreshTransitionReject("missing source metadata")
		}
	}
	var d service.RefreshTokenData
	if err := json.Unmarshal([]byte(body), &d); err != nil {
		return nil, refreshTransitionReject("invalid typed metadata")
	}
	if d.UserID <= 0 || !persistentRefreshHex(d.FamilyID, 16) || !persistentRefreshBindingValid(d.BindingHash) || d.CreatedAt.IsZero() || !d.ExpiresAt.After(d.CreatedAt) {
		return nil, refreshTransitionReject("invalid source credential bounds")
	}
	d.CreatedAt = d.CreatedAt.UTC().Truncate(time.Microsecond)
	d.ExpiresAt = d.ExpiresAt.UTC().Truncate(time.Microsecond)
	if d.FamilyExpiresAt.IsZero() {
		// Matches the legacy deadline cap without using migration-time "now".
		d.FamilyExpiresAt = d.CreatedAt.Add(7 * 24 * time.Hour)
		if d.ExpiresAt.Before(d.FamilyExpiresAt) {
			d.FamilyExpiresAt = d.ExpiresAt
		}
	}
	d.FamilyExpiresAt = d.FamilyExpiresAt.UTC().Truncate(time.Microsecond)
	if !d.FamilyExpiresAt.After(d.CreatedAt) || d.FamilyExpiresAt.After(d.CreatedAt.Add(7*24*time.Hour)) {
		return nil, refreshTransitionReject("invalid family deadline")
	}
	return &d, nil
}
