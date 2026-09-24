package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type lifecycleOperationRepository struct {
	PluginRepository

	mu           sync.Mutex
	installation *PluginInstallation
	operations   map[string]*PluginOperation
	updateErr    error
}

func (r *lifecycleOperationRepository) GetByID(context.Context, int64) (*PluginInstallation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.installation == nil {
		return nil, errors.New("插件不存在")
	}
	copy := *r.installation
	copy.Bindings = append([]PluginBinding(nil), r.installation.Bindings...)
	return &copy, nil
}

func (r *lifecycleOperationRepository) UpdateBindingsAndState(_ context.Context, _ int64, bindings []PluginBinding, state, lastError string, enabledAt *time.Time, _, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.updateErr != nil {
		return r.updateErr
	}
	r.installation.Bindings = append([]PluginBinding(nil), bindings...)
	r.installation.State = state
	r.installation.LastError = lastError
	r.installation.EnabledAt = enabledAt
	r.installation.Revision++
	r.installation.UpdatedAt = time.Now().UTC()
	return nil
}

func (r *lifecycleOperationRepository) CreatePluginOperation(_ context.Context, operation *PluginOperation) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.operations == nil {
		r.operations = make(map[string]*PluginOperation)
	}
	copy := *operation
	r.operations[operation.ID] = &copy
	return nil
}

func (r *lifecycleOperationRepository) UpdatePluginOperation(_ context.Context, operation *PluginOperation) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	copy := *operation
	r.operations[operation.ID] = &copy
	return nil
}

func (r *lifecycleOperationRepository) GetPluginOperation(_ context.Context, operationID string) (*PluginOperation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	operation := r.operations[operationID]
	if operation == nil {
		return nil, errors.New("操作不存在")
	}
	copy := *operation
	return &copy, nil
}

func (r *lifecycleOperationRepository) latestOperation() *PluginOperation {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, operation := range r.operations {
		copy := *operation
		return &copy
	}
	return nil
}

func TestPluginLifecycleOperationJournalRecordsDisable(t *testing.T) {
	repo := &lifecycleOperationRepository{installation: &PluginInstallation{
		ID:           7,
		Revision:     4,
		State:        PluginStateEnabled,
		BinarySHA256: "binary",
		Bindings:     []PluginBinding{{ID: 1, Enabled: true}},
	}}
	manager := &PluginManager{repo: repo}

	result, err := manager.Disable(context.Background(), 7)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotEmpty(t, result.OperationID)
	require.Equal(t, PluginStateDisabled, result.State)

	operation := repo.latestOperation()
	require.NotNil(t, operation)
	require.Equal(t, "lifecycle.disable", operation.Kind)
	require.Equal(t, int64(4), operation.ExpectedRevision)
	require.Equal(t, int64(5), operation.TargetRevision)
	require.Equal(t, PluginOperationStageSucceeded, operation.Stage)
	require.Equal(t, "disabled", operation.Result)
	require.NotNil(t, operation.CompletedAt)
}

func TestPluginLifecycleOperationJournalRecordsFailure(t *testing.T) {
	repo := &lifecycleOperationRepository{
		installation: &PluginInstallation{
			ID:           8,
			Revision:     2,
			State:        PluginStateEnabled,
			BinarySHA256: "binary",
			Bindings:     []PluginBinding{{ID: 1, Enabled: true}},
		},
		updateErr: errors.New("database unavailable"),
	}
	manager := &PluginManager{repo: repo}

	_, err := manager.Disable(context.Background(), 8)
	require.ErrorContains(t, err, "database unavailable")

	operation := repo.latestOperation()
	require.NotNil(t, operation)
	require.Equal(t, "lifecycle.disable", operation.Kind)
	require.Equal(t, PluginOperationStageFailed, operation.Stage)
	require.Equal(t, "PLUGIN_OPERATION_FAILED", operation.ErrorCode)
	require.NotNil(t, operation.CompletedAt)
}
