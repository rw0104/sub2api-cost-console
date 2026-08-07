use reqwest::{Client, Url};
use semver::Version;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::{
    collections::HashMap,
    fs,
    io::Write,
    net::{SocketAddr, TcpStream},
    path::{Path, PathBuf},
    process::Stdio,
    sync::{Arc, Mutex},
    time::Duration,
};
use tauri::{AppHandle, Emitter, Manager};
use tauri_plugin_shell::{process::CommandChild, process::CommandEvent, ShellExt};
use tokio::{
    io::AsyncWriteExt,
    sync::Mutex as AsyncMutex,
    time::{sleep, timeout},
};

const BACKEND_HOST: &str = "127.0.0.1";
const BACKEND_PORT: u16 = 18_765;
const BACKEND_SIDECAR_NAME: &str = "sub2api-backend";
const UPSTREAM_RELEASE_API: &str = "https://api.github.com/repos/Wei-Shaw/sub2api/releases/latest";
const UPSTREAM_REPOSITORY: &str = "Wei-Shaw/sub2api";
const UPSTREAM_CHECKSUM_ASSET: &str = "checksums.txt";
const MAX_CORE_ARCHIVE_BYTES: u64 = 300 * 1024 * 1024;
const CURATED_MODEL_PRICING: &[u8] =
    include_bytes!("../../../backend/resources/model-pricing/model_prices_and_context_window.json");
pub const CORE_VERSION: &str = env!("SUB2API_CORE_VERSION");
pub const ALGORITHM_VERSION: &str = env!("SUB2API_ALGORITHM_VERSION");
pub const UPSTREAM_SUB2API_COMMIT: &str = env!("SUB2API_UPSTREAM_COMMIT");

#[derive(Clone, Debug, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum BackendPhase {
    Starting,
    Ready,
    Stopped,
    Error,
}

#[derive(Clone, Debug, Serialize)]
pub struct BackendStatus {
    pub phase: BackendPhase,
    pub managed: bool,
    pub pid: Option<u32>,
    pub port: u16,
    pub data_dir: String,
    pub core_version: String,
    pub algorithm_version: String,
    pub upstream_commit: String,
    pub message: String,
    pub last_log: String,
}

impl BackendStatus {
    fn initial(data_dir: &Path, versions: &CoreVersions) -> Self {
        Self {
            phase: BackendPhase::Starting,
            managed: true,
            pid: None,
            port: BACKEND_PORT,
            data_dir: data_dir.display().to_string(),
            core_version: versions.current_version.clone(),
            algorithm_version: versions.current_algorithm_version.clone(),
            upstream_commit: versions.upstream_commit.clone(),
            message: "正在启动本地 Sub2API 内核".into(),
            last_log: String::new(),
        }
    }
}

struct BackendInner {
    child: Option<CommandChild>,
    status: BackendStatus,
    generation: u64,
    consecutive_failures: u32,
    shutting_down: bool,
}

#[derive(Clone)]
pub struct BackendSupervisor {
    inner: Arc<Mutex<BackendInner>>,
    update_lock: Arc<AsyncMutex<()>>,
}

impl BackendSupervisor {
    pub fn new(data_dir: &Path, versions: &CoreVersions) -> Self {
        Self {
            inner: Arc::new(Mutex::new(BackendInner {
                child: None,
                status: BackendStatus::initial(data_dir, versions),
                generation: 0,
                consecutive_failures: 0,
                shutting_down: false,
            })),
            update_lock: Arc::new(AsyncMutex::new(())),
        }
    }

    fn snapshot(&self) -> BackendStatus {
        self.inner
            .lock()
            .expect("backend state poisoned")
            .status
            .clone()
    }

    fn update_status(&self, update: impl FnOnce(&mut BackendStatus)) -> BackendStatus {
        let mut inner = self.inner.lock().expect("backend state poisoned");
        update(&mut inner.status);
        inner.status.clone()
    }
}

#[derive(Clone, Debug, Default, Deserialize, Serialize)]
struct CoreVersionRecord {
    version: String,
    algorithm_version: String,
    sha256: String,
    #[serde(default)]
    upstream_commit: String,
}

#[derive(Clone, Debug, Default, Deserialize, Serialize)]
struct CoreState {
    active: Option<CoreVersionRecord>,
    previous: Option<CoreVersionRecord>,
    pending: Option<CoreVersionRecord>,
    pending_validation: bool,
    last_error: Option<String>,
}

