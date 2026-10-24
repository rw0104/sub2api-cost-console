package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const PluginConfigUnreadableReason = "PLUGIN_CONFIG_UNREADABLE"

type PluginConfigRecoveryRepository interface {
	RecoverConfig(context.Context, *PluginInstallation, string, *int64) error
}

func pluginConfigDigest(encrypted string) string {
	hash := sha256.Sum256([]byte(encrypted))
	return hex.EncodeToString(hash[:])
}

func unreadablePluginConfig(installation *PluginInstallation) error {
	return infraerrors.Conflict(PluginConfigUnreadableReason,
		"旧插件配置无法解密，原配置已保留。请恢复原加密密钥，或先停用插件，在配置页确认备份旧配置后重新填写并保存；无需重新打包插件。").
		WithMetadata(map[string]string{"config_digest": pluginConfigDigest(installation.ConfigEncrypted)})
}

// Recovery is a separate, explicit admin operation. Ordinary reads, tests,
// starts and saves never erase or bypass an unreadable policy snapshot.
func (m *PluginManager) RecoverConfig(ctx context.Context, id int64, raw json.RawMessage, expectedDigest string, recoveredBy *int64) (json.RawMessage, error) {
	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	if len(raw) == 0 || len(raw) > pluginConfigMaxBytes || !json.Valid(raw) {
		return nil, errors.New("插件配置必须是有效且大小受限的 JSON")
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object == nil {
		return nil, errors.New("插件配置 JSON 根节点必须是对象")
	}
	installation, err := m.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if installation.State != PluginStateDisabled || hasEnabledPluginBinding(installation.Bindings) {
		return nil, errors.New("请先停用插件，再确认重新配置")
	}
	if len(expectedDigest) != 64 || expectedDigest != pluginConfigDigest(installation.ConfigEncrypted) {
		return nil, ErrPluginStateChanged
	}
	if _, err := m.decryptConfig(installation); err == nil {
		return nil, errors.New("原配置当前可正常读取，请刷新配置页后使用普通保存")
	}
	repo, ok := m.repo.(PluginConfigRecoveryRepository)
	if !ok {
		return nil, errors.New("宿主存储不支持保留旧配置，请升级宿主")
	}
	// Validate the replacement with the existing installed binary, in a
	// temporary runtime with no route bindings, before changing stored data.
	local, err := m.ensureLocalInstallation(ctx, installation)
	if err != nil {
		return nil, err
	}
	process, err := m.newRuntime(ctx, local)
	if err != nil {
		return nil, err
	}
	defer process.kill()
	validationCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	canonical, err := process.validateAndApplyNormalizedConfig(validationCtx, raw)
	if err != nil {
		return nil, err
	}
	encrypted, err := m.encryptor.Encrypt(string(canonical))
	if err != nil {
		return nil, errors.New("加密新配置失败，旧配置未修改")
	}
	if err := repo.RecoverConfig(ctx, installation, encrypted, recoveredBy); err != nil {
		return nil, err
	}
	return canonical, nil
}
