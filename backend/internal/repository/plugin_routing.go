package repository

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *pluginRepository) UpdateRouting(ctx context.Context, expected *service.PluginInstallation, bindings []service.PluginBinding) (*service.PluginInstallation, error) {
	expectedRevision := int64(0)
	if expected != nil {
		expectedRevision = expected.Revision
	}
	return r.UpdateRoutingRevision(ctx, expected, bindings, expectedRevision)
}

// UpdateRoutingRevision atomically replaces bindings and advances the common
// installation revision.  The timestamp check remains as an extra guard for
// callers that loaded a legacy snapshot, while expectedRevision is the
// authoritative validator once migration 244 has been applied.
func (r *pluginRepository) UpdateRoutingRevision(ctx context.Context, expected *service.PluginInstallation, bindings []service.PluginBinding, expectedRevision int64) (*service.PluginInstallation, error) {
	if expected == nil {
		return nil, service.ErrPluginStateChanged
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `UPDATE sub2api_plugin_installations
		SET revision=revision+1, updated_at=GREATEST(clock_timestamp(),updated_at+INTERVAL '1 microsecond')
		WHERE id=$1 AND binary_sha256=$2 AND updated_at=$3 AND state=$4 AND state<>'starting'
		  AND ($5=0 OR revision=$5)`,
		expected.ID, expected.BinarySHA256, expected.UpdatedAt, expected.State, expectedRevision)
	if err != nil {
		return nil, err
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return nil, service.ErrPluginStateChanged
	}
	if err := replacePluginBindings(ctx, tx, expected.ID, bindings); err != nil {
		return nil, err
	}
	saved, err := scanPlugin(tx.QueryRowContext(ctx, pluginSelectSQL+" WHERE id=$1", expected.ID))
	if err != nil {
		return nil, err
	}
	saved.Bindings, err = listPluginBindings(ctx, tx, expected.ID)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return saved, nil
}

var _ service.PluginRoutingRepository = (*pluginRepository)(nil)
var _ service.PluginRevisionRepository = (*pluginRepository)(nil)
