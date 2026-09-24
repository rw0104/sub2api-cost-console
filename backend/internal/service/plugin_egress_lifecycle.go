package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pluginruntime"
	pluginv2 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v2"
)

type pluginEgressAuthorizer struct {
	scope PluginAccountScope
}

func (a pluginEgressAuthorizer) Authorize(_ context.Context, request pluginruntime.EgressRequest) (pluginruntime.EgressDecision, error) {
	if !a.scope.allowsID(request.AccountID) {
		return pluginruntime.EgressDecision{Code: "ACCOUNT_SCOPE_DENIED"}, nil
	}
	return pluginruntime.EgressDecision{Allowed: true, Code: "ACCOUNT_SCOPE_ALLOWED"}, nil
}

func (m *PluginManager) createPluginEgressOwner(installation *PluginInstallation, sandbox config.PluginSandboxConfig, instanceID string) (*pluginruntime.EgressBrokerOwner, error) {
	if installation == nil || sandbox.Mode != "container" || !sandbox.EgressBroker.Enabled {
		return nil, nil
	}
	if !manifestHasProtectionTransport(installation.Manifest) {
		return nil, errors.New("container egress broker requires a protection transport capability")
	}
	if strings.TrimSpace(instanceID) == "" || strings.TrimSpace(installation.PluginKey) == "" {
		return nil, errors.New("container egress broker owner identity is incomplete")
	}
	scope := pluginAccountScopeForCapability(installation, pluginv2.CapabilityProtectionTransport)
	scopeDigest, err := pluginBindingScopeDigest(installation, pluginv2.CapabilityProtectionTransport)
	if err != nil {
		return nil, err
	}
	token, err := newPluginEgressOwnerToken()
	if err != nil {
		return nil, err
	}
	root := filepath.Join(m.installer.RootDir(), "runtime")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	socketName := pluginEgressSocketName(installation.PluginKey, instanceID)
	socketPath := filepath.Join(root, "egress-"+socketName+".sock")
	if len(socketPath) > 100 {
		return nil, errors.New("container egress broker socket path is too long")
	}
	if info, statErr := os.Lstat(socketPath); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("container egress broker socket path is a symlink")
		}
		return nil, errors.New("container egress broker socket path already exists")
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return nil, statErr
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("create scoped egress broker listener: %w", err)
	}
	cleanup := func() {
		_ = listener.Close()
		_ = os.Remove(socketPath)
	}
	if err := os.Chmod(socketPath, 0o600); err != nil {
		cleanup()
		return nil, err
	}
	owner, err := pluginruntime.NewEgressBrokerOwner(pluginruntime.EgressBrokerOwnerOptions{
		Broker: pluginruntime.EgressBrokerOptions{Enabled: true, SocketPath: socketPath,
			AllowedHosts:   append([]string(nil), sandbox.EgressBroker.AllowedHosts...),
			AllowedSchemes: append([]string(nil), sandbox.EgressBroker.AllowedSchemes...), RequireTLS: sandbox.EgressBroker.RequireTLS},
		Identity: pluginruntime.EgressBrokerIdentity{PluginKey: installation.PluginKey, RuntimeInstanceID: instanceID, SocketName: socketName,
			BindingScopeDigest: scopeDigest, OwnerToken: token},
		Listener: listener, Authorizer: pluginEgressAuthorizer{scope: scope},
	})
	if err != nil {
		cleanup()
		return nil, err
	}
	return owner, nil
}

func pluginEgressSocketName(pluginKey, instanceID string) string {
	digest := sha256.Sum256([]byte(pluginKey + "\x00" + instanceID))
	return hex.EncodeToString(digest[:])[:16]
}

func manifestHasProtectionTransport(manifest PluginManifest) bool {
	for _, capability := range manifest.Capabilities {
		if capability.ID == pluginv2.CapabilityProtectionTransport {
			return true
		}
	}
	return false
}

func newPluginEgressOwnerToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func pluginBindingScopeDigest(installation *PluginInstallation, capability string) (string, error) {
	if installation == nil {
		return "", errors.New("plugin binding scope is unavailable")
	}
	type scopeEntry struct {
		ID          int64   `json:"id"`
		Capability  string  `json:"capability"`
		Platform    string  `json:"platform"`
		AccountType string  `json:"account_type"`
		AccountIDs  []int64 `json:"account_ids"`
		UserIDs     []int64 `json:"user_ids"`
		GroupIDs    []int64 `json:"group_ids"`
	}
	entries := make([]scopeEntry, 0, len(installation.Bindings))
	for _, binding := range installation.Bindings {
		if !binding.Enabled || (capability != "" && binding.Capability != capability) {
			continue
		}
		entry := scopeEntry{ID: binding.ID, Capability: binding.Capability, Platform: binding.Platform, AccountType: binding.AccountType,
			AccountIDs: append([]int64(nil), binding.AccountIDs...), UserIDs: append([]int64(nil), binding.UserIDs...), GroupIDs: append([]int64(nil), binding.GroupIDs...)}
		sort.Slice(entry.AccountIDs, func(i, j int) bool { return entry.AccountIDs[i] < entry.AccountIDs[j] })
		sort.Slice(entry.UserIDs, func(i, j int) bool { return entry.UserIDs[i] < entry.UserIDs[j] })
		sort.Slice(entry.GroupIDs, func(i, j int) bool { return entry.GroupIDs[i] < entry.GroupIDs[j] })
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
	raw, err := json.Marshal(entries)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])[:16], nil
}
