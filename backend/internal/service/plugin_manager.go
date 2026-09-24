package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	pluginv1 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v1"
	pluginv2 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v2"
)

const (
	pluginConfigMaxBytes            = 4 * 1024 * 1024
	pluginUIAssetMaxBytes           = 32 * 1024 * 1024
	pluginReconcilePeriod           = time.Second
	pluginHealthTimeout             = 5 * time.Second
	pluginReadinessInterval         = 30 * time.Second
	pluginReadinessFailureThreshold = 3
	pluginUITokenPrefix             = "sub2api:plugin-ui:v1:"
)

type pluginRoute struct {
	pluginID       int64
	runtime        *pluginRuntime
	rolloutPercent int
	unavailable    string
	binding        PluginBinding
}

type legacyRouteTable struct {
	routes []*pluginRoute
}

// PluginManager 管理插件安装、配置、进程生命周期和 OpenAI OAuth 能力绑定。
type PluginManager struct {
	repo           PluginRepository
	encryptor      SecretEncryptor
	cfg            *config.Config
	hostInfo       PluginHostInfo
	installer      *PluginPackageInstaller
	desktopUpgrade *pluginDesktopUpgrade
	// kvStore 为运行中的插件提供通用宿主键值存储；为 nil 时不向插件暴露宿主服务。
	kvStore PluginKVStore
	// accountDirectory 为声明了对应能力的插件提供账号目录与出站身份解析（敏感能力）；
	// 通过 SetAccountDirectory 在启动装配阶段注入，为 nil 时插件拿不到该能力。
	accountDirectory any

	operationMu        sync.Mutex
	mu                 sync.Mutex
	runtimes           map[int64]*pluginRuntime
	localInstallations map[int64]*PluginInstallation
	started            bool
	reconcileCancel    context.CancelFunc
	reconcileDone      chan struct{}
	route              atomic.Pointer[pluginRoute]
	legacyRoutes       atomic.Pointer[legacyRouteTable]
	extensions         atomic.Pointer[extensionRouteTable]
	retiring           sync.WaitGroup
	retired            map[*pluginRuntime]struct{}
	controlPlaneStale  atomic.Bool
}

func NewPluginManager(repo PluginRepository, encryptor SecretEncryptor, cfg *config.Config, hostInfo PluginHostInfo, kvStores ...PluginKVStore) *PluginManager {
	installer := NewPluginPackageInstaller(cfg, hostInfo)
	if publishers, ok := repo.(PluginPublisherLookup); ok {
		installer.publishers = publishers
	}
	var kvStore PluginKVStore
	if len(kvStores) > 0 {
		kvStore = kvStores[0]
	}
	return &PluginManager{
		repo:               repo,
		encryptor:          encryptor,
		cfg:                cfg,
		hostInfo:           hostInfo,
		installer:          installer,
		desktopUpgrade:     newPluginDesktopUpgrade(installer.RootDir(), hostInfo),
		kvStore:            kvStore,
		runtimes:           make(map[int64]*pluginRuntime),
		localInstallations: make(map[int64]*PluginInstallation),
	}
}

func (m *PluginManager) MaxUploadBytes() int64 {
	if m == nil || m.cfg == nil {
		return 0
	}
	return m.cfg.Plugins.MaxUploadBytes
}

func (m *PluginManager) Start(ctx context.Context) error {
	m.operationMu.Lock()
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		m.operationMu.Unlock()
		return nil
	}
	if err := os.MkdirAll(filepath.Join(m.installer.RootDir(), "runtime"), 0o700); err != nil {
		m.mu.Unlock()
		m.operationMu.Unlock()
		return fmt.Errorf("创建插件运行目录: %w", err)
	}
	reconcileCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	m.started = true
	m.reconcileCancel = cancel
	m.reconcileDone = make(chan struct{})
	done := m.reconcileDone
	m.mu.Unlock()
	m.operationMu.Unlock()

	go m.reconcileLoop(reconcileCtx, done)
	if err := m.reconcileOnce(reconcileCtx); err != nil {
		slog.Warn("plugin_initial_reconcile_failed", "error", err)
	}
	return nil
}

func (m *PluginManager) Stop() {
	m.mu.Lock()
	cancel := m.reconcileCancel
	done := m.reconcileDone
	m.reconcileCancel = nil
	m.reconcileDone = nil
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
	m.operationMu.Lock()
	m.mu.Lock()
	runtimes := make([]*pluginRuntime, 0, len(m.runtimes))
	for _, runtime := range m.runtimes {
		runtimes = append(runtimes, runtime)
	}
	m.runtimes = make(map[int64]*pluginRuntime)
	m.route.Store(nil)
	m.legacyRoutes.Store(nil)
	m.extensions.Store(nil)
	m.started = false
	m.mu.Unlock()
	m.operationMu.Unlock()
	drainPluginRuntimes(context.Background(), runtimes, pluginDrainTimeout)
	m.retiring.Wait()
}

func (m *PluginManager) List(ctx context.Context) ([]*PluginInstallation, error) {
	plugins, err := m.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	route := m.route.Load()
	for _, installation := range plugins {
		NormalizePluginInstallationMetadata(installation)
		installation.Compatibility = EvaluatePluginCompatibility(installation.Manifest, m.hostInfo)
		installation.CapabilityRuntime = m.extensionStatus(installation.ID)
		if runtime := m.runtimes[installation.ID]; runtime != nil && !runtime.client.Exited() {
			installation.RuntimeHealthy = true
			installation.RuntimeVersion = runtime.installation.Version
			installation.RuntimeIsolation = runtime.isolation
			installation.RuntimeMessage = "插件进程运行中"
			if installation.RuntimeVersion != installation.Version {
				installation.RuntimeMessage = "目标版本尚未在本实例就绪，当前继续运行 " + installation.RuntimeVersion
			}
		} else if installation.State == PluginStateEnabled {
			installation.RuntimeMessage = installation.LastError
		}
		if route != nil && route.pluginID == installation.ID && route.runtime == nil {
			installation.RuntimeMessage = route.unavailable
		}
	}
	return plugins, nil
}

func (m *PluginManager) Get(ctx context.Context, id int64) (*PluginInstallation, error) {
	installation, err := m.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	NormalizePluginInstallationMetadata(installation)
	installation.Compatibility = EvaluatePluginCompatibility(installation.Manifest, m.hostInfo)
	installation.CapabilityRuntime = m.extensionStatus(installation.ID)
	m.mu.Lock()
	runtime := m.runtimes[id]
	m.mu.Unlock()
	installation.RuntimeHealthy = runtime != nil && !runtime.client.Exited()
	if installation.RuntimeHealthy {
		installation.RuntimeVersion = runtime.installation.Version
		installation.RuntimeIsolation = runtime.isolation
		installation.RuntimeMessage = "插件进程运行中"
		if installation.RuntimeVersion != installation.Version {
			installation.RuntimeMessage = "目标版本尚未在本实例就绪，当前继续运行 " + installation.RuntimeVersion
		}
	} else if route := m.route.Load(); route != nil && route.pluginID == id {
		installation.RuntimeMessage = route.unavailable
	}
	return installation, nil
}

func (m *PluginManager) Install(ctx context.Context, reader io.Reader, installedBy *int64) (*PluginInstallation, error) {
	return m.InstallWithApproval(ctx, reader, installedBy, nil)
}