#[derive(Clone, Debug)]
pub struct CoreVersions {
    current_version: String,
    current_algorithm_version: String,
    upstream_commit: String,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct CoreArtifact {
    pub url: String,
    pub archive_name: String,
    pub sha256: String,
    pub size: u64,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct CoreUpdateManifest {
    pub schema: u32,
    pub version: String,
    pub algorithm_version: String,
    #[serde(default)]
    pub upstream_commit: String,
    pub published_at: String,
    pub notes: String,
    pub release_url: String,
    pub platforms: HashMap<String, CoreArtifact>,
}

#[derive(Clone, Debug, Deserialize)]
struct GitHubReleaseAsset {
    name: String,
    browser_download_url: String,
    size: u64,
}

#[derive(Clone, Debug, Deserialize)]
struct GitHubRelease {
    tag_name: String,
    published_at: String,
    #[serde(default)]
    body: String,
    html_url: String,
    assets: Vec<GitHubReleaseAsset>,
}

#[derive(Clone, Debug, Serialize)]
pub struct CoreUpdateCheck {
    pub available: bool,
    pub current_version: String,
    pub current_algorithm_version: String,
    pub upstream_commit: String,
    pub update: Option<CoreUpdateManifest>,
    pub previous_version: Option<String>,
}

#[derive(Clone, Debug, Serialize)]
pub struct CoreInstallResult {
    pub version: String,
    pub algorithm_version: String,
    pub upstream_commit: String,
    pub restart_required: bool,
}

#[derive(Clone, Debug, Serialize)]
struct CoreUpdateProgress {
    stage: String,
    downloaded: u64,
    total: Option<u64>,
    message: String,
}

fn backend_data_dir(app: &AppHandle) -> Result<PathBuf, String> {
    app.path()
        .app_data_dir()
        .map(|path| path.join("backend"))
        .map_err(|error| format!("无法定位应用数据目录: {error}"))
}

fn core_dir(app: &AppHandle) -> Result<PathBuf, String> {
    app.path()
        .app_data_dir()
        .map(|path| path.join("core"))
        .map_err(|error| format!("无法定位内核目录: {error}"))
}

fn active_core_path(app: &AppHandle) -> Result<PathBuf, String> {
    Ok(core_dir(app)?.join("active").join("sub2api-backend.exe"))
}

fn previous_core_path(app: &AppHandle) -> Result<PathBuf, String> {
    Ok(core_dir(app)?.join("previous").join("sub2api-backend.exe"))
}

fn pending_core_path(app: &AppHandle) -> Result<PathBuf, String> {
    Ok(core_dir(app)?.join("pending").join("sub2api-backend.exe"))
}

fn core_state_path(app: &AppHandle) -> Result<PathBuf, String> {
    Ok(core_dir(app)?.join("state.json"))
}

fn bundled_core_path() -> Result<PathBuf, String> {
    let executable =
        std::env::current_exe().map_err(|error| format!("无法定位桌面程序: {error}"))?;
    let directory = executable
        .parent()
        .ok_or_else(|| "桌面程序路径没有父目录".to_string())?;
    Ok(directory.join(format!("{BACKEND_SIDECAR_NAME}.exe")))
}

fn load_core_state(app: &AppHandle) -> CoreState {
    let Ok(path) = core_state_path(app) else {
        return CoreState::default();
    };
    let decode = |candidate: &Path| {
        fs::read(candidate)
            .ok()
            .and_then(|bytes| serde_json::from_slice(&bytes).ok())
    };
    decode(&path)
        .or_else(|| decode(&path.with_extension("json.bak")))
        .unwrap_or_default()
}

fn save_core_state(app: &AppHandle, state: &CoreState) -> Result<(), String> {
    let path = core_state_path(app)?;
    let parent = path
        .parent()
        .ok_or_else(|| "内核状态路径无效".to_string())?;
    fs::create_dir_all(parent).map_err(|error| format!("无法创建内核状态目录: {error}"))?;
    let bytes =
        serde_json::to_vec_pretty(state).map_err(|error| format!("无法序列化内核状态: {error}"))?;
    let temporary = path.with_extension("json.tmp");
    let backup = path.with_extension("json.bak");
    fs::write(&temporary, bytes).map_err(|error| format!("无法写入内核状态: {error}"))?;
    if path.exists() {
        fs::copy(&path, &backup).map_err(|error| format!("无法备份内核状态: {error}"))?;
        fs::remove_file(&path).map_err(|error| format!("无法替换内核状态: {error}"))?;
    }
    fs::rename(&temporary, &path).map_err(|error| format!("无法提交内核状态: {error}"))?;
    let _ = fs::remove_file(backup);
    Ok(())
}

fn sha256_file(path: &Path) -> Result<String, String> {
    let bytes = fs::read(path).map_err(|error| format!("无法读取内核文件: {error}"))?;
    Ok(hex::encode(Sha256::digest(bytes)))
}

fn current_core_versions(app: &AppHandle) -> CoreVersions {
    let state = load_core_state(app);
    if let Some(active) = state.active {
        return CoreVersions {
            current_version: active.version,
            current_algorithm_version: active.algorithm_version,
            upstream_commit: if active.upstream_commit.is_empty() {
                UPSTREAM_SUB2API_COMMIT.to_string()
            } else {
                active.upstream_commit
            },
        };
    }
    CoreVersions {
        current_version: CORE_VERSION.to_string(),
        current_algorithm_version: ALGORITHM_VERSION.to_string(),
        upstream_commit: UPSTREAM_SUB2API_COMMIT.to_string(),
    }
}

pub fn activate_pending_core(app: &AppHandle) -> Result<(), String> {
    let mut state = load_core_state(app);
    let Some(pending) = state.pending.clone() else {
        return Ok(());
    };
    let pending_path = pending_core_path(app)?;
    if !pending_path.is_file() {
        let active_path = active_core_path(app)?;
        if active_path.is_file()
            && !pending.sha256.is_empty()
            && sha256_file(&active_path)?.eq_ignore_ascii_case(&pending.sha256)
        {
            let previous = state.active.clone().unwrap_or(CoreVersionRecord {
                version: CORE_VERSION.to_string(),
                algorithm_version: ALGORITHM_VERSION.to_string(),
                sha256: String::new(),
                upstream_commit: UPSTREAM_SUB2API_COMMIT.to_string(),
            });
            state.previous = Some(previous);
            state.active = Some(pending);
            state.pending = None;
            state.pending_validation = true;
            state.last_error = None;
            return save_core_state(app, &state);
        }
        state.pending = None;
        state.last_error = Some("待安装内核文件不存在，已取消本次更新".into());
        save_core_state(app, &state)?;
        return Ok(());
    }

    let active_path = active_core_path(app)?;
    let previous_path = previous_core_path(app)?;
    if let Some(parent) = active_path.parent() {
        fs::create_dir_all(parent).map_err(|error| format!("无法创建活动内核目录: {error}"))?;
    }
    if let Some(parent) = previous_path.parent() {
        fs::create_dir_all(parent).map_err(|error| format!("无法创建回滚目录: {error}"))?;
    }

    let current_record = state.active.clone().unwrap_or(CoreVersionRecord {
        version: CORE_VERSION.to_string(),
        algorithm_version: ALGORITHM_VERSION.to_string(),
        sha256: String::new(),
        upstream_commit: UPSTREAM_SUB2API_COMMIT.to_string(),
    });
    let current_path = if active_path.is_file() {
        active_path.clone()
    } else {
        bundled_core_path()?
    };
    if !current_path.is_file() {
        return Err(format!("当前内核文件不存在: {}", current_path.display()));
    }

    fs::copy(&current_path, &previous_path)
        .map_err(|error| format!("无法保留上一版内核: {error}"))?;
    if active_path.exists() {
        fs::remove_file(&active_path).map_err(|error| format!("无法替换活动内核: {error}"))?;
    }
    fs::rename(&pending_path, &active_path)
        .or_else(|_| {
            fs::copy(&pending_path, &active_path)?;
            fs::remove_file(&pending_path)
        })
        .map_err(|error| format!("无法激活新内核: {error}"))?;

    state.previous = Some(current_record);
    state.active = Some(pending);
    state.pending = None;
    state.pending_validation = true;
    state.last_error = None;
    save_core_state(app, &state)
}

pub fn initialize_backend(app: &AppHandle) -> Result<BackendSupervisor, String> {
    activate_pending_core(app)?;
    let data_dir = backend_data_dir(app)?;
    let versions = current_core_versions(app);
    Ok(BackendSupervisor::new(&data_dir, &versions))
}

fn port_is_open() -> bool {
    let address = SocketAddr::from(([127, 0, 0, 1], BACKEND_PORT));
    TcpStream::connect_timeout(&address, Duration::from_millis(250)).is_ok()
}

fn emit_backend_status(app: &AppHandle, supervisor: &BackendSupervisor) {
    let _ = app.emit("desktop-backend-status", supervisor.snapshot());
}

fn ensure_curated_model_pricing(data_dir: &Path) -> Result<(), String> {
    let pricing_dir = data_dir.join("resources").join("model-pricing");
    fs::create_dir_all(&pricing_dir).map_err(|error| format!("无法创建模型价格目录: {error}"))?;
    fs::write(
        pricing_dir.join("model_prices_and_context_window.json"),
        CURATED_MODEL_PRICING,
    )
    .map_err(|error| format!("无法写入内置模型价格目录: {error}"))
}

pub fn start_backend(app: AppHandle, supervisor: BackendSupervisor) -> Result<(), String> {
    {
        let inner = supervisor.inner.lock().expect("backend state poisoned");
        if inner.child.is_some() || inner.shutting_down {
            return Ok(());
        }
    }

    if port_is_open() {
        supervisor.update_status(|status| {
            status.phase = BackendPhase::Ready;
            status.managed = false;
            status.pid = None;
            status.message = "已连接本机现有 Sub2API 服务".into();
        });
        emit_backend_status(&app, &supervisor);
        return Ok(());
    }

    let data_dir = backend_data_dir(&app)?;
    fs::create_dir_all(&data_dir).map_err(|error| format!("无法创建后端数据目录: {error}"))?;
    ensure_curated_model_pricing(&data_dir)?;
    let active_path = active_core_path(&app)?;
    let executable = if active_path.is_file() {
        active_path
    } else {
        bundled_core_path()?
    };
    if !executable.is_file() {
        let message = format!("安装包缺少 Sub2API 内核: {}", executable.display());
        supervisor.update_status(|status| {
            status.phase = BackendPhase::Error;
            status.message = message.clone();
        });
        emit_backend_status(&app, &supervisor);
        return Err(message);
    }

    let (mut events, child) = app
        .shell()
        .command(&executable)
        .current_dir(&data_dir)
        .env("DATA_DIR", &data_dir)
        .env("SERVER_HOST", BACKEND_HOST)
        .env("SERVER_PORT", BACKEND_PORT.to_string())
        .env("SUB2API_DESKTOP", "1")
        .env(
            "SUB2API_DESKTOP_RETURN_URL",
            "http://tauri.localhost/index.html#/admin/cost-center?desktop=1",
        )
        .spawn()
        .map_err(|error| format!("无法启动 Sub2API 内核: {error}"))?;

    let pid = child.pid();
    let generation = {
        let mut inner = supervisor.inner.lock().expect("backend state poisoned");
        inner.generation += 1;
        inner.child = Some(child);
        inner.status.phase = BackendPhase::Starting;
        inner.status.managed = true;
        inner.status.pid = Some(pid);
        inner.status.message = "Sub2API 内核已启动，正在等待服务就绪".into();
        inner.generation
    };
    emit_backend_status(&app, &supervisor);

    let event_app = app.clone();
    let event_supervisor = supervisor.clone();
    tauri::async_runtime::spawn(async move {
        while let Some(event) = events.recv().await {
            match event {
                CommandEvent::Stdout(bytes) | CommandEvent::Stderr(bytes) => {
                    let line = String::from_utf8_lossy(&bytes).trim().to_string();
                    if !line.is_empty() {
                        event_supervisor.update_status(|status| status.last_log = line);
                    }
                }
                CommandEvent::Error(error) => {
                    event_supervisor.update_status(|status| status.last_log = error);
                }
                CommandEvent::Terminated(payload) => {
                    let should_restart = {
                        let mut inner = event_supervisor
                            .inner
                            .lock()
                            .expect("backend state poisoned");
                        if inner.generation != generation {
                            false
                        } else {
                            inner.child = None;
                            inner.status.pid = None;
                            inner.consecutive_failures += 1;
                            let should_restart =
                                !inner.shutting_down && inner.consecutive_failures <= 5;
                            inner.status.phase = if should_restart {
                                BackendPhase::Starting
                            } else {
                                BackendPhase::Error
                            };
                            inner.status.message = if should_restart {
                                format!("内核已退出（{:?}），正在自动重启", payload.code)
                            } else {
                                "内核连续退出，已停止自动重启；请查看诊断信息".into()
                            };
                            should_restart
                        }
                    };
                    emit_backend_status(&event_app, &event_supervisor);
                    if should_restart {
                        sleep(Duration::from_millis(700)).await;
                        let _ = start_backend(event_app.clone(), event_supervisor.clone());
                    }
                    break;
                }
                _ => {}
            }
        }
    });

    let probe_app = app.clone();
    let probe_supervisor = supervisor.clone();
    tauri::async_runtime::spawn(async move {
        probe_backend(probe_app, probe_supervisor, generation).await;
    });
    Ok(())
}

async fn probe_backend(app: AppHandle, supervisor: BackendSupervisor, generation: u64) {
    let client = match Client::builder()
        .timeout(Duration::from_secs(2))
        .user_agent("Sub2API-Cost-Console")
        .build()
    {
        Ok(client) => client,
        Err(error) => {
            supervisor.update_status(|status| {
                status.phase = BackendPhase::Error;
                status.message = format!("无法创建本地健康检查: {error}");
            });
            emit_backend_status(&app, &supervisor);
            return;
        }
    };

    for _ in 0..60 {
        let still_current = supervisor
            .inner
            .lock()
            .expect("backend state poisoned")
            .generation
            == generation;
        if !still_current {
            return;
        }
        let setup = client
            .get(format!("http://{BACKEND_HOST}:{BACKEND_PORT}/setup/status"))
            .send()
            .await;
        if setup
            .as_ref()
            .is_ok_and(|response| response.status().is_success())
        {
            {
                let mut inner = supervisor.inner.lock().expect("backend state poisoned");
                if inner.generation != generation {
                    return;
                }
                inner.consecutive_failures = 0;
                inner.status.phase = BackendPhase::Ready;
                inner.status.message = "Sub2API 内核已就绪".into();
            }
            let mut core_state = load_core_state(&app);
            if core_state.pending_validation {
                core_state.pending_validation = false;
                core_state.last_error = None;
                let _ = save_core_state(&app, &core_state);
                let _ = app.emit(
                    "core-update-validated",
                    current_core_versions(&app).current_version,
                );
            }
            emit_backend_status(&app, &supervisor);
            return;
        }
        sleep(Duration::from_millis(500)).await;
    }

    let pending_validation = load_core_state(&app).pending_validation;
    if pending_validation {
        let failure = "新内核未通过启动健康检查，已自动回滚".to_string();
        stop_backend_internal(&supervisor, false);
        sleep(Duration::from_millis(400)).await;
        if let Err(error) = restore_previous_core(&app, &failure) {
            supervisor.update_status(|status| {
                status.phase = BackendPhase::Error;
                status.message = format!("{failure}，但恢复失败: {error}");
            });
        } else {
            let versions = current_core_versions(&app);
            supervisor.update_status(|status| {
                status.core_version = versions.current_version;
                status.algorithm_version = versions.current_algorithm_version;
                status.upstream_commit = versions.upstream_commit;
                status.phase = BackendPhase::Starting;
                status.message = failure.clone();
            });
            let _ = app.emit("core-update-rollback", failure);
            let _ = start_backend(app.clone(), supervisor.clone());
        }
    } else {
        supervisor.update_status(|status| {
            status.phase = BackendPhase::Error;
            status.message = "Sub2API 内核启动超时；请检查 PostgreSQL、Redis 与诊断日志".into();
        });
    }
    emit_backend_status(&app, &supervisor);
}

fn stop_backend_internal(supervisor: &BackendSupervisor, shutting_down: bool) {
    let child = {
        let mut inner = supervisor.inner.lock().expect("backend state poisoned");
        inner.generation += 1;
        inner.shutting_down = shutting_down;
        inner.status.phase = BackendPhase::Stopped;
        inner.status.pid = None;
        inner.status.message = if shutting_down {
            "桌面端正在退出".into()
        } else {
            "内核已停止".into()
        };
        inner.child.take()
    };
    if let Some(child) = child {
        let _ = child.kill();
    }
}

/// Stop the managed sidecar and wait until its listening socket is released.
/// Tauri's `relaunch` can otherwise race child shutdown on Windows.
#[tauri::command]
pub async fn desktop_backend_prepare_relaunch(
    supervisor: tauri::State<'_, BackendSupervisor>,
) -> Result<(), String> {
    stop_backend_internal(&supervisor, false);
    for _ in 0..50 {
        if !port_is_open() {
            return Ok(());
        }
        sleep(Duration::from_millis(100)).await;
    }
    Err("本地内核仍在退出，无法安全重启桌面端；请稍后重试".into())
}

pub fn shutdown_backend(app: &AppHandle) {
    if let Some(supervisor) = app.try_state::<BackendSupervisor>() {
        stop_backend_internal(&supervisor, true);
    }
}

fn restore_previous_core(app: &AppHandle, reason: &str) -> Result<(), String> {
    let mut state = load_core_state(app);
    let previous = state
        .previous
        .clone()
        .ok_or_else(|| "没有可回滚的上一版内核".to_string())?;
    let previous_path = previous_core_path(app)?;
    let active_path = active_core_path(app)?;
    if !previous_path.is_file() {
        return Err("上一版内核文件不存在".into());
    }
    if let Some(parent) = active_path.parent() {
        fs::create_dir_all(parent).map_err(|error| format!("无法创建活动内核目录: {error}"))?;
    }
    fs::copy(&previous_path, &active_path)
        .map_err(|error| format!("无法恢复上一版内核: {error}"))?;
    state.active = Some(previous);
    state.previous = None;
    state.pending = None;
    state.pending_validation = false;
    state.last_error = Some(reason.to_string());
    save_core_state(app, &state)
}

fn validate_https_url(value: &str) -> Result<Url, String> {
    let url = Url::parse(value).map_err(|error| format!("更新地址无效: {error}"))?;
    if url.scheme() != "https" {
        return Err("更新地址必须使用 HTTPS".into());
    }
    Ok(url)
}

fn validate_upstream_asset_url(value: &str, tag: &str, filename: &str) -> Result<Url, String> {
    let url = validate_https_url(value)?;
    let expected_path = format!("/{UPSTREAM_REPOSITORY}/releases/download/{tag}/{filename}");
    if url.host_str() != Some("github.com") || url.path() != expected_path {
        return Err(format!(
            "上游资产地址不属于固定仓库 {UPSTREAM_REPOSITORY}: {value}"
        ));
    }
    Ok(url)
}

fn checksum_for_asset(checksums: &str, filename: &str) -> Result<String, String> {
    for line in checksums.lines() {
        let mut fields = line.split_whitespace();
        let Some(checksum) = fields.next() else {
            continue;
        };
        let Some(name) = fields.next() else {
            continue;
        };
        if name.trim_start_matches('*') == filename {
            let normalized = checksum.trim().to_ascii_lowercase();
            if normalized.len() == 64 && normalized.bytes().all(|byte| byte.is_ascii_hexdigit()) {
                return Ok(normalized);
            }
            return Err(format!("{filename} 的上游 SHA-256 格式无效"));
        }
    }
    Err(format!("上游 checksums.txt 未包含 {filename}"))
}

fn build_upstream_manifest(
    release: GitHubRelease,
    checksums: &str,
) -> Result<CoreUpdateManifest, String> {
    let version = release.tag_name.trim_start_matches('v').trim().to_string();
    Version::parse(&version).map_err(|error| format!("上游内核版本号无效: {error}"))?;
    if version.is_empty() || release.tag_name != format!("v{version}") {
        return Err(format!(
            "上游 Release 标签格式不受支持: {}",
            release.tag_name
        ));
    }

    let archive_name = format!("sub2api_{version}_windows_amd64.zip");
    let archive = release
        .assets
        .iter()
        .find(|asset| asset.name == archive_name)
        .ok_or_else(|| format!("上游 Release 缺少 Windows x64 资产 {archive_name}"))?;
    validate_upstream_asset_url(
        &archive.browser_download_url,
        &release.tag_name,
        &archive_name,
    )?;
    if archive.size == 0 || archive.size > MAX_CORE_ARCHIVE_BYTES {
        return Err(format!("上游内核压缩包大小异常: {} 字节", archive.size));
    }

    let sha256 = checksum_for_asset(checksums, &archive_name)?;
    let mut platforms = HashMap::new();
    platforms.insert(
        "windows-x86_64".to_string(),
        CoreArtifact {
            url: archive.browser_download_url.clone(),
            archive_name,
            sha256,
            size: archive.size,
        },
    );

    Ok(CoreUpdateManifest {
        schema: 1,
        version,
        algorithm_version: ALGORITHM_VERSION.to_string(),
        upstream_commit: String::new(),
        published_at: release.published_at,
        notes: release.body,
        release_url: release.html_url,
        platforms,
    })
}

fn update_client() -> Result<Client, String> {
    Client::builder()
        .connect_timeout(Duration::from_secs(20))
        .timeout(Duration::from_secs(10 * 60))
        .user_agent("Sub2API-Cost-Console-Updater")
        .build()
        .map_err(|error| format!("无法创建更新客户端: {error}"))
}

async fn fetch_upstream_manifest(client: &Client) -> Result<CoreUpdateManifest, String> {
    let release = client
        .get(UPSTREAM_RELEASE_API)
        .header("Cache-Control", "no-cache")
        .header("Accept", "application/vnd.github+json")
        .header("X-GitHub-Api-Version", "2022-11-28")
        .send()
        .await
        .map_err(|error| format!("无法扫描 Wei-Shaw/sub2api 上游 Release: {error}"))?
        .error_for_status()
        .map_err(|error| format!("Wei-Shaw/sub2api Release API 返回错误: {error}"))?
        .json::<GitHubRelease>()
        .await
        .map_err(|error| format!("无法解析 Wei-Shaw/sub2api Release: {error}"))?;

    let checksums_asset = release
        .assets
        .iter()
        .find(|asset| asset.name == UPSTREAM_CHECKSUM_ASSET)
        .ok_or_else(|| "上游 Release 缺少 checksums.txt，已拒绝更新".to_string())?;
    let checksums_url = validate_upstream_asset_url(
        &checksums_asset.browser_download_url,
        &release.tag_name,
        UPSTREAM_CHECKSUM_ASSET,
    )?;
    let checksums = client
        .get(checksums_url)
        .header("Cache-Control", "no-cache")
        .send()
        .await
        .map_err(|error| format!("无法获取上游 checksums.txt: {error}"))?
        .error_for_status()
        .map_err(|error| format!("上游 checksums.txt 返回错误: {error}"))?
        .text()
        .await
        .map_err(|error| format!("无法读取上游 checksums.txt: {error}"))?;

    build_upstream_manifest(release, &checksums)
}

fn platform_key() -> Result<&'static str, String> {
    #[cfg(all(target_os = "windows", target_arch = "x86_64"))]
    {
        return Ok("windows-x86_64");
    }
    #[allow(unreachable_code)]
    Err("当前平台暂不支持独立内核更新".into())
}

async fn extract_upstream_core(archive_path: PathBuf, target_path: PathBuf) -> Result<(), String> {
    tokio::task::spawn_blocking(move || {
        let archive_file = fs::File::open(&archive_path)
            .map_err(|error| format!("无法打开上游内核压缩包: {error}"))?;
        let mut archive = zip::ZipArchive::new(archive_file)
            .map_err(|error| format!("上游内核压缩包格式无效: {error}"))?;
        let mut entry = archive
            .by_name("sub2api.exe")
            .map_err(|_| "上游 Windows 压缩包中缺少 sub2api.exe".to_string())?;
        if entry.is_dir() || entry.size() == 0 || entry.size() > MAX_CORE_ARCHIVE_BYTES {
            return Err(format!("上游 sub2api.exe 大小异常: {} 字节", entry.size()));
        }
        if let Some(parent) = target_path.parent() {
            fs::create_dir_all(parent)
                .map_err(|error| format!("无法创建待更新内核目录: {error}"))?;
        }
        let mut output = fs::File::create(&target_path)
            .map_err(|error| format!("无法创建待验证内核: {error}"))?;
        let copied = std::io::copy(&mut entry, &mut output)
            .map_err(|error| format!("无法解压上游内核: {error}"))?;
        output
            .flush()
            .map_err(|error| format!("无法写入待验证内核: {error}"))?;
        if copied != entry.size() {
            return Err(format!(
                "上游内核解压大小不一致：期望 {}，实际 {copied}",
                entry.size()
            ));
        }
        Ok(())
    })
    .await
    .map_err(|error| format!("内核解压任务失败: {error}"))?
}

fn parse_verified_core_build(output: &str, expected_version: &str) -> Result<String, String> {
    let version_marker = format!("Sub2API {expected_version} ");
    if !output.contains(&version_marker) {
        return Err(format!(
            "下载的内核版本与上游 Release 不一致，期望 {expected_version}"
        ));
    }
    let commit = output
        .split("commit:")
        .nth(1)
        .and_then(|value| value.split([',', ')']).next())
        .map(str::trim)
        .filter(|value| !value.is_empty())
        .ok_or_else(|| "下载的内核未报告真实上游提交号".to_string())?;
    if commit.len() < 7 || !commit.bytes().all(|byte| byte.is_ascii_hexdigit()) {
        return Err("下载的内核报告了无效的上游提交号".into());
    }
    Ok(commit.to_string())
}

async fn verify_upstream_core_build(path: &Path, expected_version: &str) -> Result<String, String> {
    let mut command = tokio::process::Command::new(path);
    command
        .arg("--version")
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped());
    let output = timeout(Duration::from_secs(15), command.output())
        .await
        .map_err(|_| "验证上游内核版本超时".to_string())?
        .map_err(|error| format!("无法执行待验证内核: {error}"))?;
    if !output.status.success() {
        return Err(format!("待验证内核退出码异常: {}", output.status));
    }
    let combined = format!(
        "{}\n{}",
        String::from_utf8_lossy(&output.stdout),
        String::from_utf8_lossy(&output.stderr)
    );
    parse_verified_core_build(&combined, expected_version)
}

