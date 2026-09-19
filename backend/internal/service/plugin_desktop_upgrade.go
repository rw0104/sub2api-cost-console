package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/sysutil"
)

const pluginDesktopUpgradeNotice = "桌面或兼容内核已更新，插件已自动停用；配置已保留，请检查兼容性后重新启用"

// Server replicas retain their routing policy. This reset belongs only to the
// desktop's single local supervisor and its persistent plugin directory.
type pluginDesktopUpgrade struct {
	path     string
	identity string
	ready    bool
}

func newPluginDesktopUpgrade(root string, host PluginHostInfo) *pluginDesktopUpgrade {
	if !sysutil.IsDesktopMode() {
		return nil
	}
	return &pluginDesktopUpgrade{
		path:     filepath.Join(root, ".desktop-host-version"),
		identity: strings.Join([]string{strings.TrimSpace(os.Getenv("SUB2API_DESKTOP_VERSION")), host.Version, host.ExtensionVersion}, "|"),
	}
}

// Called under operationMu before any route or child is restored. Missing
// markers cover upgrades from older desktops, including interrupted installs.
// Persist each disable before recording completion, so retries after a crash
// cannot leave enabled bindings pointing at an unavailable plugin process.
func (m *PluginManager) prepareDesktopPlugins(ctx context.Context, installations []*PluginInstallation) error {
	upgrade := m.desktopUpgrade
	if upgrade == nil || upgrade.ready {
		return nil
	}
	previous, err := os.ReadFile(upgrade.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("读取桌面插件升级状态: %w", err)
	}
	if err == nil && string(previous) == upgrade.identity {
		upgrade.ready = true
		return nil
	}
	for _, installation := range installations {
		if !hasEnabledPluginBinding(installation.Bindings) && installation.State != PluginStateEnabled && installation.State != PluginStateStarting {
			continue
		}
		bindings := append([]PluginBinding(nil), installation.Bindings...)
		for i := range bindings {
			bindings[i].Enabled = false
		}
		if err := m.repo.UpdateBindingsAndState(ctx, installation.ID, bindings, PluginStateDisabled, pluginDesktopUpgradeNotice, nil, installation.State, installation.BinarySHA256); err != nil {
			return fmt.Errorf("停用升级前的插件 %s: %w", installation.PluginKey, err)
		}
		installation.Bindings = bindings
		installation.State = PluginStateDisabled
		installation.LastError = pluginDesktopUpgradeNotice
		installation.EnabledAt = nil
	}
	if err := os.MkdirAll(filepath.Dir(upgrade.path), 0o700); err != nil {
		return fmt.Errorf("保存桌面插件升级状态: %w", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(upgrade.path), ".desktop-host-version-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(temp.Name()) }()
	_, writeErr := temp.WriteString(upgrade.identity)
	if writeErr == nil {
		writeErr = temp.Sync()
	}
	if err := errors.Join(writeErr, temp.Close()); err != nil {
		return err
	}
	if err := os.Rename(temp.Name(), upgrade.path); err != nil {
		return err
	}
	upgrade.ready = true
	return nil
}
