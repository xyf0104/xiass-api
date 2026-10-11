package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/redis/go-redis/v9"
)

type refreshMigrationNode struct {
	URL            string                                     `json:"url"`
	RunID          string                                     `json:"run_id"`
	ReplicaAddress string                                     `json:"replica_address,omitempty"`
	ACLUsers       []string                                   `json:"acl_users"`
	Modules        []repository.LegacyRefreshTransitionModule `json:"modules"`
}

type refreshMigrationManifest struct {
	Version              int                          `json:"version"`
	DatabaseURL          string                       `json:"database_url"`
	RecoverySecretFile   string                       `json:"recovery_secret_file"`
	Primary              refreshMigrationNode         `json:"primary"`
	PrimaryReplicationID string                       `json:"primary_replication_id"`
	PrimaryAddress       string                       `json:"primary_address"`
	Replicas             []refreshMigrationNode       `json:"replicas"`
	Runtime              *refreshRuntimeManifest      `json:"runtime,omitempty"`
	SessionFence         *refreshSessionFenceManifest `json:"session_fence,omitempty"`
}

type refreshSessionFenceManifest struct {
	PreserveRuntimeAccess          bool `json:"preserve_runtime_access"`
	AuthEndpointsBlockedAndDrained bool `json:"auth_endpoints_blocked_and_drained"`
}

func (m *refreshMigrationManifest) validateSessionFence() error {
	if m.Version == 1 && m.SessionFence == nil {
		return nil
	}
	if m.Version != 2 || m.SessionFence == nil || !m.SessionFence.PreserveRuntimeAccess || !m.SessionFence.AuthEndpointsBlockedAndDrained || m.Runtime == nil {
		return errors.New("manifest v2 requires explicit runtime ACL parameters and independently blocked/drained login, refresh and revocation endpoints; v1 remains offline-only")
	}
	return nil
}

func readRefreshMigrationFile(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > limit {
		return nil, errors.New("migration input must be a private regular file (0400/0600) within the size limit")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("cannot open private migration input")
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) || !opened.Mode().IsRegular() || opened.Mode().Perm()&0077 != 0 {
		return nil, errors.New("migration input changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errors.New("cannot read bounded migration input")
	}
	return data, nil
}

func readRefreshMigrationManifest(path string) (*refreshMigrationManifest, []byte, error) {
	data, err := readRefreshMigrationFile(path, 128<<10)
	if err != nil {
		return nil, nil, err
	}
	var manifest refreshMigrationManifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&manifest) != nil || decoder.Decode(new(any)) != io.EOF || len(manifest.Replicas) > 8 {
		return nil, nil, errors.New("invalid migration manifest version, fields or node count")
	}
	if err := manifest.validateSessionFence(); err != nil {
		return nil, nil, err
	}
	dbURL, err := url.Parse(manifest.DatabaseURL)
	if err != nil || (dbURL.Scheme != "postgres" && dbURL.Scheme != "postgresql") || dbURL.Hostname() == "" || dbURL.Path == "" || dbURL.Fragment != "" {
		return nil, nil, errors.New("migration requires an explicit PostgreSQL database URL")
	}
	secretHex, err := readRefreshMigrationFile(manifest.RecoverySecretFile, 128)
	if err != nil {
		return nil, nil, err
	}
	secret, err := hex.DecodeString(strings.TrimSpace(string(secretHex)))
	if err != nil || len(secret) != 32 {
		return nil, nil, errors.New("recovery file must contain exactly 32 random bytes encoded as hex")
	}
	return &manifest, secret, nil
}