func (m *PluginManager) InstallWithApproval(ctx context.Context, reader io.Reader, installedBy *int64, approval *PluginPublisherApproval) (*PluginInstallation, error) {
	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	packageInfo, err := m.installer.InstallWithApproval(ctx, reader, installedBy, approval)
	if err != nil {
		return nil, err
	}
	var previous *PluginInstallation
	if existing, getErr := m.repo.GetByKey(ctx, packageInfo.PluginKey); getErr == nil {
		if existing.State == PluginStateEnabled || hasEnabledPluginBinding(existing.Bindings) {
			cleanupErr := m.cleanupInstallationFiles(packageInfo)
			return nil, errors.Join(errors.New("请先停用当前插件，再上传同 ID 的新版本"), cleanupErr)
		}
		previous = existing
	} else if !errors.Is(getErr, sql.ErrNoRows) {
		cleanupErr := m.cleanupInstallationFiles(packageInfo)
		return nil, errors.Join(getErr, cleanupErr)
	}
	bindings := make([]PluginBinding, 0, len(packageInfo.Manifest.Capabilities))
	for _, capability := range packageInfo.Manifest.SortedCapabilities() {
		bindings = append(bindings, PluginBinding{
			Capability:     capability.ID,
			Platform:       capability.Platform,
			AccountType:    capability.AccountType,
			Enabled:        false,
			RolloutPercent: 100,
		})
	}
	var installed *PluginInstallation
	if packageInfo.PublisherToTrust != nil {
		if publishers, ok := m.repo.(PluginPublisherRepository); ok {
			installed, err = publishers.InstallWithPublisher(ctx, packageInfo, bindings, packageInfo.PublisherToTrust)
		} else {
			err = errors.New("发布者信任存储不可用")
		}
	} else {
		installed, err = m.repo.Install(ctx, packageInfo, bindings)
	}
	if err != nil {
		cleanupErr := m.cleanupInstallationFiles(packageInfo)
		return nil, errors.Join(err, cleanupErr)
	}
	local := *packageInfo
	local.ID = installed.ID
	local.ConfigEncrypted = installed.ConfigEncrypted
	local.Bindings = append([]PluginBinding(nil), installed.Bindings...)
	m.mu.Lock()
	localPrevious := m.localInstallations[installed.ID]
	m.localInstallations[installed.ID] = &local
	m.mu.Unlock()
	if previous != nil {
		if cleanupErr := m.cleanupInstallationFiles(previous); cleanupErr != nil {
			slog.Warn("plugin_previous_install_cleanup_failed", "plugin_id", previous.ID, "error", cleanupErr)
		}
		if localPrevious != nil && (localPrevious.InstallPath != previous.InstallPath || localPrevious.ArtifactPath != previous.ArtifactPath) {
			if cleanupErr := m.cleanupInstallationFiles(localPrevious); cleanupErr != nil {
				slog.Warn("plugin_previous_local_install_cleanup_failed", "plugin_id", previous.ID, "error", cleanupErr)
			}
		}
	}
	return m.Get(ctx, installed.ID)
}

func (m *PluginManager) cleanupInstallationFiles(installation *PluginInstallation) error {
	if installation == nil {
		return nil
	}
	var cleanupErr error
	for _, path := range []string{installation.ArtifactPath, installation.InstallPath} {
		if err := m.removeManagedPath(path); err != nil {
			cleanupErr = errors.Join(cleanupErr, err)
		}
	}
	return cleanupErr
}

func (m *PluginManager) reconcileLoop(ctx context.Context, done chan struct{}) {
	defer close(done)
	ticker := time.NewTicker(pluginReconcilePeriod)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := m.reconcileOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
				slog.Warn("plugin_reconcile_failed", "error", err)
			}
		}
	}
}

// reconcileOnce 以数据库中的绑定为权威状态，让每个实例独立恢复并启动同一插件。
func (m *PluginManager) reconcileOnce(ctx context.Context) error {
	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	installations, err := m.repo.List(ctx)
	if err != nil {
		// Repository availability is a control-plane concern. Keep the last
		// authoritative route and live runtimes serving while the next reconcile
		// retries; a transient list failure must never drain every plugin.
		m.controlPlaneStale.Store(true)
		m.markExtensionsStale("插件启用状态暂时无法读取")
		return fmt.Errorf("读取插件启用状态: %w", err)
	}
	m.controlPlaneStale.Store(false)
	m.clearExtensionsStale()
	if err := m.prepareDesktopPlugins(ctx, installations); err != nil {
		m.publishUnavailableExtensions("桌面升级后的插件停用尚未完成")
		m.publishUnavailableRoute(0, 100, "桌面升级后的插件停用尚未完成")
		return err
	}
	m.cleanupStaleLocalInstallations(installations)
	if secrets, ok := m.repo.(PluginSecretRepository); ok {
		pruneCtx, cancel := context.WithTimeout(ctx, time.Second)
		pruneErr := secrets.PruneSecretGrants(pruneCtx)
		cancel()
		if pruneErr != nil {
			slog.Warn("plugin_secret_prune_failed", "error", pruneErr)
		}
	}
	desired := make(map[int64]bool)
	var reconcileErr error
	for _, installation := range installations {
		if !hasEnabledPluginBinding(installation.Bindings) {
			if installation.State == PluginStateStarting && m.startingStateExpired(installation) {
				err := m.repo.UpdateState(ctx, installation.ID, PluginStateDisabled, "插件启动超时，已自动恢复为停用状态", nil, installation.BinarySHA256, PluginStateStarting)
				if err != nil && !errors.Is(err, ErrPluginStateChanged) {
					reconcileErr = errors.Join(reconcileErr, err)
				}
			}
			continue
		}
		desired[installation.ID] = true
		m.mu.Lock()
		current := m.runtimes[installation.ID]
		m.mu.Unlock()
		compatible := EvaluatePluginCompatibility(installation.Manifest, m.hostInfo)
		if !compatible.Compatible {
			m.publishInstallationUnavailable(installation, compatible.Message)
			reconcileErr = errors.Join(reconcileErr, errors.New(compatible.Message))
			continue
		}
		if current != nil && current.client != nil && !current.client.Exited() &&
			current.installation.BinarySHA256 == installation.BinarySHA256 &&
			current.installation.ConfigEncrypted == installation.ConfigEncrypted {
			healthCtx, cancel := context.WithTimeout(ctx, pluginHealthTimeout)
			healthErr := current.checkReadiness(healthCtx)
			cancel()
			if healthErr != nil {
				m.publishInstallationUnavailable(installation, healthErr.Error())
				reconcileErr = errors.Join(reconcileErr, healthErr)
				continue
			}
			m.mu.Lock()
			m.publishRuntimeLocked(installation, current)
			m.mu.Unlock()
			if installation.State == PluginStateError || (installation.State == PluginStateStarting && m.startingStateExpired(installation)) {
				reconcileErr = errors.Join(reconcileErr, m.repo.MarkRuntimeHealthy(ctx, installation.ID, installation.BinarySHA256, installation.ConfigEncrypted))
			}
			continue
		}
		if installation.State == PluginStateStarting && !m.startingStateExpired(installation) {
			if current == nil {
				m.publishInstallationUnavailable(installation, "插件正在其他实例中启动")
			}
			continue
		}
		if current == nil {
			// Reserve the authoritative priority/scope before the local process
			// is ready; a cold replica must not fall through to a lower policy.
			m.publishInstallationUnavailable(installation, "插件正在本实例启动")
		}
		local, prepareErr := m.ensureLocalInstallation(ctx, installation)
		var candidate *pluginRuntime
		if prepareErr == nil {
			candidate, prepareErr = m.prepareRuntime(ctx, local, true)
		}
		if prepareErr != nil {
			if m.canKeepRuntimeDuringReplacement(ctx, current, installation) {
				reconcileErr = errors.Join(reconcileErr, prepareErr)
				continue
			}
			m.publishInstallationUnavailable(installation, prepareErr.Error())
			reconcileErr = errors.Join(reconcileErr, prepareErr)
			continue
		}
		latest, latestErr := m.repo.GetByID(ctx, installation.ID)
		if latestErr != nil {
			candidate.kill()
			reconcileErr = errors.Join(reconcileErr, latestErr)
			continue
		}
		if !hasEnabledPluginBinding(latest.Bindings) || latest.BinarySHA256 != installation.BinarySHA256 ||
			latest.ConfigEncrypted != installation.ConfigEncrypted || !samePluginBindings(latest.Bindings, installation.Bindings) ||
			(latest.State == PluginStateStarting && !m.startingStateExpired(latest)) {
			candidate.kill()
			continue
		}
		if err := m.repo.MarkRuntimeHealthy(ctx, installation.ID, installation.BinarySHA256, installation.ConfigEncrypted); err != nil {
			candidate.kill()
			if !errors.Is(err, ErrPluginStateChanged) {
				reconcileErr = errors.Join(reconcileErr, err)
			}
			continue
		}
		m.mu.Lock()
		m.publishRuntimeLocked(latest, candidate)
		m.mu.Unlock()
	}
	m.mu.Lock()
	var stale []*pluginRuntime
	for id := range m.runtimes {
		if !desired[id] {
			if runtime := m.removeRuntimeLocked(id); runtime != nil {
				stale = append(stale, runtime)
			}
		}
	}
	// Remove unavailable legacy routes too; these have no process in runtimes.
	m.pruneLegacyRoutesLocked(desired)
	m.pruneExtensionRoutesLocked(desired)
	m.mu.Unlock()
	drainPluginRuntimes(ctx, stale, pluginDrainTimeout)
	return reconcileErr
}

