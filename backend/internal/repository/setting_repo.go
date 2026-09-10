package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/account"
	"github.com/Wei-Shaw/sub2api/ent/apikey"
	"github.com/Wei-Shaw/sub2api/ent/group"
	"github.com/Wei-Shaw/sub2api/ent/proxy"
	"github.com/Wei-Shaw/sub2api/ent/setting"
	"github.com/Wei-Shaw/sub2api/ent/user"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type settingRepository struct {
	client *ent.Client
	db     *sql.DB
}

func NewSettingRepository(client *ent.Client) service.SettingRepository {
	var db *sql.DB
	if client != nil {
		if driver, ok := client.Driver().(*entsql.Driver); ok {
			db = driver.DB()
		}
	}
	return &settingRepository{client: client, db: db}
}

func (r *settingRepository) Get(ctx context.Context, key string) (*service.Setting, error) {
	m, err := r.client.Setting.Query().Where(setting.KeyEQ(key)).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, service.ErrSettingNotFound
		}
		return nil, err
	}
	return &service.Setting{
		ID:        m.ID,
		Key:       m.Key,
		Value:     m.Value,
		UpdatedAt: m.UpdatedAt,
	}, nil
}

func (r *settingRepository) GetValue(ctx context.Context, key string) (string, error) {
	setting, err := r.Get(ctx, key)
	if err != nil {
		return "", err
	}
	return setting.Value, nil
}

func (r *settingRepository) Set(ctx context.Context, key, value string) error {
	now := time.Now()
	return r.client.Setting.
		Create().
		SetKey(key).
		SetValue(value).
		SetUpdatedAt(now).
		OnConflictColumns(setting.FieldKey).
		UpdateNewValues().
		Exec(ctx)
}

func (r *settingRepository) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	if len(keys) == 0 {
		return map[string]string{}, nil
	}
	settings, err := r.client.Setting.Query().Where(setting.KeyIn(keys...)).All(ctx)
	if err != nil {
		return nil, err
	}

	result := make(map[string]string)
	for _, s := range settings {
		result[s.Key] = s.Value
	}
	return result, nil
}

func (r *settingRepository) SetMultiple(ctx context.Context, settings map[string]string) error {
	if len(settings) == 0 {
		return nil
	}

	now := time.Now()
	builders := make([]*ent.SettingCreate, 0, len(settings))
	for key, value := range settings {
		builders = append(builders, r.client.Setting.Create().SetKey(key).SetValue(value).SetUpdatedAt(now))
	}
	return r.client.Setting.
		CreateBulk(builders...).
		OnConflictColumns(setting.FieldKey).
		UpdateNewValues().
		Exec(ctx)
}

// MigrateExecutionNodeDefaultWeights atomically claims the one-time migration
// and changes only an untouched two-node 1:1 policy. Explicit custom ratios,
// drained nodes, malformed values, and already-migrated installations remain
// unchanged.
func (r *settingRepository) MigrateExecutionNodeDefaultWeights(ctx context.Context, sourceNodeID string) (bool, error) {
	if r == nil || r.db == nil {
		return false, errors.New("settings database is unavailable")
	}
	sourceNodeID = strings.TrimSpace(sourceNodeID)
	if sourceNodeID == "" {
		return false, errors.New("source execution node ID is empty")
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	rollback := func() { _ = tx.Rollback() }

	var claimed string
	err = tx.QueryRowContext(ctx, `
		INSERT INTO settings (key, value, updated_at)
		VALUES ($1, 'pending', NOW())
		ON CONFLICT (key) DO NOTHING
		RETURNING value
	`, service.SettingKeyExecutionNodeDefaultWeightsMigrated).Scan(&claimed)
	if errors.Is(err, sql.ErrNoRows) {
		rollback()
		return false, nil
	}
	if err != nil {
		rollback()
		return false, err
	}

	var rawWeights string
	err = tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = $1 FOR UPDATE`, service.SettingKeyExecutionNodeWeights).Scan(&rawWeights)
	if errors.Is(err, sql.ErrNoRows) {
		// Do not consume the one-time marker before a two-node policy exists.
		// Pairing can then complete the migration atomically when the peer joins.
		rollback()
		return false, nil
	}
	if err != nil {
		rollback()
		return false, err
	}

	weights := map[string]float64{}
	if err := json.Unmarshal([]byte(rawWeights), &weights); err != nil {
		if err := finalizeExecutionNodeDefaultWeightsMigration(ctx, tx, sourceNodeID, "preserved_invalid"); err != nil {
			rollback()
			return false, err
		}
		return false, tx.Commit()
	}
	if len(weights) < 2 {
		rollback()
		return false, nil
	}

	migrated := len(weights) == 2
	if _, exists := weights[sourceNodeID]; !exists {
		migrated = false
	}
	for _, weight := range weights {
		if weight != 1 {
			migrated = false
			break
		}
	}
	if migrated {
		weights[sourceNodeID] = 9
		encoded, err := json.Marshal(weights)
		if err != nil {
			rollback()
			return false, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE settings SET value = $1, updated_at = NOW() WHERE key = $2`, string(encoded), service.SettingKeyExecutionNodeWeights); err != nil {
			rollback()
			return false, err
		}
	}
	result := "preserved_custom"
	if migrated {
		result = "migrated_9_to_1"
	}
	if err := finalizeExecutionNodeDefaultWeightsMigration(ctx, tx, sourceNodeID, result); err != nil {
		rollback()
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return migrated, nil
}