func (m *refreshMigrationManifest) options(secret []byte) (repository.LegacyRefreshTransitionOptions, func(), error) {
	if err := m.validateSessionFence(); err != nil {
		return repository.LegacyRefreshTransitionOptions{}, nil, err
	}
	access, err := m.runtimeAccess()
	if err != nil {
		return repository.LegacyRefreshTransitionOptions{}, nil, err
	}
	var clients []*redis.Client
	closeClients := func() {
		for _, client := range clients {
			_ = client.Close()
		}
	}
	for _, node := range append([]refreshMigrationNode{m.Primary}, m.Replicas...) {
		identity, err := hex.DecodeString(node.RunID)
		if err != nil || len(identity) != 20 {
			closeClients()
			return repository.LegacyRefreshTransitionOptions{}, nil, errors.New("every Redis node requires its pre-inspected 40-character run ID")
		}
		u, err := url.Parse(node.URL)
		if err != nil || (u.Scheme != "redis" && u.Scheme != "rediss") || u.Hostname() == "" || u.RawQuery != "" || u.Fragment != "" {
			closeClients()
			return repository.LegacyRefreshTransitionOptions{}, nil, errors.New("redis nodes require explicit fixed TCP URLs without query overrides")
		}
		opts, err := redis.ParseURL(node.URL)
		if err != nil || opts.Network != "tcp" || opts.DB < 0 {
			closeClients()
			return repository.LegacyRefreshTransitionOptions{}, nil, errors.New("redis nodes require fixed redis/rediss TCP URLs")
		}
		opts.MaxRetries, opts.ContextTimeoutEnabled = -1, true
		opts.DialTimeout, opts.ReadTimeout, opts.WriteTimeout = 5*time.Second, 5*time.Second, 5*time.Second
		clients = append(clients, redis.NewClient(opts))
	}
	group := &repository.LegacyRefreshTransitionGroup{
		PrimaryReplicationID: m.PrimaryReplicationID, PrimaryAddress: m.PrimaryAddress,
		PrimaryACLUsers: m.Primary.ACLUsers, PrimaryModules: m.Primary.Modules,
	}
	for i, node := range m.Replicas {
		group.Replicas = append(group.Replicas, repository.LegacyRefreshTransitionReplica{
			Client: clients[i+1], ExpectedRunID: node.RunID, ReplicaAddress: node.ReplicaAddress,
			ACLUsers: node.ACLUsers, Modules: node.Modules,
		})
	}
	return repository.LegacyRefreshTransitionOptions{
		Source: clients[0], ExpectedRunID: m.Primary.RunID, RecoverySecret: secret, Group: group, PreserveRuntimeAccess: access,
	}, closeClients, nil
}

func migrateRefreshSessions(ctx context.Context, path string, output io.Writer) error {
	manifest, secret, err := readRefreshMigrationManifest(path)
	if err != nil {
		return err
	}
	options, closeClients, err := manifest.options(secret)
	if err != nil {
		return err
	}
	defer closeClients()
	// Deliberately do not use InitEnt: this offline operation must not run
	// migrations, create users, start tunnels, or alter normal installation state.
	db, err := sql.Open("postgres", manifest.DatabaseURL)
	if err != nil {
		return errors.New("cannot open migration database; connection details withheld")
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(2)
	runtime, err := prepareRefreshRuntime(ctx, db, manifest, secret)
	if err != nil {
		return err
	}
	result, err := repository.NewPersistentRefreshTokenStore(db).AdoptLegacyRefreshTokens(ctx, options)
	if err != nil {
		if manifest.SessionFence != nil {
			if errors.Is(err, repository.ErrRefreshTransitionUnsafe) {
				return fmt.Errorf("%w; keep auth endpoints blocked and drained, preserve inference traffic, retry with identical private inputs", err)
			}
			return errors.New("session migration not confirmed; keep login/refresh/revocation blocked and drained, preserve inference traffic, and retry with the same private manifest/recovery secret (connection details withheld)")
		}
		if errors.Is(err, repository.ErrRefreshTransitionUnsafe) {
			return fmt.Errorf("%w; keep applications stopped and retain the same manifest/recovery secret for retry", err)
		}
		return errors.New("session migration not confirmed; keep applications stopped and retain the same manifest/recovery secret for retry (connection details withheld)")
	}
	if runtime != nil {
		if err := runtime.restore(ctx, manifest, secret, result.TransitionID); err != nil {
			if manifest.SessionFence != nil {
				return fmt.Errorf("sessions migrated, runtime not confirmed: %w; keep auth endpoints blocked, preserve inference traffic, retry with identical private inputs", err)
			}
			return fmt.Errorf("sessions migrated, runtime not confirmed: %w; keep applications stopped and retry with the same protected inputs", err)
		}
	}
	if err := json.NewEncoder(output).Encode(result); err != nil {
		return errors.New("migration committed but result output failed; retry with the same protected inputs to retrieve its witness")
	}
	return nil
}