func (m *PluginManager) startingStateExpired(installation *PluginInstallation) bool {
	if installation == nil || installation.UpdatedAt.IsZero() {
		return false
	}
	startTimeout := 15 * time.Second
	if m.cfg != nil && m.cfg.Plugins.StartTimeoutSeconds > 0 {
		startTimeout = time.Duration(m.cfg.Plugins.StartTimeoutSeconds) * time.Second
	}
	recoveryDelay := startTimeout + 45*time.Second
	if recoveryDelay < time.Minute {
		recoveryDelay = time.Minute
	}
	return time.Since(installation.UpdatedAt) > recoveryDelay
}

func (m *PluginManager) publishUnavailableRoute(pluginID int64, rollout int, message string) {
	m.mu.Lock()
	stale := make([]*pluginRuntime, 0, len(m.runtimes))
	for id, runtime := range m.runtimes {
		runtime.draining.Store(true)
		stale = append(stale, runtime)
		delete(m.runtimes, id)
	}
	unavailable := &pluginRoute{pluginID: pluginID, rolloutPercent: rollout, unavailable: message,
		binding: PluginBinding{Capability: PluginCapabilityOpenAIOAuthOutbound, Platform: PlatformOpenAI, AccountType: AccountTypeOAuth, Enabled: true, RolloutPercent: rollout}}
	m.legacyRoutes.Store(&legacyRouteTable{routes: []*pluginRoute{unavailable}})
	m.route.Store(unavailable)
	m.mu.Unlock()
	drainPluginRuntimes(context.Background(), stale, pluginDrainTimeout)
}

func (m *PluginManager) ensureLocalInstallation(ctx context.Context, installation *PluginInstallation) (*PluginInstallation, error) {
	if installation == nil {
		return nil, errors.New("插件安装记录为空")
	}
	m.mu.Lock()
	local := m.localInstallations[installation.ID]
	m.mu.Unlock()
	if local != nil && local.BinarySHA256 == installation.BinarySHA256 && local.Version == installation.Version {
		if err := verifyLocalPluginBinary(local, m.installer.RootDir(), m.installer.runtimeKey(installation.Manifest)); err == nil {
			return mergeLocalInstallation(local, installation), nil
		}
	}
	if err := verifyLocalPluginBinary(installation, m.installer.RootDir(), m.installer.runtimeKey(installation.Manifest)); err == nil {
		local = mergeLocalInstallation(installation, installation)
		m.mu.Lock()
		m.localInstallations[installation.ID] = local
		m.mu.Unlock()
		return local, nil
	}

	artifact, err := m.repo.GetArtifact(ctx, installation.ID)
	if err != nil {
		return nil, fmt.Errorf("读取插件包原件: %w", err)
	}
	if len(artifact) == 0 {
		return nil, errors.New("插件包原件缺失，请重新上传插件")
	}
	restored, err := m.installer.Install(ctx, bytes.NewReader(artifact), installation.InstalledBy)
	if err != nil {
		return nil, fmt.Errorf("恢复并复验插件包: %w", err)
	}
	if !samePluginPackage(restored, installation) {
		cleanupErr := m.cleanupInstallationFiles(restored)
		return nil, errors.Join(errors.New("数据库插件包与安装记录不一致"), cleanupErr)
	}
	local = mergeLocalInstallation(restored, installation)
	m.mu.Lock()
	m.localInstallations[installation.ID] = local
	m.mu.Unlock()
	return local, nil
}

// cleanupStaleLocalInstallations 回收本实例缓存中已从数据库删除或已被新包替换的文件。
// 数据库是跨实例的权威状态，本地目录不能因其他实例的卸载/升级永久残留。
func (m *PluginManager) cleanupStaleLocalInstallations(installations []*PluginInstallation) {
	persisted := make(map[int64]*PluginInstallation, len(installations))
	for _, installation := range installations {
		if installation != nil {
			persisted[installation.ID] = installation
		}
	}
	m.mu.Lock()
	stale := make([]*PluginInstallation, 0)
	for id, local := range m.localInstallations {
		current := persisted[id]
		if current == nil || current.BinarySHA256 != local.BinarySHA256 || current.Version != local.Version {
			if running := m.runtimes[id]; running != nil && running.installation.BinaryPath == local.BinaryPath {
				// The current process may still be serving in-flight requests.
				// Its retirement path removes files only after it has drained.
				continue
			}
			stale = append(stale, local)
			delete(m.localInstallations, id)
		}
	}
	m.mu.Unlock()
	for _, local := range stale {
		if err := m.cleanupInstallationFiles(local); err != nil {
			slog.Warn("plugin_stale_local_install_cleanup_failed", "plugin_id", local.ID, "error", err)
		}
	}
}

func mergeLocalInstallation(local, persisted *PluginInstallation) *PluginInstallation {
	merged := *persisted
	merged.ArtifactData = nil
	merged.ArtifactPath = local.ArtifactPath
	merged.InstallPath = local.InstallPath
	merged.BinaryPath = local.BinaryPath
	merged.Bindings = append([]PluginBinding(nil), persisted.Bindings...)
	return &merged
}

func samePluginPackage(local, persisted *PluginInstallation) bool {
	if local == nil || persisted == nil || local.PluginKey != persisted.PluginKey ||
		local.Version != persisted.Version || local.BinarySHA256 != persisted.BinarySHA256 {
		return false
	}
	localManifest, localErr := json.Marshal(local.Manifest)
	persistedManifest, persistedErr := json.Marshal(persisted.Manifest)
	return localErr == nil && persistedErr == nil && bytes.Equal(localManifest, persistedManifest)
}

func verifyLocalPluginBinary(installation *PluginInstallation, root string, targets ...string) error {
	if installation == nil {
		return errors.New("插件安装记录为空")
	}
	rootPath, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	installPath, err := filepath.Abs(installation.InstallPath)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(rootPath, installPath)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("插件安装目录不在受管目录内")
	}
	target := installation.Manifest.RuntimeKey()
	if len(targets) > 0 {
		target = targets[0]
	}
	runtimeEntry, ok := installation.Manifest.Runtimes[target]
	if !ok {
		return errors.New("插件未声明当前平台运行时")
	}
	binaryPath, err := safePluginJoin(installPath, runtimeEntry.Path)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(binaryPath)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != installation.BinarySHA256 {
		return errors.New("本地插件二进制哈希不匹配")
	}
	installation.BinaryPath = binaryPath
	return nil
}