func finalizeExecutionNodeDefaultWeightsMigration(ctx context.Context, tx *sql.Tx, sourceNodeID, result string) error {
	marker, err := json.Marshal(map[string]string{"source_node_id": sourceNodeID, "result": result})
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE settings SET value = $1, updated_at = NOW() WHERE key = $2`, string(marker), service.SettingKeyExecutionNodeDefaultWeightsMigrated)
	return err
}

func (r *settingRepository) GetAll(ctx context.Context) (map[string]string, error) {
	settings, err := r.client.Setting.Query().All(ctx)
	if err != nil {
		return nil, err
	}

	result := make(map[string]string)
	for _, s := range settings {
		result[s.Key] = s.Value
	}
	return result, nil
}

func (r *settingRepository) Delete(ctx context.Context, key string) error {
	_, err := r.client.Setting.Delete().Where(setting.KeyEQ(key)).Exec(ctx)
	return err
}

// EnsureExecutionNodeClusterID creates a stable PostgreSQL-cluster identity
// without trusting a UUID that may have been copied by a logical database
// snapshot. The fallback is retained for non-PostgreSQL test doubles.
func (r *settingRepository) EnsureExecutionNodeClusterID(ctx context.Context, candidate string) (string, error) {
	identity := strings.TrimSpace(candidate)
	if r.db != nil {
		var systemIdentifier string
		if err := r.db.QueryRowContext(ctx, "SELECT system_identifier::text FROM pg_control_system()").Scan(&systemIdentifier); err != nil {
			return "", fmt.Errorf("read PostgreSQL system identifier: %w", err)
		}
		identity = strings.TrimSpace(systemIdentifier)
		if identity == "" {
			return "", fmt.Errorf("PostgreSQL system identifier is empty")
		}
	}
	if identity == "" {
		return "", fmt.Errorf("database cluster identity candidate is empty")
	}
	if err := r.client.Setting.
		Create().
		SetKey(service.SettingKeyExecutionNodeClusterID).
		SetValue(identity).
		SetUpdatedAt(time.Now()).
		OnConflictColumns(setting.FieldKey).
		Ignore().
		Exec(ctx); err != nil {
		return "", err
	}
	item, err := r.client.Setting.Query().Where(setting.KeyEQ(service.SettingKeyExecutionNodeClusterID)).Only(ctx)
	if err != nil {
		return "", err
	}
	// Existing installations may contain the older generated UUID. Replace it
	// with the actual PostgreSQL identity so a cloned settings table cannot keep
	// passing the shared-state check after the upgrade.
	if strings.TrimSpace(item.Value) != identity {
		if err := r.client.Setting.
			UpdateOneID(item.ID).
			SetValue(identity).
			SetUpdatedAt(time.Now()).
			Exec(ctx); err != nil {
			return "", err
		}
		return identity, nil
	}
	return item.Value, nil
}

// IsExecutionNodeJoinTargetEmpty allows a source-authoritative join only on a
// target that has no customer/business state. The target's bootstrap admin and
// migration metadata are intentionally ignored; silently discarding accounts,
// keys, usage, proxies, or groups would create an unrecoverable split ledger.
func (r *settingRepository) IsExecutionNodeJoinTargetEmpty(ctx context.Context) (bool, error) {
	// Use Ent queries instead of the repository's optional *sql.DB handle. The
	// pairing preflight also runs inside an Ent transaction during integration
	// tests, where the transaction client intentionally does not expose the
	// underlying *sql.DB.
	accountExists, err := r.client.Account.Query().
		Where(account.DeletedAtIsNil()).
		Exist(ctx)
	if err != nil {
		return false, fmt.Errorf("inspect target accounts: %w", err)
	}
	if accountExists {
		return false, nil
	}

	apiKeyExists, err := r.client.APIKey.Query().
		Where(apikey.DeletedAtIsNil()).
		Exist(ctx)
	if err != nil {
		return false, fmt.Errorf("inspect target API keys: %w", err)
	}
	if apiKeyExists {
		return false, nil
	}

	usageLogExists, err := r.client.UsageLog.Query().Exist(ctx)
	if err != nil {
		return false, fmt.Errorf("inspect target usage logs: %w", err)
	}
	if usageLogExists {
		return false, nil
	}

	proxies, err := r.client.Proxy.Query().
		Where(proxy.DeletedAtIsNil()).
		All(ctx)
	if err != nil {
		return false, fmt.Errorf("inspect target proxies: %w", err)
	}
	for _, item := range proxies {
		if !isExecutionNodeBootstrapProxy(item) {
			return false, nil
		}
	}

	groupExists, err := r.client.Group.Query().
		Where(group.DeletedAtIsNil()).
		Exist(ctx)
	if err != nil {
		return false, fmt.Errorf("inspect target groups: %w", err)
	}
	if groupExists {
		return false, nil
	}

	activeUserCount, err := r.client.User.Query().
		Where(user.DeletedAtIsNil()).
		Count(ctx)
	if err != nil {
		return false, fmt.Errorf("inspect target users: %w", err)
	}
	return activeUserCount <= 1, nil
}

func isExecutionNodeBootstrapProxy(item *ent.Proxy) bool {
	if item == nil || !strings.HasPrefix(item.Name, service.ExecutionNodeBuiltinProxyNamePrefix) {
		return false
	}
	if item.Protocol != "socks5" || item.Host != "127.0.0.1" || item.Port != 19080 || item.Username == nil {
		return false
	}
	return *item.Username == strings.TrimPrefix(item.Name, service.ExecutionNodeBuiltinProxyNamePrefix)
}

// AcceptExecutionNodePairing consumes the single-use invite and publishes both
// sides of the peer relationship atomically. A zero row count means the invite
// was replayed or replaced by a newer one.
func (r *settingRepository) AcceptExecutionNodePairing(ctx context.Context, expectedInvite string, peerSettings map[string]string) (bool, error) {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return false, err
	}
	count, err := tx.Setting.
		Update().
		Where(setting.KeyEQ(service.SettingKeyExecutionNodePairingInvite), setting.ValueEQ(expectedInvite)).
		SetValue("").
		SetUpdatedAt(time.Now()).
		Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return false, err
	}
	if count != 1 {
		_ = tx.Rollback()
		return false, nil
	}
	builders := make([]*ent.SettingCreate, 0, len(peerSettings))
	for key, value := range peerSettings {
		builders = append(builders, tx.Setting.Create().SetKey(key).SetValue(value).SetUpdatedAt(time.Now()))
	}
	if len(builders) > 0 {
		if err := tx.Setting.CreateBulk(builders...).OnConflictColumns(setting.FieldKey).UpdateNewValues().Exec(ctx); err != nil {
			_ = tx.Rollback()
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
