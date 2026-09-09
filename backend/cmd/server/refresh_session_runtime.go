package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/redis/go-redis/v9"
)

type refreshRuntimeManifest struct {
	AppPasswordFile       string  `json:"app_password_file"`
	ReplicaPasswordFile   string  `json:"replica_password_file,omitempty"`
	BackupPasswordFile    string  `json:"backup_password_file,omitempty"`
	BackupCredentialsFile string  `json:"backup_credentials_file,omitempty"`
	EnvironmentFile       string  `json:"environment_file"`
	DashboardPrefix       *string `json:"dashboard_prefix,omitempty"`
	QueueReadyKey         string  `json:"queue_ready_key,omitempty"`
	QueueDelayedKey       string  `json:"queue_delayed_key,omitempty"`
	QueueActiveKey        string  `json:"queue_active_key,omitempty"`
	InflightPrefix        string  `json:"inflight_prefix,omitempty"`
	LockPrefix            string  `json:"lock_prefix,omitempty"`
	AlertLockKey          string  `json:"alert_lock_key,omitempty"`
}

type refreshRuntimePlan struct {
	appPassword, replicaPassword string
	backupPassword, backupFile   string
	rules                        []string
	file                         string
	db                           *sql.DB
	disableMigrationReadiness    bool
}

func (r *refreshRuntimeManifest) aclParameters() repository.RefreshSessionRuntimeACLParameters {
	return repository.RefreshSessionRuntimeACLParameters{
		DashboardPrefix: r.DashboardPrefix, QueueReadyKey: r.QueueReadyKey, QueueDelayedKey: r.QueueDelayedKey,
		QueueActiveKey: r.QueueActiveKey, InflightPrefix: r.InflightPrefix, LockPrefix: r.LockPrefix, AlertLockKey: r.AlertLockKey,
	}
}

func (m *refreshMigrationManifest) runtimeAccess() (*repository.LegacyRefreshRuntimeAccess, error) {
	if m.SessionFence == nil {
		return nil, nil
	}
	if err := m.validateSessionFence(); err != nil {
		return nil, err
	}
	r := m.Runtime
	access := &repository.LegacyRefreshRuntimeAccess{AuthEndpointsBlockedAndDrained: true, ACL: r.aclParameters()}
	if _, err := access.ACL.Rules(0); err != nil {
		return nil, err
	}
	paths := []string{r.AppPasswordFile}
	if len(m.Replicas) > 0 {
		paths = append(paths, r.ReplicaPasswordFile)
	}
	if r.BackupPasswordFile != "" {
		paths = append(paths, r.BackupPasswordFile)
	}
	for _, path := range paths {
		password, err := refreshRuntimePassword(path)
		if err != nil {
			return nil, err
		}
		access.ReservedPasswordSHA256 = append(access.ReservedPasswordSHA256, refreshRuntimeHash(password))
	}
	return access, nil
}

func refreshRuntimePassword(path string) (string, error) {
	data, err := readRefreshMigrationFile(path, 128)
	if err != nil {
		return "", err
	}
	secret, err := hex.DecodeString(strings.TrimSpace(string(data)))
	if err != nil || len(secret) != 32 {
		return "", errors.New("runtime password files require 32 random bytes encoded as hex")
	}
	return hex.EncodeToString(secret), nil
}