func (m *PluginManager) Enable(ctx context.Context, id int64, acceptUntested bool, rolloutPercent int) (result *PluginInstallation, err error) {
	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	if rolloutPercent < 0 || rolloutPercent > 100 {
		return nil, errors.New("灰度比例必须在 0 到 100 之间")
	}
	installation, err := m.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if installation.Manifest.SchemaVersion == 1 && rolloutPercent == 0 {
		return nil, errors.New("v1 传输插件灰度比例必须在 1 到 100 之间")
	}
	if installation.State == PluginStateEnabled && hasEnabledPluginBinding(installation.Bindings) {
		installation.Compatibility = EvaluatePluginCompatibility(installation.Manifest, m.hostInfo)
		m.mu.Lock()
		runtime := m.runtimes[id]
		m.mu.Unlock()
		installation.RuntimeHealthy = runtime != nil && !runtime.client.Exited() &&
			runtime.installation.BinarySHA256 == installation.BinarySHA256 && runtime.installation.Version == installation.Version
		if installation.RuntimeHealthy {
			return installation, nil
		}
	}
	compatibility := EvaluatePluginCompatibility(installation.Manifest, m.hostInfo)
	if !compatibility.Compatible {
		stateErr := m.repo.UpdateState(ctx, id, PluginStateIncompatible, compatibility.Message, nil, installation.BinarySHA256, installation.State)
		return nil, errors.Join(errors.New(compatibility.Message), stateErr)
	}
	if !compatibility.Tested && !acceptUntested {
		return nil, errors.New("插件未声明已测试当前 Sub2API 版本，需要管理员确认后启用")
	}
	installation, err = m.ensureLocalInstallation(ctx, installation)
	if err != nil {
		return nil, err
	}
	op, err := m.beginPluginOperation(ctx, id, "lifecycle.enable", installation.Revision)
	if err != nil {
		return nil, err
	}
	operationCommitted := false
	defer func() {
		if err != nil && !operationCommitted {
			m.updatePluginOperation(ctx, op, PluginOperationStageFailed, "", err)
		}
	}()
	originalBindings := append([]PluginBinding(nil), installation.Bindings...)
	for index := range installation.Bindings {
		installation.Bindings[index].Enabled = true
		installation.Bindings[index].RolloutPercent = rolloutPercent
	}
	if err := m.repo.BeginEnable(ctx, id, installation.BinarySHA256, installation.State); err != nil {
		return nil, err
	}
	m.updatePluginOperation(ctx, op, PluginOperationStageApplying, "", nil)
	runtime, err := m.prepareRuntime(ctx, installation, true)
	if err != nil {
		stateCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		stateErr := m.repo.UpdateState(stateCtx, id, PluginStateError, err.Error(), nil, installation.BinarySHA256, PluginStateStarting)
		cancel()
		if hasEnabledPluginBinding(originalBindings) {
			installation.Bindings = originalBindings
			m.publishInstallationUnavailable(installation, err.Error())
		}
		return nil, errors.Join(err, stateErr)
	}
	now := time.Now()
	if err := m.repo.UpdateBindingsAndState(ctx, id, installation.Bindings, PluginStateEnabled, "", &now, PluginStateStarting, installation.BinarySHA256); err != nil {
		runtime.kill()
		if errors.Is(err, ErrPluginStateChanged) {
			return nil, err
		}
		stateCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		stateErr := m.repo.UpdateState(stateCtx, id, PluginStateError, err.Error(), nil, installation.BinarySHA256, PluginStateStarting)
		cancel()
		return nil, errors.Join(err, stateErr)
	}
	m.updatePluginOperation(ctx, op, PluginOperationStagePublishing, "", nil)
	m.mu.Lock()
	m.publishRuntimeLocked(installation, runtime)
	m.mu.Unlock()
	op.TargetRevision = op.ExpectedRevision + 1
	operationCommitted = true
	m.updatePluginOperation(ctx, op, PluginOperationStageSucceeded, "enabled", nil)
	result, err = m.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if result.Revision > op.ExpectedRevision {
		op.TargetRevision = result.Revision
		m.updatePluginOperation(ctx, op, PluginOperationStageSucceeded, "enabled", nil)
	}
	result.OperationID = op.ID
	result.Compatibility = compatibility
	result.RuntimeHealthy = true
	result.RuntimeIsolation = runtime.isolation
	result.RuntimeVersion = runtime.installation.Version
	result.RuntimeMessage = "插件进程运行中"
	return result, nil
}

func (m *PluginManager) Disable(ctx context.Context, id int64) (result *PluginInstallation, err error) {
	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	m.mu.Lock()
	installation, err := m.repo.GetByID(ctx, id)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	op, err := m.beginPluginOperation(ctx, id, "lifecycle.disable", installation.Revision)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	operationCommitted := false
	defer func() {
		if err != nil && !operationCommitted {
			m.updatePluginOperation(ctx, op, PluginOperationStageFailed, "", err)
		}
	}()
	for index := range installation.Bindings {
		installation.Bindings[index].Enabled = false
	}
	if err := m.repo.UpdateBindingsAndState(ctx, id, installation.Bindings, PluginStateDisabled, "", nil, "", installation.BinarySHA256); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	runtime := m.removeRuntimeLocked(id)
	m.mu.Unlock()
	m.updatePluginOperation(ctx, op, PluginOperationStagePublishing, "", nil)
	if runtime != nil {
		drainPluginRuntimes(context.Background(), []*pluginRuntime{runtime}, pluginDrainTimeout)
	}
	op.TargetRevision = op.ExpectedRevision + 1
	operationCommitted = true
	m.updatePluginOperation(ctx, op, PluginOperationStageSucceeded, "disabled", nil)
	result, err = m.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if result.Revision > op.ExpectedRevision {
		op.TargetRevision = result.Revision
		m.updatePluginOperation(ctx, op, PluginOperationStageSucceeded, "disabled", nil)
	}
	result.OperationID = op.ID
	return result, nil
}

func (m *PluginManager) Delete(ctx context.Context, id int64) (err error) {
	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	m.mu.Lock()
	installation, err := m.repo.GetByID(ctx, id)
	if err != nil {
		m.mu.Unlock()
		return err
	}
	if installation.State == PluginStateEnabled || hasEnabledPluginBinding(installation.Bindings) {
		m.mu.Unlock()
		return errors.New("请先停用插件，再执行卸载")
	}
	op, err := m.beginPluginOperation(ctx, id, "lifecycle.delete", installation.Revision)
	if err != nil {
		m.mu.Unlock()
		return err
	}
	operationCommitted := false
	defer func() {
		if err != nil && !operationCommitted {
			m.updatePluginOperation(ctx, op, PluginOperationStageFailed, "", err)
		}
	}()
	m.updatePluginOperation(ctx, op, PluginOperationStageApplying, "", nil)
	if err := m.repo.Delete(ctx, id, installation.BinarySHA256); err != nil {
		m.mu.Unlock()
		return err
	}
	runtime := m.removeRuntimeLocked(id)
	local := m.localInstallations[id]
	delete(m.localInstallations, id)
	m.mu.Unlock()
	m.updatePluginOperation(ctx, op, PluginOperationStagePublishing, "", nil)
	if runtime != nil {
		drainPluginRuntimes(context.Background(), []*pluginRuntime{runtime}, pluginDrainTimeout)
	}
	cleanupErr := m.cleanupInstallationFiles(installation)
	if local != nil && (local.InstallPath != installation.InstallPath || local.ArtifactPath != installation.ArtifactPath) {
		cleanupErr = errors.Join(cleanupErr, m.cleanupInstallationFiles(local))
	}
	if cleanupErr != nil {
		return cleanupErr
	}
	operationCommitted = true
	m.updatePluginOperation(ctx, op, PluginOperationStageSucceeded, "deleted", nil)
	return cleanupErr
}

func (m *PluginManager) GetConfig(ctx context.Context, id int64) (json.RawMessage, error) {
	installation, err := m.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return m.decryptConfig(installation)
}

// SaveConfig preserves the pre-revision API for internal callers and older
// plugin UI bridges. The repository still uses the installation revision as
// the CAS token whenever it supports the additive revision interface.
func (m *PluginManager) SaveConfig(ctx context.Context, id int64, raw json.RawMessage) (json.RawMessage, error) {
	saved, _, err := m.SaveConfigWithRevision(ctx, id, raw, 0)
	return saved, err
}

