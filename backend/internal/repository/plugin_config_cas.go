package repository

import (
	"context"
	"database/sql"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Compare the encrypted snapshot as well as the binary. Concurrent managers
// cannot silently overwrite a protection snapshot produced by another instance.
func (r *pluginRepository) UpdateConfigCAS(ctx context.Context, id int64, encrypted, expectedBinary, expectedConfig string) error {
	_, err := r.UpdateConfigCASRevision(ctx, id, encrypted, expectedBinary, expectedConfig, 0)
	return err
}

// UpdateConfigCASRevision is the common config write path. expectedRevision=0
// retains the legacy protection-CAS behavior during the migration window;
// production callers pass the installation revision and therefore cannot
// overwrite another manager's update.
func (r *pluginRepository) UpdateConfigCASRevision(ctx context.Context, id int64, encrypted, expectedBinary, expectedConfig string, expectedRevision int64) (service.PluginMutationResult, error) {
	result := service.PluginMutationResult{}
	err := r.db.QueryRowContext(ctx, `UPDATE sub2api_plugin_installations
	SET config_encrypted=$2, revision=revision+1, updated_at=NOW()
	WHERE id=$1 AND binary_sha256=$3 AND COALESCE(config_encrypted,'')=$4
	  AND ($5=0 OR revision=$5)
		RETURNING revision, updated_at`, id, encrypted, expectedBinary, expectedConfig, expectedRevision).
		Scan(&result.Revision, &result.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return result, service.ErrPluginStateChanged
		}
		return result, err
	}
	result.ETag = service.PluginInstallationETag(id, result.Revision)
	return result, nil
}

var _ service.PluginRevisionRepository = (*pluginRepository)(nil)
