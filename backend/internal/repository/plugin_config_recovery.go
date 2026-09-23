package repository

import (
	"context"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *pluginRepository) RecoverConfig(ctx context.Context, expected *service.PluginInstallation, encrypted string, recoveredBy *int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	current, err := scanPlugin(tx.QueryRowContext(ctx, pluginSelectSQL+" WHERE id=$1 FOR UPDATE", expected.ID))
	if err != nil {
		return err
	}
	if current.State != service.PluginStateDisabled || current.BinarySHA256 != expected.BinarySHA256 ||
		current.ConfigEncrypted != expected.ConfigEncrypted || !current.UpdatedAt.Equal(expected.UpdatedAt) {
		return service.ErrPluginStateChanged
	}
	if expected.Revision > 0 && current.Revision != expected.Revision {
		return service.ErrPluginStateChanged
	}
	var enabled bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM sub2api_plugin_bindings WHERE plugin_id=$1 AND enabled=TRUE)", expected.ID).Scan(&enabled); err != nil {
		return err
	}
	if enabled {
		return service.ErrPluginStateChanged
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO sub2api_plugin_config_backups(plugin_id,binary_sha256,config_encrypted,recovered_by) VALUES ($1,$2,$3,$4)",
		expected.ID, current.BinarySHA256, current.ConfigEncrypted, recoveredBy); err != nil {
		return fmt.Errorf("备份旧插件配置失败，原配置未修改: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE sub2api_plugin_installations SET config_encrypted=$2,last_error='',revision=revision+1,updated_at=NOW() WHERE id=$1", expected.ID, encrypted); err != nil {
		return err
	}
	return tx.Commit()
}

var _ service.PluginConfigRecoveryRepository = (*pluginRepository)(nil)