// SaveConfigWithRevision performs the common validate -> CAS persist -> apply
// sequence. expectedRevision is the value read by the editor; zero is only a
// compatibility value for legacy in-process callers.
func (m *PluginManager) SaveConfigWithRevision(ctx context.Context, id int64, raw json.RawMessage, expectedRevision int64) (json.RawMessage, PluginMutationResult, error) {
	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	if len(raw) == 0 || len(raw) > pluginConfigMaxBytes || !json.Valid(raw) {
		return nil, PluginMutationResult{}, errors.New("插件配置必须是有效且大小受限的 JSON")
	}
	installation, err := m.repo.GetByID(ctx, id)
	if err != nil {
		return nil, PluginMutationResult{}, err
	}
	if expectedRevision > 0 && installation.Revision != expectedRevision {
		return nil, PluginMutationResult{}, ErrPluginStateChanged
	}
	previousConfig, err := m.decryptConfig(installation)
	if err != nil {
		return nil, PluginMutationResult{}, err
	}
	var normalized any
	if err := json.Unmarshal(raw, &normalized); err != nil {
		return nil, PluginMutationResult{}, err
	}
	if normalized == nil {
		return nil, PluginMutationResult{}, errors.New("插件配置 JSON 根节点必须是对象")
	}
	if _, ok := normalized.(map[string]any); !ok {
		return nil, PluginMutationResult{}, errors.New("插件配置 JSON 根节点必须是对象")
	}
	canonical, err := json.Marshal(normalized)
	if err != nil {
		return nil, PluginMutationResult{}, err
	}
	m.mu.Lock()
	runtime := m.runtimes[id]
	m.mu.Unlock()
	temporary := false
	if runtime != nil && (runtime.installation.BinarySHA256 != installation.BinarySHA256 || runtime.installation.Version != installation.Version) {
		runtime = nil
	}
	if runtime == nil {
		installation, err = m.ensureLocalInstallation(ctx, installation)
		if err != nil {
			return nil, PluginMutationResult{}, err
		}
		runtime, err = m.newRuntime(ctx, installation)
		if err != nil {
			return nil, PluginMutationResult{}, err
		}
		temporary = true
		defer runtime.kill()
	}
	if runtime != nil {
		applyCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		canonical, err = runtime.validateNormalizedConfig(applyCtx, canonical)
		cancel()
		if err != nil {
			return nil, PluginMutationResult{}, err
		}
	}
	encrypted, err := m.encryptor.Encrypt(string(canonical))
	if err != nil {
		return nil, PluginMutationResult{}, fmt.Errorf("加密插件配置: %w", err)
	}
	op, err := m.beginPluginOperation(ctx, id, "config.save", installation.Revision)
	if err != nil {
		return nil, PluginMutationResult{}, err
	}
	mutation := PluginMutationResult{Revision: installation.Revision + 1}
	mutation.OperationID = op.ID
	if mutation.Revision <= 0 {
		mutation.Revision = 0
	}
	if hasProtectionCapability(installation.Manifest) {
		if repo, ok := m.repo.(PluginConfigRevisionRepository); ok {
			mutation, err = repo.UpdateConfigCASRevision(ctx, id, encrypted, installation.BinarySHA256, installation.ConfigEncrypted, installation.Revision)
		} else if repo, ok := m.repo.(PluginConfigCASRepository); ok {
			err = repo.UpdateConfigCAS(ctx, id, encrypted, installation.BinarySHA256, installation.ConfigEncrypted)
		} else {
			err = errors.New("宿主存储不支持保护配置的原子比较更新")
		}
	} else if repo, ok := m.repo.(PluginConfigRevisionRepository); ok {
		mutation, err = repo.UpdateConfigCASRevision(ctx, id, encrypted, installation.BinarySHA256, installation.ConfigEncrypted, installation.Revision)
	} else {
		err = m.repo.UpdateConfig(ctx, id, encrypted, installation.BinarySHA256)
	}
	if err != nil {
		// Validation has not applied settings. A failed CAS/encryption/persistence
		// leaves the live process, its tokens, and in-flight streams untouched.
		m.updatePluginOperation(ctx, op, PluginOperationStageFailed, "", err)
		return nil, PluginMutationResult{}, err
	}
	// Repository adapters return revision metadata only; the operation ID is
	// owned by the manager and must remain stable in the response envelope.
	mutation.OperationID = op.ID
	if mutation.Revision > 0 {
		mutation.ETag = PluginInstallationETag(id, mutation.Revision)
	}
	op.TargetRevision = mutation.Revision
	m.updatePluginOperation(ctx, op, PluginOperationStageApplying, "", nil)
	applyCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	applyErr := runtime.applyNormalizedConfig(applyCtx, canonical)
	cancel()
	if applyErr != nil {
		rollbackCtx, rollbackCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer rollbackCancel()
		var rollbackErr error
		if repo, ok := m.repo.(PluginConfigRevisionRepository); ok && mutation.Revision > 0 {
			_, rollbackErr = repo.UpdateConfigCASRevision(rollbackCtx, id, installation.ConfigEncrypted, installation.BinarySHA256, encrypted, mutation.Revision)
		} else if repo, ok := m.repo.(PluginConfigCASRepository); ok {
			rollbackErr = repo.UpdateConfigCAS(rollbackCtx, id, installation.ConfigEncrypted, installation.BinarySHA256, encrypted)
		} else {
			rollbackErr = m.repo.UpdateConfig(rollbackCtx, id, installation.ConfigEncrypted, installation.BinarySHA256)
		}
		if rollbackErr != nil {
			m.publishInstallationUnavailable(installation, "插件配置应用与恢复失败")
		} else if !temporary {
			rollbackErr = m.restoreRuntimeConfig(id, runtime, previousConfig)
		}
		operationErr := errors.Join(applyErr, rollbackErr)
		m.updatePluginOperation(ctx, op, PluginOperationStageFailed, "", operationErr)
		return nil, PluginMutationResult{}, operationErr
	}
	if !temporary {
		runtime.installation.ConfigEncrypted = encrypted
		runtime.installation.Revision = mutation.Revision
		runtime.installation.ETag = mutation.ETag
	}
	m.updatePluginOperation(ctx, op, PluginOperationStagePublishing, "", nil)
	m.updatePluginOperation(ctx, op, PluginOperationStageSucceeded, "applied", nil)
	return canonical, mutation, nil
}

func (m *PluginManager) restoreRuntimeConfig(id int64, runtime *pluginRuntime, previous json.RawMessage) error {
	if runtime == nil {
		return nil
	}
	rollbackCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if hasProtectionCapability(runtime.installation.Manifest) {
		if err := restoreStoredProtectionConfig(rollbackCtx, runtime, previous); err != nil {
			m.publishInstallationUnavailable(runtime.installation, "插件配置回滚失败")
			return err
		}
		return nil
	}
	if err := runtime.validateAndApplyConfig(rollbackCtx, previous); err != nil {
		if runtime.extension != nil {
			m.publishInstallationUnavailable(runtime.installation, "插件配置回滚失败")
			return err
		}
		route := m.route.Load()
		if route != nil && route.pluginID == id && route.runtime == runtime {
			stateErr := m.markRuntimeUnavailable(route, "插件配置回滚失败: "+err.Error())
			return errors.Join(err, stateErr)
		}
		return err
	}
	return nil
}

func (m *PluginManager) Test(ctx context.Context, id int64) (*pluginv1.TestConfigResponse, error) {
	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	installation, err := m.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	configJSON, err := m.decryptConfig(installation)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	runtime := m.runtimes[id]
	m.mu.Unlock()
	temporary := false
	if runtime != nil && (runtime.installation.BinarySHA256 != installation.BinarySHA256 || runtime.installation.Version != installation.Version) {
		runtime = nil
	}
	if runtime == nil {
		installation, err = m.ensureLocalInstallation(ctx, installation)
		if err != nil {
			return nil, err
		}
		runtime, err = m.newRuntime(ctx, installation)
		if err != nil {
			return nil, err
		}
		temporary = true
	}
	if temporary {
		defer runtime.kill()
	}
	testCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := runtime.validateAndApplyConfig(testCtx, configJSON); err != nil {
		return nil, err
	}
	return runtime.testConfig(testCtx, configJSON)
}

