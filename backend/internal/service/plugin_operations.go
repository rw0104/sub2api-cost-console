package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// PluginMutationResult is the small, stable metadata returned by a durable
// plugin write.  The config endpoint keeps returning the raw plugin JSON for
// compatibility and exposes this metadata through response headers.
type PluginMutationResult struct {
	OperationID string
	Revision    int64
	ETag        string
	UpdatedAt   time.Time
}

// PluginRevisionRepository is an additive interface.  Keeping it separate
// from PluginRepository lets older repositories and test doubles continue to
// serve v1 plugins while the production repository opts into the common CAS
// protocol.
type PluginConfigRevisionRepository interface {
	UpdateConfigCASRevision(context.Context, int64, string, string, string, int64) (PluginMutationResult, error)
}

type PluginRoutingRevisionRepository interface {
	UpdateRoutingRevision(context.Context, *PluginInstallation, []PluginBinding, int64) (*PluginInstallation, error)
}

// PluginRevisionRepository is implemented by the production repository once
// migration 244 is applied. The narrower interfaces above also let focused
// repositories opt into one mutation family without weakening the other.
type PluginRevisionRepository interface {
	PluginConfigRevisionRepository
	PluginRoutingRevisionRepository
}

type PluginOperationStage string

const (
	PluginOperationStagePersisting PluginOperationStage = "persisting"
	PluginOperationStageApplying   PluginOperationStage = "applying"
	PluginOperationStagePublishing PluginOperationStage = "publishing"
	PluginOperationStageSucceeded  PluginOperationStage = "succeeded"
	PluginOperationStageFailed     PluginOperationStage = "failed"
)

// PluginOperation is a durable, restart-readable record for an installation
// mutation.  ErrorMessage is deliberately bounded and must never contain
// configuration or credentials.
type PluginOperation struct {
	ID               string
	PluginID         int64
	Kind             string
	ExpectedRevision int64
	TargetRevision   int64
	Stage            PluginOperationStage
	Result           string
	ErrorCode        string
	ErrorMessage     string
	CreatedAt        time.Time
	UpdatedAt        time.Time
	CompletedAt      *time.Time
}

// PluginOperationRepository is optional during the migration window.  A
// production repository implements it; older repositories simply omit the
// durable operation journal while retaining the existing behavior.
type PluginOperationRepository interface {
	CreatePluginOperation(context.Context, *PluginOperation) error
	UpdatePluginOperation(context.Context, *PluginOperation) error
	GetPluginOperation(context.Context, string) (*PluginOperation, error)
}

func newPluginOperationID() string {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		// crypto/rand failures are exceptionally rare.  A timestamp keeps the
		// operation identifiable without making an already failing write panic.
		return fmt.Sprintf("plugin-op-%d", time.Now().UnixNano())
	}
	return "plugin-op-" + hex.EncodeToString(random[:])
}

func pluginETag(id, revision int64) string {
	if id <= 0 || revision <= 0 {
		return ""
	}
	return fmt.Sprintf(`"plugin-%d-%d"`, id, revision)
}

// PluginInstallationETag returns the stable wire representation used by
// admin clients for If-Match/expected-revision writes.
func PluginInstallationETag(id, revision int64) string {
	return pluginETag(id, revision)
}

func NormalizePluginInstallationMetadata(installation *PluginInstallation) {
	if installation == nil {
		return
	}
	if installation.ETag == "" {
		installation.ETag = pluginETag(installation.ID, installation.Revision)
	}
}

func (m *PluginManager) beginPluginOperation(ctx context.Context, pluginID int64, kind string, expectedRevision int64) (*PluginOperation, error) {
	op := &PluginOperation{
		ID:               newPluginOperationID(),
		PluginID:         pluginID,
		Kind:             kind,
		ExpectedRevision: expectedRevision,
		Stage:            PluginOperationStagePersisting,
		CreatedAt:        time.Now().UTC(),
		UpdatedAt:        time.Now().UTC(),
	}
	repo, ok := m.repo.(PluginOperationRepository)
	if !ok {
		return op, nil
	}
	if err := repo.CreatePluginOperation(ctx, op); err != nil {
		return nil, fmt.Errorf("创建插件操作记录: %w", err)
	}
	return op, nil
}

func (m *PluginManager) updatePluginOperation(ctx context.Context, op *PluginOperation, stage PluginOperationStage, result string, err error) {
	if op == nil {
		return
	}
	op.Stage = stage
	op.Result = result
	op.ErrorCode = ""
	op.ErrorMessage = ""
	if err != nil {
		op.ErrorCode = pluginOperationErrorCode(err)
		op.ErrorMessage = boundedPluginOperationError(err)
	}
	now := time.Now().UTC()
	op.UpdatedAt = now
	if stage == PluginOperationStageSucceeded || stage == PluginOperationStageFailed {
		op.CompletedAt = &now
	}
	if repo, ok := m.repo.(PluginOperationRepository); ok {
		// The primary mutation result must win over a best-effort journal update.
		// This keeps a database outage from turning a successful CAS into a
		// misleading failure response.
		_ = repo.UpdatePluginOperation(ctx, op)
	}
}

func pluginOperationErrorCode(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, ErrPluginStateChanged) {
		return "PLUGIN_REVISION_CONFLICT"
	}
	return "PLUGIN_OPERATION_FAILED"
}

func boundedPluginOperationError(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	if len(message) > 512 {
		message = message[:512]
	}
	return message
}
