package repository

import (
	"context"
	"database/sql"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *pluginRepository) CreatePluginOperation(ctx context.Context, operation *service.PluginOperation) error {
	if operation == nil || operation.ID == "" || operation.PluginID <= 0 {
		return service.ErrPluginStateChanged
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO sub2api_plugin_operations
		(operation_id, plugin_id, operation_kind, expected_revision, target_revision,
		 stage, result, error_code, error_message, created_at, updated_at, completed_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,COALESCE($10,NOW()),COALESCE($11,NOW()),$12)
	`, operation.ID, operation.PluginID, operation.Kind, operation.ExpectedRevision, operation.TargetRevision,
		operation.Stage, operation.Result, operation.ErrorCode, operation.ErrorMessage,
		operation.CreatedAt, operation.UpdatedAt, operation.CompletedAt)
	return err
}

func (r *pluginRepository) UpdatePluginOperation(ctx context.Context, operation *service.PluginOperation) error {
	if operation == nil || operation.ID == "" {
		return service.ErrPluginStateChanged
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE sub2api_plugin_operations
		SET target_revision=$2, stage=$3, result=$4, error_code=$5, error_message=$6,
		    updated_at=COALESCE($7,NOW()), completed_at=$8
		WHERE operation_id=$1
	`, operation.ID, operation.TargetRevision, operation.Stage, operation.Result, operation.ErrorCode,
		operation.ErrorMessage, operation.UpdatedAt, operation.CompletedAt)
	if err != nil {
		return err
	}
	if rows, err := result.RowsAffected(); err != nil {
		return err
	} else if rows != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (r *pluginRepository) GetPluginOperation(ctx context.Context, operationID string) (*service.PluginOperation, error) {
	operation := &service.PluginOperation{}
	err := r.db.QueryRowContext(ctx, `
		SELECT operation_id, plugin_id, operation_kind, expected_revision, target_revision,
		       stage, result, error_code, error_message, created_at, updated_at, completed_at
		FROM sub2api_plugin_operations WHERE operation_id=$1
	`, operationID).Scan(
		&operation.ID, &operation.PluginID, &operation.Kind, &operation.ExpectedRevision, &operation.TargetRevision,
		&operation.Stage, &operation.Result, &operation.ErrorCode, &operation.ErrorMessage,
		&operation.CreatedAt, &operation.UpdatedAt, &operation.CompletedAt,
	)
	if err != nil {
		return nil, err
	}
	return operation, nil
}

var _ service.PluginOperationRepository = (*pluginRepository)(nil)