// Status returns the plugin's passive runtime status (its Health response, including
// any status_json blob) for the config UI. Unlike Test it never applies config,
// starts a temporary runtime, or reaches upstream — a plugin that is not currently
// running simply reports "not running" with no status blob. This makes it safe to
// serve from a lightweight, ungated, read-only endpoint used for status polling.
func (m *PluginManager) Status(ctx context.Context, id int64) (*pluginv1.HealthResponse, error) {
	installation, err := m.repo.GetByID(ctx, id)
	if err != nil {
		// Status is a read-only data-plane view. During a short control-plane
		// outage, prefer the runtime's last installation snapshot so the UI can
		// show a stale report instead of turning a repository blip into "offline".
		m.mu.Lock()
		runtime := m.runtimes[id]
		local := m.localInstallations[id]
		m.mu.Unlock()
		if runtime != nil && runtime.installation != nil {
			installation = runtime.installation
		} else if local != nil {
			installation = local
		} else {
			return nil, err
		}
		m.controlPlaneStale.Store(true)
	}
	m.mu.Lock()
	runtime := m.runtimes[id]
	m.mu.Unlock()
	if runtime == nil || runtime.client == nil || runtime.client.Exited() || runtime.draining.Load() {
		// A newly installed extension normally has no process until its first
		// matching request. Return the same safe report shape as a live process so
		// the UI can render an empty runtime instead of parsing this text as JSON.
		revision := uint64(0)
		if raw, decryptErr := m.decryptConfig(installation); decryptErr == nil {
			var fields map[string]json.RawMessage
			if json.Unmarshal(raw, &fields) == nil {
				_ = json.Unmarshal(fields["revision"], &revision)
			}
		}
		report := map[string]any{
			"schema": 1, "config_revision": revision,
			"generated_at": time.Now().UTC().Format(time.RFC3339Nano),
			"engines":      0, "requests_total": uint64(0),
			"sessions": []any{}, "pool": []any{}, "node_verifications": []any{},
			"message":    messageForUnavailableRuntime(runtime),
			"error_code": codeForUnavailableRuntime(runtime),
		}
		payload := m.statusSnapshotJSON(installation, runtime, false, report["message"].(string), map[string]any{"schema": 1, "core_report": report})
		return &pluginv1.HealthResponse{
			Healthy:    false,
			Message:    "插件未运行；尚无运行会话",
			StatusJson: payload,
		}, nil
	}
	statusCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	health, err := runtime.status(statusCtx)
	if err != nil {
		return nil, err
	}
	statusPayload := map[string]any{}
	if strings.TrimSpace(health.StatusJson) != "" && json.Valid([]byte(health.StatusJson)) {
		_ = json.Unmarshal([]byte(health.StatusJson), &statusPayload)
	}
	health.StatusJson = m.statusSnapshotJSON(installation, runtime, health.Healthy, health.Message, statusPayload)
	return health, nil
}

// statusSnapshotJSON wraps plugin-defined status data in a host-owned envelope.
// The old core_report/status_json keys are retained at the top level for older
// plugin UIs while the host fields provide a stable, queryable contract.
func (m *PluginManager) statusSnapshotJSON(installation *PluginInstallation, runtime *pluginRuntime, healthy bool, message string, payload map[string]any) string {
	if payload == nil {
		payload = map[string]any{}
	}
	revision := uint64(0)
	if installation != nil {
		if raw, err := m.decryptConfig(installation); err == nil {
			var fields map[string]json.RawMessage
			if json.Unmarshal(raw, &fields) == nil {
				_ = json.Unmarshal(fields["revision"], &revision)
			}
		}
	}
	bindingID := int64(0)
	capability := ""
	var scopeInputs []struct {
		Capability string  `json:"capability"`
		Platform   string  `json:"platform"`
		Account    string  `json:"account_type"`
		Accounts   []int64 `json:"account_ids"`
		Users      []int64 `json:"user_ids"`
		Groups     []int64 `json:"group_ids"`
	}
	if installation != nil {
		for _, binding := range installation.Bindings {
			if !binding.Enabled {
				continue
			}
			if bindingID == 0 {
				bindingID = binding.ID
				capability = binding.Capability
			}
			scopeInputs = append(scopeInputs, struct {
				Capability string  `json:"capability"`
				Platform   string  `json:"platform"`
				Account    string  `json:"account_type"`
				Accounts   []int64 `json:"account_ids"`
				Users      []int64 `json:"user_ids"`
				Groups     []int64 `json:"group_ids"`
			}{binding.Capability, binding.Platform, binding.AccountType, binding.AccountIDs, binding.UserIDs, binding.GroupIDs})
		}
	}
	scopeDigest := ""
	if raw, err := json.Marshal(scopeInputs); err == nil {
		digest := sha256.Sum256(raw)
		scopeDigest = hex.EncodeToString(digest[:])[:16]
	}
	instanceID := ""
	liveness := "not_running"
	readiness := "not_ready"
	requestsTotal := uint64(0)
	inFlight := int64(0)
	errorsTotal := uint64(0)
	deniedTotal := uint64(0)
	stale := m.controlPlaneStale.Load()
	routeDecision := "NOT_SELECTED"
	timeline := pluginRuntimeTimelineSnapshot{}
	if runtime != nil {
		stale = stale || runtime.statusIsStale()
		timeline = runtime.runtimeTimelineSnapshot()
		instanceID = runtime.instanceID
		if runtime.client != nil && !runtime.client.Exited() {
			liveness = "alive"
		}
		if runtime.draining.Load() {
			liveness = "draining"
		}
		if healthy {
			readiness = "ready"
		}
		inFlight = runtime.inFlight.Load()
	}
	if installation != nil && installation.Manifest.SchemaVersion == 2 {
		inFlight = 0
		for _, capability := range m.extensionStatus(installation.ID) {
			requestsTotal += capability.Calls
			inFlight += capability.InFlight
			errorsTotal += capability.Errors
			deniedTotal += capability.Denied
			if capability.Healthy {
				routeDecision = "SELECTED"
			}
		}
		if table := m.extensions.Load(); table != nil {
			stale = table.stateUnavailable
			if stale {
				routeDecision = "STALE_CONTROL_PLANE"
			}
		}
	} else if route := m.route.Load(); route != nil && route.pluginID == installationID(installation) {
		if route.runtime != nil && !route.runtime.draining.Load() {
			routeDecision = "SELECTED"
		} else {
			routeDecision = "RUNTIME_UNAVAILABLE"
		}
	}
	lastErrorCode := lastPluginErrorCode(installation)
	if timeline.LastErrorCode != "" && liveness == "alive" {
		lastErrorCode = timeline.LastErrorCode
	}
	if lastErrorCode == "" && liveness != "alive" {
		lastErrorCode = codeForUnavailableRuntime(runtime)
	}
	var lastErrorAt any
	if !timeline.LastErrorAt.IsZero() {
		lastErrorAt = timeline.LastErrorAt.UTC().Format(time.RFC3339Nano)
	}
	envelope := map[string]any{
		"schema":               1,
		"revision":             revision,
		"generated_at":         time.Now().UTC().Format(time.RFC3339Nano),
		"plugin_id":            installationID(installation),
		"plugin_key":           installationKey(installation),
		"capability":           capability,
		"runtime_instance_id":  instanceID,
		"liveness":             liveness,
		"readiness":            readiness,
		"stale":                stale,
		"binding_id":           bindingID,
		"binding_scope_digest": scopeDigest,
		"requests_total":       requestsTotal,
		"in_flight":            inFlight,
		"errors_total":         errorsTotal,
		"denied_total":         deniedTotal,
		"last_error_code":      lastErrorCode,
		"last_error_at":        lastErrorAt,
		"route_decision":       routeDecision,
		"timeline":             timeline.mapValue(),
		"error_code":           lastErrorCode,
		"message":              message,
		"payload":              payload,
	}
	for key, value := range payload {
		if _, exists := envelope[key]; !exists {
			envelope[key] = value
		}
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return `{"schema":1,"stale":true,"last_error_code":"STATUS_ENCODE_FAILED"}`
	}
	return string(encoded)
}

func installationID(installation *PluginInstallation) int64 {
	if installation == nil {
		return 0
	}
	return installation.ID
}

func installationKey(installation *PluginInstallation) string {
	if installation == nil {
		return ""
	}
	return installation.PluginKey
}

func lastPluginErrorCode(installation *PluginInstallation) string {
	if installation == nil {
		return ""
	}
	return strings.TrimSpace(installation.LastError)
}

func messageForUnavailableRuntime(runtime *pluginRuntime) string {
	if runtime != nil && runtime.draining.Load() {
		return "插件进程正在切换；请稍后刷新运行状态。"
	}
	return "插件尚未启动；发送一次匹配的 OAuth 请求后可查看运行状态。"
}