fn emit_core_progress(
    app: &AppHandle,
    stage: &str,
    downloaded: u64,
    total: Option<u64>,
    message: &str,
) {
    let _ = app.emit(
        "core-update-progress",
        CoreUpdateProgress {
            stage: stage.into(),
            downloaded,
            total,
            message: message.into(),
        },
    );
}

#[tauri::command]
pub fn desktop_backend_status(supervisor: tauri::State<'_, BackendSupervisor>) -> BackendStatus {
    supervisor.snapshot()
}

#[tauri::command]
pub fn desktop_backend_start(
    app: AppHandle,
    supervisor: tauri::State<'_, BackendSupervisor>,
) -> Result<BackendStatus, String> {
    {
        let mut inner = supervisor.inner.lock().expect("backend state poisoned");
        inner.shutting_down = false;
        inner.consecutive_failures = 0;
    }
    start_backend(app.clone(), supervisor.inner().clone())?;
    Ok(supervisor.snapshot())
}

#[tauri::command]
pub fn desktop_backend_stop(supervisor: tauri::State<'_, BackendSupervisor>) -> BackendStatus {
    stop_backend_internal(&supervisor, false);
    supervisor.snapshot()
}

#[tauri::command]
pub async fn check_core_update(app: AppHandle) -> Result<CoreUpdateCheck, String> {
    let client = update_client()?;
    let manifest = fetch_upstream_manifest(&client).await?;
    let current = current_core_versions(&app);
    let current_version = Version::parse(current.current_version.trim_start_matches('v'))
        .map_err(|error| format!("当前内核版本无效: {error}"))?;
    let remote_version = Version::parse(manifest.version.trim_start_matches('v'))
        .map_err(|error| format!("远端内核版本无效: {error}"))?;
    let previous_version = load_core_state(&app).previous.map(|record| record.version);
    Ok(CoreUpdateCheck {
        available: remote_version > current_version,
        current_version: current.current_version,
        current_algorithm_version: current.current_algorithm_version,
        upstream_commit: current.upstream_commit,
        update: Some(manifest),
        previous_version,
    })
}

