package repository

import (
	"context"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/setting"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type proxySubscriptionPersistence struct{ client *ent.Client }

func NewProxySubscriptionPersistence(client *ent.Client) service.ProxySubscriptionPersistence {
	return &proxySubscriptionPersistence{client: client}
}

func (r *proxySubscriptionPersistence) Load(ctx context.Context) (string, error) {
	row, err := r.client.Setting.Query().Where(setting.KeyEQ(service.SettingKeyProxySubscriptions)).Only(ctx)
	if ent.IsNotFound(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return row.Value, nil
}

func (r *proxySubscriptionPersistence) Update(ctx context.Context, expected string, mutate func(service.ProxyRepository) (string, error)) error {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	client := tx.Client()
	// Claim the absent settings row, then serialize all writers across instances.
	_, err = client.ExecContext(ctx, `INSERT INTO settings (key,value,updated_at) VALUES ($1,'',NOW()) ON CONFLICT (key) DO NOTHING`, service.SettingKeyProxySubscriptions)
	if err != nil {
		return err
	}
	rows, err := client.QueryContext(ctx, `SELECT value FROM settings WHERE key=$1 FOR UPDATE`, service.SettingKeyProxySubscriptions)
	if err != nil {
		return err
	}
	var current string
	if !rows.Next() {
		_ = rows.Close()
		return errors.New("subscription settings row missing")
	}
	err = rows.Scan(&current)
	_ = rows.Close()
	if err != nil {
		return err
	}
	if current != expected {
		return service.ErrProxySubscriptionChanged
	}
	ctx = ent.NewTxContext(ctx, tx)
	repo := newProxyRepositoryWithSQL(client, client)
	value, err := mutate(repo)
	if err != nil {
		return err
	}
	if _, err = client.Setting.Update().Where(setting.KeyEQ(service.SettingKeyProxySubscriptions)).SetValue(value).SetUpdatedAt(time.Now()).Save(ctx); err != nil {
		return err
	}
	return tx.Commit()
}