func codeForUnavailableRuntime(runtime *pluginRuntime) string {
	if runtime != nil && runtime.draining.Load() {
		return "RUNTIME_DRAINING"
	}
	return "PLUGIN_NOT_RUNNING"
}

type pluginUIAssetClaims struct {
	Version  int   `json:"version"`
	PluginID int64 `json:"plugin_id"`
	Expires  int64 `json:"expires"`
}

// CreateUIAssetToken 创建可跨实例校验的短时能力令牌，令牌不包含管理员凭据。
func (m *PluginManager) CreateUIAssetToken(ctx context.Context, id int64, ttl time.Duration) (string, time.Time, error) {
	if ttl <= 0 || ttl > time.Hour {
		return "", time.Time{}, errors.New("插件 UI 会话有效期无效")
	}
	if _, err := m.repo.GetByID(ctx, id); err != nil {
		return "", time.Time{}, err
	}
	expires := time.Now().Add(ttl)
	raw, err := json.Marshal(pluginUIAssetClaims{Version: 1, PluginID: id, Expires: expires.Unix()})
	if err != nil {
		return "", time.Time{}, err
	}
	// 加用途前缀，避免复用同一 AES-GCM 密钥的其他密文被当作 UI 能力令牌。
	encrypted, err := m.encryptor.Encrypt(pluginUITokenPrefix + string(raw))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("加密插件 UI 会话: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString([]byte(encrypted)), expires, nil
}

func (m *PluginManager) ResolveUIAssetToken(token string) (int64, error) {
	if len(token) == 0 || len(token) > 4096 {
		return 0, errors.New("插件 UI 会话无效")
	}
	encrypted, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return 0, errors.New("插件 UI 会话无效")
	}
	plaintext, err := m.encryptor.Decrypt(string(encrypted))
	if err != nil {
		return 0, errors.New("插件 UI 会话无效")
	}
	plaintext, ok := strings.CutPrefix(plaintext, pluginUITokenPrefix)
	if !ok {
		return 0, errors.New("插件 UI 会话无效")
	}
	var claims pluginUIAssetClaims
	decoder := json.NewDecoder(strings.NewReader(plaintext))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&claims); err != nil || claims.Version != 1 || claims.PluginID <= 0 {
		return 0, errors.New("插件 UI 会话无效")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return 0, errors.New("插件 UI 会话无效")
	}
	now := time.Now().Unix()
	if now >= claims.Expires {
		return 0, errors.New("插件 UI 会话已过期")
	}
	if claims.Expires > now+int64(time.Hour/time.Second) {
		return 0, errors.New("插件 UI 会话无效")
	}
	return claims.PluginID, nil
}

func (m *PluginManager) ReadUIAsset(ctx context.Context, id int64, relative string) ([]byte, string, error) {
	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	installation, err := m.repo.GetByID(ctx, id)
	if err != nil {
		return nil, "", err
	}
	installation, err = m.ensureLocalInstallation(ctx, installation)
	if err != nil {
		return nil, "", err
	}
	path := strings.TrimPrefix(strings.ReplaceAll(relative, "\\", "/"), "/")
	if path == "" || path == "index.html" {
		path = installation.Manifest.UI.Entrypoint
	} else {
		path = "ui/" + path
	}
	if _, declared := installation.Manifest.Files[path]; !declared || !strings.HasPrefix(path, "ui/") {
		return nil, "", os.ErrNotExist
	}
	fullPath, err := safePluginJoin(installation.InstallPath, path)
	if err != nil {
		return nil, "", err
	}
	file, err := os.Open(fullPath)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, pluginUIAssetMaxBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) > pluginUIAssetMaxBytes {
		return nil, "", errors.New("插件 UI 资源超过大小限制")
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != installation.Manifest.Files[path] {
		return nil, "", errors.New("插件 UI 资源哈希不匹配")
	}
	return data, path, nil
}

func (m *PluginManager) RoundTripOpenAIOAuth(ctx context.Context, request *http.Request, proxyURL string, account *Account) (*http.Response, bool, error) {
	if response, handled, err := m.roundTripProtection(ctx, request, proxyURL, account); handled {
		return response, true, err
	}
	if !m.ShouldRouteOpenAIOAuth(account) {
		return nil, false, nil
	}
	route := m.selectLegacyRoute(ctx, account)
	if route == nil {
		return nil, false, nil
	}
	if route.runtime == nil {
		return nil, true, fmt.Errorf("OpenAI OAuth 插件不可用: %s", route.unavailable)
	}
	if route.runtime.client.Exited() {
		runtimeErr := errors.New("OpenAI OAuth 插件进程已退出")
		if stateErr := m.markRuntimeUnavailable(route, runtimeErr.Error()); stateErr != nil {
			return nil, true, errors.Join(runtimeErr, stateErr)
		}
		return nil, true, runtimeErr
	}
	if !route.runtime.beginRequest() {
		return nil, true, errors.New("OpenAI OAuth 插件正在停止")
	}
	response, err := route.runtime.roundTrip(ctx, request, proxyURL, account)
	if err != nil {
		route.runtime.finishRequest()
		if route.runtime.client.Exited() {
			if stateErr := m.markRuntimeUnavailable(route, err.Error()); stateErr != nil {
				err = errors.Join(err, stateErr)
			}
		}
		return nil, true, err
	}
	return response, true, nil
}

// ShouldRouteOpenAIOAuth 判断该账号是否命中当前 OpenAI OAuth 插件绑定。
// WebSocket 入口用它把命中的账号切换到 HTTP Bridge，避免绕过 v1 HTTP 插件协议。
func (m *PluginManager) ShouldRouteOpenAIOAuth(account *Account) bool {
	if m == nil || account == nil || account.Platform != PlatformOpenAI || account.Type != AccountTypeOAuth {
		return false
	}
	if m.hasProtectionTransport(account) {
		return true
	}
	route := m.selectLegacyRoute(context.Background(), account)
	return route != nil
}

func (m *PluginManager) markRuntimeUnavailable(failedRoute *pluginRoute, message string) error {
	m.mu.Lock()
	if failedRoute == nil {
		m.mu.Unlock()
		return nil
	}
	if m.markLegacyRouteUnavailableLocked(failedRoute, message) {
		if failedRoute.runtime != nil {
			failedRoute.runtime.kill()
		}
		delete(m.runtimes, failedRoute.pluginID)
		m.mu.Unlock()
		return nil
	}
	current := m.route.Load()
	if current != failedRoute {
		m.mu.Unlock()
		return nil
	}
	if failedRoute.runtime != nil {
		failedRoute.runtime.kill()
	}
	delete(m.runtimes, failedRoute.pluginID)
	unavailable := *failedRoute
	unavailable.runtime = nil
	unavailable.unavailable = message
	m.route.Store(&unavailable)
	m.mu.Unlock()
	return nil
}

func (m *PluginManager) prepareRuntime(ctx context.Context, installation *PluginInstallation, validateConfig bool) (*pluginRuntime, error) {
	configJSON, err := m.decryptConfig(installation)
	if err != nil {
		return nil, err
	}
	runtime, err := m.newRuntime(ctx, installation)
	if err != nil {
		return nil, err
	}
	if err == nil && validateConfig {
		applyCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err = runtime.validateAndApplyConfig(applyCtx, configJSON)
		cancel()
	}
	if err != nil {
		runtime.kill()
		return nil, err
	}
	return runtime, nil
}

