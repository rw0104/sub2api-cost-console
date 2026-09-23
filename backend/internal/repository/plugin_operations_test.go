package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestPluginOperationRepositoryPersistsAndReadsLifecycle(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	repo := &pluginRepository{db: db}
	now := time.Now().UTC().Truncate(time.Microsecond)
	op := &service.PluginOperation{ID: "plugin-op-test", PluginID: 4, Kind: "config.save", ExpectedRevision: 3,
		Stage: service.PluginOperationStagePersisting, CreatedAt: now, UpdatedAt: now}
	mock.ExpectExec(`INSERT INTO sub2api_plugin_operations`).
		WithArgs(op.ID, op.PluginID, op.Kind, op.ExpectedRevision, op.TargetRevision, op.Stage, op.Result, op.ErrorCode, op.ErrorMessage, op.CreatedAt, op.UpdatedAt, op.CompletedAt).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.CreatePluginOperation(context.Background(), op))

	completed := now.Add(time.Second)
	op.Stage = service.PluginOperationStageSucceeded
	op.TargetRevision = 4
	op.Result = "applied"
	op.UpdatedAt = completed
	op.CompletedAt = &completed
	mock.ExpectExec(`UPDATE sub2api_plugin_operations`).
		WithArgs(op.ID, op.TargetRevision, op.Stage, op.Result, op.ErrorCode, op.ErrorMessage, op.UpdatedAt, op.CompletedAt).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.UpdatePluginOperation(context.Background(), op))

	mock.ExpectQuery(`SELECT operation_id, plugin_id, operation_kind`).
		WithArgs(op.ID).
		WillReturnRows(sqlmock.NewRows([]string{"operation_id", "plugin_id", "operation_kind", "expected_revision", "target_revision", "stage", "result", "error_code", "error_message", "created_at", "updated_at", "completed_at"}).
			AddRow(op.ID, op.PluginID, op.Kind, op.ExpectedRevision, op.TargetRevision, op.Stage, op.Result, op.ErrorCode, op.ErrorMessage, op.CreatedAt, op.UpdatedAt, op.CompletedAt))
	loaded, err := repo.GetPluginOperation(context.Background(), op.ID)
	require.NoError(t, err)
	require.Equal(t, op.ID, loaded.ID)
	require.Equal(t, service.PluginOperationStageSucceeded, loaded.Stage)
	require.EqualValues(t, 4, loaded.TargetRevision)
	require.NoError(t, mock.ExpectationsWereMet())
}