#[tauri::command]
pub async fn install_core_update(
    app: AppHandle,
    supervisor: tauri::State<'_, BackendSupervisor>,
) -> Result<CoreInstallResult, String> {
    let _guard = supervisor.update_lock.lock().await;
    let client = update_client()?;
    emit_core_progress(
        &app,
        "checking",
        0,
        None,
        "正在扫描上游 Release 与 checksums",
    );
    let manifest = fetch_upstream_manifest(&client).await?;
    let current = current_core_versions(&app);
    let current_version = Version::parse(current.current_version.trim_start_matches('v'))
        .map_err(|error| format!("当前内核版本无效: {error}"))?;
    let remote_version = Version::parse(manifest.version.trim_start_matches('v'))
        .map_err(|error| format!("远端内核版本无效: {error}"))?;
    if remote_version <= current_version {
        return Err("当前内核已是最新版本".into());
    }

    let artifact = manifest
        .platforms
        .get(platform_key()?)
        .ok_or_else(|| "更新清单不包含当前 Windows 架构".to_string())?
        .clone();
    let artifact_url = validate_upstream_asset_url(
        &artifact.url,
        &format!("v{}", manifest.version),
        &artifact.archive_name,
    )?;

    let mut response = client
        .get(artifact_url)
        .send()
        .await
        .map_err(|error| format!("无法下载内核: {error}"))?
        .error_for_status()
        .map_err(|error| format!("内核下载返回错误: {error}"))?;
    let total = response.content_length().or(Some(artifact.size));
    if total.is_some_and(|value| value > MAX_CORE_ARCHIVE_BYTES) {
        return Err("内核更新文件超过 300 MB 安全上限".into());
    }
    let pending_path = pending_core_path(&app)?;
    if let Some(parent) = pending_path.parent() {
        tokio::fs::create_dir_all(parent)
            .await
            .map_err(|error| format!("无法创建待更新目录: {error}"))?;
    }
    let temporary_archive = pending_path.with_extension("zip.download");
    let temporary_core = pending_path.with_extension("exe.download");
    let _ = tokio::fs::remove_file(&temporary_archive).await;
    let _ = tokio::fs::remove_file(&temporary_core).await;
    let mut file = tokio::fs::File::create(&temporary_archive)
        .await
        .map_err(|error| format!("无法创建内核压缩包下载文件: {error}"))?;
    let mut downloaded = 0_u64;
    let mut hasher = Sha256::new();
    while let Some(chunk) = response
        .chunk()
        .await
        .map_err(|error| format!("内核下载中断: {error}"))?
    {
        downloaded += chunk.len() as u64;
        if downloaded > MAX_CORE_ARCHIVE_BYTES {
            return Err("内核更新文件超过 300 MB 安全上限".into());
        }
        hasher.update(&chunk);
        file.write_all(&chunk)
            .await
            .map_err(|error| format!("无法写入内核下载文件: {error}"))?;
        emit_core_progress(
            &app,
            "downloading",
            downloaded,
            total,
            "正在下载上游 Windows 内核包",
        );
    }
    file.flush()
        .await
        .map_err(|error| format!("无法刷新内核下载文件: {error}"))?;
    drop(file);

    let archive_sha256 = hex::encode(hasher.finalize());
    if !archive_sha256.eq_ignore_ascii_case(artifact.sha256.trim()) {
        let _ = tokio::fs::remove_file(&temporary_archive).await;
        return Err(format!(
            "上游压缩包 SHA-256 校验失败：期望 {}，实际 {}",
            artifact.sha256, archive_sha256
        ));
    }

    emit_core_progress(
        &app,
        "extracting",
        downloaded,
        total,
        "上游 SHA-256 已通过，正在提取 sub2api.exe",
    );
    if let Err(error) =
        extract_upstream_core(temporary_archive.clone(), temporary_core.clone()).await
    {
        let _ = tokio::fs::remove_file(&temporary_archive).await;
        let _ = tokio::fs::remove_file(&temporary_core).await;
        return Err(error);
    }
    let upstream_commit = match verify_upstream_core_build(&temporary_core, &manifest.version).await
    {
        Ok(commit) => commit,
        Err(error) => {
            let _ = tokio::fs::remove_file(&temporary_archive).await;
            let _ = tokio::fs::remove_file(&temporary_core).await;
            return Err(error);
        }
    };
    let core_sha256 = sha256_file(&temporary_core)?;
    emit_core_progress(
        &app,
        "verified",
        downloaded,
        total,
        "上游 checksums、内核版本与提交号验证通过",
    );
    let _ = tokio::fs::remove_file(&temporary_archive).await;

    if pending_path.exists() {
        tokio::fs::remove_file(&pending_path)
            .await
            .map_err(|error| format!("无法清理旧的待更新内核: {error}"))?;
    }
    tokio::fs::rename(&temporary_core, &pending_path)
        .await
        .map_err(|error| format!("无法暂存内核更新: {error}"))?;
    let mut state = load_core_state(&app);
    state.pending = Some(CoreVersionRecord {
        version: manifest.version.clone(),
        algorithm_version: manifest.algorithm_version.clone(),
        sha256: core_sha256,
        upstream_commit: upstream_commit.clone(),
    });
    state.last_error = None;
    save_core_state(&app, &state)?;
    emit_core_progress(
        &app,
        "ready",
        downloaded,
        total,
        "内核更新已就绪，等待安全重启",
    );
    Ok(CoreInstallResult {
        version: manifest.version,
        algorithm_version: manifest.algorithm_version,
        upstream_commit,
        restart_required: true,
    })
}