func (m *PluginManager) publishRuntimeLocked(installation *PluginInstallation, runtime *pluginRuntime) {
	runtime.installation.Bindings = append([]PluginBinding(nil), installation.Bindings...)
	if old := m.runtimes[installation.ID]; old != nil && old != runtime {
		old.draining.Store(true)
		if m.retired == nil {
			m.retired = make(map[*pluginRuntime]struct{})
		}
		m.retired[old] = struct{}{}
		m.retiring.Add(1)
		go func() {
			defer m.retiring.Done()
			drainPluginRuntimes(context.Background(), []*pluginRuntime{old}, pluginDrainTimeout)
			m.mu.Lock()
			delete(m.retired, old)
			inUse := false
			for _, active := range m.runtimes {
				if active.installation.InstallPath == old.installation.InstallPath {
					inUse = true
					break
				}
			}
			for active := range m.retired {
				if active.installation.InstallPath == old.installation.InstallPath {
					inUse = true
					break
				}
			}
			if !inUse && old.installation.InstallPath != installation.InstallPath {
				if err := m.cleanupRetiredInstallationFiles(old.installation); err != nil {
					slog.Warn("plugin_retired_files_cleanup_failed", "plugin_id", installation.ID, "error", err)
				}
			}
			m.mu.Unlock()
		}()
	}
	m.runtimes[installation.ID] = runtime
	if installation.Manifest.SchemaVersion == 2 {
		m.publishExtensionRoutesLocked(installation, runtime, "")
		return
	}
	m.publishLegacyRouteLocked(installation, runtime, "")
}

func (m *PluginManager) newRuntime(ctx context.Context, installation *PluginInstallation) (*pluginRuntime, error) {
	if compatibility := EvaluatePluginCompatibility(installation.Manifest, m.hostInfo); !compatibility.Compatible {
		return nil, errors.New(compatibility.Message)
	}
	socketDir := filepath.Join(m.installer.RootDir(), "runtime")
	if err := os.MkdirAll(socketDir, 0o700); err != nil {
		return nil, err
	}
	timeout := time.Duration(m.cfg.Plugins.StartTimeoutSeconds) * time.Second
	process, err := startPluginRuntimeWithSandboxAndHost(ctx, installation, timeout, socketDir, m.cfg.Plugins.V2Sandbox, m.buildHostServices(installation))
	if err != nil {
		return nil, err
	}
	if installation.Manifest.SchemaVersion == 2 && hasHostPermissions(installation.Manifest) {
		host := newPluginHostServices(installation,
			func(ctx context.Context, capability, alias string) (pluginv2.SecretValue, error) {
				return m.readPluginSecret(ctx, installation.ID, capability, alias)
			},
			func(ctx context.Context, capability string, accountID int64) ([]byte, error) {
				return m.readPluginAccountMetadata(ctx, installation, capability, accountID)
			})
		process.host = host
		host.setRuntimeMetadata(process.instanceID)
		connector, ok := process.extension.(pluginv2.HostConnector)
		if !ok {
			process.kill()
			return nil, errors.New("插件客户端未支持 Host API")
		}
		attachCtx, cancel := context.WithTimeout(ctx, timeout)
		err = connector.AttachHost(attachCtx, host)
		cancel()
		if err != nil {
			process.kill()
			return nil, errors.New("连接插件 Host API 失败")
		}
		process.markRuntimeHostAttached()
	}
	// Host API attachment precedes readiness: initialization may legitimately
	// call host services before the plugin can report healthy.
	readyCtx, cancel := context.WithTimeout(ctx, timeout)
	err = process.checkHealth(readyCtx)
	cancel()
	if err != nil {
		process.kill()
		return nil, err
	}
	process.markRuntimeReadiness(nil)
	return process, nil
}

func (m *PluginManager) readPluginAccountMetadata(ctx context.Context, installation *PluginInstallation, capability string, accountID int64) ([]byte, error) {
	if installation == nil || accountID <= 0 {
		return nil, errors.New("账号元数据不可用")
	}
	scope := pluginAccountScopeForCapability(installation, capability)
	if !scope.allowsID(accountID) {
		return nil, errors.New("账号不在插件作用域内")
	}
	m.mu.Lock()
	directory := m.accountDirectory
	m.mu.Unlock()
	scoped, ok := directory.(ScopedPluginAccountDirectory)
	if !ok {
		return nil, errors.New("账号目录不支持元数据接口")
	}
	infos, err := scoped.ListPluginAccounts(ctx, scope, PlatformOpenAI, AccountTypeOAuth)
	if err != nil {
		return nil, err
	}
	for _, info := range infos {
		if info.ID == accountID && len(info.MetadataJSON) > 0 {
			return append([]byte(nil), info.MetadataJSON...), nil
		}
	}
	return nil, errors.New("账号元数据不存在")
}

// SetAccountDirectory 注入账号目录实现（敏感能力）。仅在启动装配阶段调用一次，
// 早于 Start，因此运行期读取无需额外同步。
func (m *PluginManager) SetAccountDirectory(directory any) {
	m.mu.Lock()
	m.accountDirectory = directory
	m.mu.Unlock()
}

// buildHostServices 为单个插件构造绑定其 pluginKey 的宿主服务端点。返回 nil（未配置
// 键值存储或缺少 pluginKey）时，startPluginRuntime 不会向插件暴露任何宿主服务。
// 账号目录（会向插件交付账号凭据）只对「清单声明了 OpenAI OAuth 出站能力」的插件开放，
// 从而把凭据暴露面收敛到本就要处理这些账号的插件。
func (m *PluginManager) buildHostServices(installation *PluginInstallation) pluginv1.HostServiceServer {
	if m.kvStore == nil || installation == nil || strings.TrimSpace(installation.PluginKey) == "" {
		return nil
	}
	var directory any
	scope := PluginAccountScope{}
	if pluginDeclaresOpenAIOAuthCapability(installation.Manifest) {
		m.mu.Lock()
		directory = m.accountDirectory
		m.mu.Unlock()
		entries := make([]pluginAccountScopeEntry, 0, len(installation.Bindings))
		for _, binding := range installation.Bindings {
			if binding.Enabled && binding.Capability == PluginCapabilityOpenAIOAuthOutbound {
				entries = append(entries, pluginAccountScopeEntry{Platform: binding.Platform, AccountType: binding.AccountType, AccountIDs: append([]int64(nil), binding.AccountIDs...), UserIDs: append([]int64(nil), binding.UserIDs...), GroupIDs: append([]int64(nil), binding.GroupIDs...)})
			}
		}
		scope = newPluginAccountScope(entries...)
	}
	return newPluginHostServiceServer(installation.PluginKey, m.kvStore, directory, scope)
}

// pluginDeclaresOpenAIOAuthCapability reports whether the (install-validated)
// manifest declares the OpenAI OAuth outbound transport capability.
func pluginDeclaresOpenAIOAuthCapability(manifest PluginManifest) bool {
	for _, capability := range manifest.Capabilities {
		if capability.ID == PluginCapabilityOpenAIOAuthOutbound &&
			capability.Platform == PlatformOpenAI && capability.AccountType == AccountTypeOAuth {
			return true
		}
	}
	return false
}

func (m *PluginManager) removeRuntimeLocked(id int64) *pluginRuntime {
	runtime := m.runtimes[id]
	delete(m.runtimes, id)
	m.removeLegacyRouteLocked(id)
	m.removeExtensionRoutesLocked(id)
	return runtime
}

func (m *PluginManager) decryptConfig(installation *PluginInstallation) (json.RawMessage, error) {
	if installation == nil || strings.TrimSpace(installation.ConfigEncrypted) == "" {
		return json.RawMessage(`{}`), nil
	}
	plaintext, err := m.encryptor.Decrypt(installation.ConfigEncrypted)
	if err != nil {
		return nil, unreadablePluginConfig(installation)
	}
	if !json.Valid([]byte(plaintext)) {
		return nil, unreadablePluginConfig(installation)
	}
	return json.RawMessage(plaintext), nil
}

func (m *PluginManager) removeManagedPath(target string) error {
	if strings.TrimSpace(target) == "" {
		return nil
	}
	root, err := filepath.Abs(m.installer.RootDir())
	if err != nil {
		return err
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(root, absTarget)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("拒绝删除插件根目录之外的路径")
	}
	return os.RemoveAll(absTarget)
}

func bindingRollout(bindings []PluginBinding) int {
	for _, binding := range bindings {
		if binding.Capability == PluginCapabilityOpenAIOAuthOutbound {
			return binding.RolloutPercent
		}
	}
	return 100
}

func stablePluginBucket(accountID int64) uint64 {
	value := uint64(accountID)
	value ^= value >> 33
	value *= 0xff51afd7ed558ccd
	value ^= value >> 33
	return value % 100
}
