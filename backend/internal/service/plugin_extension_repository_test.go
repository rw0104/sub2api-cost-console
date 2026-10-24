package service

import (
	"context"
	"sync"
	"time"
)

// extensionMemoryRepository models the production repository's optimistic
// updates so two independent managers can exercise process reconciliation.
type extensionMemoryRepository struct {
	PluginRepository
	mu       sync.Mutex
	row      *PluginInstallation
	history  map[int64]*PluginInstallation
	versions []PluginVersion
	grants   map[string]PluginSecretGrant
}

func (r *extensionMemoryRepository) PruneSecretGrants(context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, grant := range r.grants {
		if !time.Now().Before(grant.ExpiresAt) {
			delete(r.grants, key)
		}
	}
	return nil
}

func (r *extensionMemoryRepository) PutSecretGrant(_ context.Context, g PluginSecretGrant) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.grants == nil {
		r.grants = map[string]PluginSecretGrant{}
	}
	g.UpdatedAt = time.Now()
	r.grants[g.Capability+":"+g.Alias] = g
	return nil
}
func (r *extensionMemoryRepository) GetSecretGrant(_ context.Context, id int64, capability, alias string) (*PluginSecretGrant, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	g, ok := r.grants[capability+":"+alias]
	if !ok || g.PluginID != id || !time.Now().Before(g.ExpiresAt) {
		return nil, ErrPluginStateChanged
	}
	return &g, nil
}
func (r *extensionMemoryRepository) ListSecretGrants(_ context.Context, id int64) ([]PluginSecretGrant, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []PluginSecretGrant{}
	for _, g := range r.grants {
		if g.PluginID == id && time.Now().Before(g.ExpiresAt) {
			g.EncryptedValue = ""
			out = append(out, g)
		}
	}
	return out, nil
}
func (r *extensionMemoryRepository) DeleteSecretGrant(_ context.Context, _ int64, capability, alias string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.grants, capability+":"+alias)
	return nil
}

func (r *extensionMemoryRepository) UpdateRouting(_ context.Context, expected *PluginInstallation, bindings []PluginBinding) (*PluginInstallation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.row.BinarySHA256 != expected.BinarySHA256 || r.row.State != expected.State || !r.row.UpdatedAt.Equal(expected.UpdatedAt) {
		return nil, ErrPluginStateChanged
	}
	r.row.Bindings = append([]PluginBinding(nil), bindings...)
	next := time.Now()
	if !next.After(r.row.UpdatedAt) {
		next = r.row.UpdatedAt.Add(time.Microsecond)
	}
	r.row.UpdatedAt = next
	return cloneExtensionInstallation(r.row), nil
}

func (r *extensionMemoryRepository) ListVersions(context.Context, int64) ([]PluginVersion, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]PluginVersion(nil), r.versions...), nil
}
func (r *extensionMemoryRepository) GetVersion(_ context.Context, _ int64, versionID int64) (*PluginInstallation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.history[versionID] == nil {
		return nil, ErrPluginStateChanged
	}
	return cloneExtensionInstallation(r.history[versionID]), nil
}
func (r *extensionMemoryRepository) SwapVersion(_ context.Context, expected, replacement *PluginInstallation) (*PluginInstallation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.row.BinarySHA256 != expected.BinarySHA256 || r.row.State != expected.State || r.row.ConfigEncrypted != expected.ConfigEncrypted || !r.row.UpdatedAt.Equal(expected.UpdatedAt) {
		return nil, ErrPluginStateChanged
	}
	if r.history == nil {
		r.history = map[int64]*PluginInstallation{}
	}
	id := int64(len(r.versions) + 1)
	r.history[id] = cloneExtensionInstallation(r.row)
	r.versions = append(r.versions, PluginVersion{ID: id, PluginID: r.row.ID, Version: r.row.Version, BinarySHA256: r.row.BinarySHA256,
		SavedAt: time.Now(), ExpiresAt: time.Now().Add(24 * time.Hour), Manifest: r.row.Manifest})
	r.row = cloneExtensionInstallation(replacement)
	r.row.UpdatedAt = time.Now()
	return cloneExtensionInstallation(r.row), nil
}

func cloneExtensionInstallation(p *PluginInstallation) *PluginInstallation {
	out := *p
	out.Bindings = append([]PluginBinding(nil), p.Bindings...)
	out.ArtifactData = append([]byte(nil), p.ArtifactData...)
	return &out
}
func (r *extensionMemoryRepository) List(context.Context) ([]*PluginInstallation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return []*PluginInstallation{cloneExtensionInstallation(r.row)}, nil
}
func (r *extensionMemoryRepository) GetByID(context.Context, int64) (*PluginInstallation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneExtensionInstallation(r.row), nil
}
func (r *extensionMemoryRepository) GetArtifact(context.Context, int64) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]byte(nil), r.row.ArtifactData...), nil
}
func (r *extensionMemoryRepository) BeginEnable(_ context.Context, _ int64, hash, state string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.row.BinarySHA256 != hash || r.row.State != state || state == PluginStateStarting {
		return ErrPluginStateChanged
	}
	r.row.State = PluginStateStarting
	r.row.UpdatedAt = time.Now()
	return nil
}
func (r *extensionMemoryRepository) MarkRuntimeHealthy(_ context.Context, _ int64, hash, config string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.row.BinarySHA256 != hash || r.row.ConfigEncrypted != config || !hasEnabledPluginBinding(r.row.Bindings) {
		return ErrPluginStateChanged
	}
	r.row.State = PluginStateEnabled
	return nil
}
func (r *extensionMemoryRepository) UpdateState(_ context.Context, _ int64, state, message string, enabledAt *time.Time, hash, expected string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.row.BinarySHA256 != hash || r.row.State != expected {
		return ErrPluginStateChanged
	}
	r.row.State, r.row.LastError, r.row.EnabledAt = state, message, enabledAt
	r.row.UpdatedAt = time.Now()
	return nil
}
func (r *extensionMemoryRepository) UpdateConfig(_ context.Context, _ int64, config, hash string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.row.BinarySHA256 != hash {
		return ErrPluginStateChanged
	}
	r.row.ConfigEncrypted = config
	r.row.UpdatedAt = time.Now()
	return nil
}
func (r *extensionMemoryRepository) UpdateBindingsAndState(_ context.Context, _ int64, bindings []PluginBinding, state, message string, enabledAt *time.Time, expected, hash string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.row.BinarySHA256 != hash || (expected != "" && r.row.State != expected) {
		return ErrPluginStateChanged
	}
	r.row.Bindings = append([]PluginBinding(nil), bindings...)
	r.row.State, r.row.LastError, r.row.EnabledAt = state, message, enabledAt
	r.row.UpdatedAt = time.Now()
	return nil
}