#[tauri::command]
pub fn prepare_core_rollback(app: AppHandle) -> Result<CoreInstallResult, String> {
    let mut state = load_core_state(&app);
    let previous = state
        .previous
        .clone()
        .ok_or_else(|| "没有保留可回滚的上一版内核".to_string())?;
    let source = previous_core_path(&app)?;
    let pending = pending_core_path(&app)?;
    if !source.is_file() {
        return Err("上一版内核文件不存在".into());
    }
    if let Some(parent) = pending.parent() {
        fs::create_dir_all(parent).map_err(|error| format!("无法创建回滚暂存目录: {error}"))?;
    }
    fs::copy(&source, &pending).map_err(|error| format!("无法暂存上一版内核: {error}"))?;
    state.pending = Some(previous.clone());
    state.last_error = None;
    save_core_state(&app, &state)?;
    Ok(CoreInstallResult {
        version: previous.version,
        algorithm_version: previous.algorithm_version,
        upstream_commit: previous.upstream_commit,
        restart_required: true,
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn rejects_non_https_update_urls() {
        assert!(validate_https_url("http://example.com/core.exe").is_err());
        assert!(validate_https_url("https://example.com/core.exe").is_ok());
    }

    #[test]
    fn accepts_only_fixed_upstream_release_assets() {
        let accepted = validate_upstream_asset_url(
            "https://github.com/Wei-Shaw/sub2api/releases/download/v0.1.171/sub2api_0.1.171_windows_amd64.zip",
            "v0.1.171",
            "sub2api_0.1.171_windows_amd64.zip",
        );
        assert!(accepted.is_ok());
        assert!(validate_upstream_asset_url(
            "https://github.com/example/sub2api/releases/download/v0.1.171/sub2api_0.1.171_windows_amd64.zip",
            "v0.1.171",
            "sub2api_0.1.171_windows_amd64.zip",
        )
        .is_err());
    }

    #[test]
    fn builds_windows_manifest_from_upstream_release_and_checksum() {
        let archive_name = "sub2api_0.1.171_windows_amd64.zip";
        let archive_url = format!(
            "https://github.com/Wei-Shaw/sub2api/releases/download/v0.1.171/{archive_name}"
        );
        let manifest = build_upstream_manifest(
            GitHubRelease {
                tag_name: "v0.1.171".into(),
                published_at: "2026-08-04T13:41:32Z".into(),
                body: "release notes".into(),
                html_url: "https://github.com/Wei-Shaw/sub2api/releases/tag/v0.1.171".into(),
                assets: vec![GitHubReleaseAsset {
                    name: archive_name.into(),
                    browser_download_url: archive_url,
                    size: 36_048_475,
                }],
            },
            &format!("{}  {archive_name}", "a".repeat(64)),
        )
        .expect("manifest should be valid");

        assert_eq!(manifest.version, "0.1.171");
        assert_eq!(manifest.algorithm_version, ALGORITHM_VERSION);
        assert_eq!(manifest.platforms["windows-x86_64"].sha256, "a".repeat(64));
    }

    #[test]
    fn parses_version_and_real_commit_from_downloaded_core() {
        let commit = parse_verified_core_build(
            "Sub2API 0.1.171 (commit: f0e7a9c7a23a7d02fb159b62fa809621eb0475a6, built: 2026-08-04T13:31:28Z)",
            "0.1.171",
        )
        .expect("version output should be accepted");
        assert_eq!(commit, "f0e7a9c7a23a7d02fb159b62fa809621eb0475a6");
        assert!(parse_verified_core_build("Sub2API 0.1.170 (commit: abcdef0)", "0.1.171").is_err());
    }

    #[test]
    fn bundled_versions_are_semver() {
        assert!(Version::parse(env!("CARGO_PKG_VERSION")).is_ok());
        assert!(Version::parse(CORE_VERSION).is_ok());
        assert!(Version::parse(ALGORITHM_VERSION).is_ok());
    }
}