func prepareRefreshRuntime(ctx context.Context, db *sql.DB, m *refreshMigrationManifest, recovery []byte) (*refreshRuntimePlan, error) {
	if m.Runtime == nil {
		return nil, nil
	}
	r := m.Runtime
	if (r.BackupPasswordFile == "") != (r.BackupCredentialsFile == "") {
		return nil, errors.New("backup password and credentials files must be provided together")
	}
	if !filepath.IsAbs(r.EnvironmentFile) || filepath.Base(r.EnvironmentFile) == ".env" {
		return nil, errors.New("runtime environment must use a new absolute file path, not the installation .env")
	}
	app, err := refreshRuntimePassword(r.AppPasswordFile)
	if err != nil {
		return nil, err
	}
	plan := &refreshRuntimePlan{appPassword: app, file: r.EnvironmentFile, db: db, disableMigrationReadiness: m.Version == 2}
	if len(m.Replicas) > 0 {
		plan.replicaPassword, err = refreshRuntimePassword(r.ReplicaPasswordFile)
		if err != nil {
			return nil, err
		}
	}
	if app == hex.EncodeToString(recovery) || plan.replicaPassword == app || plan.replicaPassword == hex.EncodeToString(recovery) {
		return nil, errors.New("application, replication and recovery passwords must be independent")
	}
	if r.BackupCredentialsFile != "" {
		if !filepath.IsAbs(r.BackupCredentialsFile) || filepath.Base(r.BackupCredentialsFile) == ".env" {
			return nil, errors.New("backup credentials must use a new absolute private file path, not the installation .env")
		}
		for _, input := range []string{r.EnvironmentFile, r.AppPasswordFile, r.ReplicaPasswordFile, r.BackupPasswordFile, m.RecoverySecretFile} {
			if input != "" && filepath.Clean(input) == filepath.Clean(r.BackupCredentialsFile) {
				return nil, errors.New("backup credentials output must be separate from runtime environment and password files")
			}
		}
		plan.backupPassword, err = refreshRuntimePassword(r.BackupPasswordFile)
		if err != nil {
			return nil, err
		}
		replica := plan.replicaPassword
		if replica == "" && r.ReplicaPasswordFile != "" {
			replica, err = refreshRuntimePassword(r.ReplicaPasswordFile)
			if err != nil {
				return nil, err
			}
		}
		if plan.backupPassword == app || plan.backupPassword == replica || plan.backupPassword == hex.EncodeToString(recovery) {
			return nil, errors.New("backup, application, replication and recovery passwords must be independent")
		}
		plan.backupFile = r.BackupCredentialsFile
	}
	plan.rules, err = r.aclParameters().Rules(0)
	if err != nil {
		return nil, err
	}
	// Redis needs a real config file to persist replacement replication credentials.
	// Check it before the irreversible adoption fence, including partial retries.
	var id string
	err = db.QueryRowContext(ctx, `SELECT transition_id::text FROM refresh_token_legacy_transition WHERE singleton=TRUE`).Scan(&id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, errors.New("cannot inspect runtime transition state")
	}
	outputs := map[string][]byte{plan.file: plan.environment(id)}
	if plan.backupFile != "" {
		outputs[plan.backupFile] = plan.backupCredentials(id)
	}
	for path, expected := range outputs {
		if _, err := os.Lstat(path); err == nil {
			data, readErr := readRefreshMigrationFile(path, 4096)
			if id == "" || readErr != nil || !bytes.Equal(data, expected) {
				return nil, errors.New("runtime output already exists with different contents; refusing to replace it")
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, errors.New("cannot inspect runtime output")
		}
	}
	nodes := m.Replicas
	if len(nodes) > 0 {
		// The current primary also needs credentials if it later becomes a replica.
		nodes = append([]refreshMigrationNode{m.Primary}, nodes...)
	}
	for _, node := range nodes {
		client, err := refreshRuntimeClient(node, "", "")
		if err != nil {
			return nil, err
		}
		info, err := client.Info(ctx, "server").Result()
		if err == nil && refreshRuntimeInfo(info, "config_file") != "" {
			err = client.Do(ctx, "CONFIG", "REWRITE").Err()
		}
		_ = client.Close()
		if err != nil && id != "" {
			client, err = refreshRuntimeClient(node, "xiass-refresh-transition-"+id, hex.EncodeToString(recovery))
			if err != nil {
				return nil, err
			}
			info, err = client.Info(ctx, "server").Result()
			if err == nil && refreshRuntimeInfo(info, "config_file") != "" {
				err = client.Do(ctx, "CONFIG", "REWRITE").Err()
			}
			_ = client.Close()
		}
		if err != nil || refreshRuntimeInfo(info, "config_file") == "" {
			return nil, errors.New("every Redis node in a replicated runtime needs its pinned process and a writable persistent redis.conf before adoption")
		}
	}
	return plan, nil
}

func refreshRuntimeInfo(info, field string) string {
	for _, line := range strings.Split(info, "\n") {
		if key, value, ok := strings.Cut(strings.TrimSpace(line), ":"); ok && key == field {
			return value
		}
	}
	return ""
}

func refreshRuntimeClient(node refreshMigrationNode, username, password string) (*redis.Client, error) {
	opts, err := redis.ParseURL(node.URL)
	if err != nil {
		return nil, errors.New("invalid inventoried Redis endpoint")
	}
	if username != "" {
		opts.Username, opts.Password = username, password
	}
	opts.MaxRetries, opts.ContextTimeoutEnabled = -1, true
	opts.DialTimeout, opts.ReadTimeout, opts.WriteTimeout = 5*time.Second, 5*time.Second, 5*time.Second
	opts.OnConnect = func(ctx context.Context, conn *redis.Conn) error {
		info, err := conn.Info(ctx, "server").Result()
		if err != nil || refreshRuntimeInfo(info, "run_id") != node.RunID {
			return errors.New("runtime Redis process does not match the pinned inventory")
		}
		return nil
	}
	return redis.NewClient(opts), nil
}

func (p *refreshRuntimePlan) restore(ctx context.Context, m *refreshMigrationManifest, recovery []byte, id string) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	access, err := m.runtimeAccess()
	if err != nil {
		return err
	}
	appUser, replUser := "xiass-app-"+id, "xiass-replica-"+id
	backupUser := "xiass-backup-" + id
	var nodes []*redis.Client
	defer func() {
		for _, node := range nodes {
			_ = node.Close()
		}
	}()
	for _, entry := range append([]refreshMigrationNode{m.Primary}, m.Replicas...) {
		client, err := refreshRuntimeClient(entry, "xiass-refresh-transition-"+id, hex.EncodeToString(recovery))
		if err != nil {
			return err
		}
		nodes = append(nodes, client)
		users, err := client.ACLUsers(ctx).Result()
		if err != nil {
			return errors.New("cannot verify runtime ACL inventory")
		}
		allowed := map[string]bool{appUser: true, "xiass-refresh-transition-" + id: true}
		if len(m.Replicas) > 0 {
			allowed[replUser] = true
		}
		if p.backupFile != "" {
			allowed[backupUser] = true
		}
		for _, user := range entry.ACLUsers {
			allowed[user] = true
		}
		for _, user := range users {
			if !allowed[user] {
				return errors.New("unexpected Redis principal appeared after adoption")
			}
		}
		if access != nil {
			if p.db == nil {
				return errors.New("runtime fence database witness missing")
			}
			if err := repository.VerifyLegacyRefreshRuntimeFence(ctx, p.db, client, id, entry.RunID, *access); err != nil {
				return err
			}
		}
		for _, oldUser := range entry.ACLUsers {
			if access != nil {
				continue
			}
			command := redis.NewMapStringInterfaceCmd(ctx, "ACL", "GETUSER", oldUser)
			_ = client.Process(ctx, command)
			state, err := command.Result()
			if err != nil || !refreshRuntimeOldUserFenced(state) {
				return errors.New("legacy Redis permissions changed; no runtime grant applied")
			}
		}
		rules := append([]string{"reset", "on", "#" + refreshRuntimeHash(p.appPassword), "resetkeys", "resetchannels", "-@all"}, p.rules...)
		if client.Options().DB != 0 {
			rules = append(rules, "+select")
		}
		if err := client.ACLSetUser(ctx, appUser, rules...).Err(); err != nil {
			return errors.New("cannot install restricted application ACL")
		}
		if len(m.Replicas) > 0 {
			if err := client.ACLSetUser(ctx, replUser, "reset", "on", "#"+refreshRuntimeHash(p.replicaPassword), "-@all", "+ping", "+replconf", "+psync").Err(); err != nil {
				return errors.New("cannot install replication ACL")
			}
		}
		if p.backupFile != "" {
			// redis-cli --rdb uses SYNC and REPLCONF, not key reads or PSYNC.
			if err := client.ACLSetUser(ctx, backupUser, "reset", "on", "#"+refreshRuntimeHash(p.backupPassword), "resetkeys", "resetchannels", "-@all", "+auth", "+hello", "+ping", "+info", "+role", "+sync", "+replconf", "+acl|list", "+module|list").Err(); err != nil {
				return errors.New("cannot install read-only backup ACL")
			}
		}
		if err := client.Do(ctx, "ACL", "SAVE").Err(); err != nil {
			return errors.New("runtime ACL could not be persisted")
		}
		// A dry run never executes the prohibited destructive command.
		if client.Do(ctx, "ACL", "DRYRUN", appUser, "PING").Val() != "OK" {
			return errors.New("application ACL positive control failed")
		}
		for _, args := range [][]any{{"GET", "refresh_token:probe"}, {"SET", "user_refresh_tokens:probe", "x"}, {"GET", "token_family:probe"}, {"FLUSHDB"}, {"ACL", "LIST"}} {
			command := append([]any{"ACL", "DRYRUN", appUser}, args...)
			// DRYRUN reports a denial as a bulk string, not a Redis error reply.
			result, err := client.Do(ctx, command...).Text()
			if err != nil || !strings.HasPrefix(result, "User "+appUser+" has no permissions to ") {
				return errors.New("application ACL isolation check failed")
			}
		}
	}
	if len(m.Replicas) > 0 {
		// Persist on every potential replica before waiting for the current replicas.
		for _, client := range nodes {
			if err := client.Do(ctx, "CONFIG", "SET", "masteruser", replUser, "masterauth", p.replicaPassword).Err(); err != nil {
				return errors.New("cannot configure replacement replication credentials")
			}
			if err := client.Do(ctx, "CONFIG", "REWRITE").Err(); err != nil {
				return errors.New("replication credentials were not persisted")
			}
		}
	}
	for i := range m.Replicas {
		client := nodes[i+1]
		for {
			info, err := client.Info(ctx, "replication").Result()
			if err == nil && refreshRuntimeInfo(info, "master_link_status") == "up" && refreshRuntimeInfo(info, "master_sync_in_progress") == "0" {
				break
			}
			select {
			case <-ctx.Done():
				return errors.New("replica did not reconnect with replacement credentials")
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	if p.backupFile != "" {
		if err := writeRefreshRuntimeEnvironment(p.backupFile, p.backupCredentials(id)); err != nil {
			return err
		}
	}
	return writeRefreshRuntimeEnvironment(p.file, p.environment(id))
}

func (p *refreshRuntimePlan) backupCredentials(id string) []byte {
	data, _ := json.Marshal(struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}{Username: "xiass-backup-" + id, Password: p.backupPassword})
	return append(data, '\n')
}

func (p *refreshRuntimePlan) environment(id string) []byte {
	data := "JWT_REFRESH_TOKEN_STORE=postgres\n"
	if p.disableMigrationReadiness {
		data += "JWT_REFRESH_TOKEN_MIGRATION_READINESS=false\n"
	}
	return []byte(data + "REDIS_USERNAME=xiass-app-" + id + "\nREDIS_PASSWORD=" + p.appPassword + "\n")
}

func refreshRuntimeHash(password string) string {
	digest := sha256.Sum256([]byte(password))
	return hex.EncodeToString(digest[:])
}

func refreshRuntimeOldUserFenced(state map[string]any) bool {
	flags, ok := state["flags"].([]any)
	if !ok || state["commands"] != "-@all" || state["keys"] != "" || state["channels"] != "" {
		return false
	}
	off := false
	for _, flag := range flags {
		if flag == "on" || flag == "nopass" {
			return false
		}
		off = off || flag == "off"
	}
	for _, field := range []string{"passwords", "selectors"} {
		items, ok := state[field].([]any)
		if !ok || len(items) != 0 {
			return false
		}
	}
	return off
}

func writeRefreshRuntimeEnvironment(path string, data []byte) error {
	if _, err := os.Lstat(path); err == nil {
		existing, err := readRefreshMigrationFile(path, 4096)
		if err == nil && bytes.Equal(existing, data) {
			return nil
		}
		return errors.New("runtime output already exists with different contents; refusing to replace it")
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".xiass-runtime-*")
	if err != nil {
		return errors.New("cannot create private runtime output")
	}
	defer func() { _ = os.Remove(f.Name()) }()
	defer func() { _ = f.Close() }()
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	if err != nil {
		return errors.New("cannot persist runtime output")
	}
	if err = f.Close(); err != nil {
		return errors.New("cannot close runtime output")
	}
	if err = os.Link(f.Name(), path); err != nil {
		return errors.New("cannot publish runtime output without replacement")
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return errors.New("cannot sync runtime output directory")
	}
	defer func() { _ = dir.Close() }()
	if err = dir.Sync(); err != nil {
		return errors.New("cannot sync runtime output directory")
	}
	return nil
}
