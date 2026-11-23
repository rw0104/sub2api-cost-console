#[cfg(target_os = "macos")]
mod macos_clients;
#[cfg(target_os = "macos")]
mod macos_terminal;
#[cfg(any(windows, test))]
mod windows_clients;

use reqwest::Url;
use serde::{Deserialize, Serialize};
use std::{
    env,
    io::Read,
    path::{Path, PathBuf},
    process::{Command, Stdio},
    sync::mpsc,
    time::{Duration, Instant},
};

const CODEX_API_KEY_ENV: &str = "SUB2API_CODEX_API_KEY";
const CURSOR_API_KEY_ENV: &str = "CURSOR_API_KEY";
const GROK_BASE_URL_ENV: &str = "GROK_MODELS_BASE_URL";
const GROK_API_KEY_ENV: &str = "XAI_API_KEY";
const OPENCODE_API_KEY_ENV: &str = "SUB2API_OPENCODE_API_KEY";
const OPENCODE_CONFIG_ENV: &str = "OPENCODE_CONFIG_CONTENT";
const CHATGPT_APP_SHELL_PREFIX: &str = "shell:AppsFolder\\";

#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum NativeClientId {
    #[serde(rename = "chatgpt")]
    ChatGpt,
    Codex,
    #[serde(rename = "claude-code")]
    ClaudeCode,
    Cursor,
    #[serde(rename = "opencode")]
    OpenCode,
    Grok,
}

#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "lowercase")]
pub enum NativeGatewayProfile {
    Anthropic,
    OpenAi,
    Gemini,
    Antigravity,
    Grok,
    Composite,
}

#[derive(Clone, Debug, Deserialize)]
pub struct NativeClientLaunchRequest {
    pub client_id: NativeClientId,
    pub gateway_profile: NativeGatewayProfile,
    pub base_url: String,
    pub api_key: String,
    pub working_directory: String,
}

#[derive(Clone, Debug, Serialize)]
pub struct NativeClientAvailability {
    pub client_id: NativeClientId,
    pub label: String,
    pub executable: Option<String>,
    pub available: bool,
    pub message: String,
    pub identity: Option<NativeClientIdentity>,
}

#[derive(Clone, Debug, Serialize)]
pub struct NativeClientIdentity {
    pub product_name: String,
    pub version: Option<String>,
    pub installation_source: &'static str,
    pub release_channel: &'static str,
    pub package_identity: Option<String>,
    pub probe_status: &'static str,
}

#[derive(Clone, Debug, Serialize)]
pub struct NativeClientLaunchPreview {
    pub client_id: NativeClientId,
    pub label: String,
    pub executable: Option<String>,
    pub working_directory: String,
    pub display_command: String,
    pub environment_keys: Vec<String>,
    pub available: bool,
    pub message: String,
    pub identity: Option<NativeClientIdentity>,
}

#[derive(Clone, Debug, Serialize)]
pub struct NativeClientLaunchReceipt {
    pub client_id: NativeClientId,
    pub pid: u32,
    pub executable: String,
    pub message: String,
}

#[derive(Clone, Debug)]
struct LaunchPlan {
    program: PathBuf,
    args: Vec<String>,
    environment: Vec<(String, String)>,
    working_directory: PathBuf,
    display_command: String,
    open_terminal: bool,
}

impl NativeClientId {
    fn label(&self) -> &'static str {
        match self {
            Self::ChatGpt => "ChatGPT Desktop",
            Self::Codex => "Codex CLI",
            Self::ClaudeCode => "Claude Code",
            Self::Cursor => "Cursor Agent",
            Self::OpenCode => "OpenCode",
            Self::Grok => "Grok CLI",
        }
    }

    fn executable_candidates(&self) -> &'static [&'static str] {
        match self {
            Self::ChatGpt => &["chatgpt.exe", "chatgpt.cmd", "chatgpt.bat", "chatgpt"],
            Self::Codex => &["codex.exe", "codex.cmd", "codex.bat", "codex"],
            Self::ClaudeCode => &[
                "claude.exe",
                "claude.cmd",
                "claude.bat",
                "claude-code",
                "claude-code.exe",
                "claude-code.cmd",
                "claude-code.bat",
                "claude",
            ],
            Self::Cursor => &[
                "cursor-agent.exe",
                "cursor-agent.cmd",
                "cursor-agent.bat",
                "cursor-agent",
            ],
            Self::OpenCode => &["opencode.exe", "opencode.cmd", "opencode.bat", "opencode"],
            Self::Grok => &["grok.exe", "grok.cmd", "grok.bat", "grok"],
        }
    }
}

fn validate_request(request: &NativeClientLaunchRequest) -> Result<PathBuf, String> {
    if !matches!(request.client_id, NativeClientId::ChatGpt) {
        let parsed = Url::parse(request.base_url.trim())
            .map_err(|_| "Base URL 必须是有效的 URL".to_string())?;
        if !matches!(parsed.scheme(), "http" | "https") || parsed.host_str().is_none() {
            return Err("Base URL 只支持带主机名的 http 或 https 地址".into());
        }
        if request.api_key.trim().is_empty() {
            return Err("API Key 不能为空".into());
        }
        if request
            .api_key
            .chars()
            .any(|character| character.is_control())
        {
            return Err("API Key 不能包含控制字符".into());
        }
    }

    let working_directory = if request.working_directory.trim().is_empty() {
        env::current_dir().map_err(|error| format!("无法读取当前目录: {error}"))?
    } else {
        PathBuf::from(request.working_directory.trim())
    };
    if !working_directory.is_dir() {
        return Err(format!(
            "工作目录不存在或不是目录: {}",
            working_directory.display()
        ));
    }
    Ok(working_directory)
}

fn toml_string(value: &str) -> String {
    format!("\"{}\"", value.replace('\\', "\\\\").replace('\"', "\\\""))
}

fn codex_plan(
    request: &NativeClientLaunchRequest,
    executable: PathBuf,
    working_directory: PathBuf,
) -> LaunchPlan {
    let base_url = request.base_url.trim();
    let args = vec![
        "-c".into(),
        "model_provider=\"sub2api\"".into(),
        "-c".into(),
        "web_search=\"live\"".into(),
        "-c".into(),
        "model_providers.sub2api.name=\"Sub2API\"".into(),
        "-c".into(),
        format!("model_providers.sub2api.base_url={}", toml_string(base_url)),
        "-c".into(),
        format!(
            "model_providers.sub2api.model_catalog_url={}",
            toml_string(&format!("{}/models", base_url.trim_end_matches('/')))
        ),
        "-c".into(),
        format!(
            "model_providers.sub2api.env_key={}",
            toml_string(CODEX_API_KEY_ENV)
        ),
        "-c".into(),
        "model_providers.sub2api.wire_api=\"responses\"".into(),
        "-c".into(),
        "model_providers.sub2api.requires_openai_auth=false".into(),
        "-c".into(),
        // This is the same static image-extension marker as the API-key
        // config exports. The actual gateway key stays in the child env.
        "model_providers.sub2api.http_headers={\"x-openai-actor-authorization\"=\"local-image-extension\"}".into(),
        "-c".into(),
        "features.api_key_model_discovery=true".into(),
        "-c".into(),
        "features.image_generation=true".into(),
    ];
    let display_command = format!(
        "{} {}",
        executable.display(),
        args.iter()
            .map(String::as_str)
            .collect::<Vec<_>>()
            .join(" ")
    );
    LaunchPlan {
        program: executable,
        args,
        environment: vec![(CODEX_API_KEY_ENV.into(), request.api_key.clone())],
        working_directory,
        display_command,
        open_terminal: true,
    }
}

fn chatgpt_plan(executable: PathBuf, working_directory: PathBuf) -> LaunchPlan {
    #[cfg(target_os = "macos")]
    if executable.extension().is_some_and(|ext| ext == "app") {
        return LaunchPlan {
            program: PathBuf::from("/usr/bin/open"),
            args: vec!["-a".into(), executable.display().to_string()],
            environment: Vec::new(),
            working_directory,
            display_command: format!("open -a {}", executable.display()),
            open_terminal: false,
        };
    }
    if executable
        .to_string_lossy()
        .starts_with(CHATGPT_APP_SHELL_PREFIX)
    {
        let app_target = executable.display().to_string();
        return LaunchPlan {
            program: PathBuf::from("explorer.exe"),
            args: vec![app_target.clone()],
            environment: Vec::new(),
            working_directory,
            display_command: format!("explorer.exe {app_target}"),
            open_terminal: false,
        };
    }

    LaunchPlan {
        program: executable.clone(),
        args: Vec::new(),
        environment: Vec::new(),
        working_directory,
        display_command: executable.display().to_string(),
        open_terminal: false,
    }
}

fn claude_plan(
    request: &NativeClientLaunchRequest,
    executable: PathBuf,
    working_directory: PathBuf,
) -> LaunchPlan {
    let args = Vec::new();
    let display_command = executable.display().to_string();
    LaunchPlan {
        program: executable,
        args,
        environment: vec![
            ("ANTHROPIC_BASE_URL".into(), request.base_url.trim().into()),
            ("ANTHROPIC_AUTH_TOKEN".into(), request.api_key.clone()),
        ],
        working_directory,
        display_command,
        open_terminal: true,
    }
}

fn cursor_plan(
    request: &NativeClientLaunchRequest,
    executable: PathBuf,
    working_directory: PathBuf,
) -> LaunchPlan {
    let args = vec!["--endpoint".into(), request.base_url.trim().into()];
    let display_command = format!(
        "{} {}",
        executable.display(),
        args.iter()
            .map(String::as_str)
            .collect::<Vec<_>>()
            .join(" ")
    );
    LaunchPlan {
        program: executable,
        args,
        environment: vec![(CURSOR_API_KEY_ENV.into(), request.api_key.clone())],
        working_directory,
        display_command,
        open_terminal: true,
    }
}

fn opencode_plan(
    request: &NativeClientLaunchRequest,
    executable: PathBuf,
    working_directory: PathBuf,
) -> LaunchPlan {
    let config = open_code_config(&request.gateway_profile, request.base_url.trim());
    LaunchPlan {
        display_command: format!("{} (Sub2API runtime config)", executable.display()),
        program: executable,
        args: Vec::new(),
        environment: vec![
            (OPENCODE_API_KEY_ENV.into(), request.api_key.clone()),
            (OPENCODE_CONFIG_ENV.into(), config),
        ],
        working_directory,
        open_terminal: true,
    }
}

fn grok_plan(
    request: &NativeClientLaunchRequest,
    executable: PathBuf,
    working_directory: PathBuf,
) -> LaunchPlan {
    let args = vec!["--model".into(), "grok-4.5".into()];
    let display_command = format!(
        "{} {}",
        executable.display(),
        args.iter()
            .map(String::as_str)
            .collect::<Vec<_>>()
            .join(" ")
    );
    LaunchPlan {
        program: executable,
        args,
        environment: vec![
            (GROK_BASE_URL_ENV.into(), request.base_url.trim().into()),
            (GROK_API_KEY_ENV.into(), request.api_key.clone()),
        ],
        working_directory,
        display_command,
        open_terminal: true,
    }
}

fn build_launch_plan(
    request: &NativeClientLaunchRequest,
    executable: PathBuf,
    is_windows: bool,
) -> Result<LaunchPlan, String> {
    let working_directory = validate_request(request)?;
    let plan = match request.client_id {
        NativeClientId::ChatGpt => chatgpt_plan(executable, working_directory),
        NativeClientId::Codex => codex_plan(request, executable, working_directory),
        NativeClientId::ClaudeCode => claude_plan(request, executable, working_directory),
        NativeClientId::Cursor => cursor_plan(request, executable, working_directory),
        NativeClientId::OpenCode => opencode_plan(request, executable, working_directory),
        NativeClientId::Grok => grok_plan(request, executable, working_directory),
    };
    if is_windows && plan.program.as_os_str().is_empty() {
        return Err("客户端可执行文件不能为空".into());
    }
    Ok(plan)
}

fn open_code_config(profile: &NativeGatewayProfile, base_url: &str) -> String {
    let (provider_id, package, model_id, model_name) = match profile {
        NativeGatewayProfile::OpenAi => ("openai", None, "gpt-5.5", "GPT-5.5"),
        NativeGatewayProfile::Anthropic => (
            "anthropic",
            Some("@ai-sdk/anthropic"),
            "claude-sonnet-4-6",
            "Claude Sonnet 4.6",
        ),
        NativeGatewayProfile::Gemini => (
            "gemini",
            Some("@ai-sdk/google"),
            "gemini-3.1-pro-preview",
            "Gemini 3.1 Pro Preview",
        ),
        NativeGatewayProfile::Antigravity => (
            "antigravity-claude",
            Some("@ai-sdk/anthropic"),
            "claude-sonnet-4-6",
            "Claude Sonnet 4.6",
        ),
        NativeGatewayProfile::Grok => (
            "grok",
            Some("@ai-sdk/openai-compatible"),
            "grok-4.5",
            "Grok 4.5",
        ),
        NativeGatewayProfile::Composite => (
            "sub2api",
            Some("@ai-sdk/openai-compatible"),
            "gpt-5.5",
            "GPT-5.5",
        ),
    };
    let mut models = serde_json::Map::new();
    models.insert(
        model_id.into(),
        serde_json::json!({
            "name": model_name
        }),
    );
    let mut provider = serde_json::json!({
        "name": "Sub2API",
        "options": {
            "baseURL": base_url,
            "apiKey": format!("{{env:{OPENCODE_API_KEY_ENV}}}")
        },
        "models": models
    });
    if let Some(package) = package {
        provider["npm"] = serde_json::Value::String(package.into());
    }
    let mut providers = serde_json::Map::new();
    providers.insert(provider_id.into(), provider);

    serde_json::json!({
        "$schema": "https://opencode.ai/config.json",
        "model": format!("{provider_id}/{model_id}"),
        "enabled_providers": [provider_id],
        "provider": providers
    })
    .to_string()
}

fn find_executable(client_id: &NativeClientId) -> Option<PathBuf> {
    // Prefer the OS-registered desktop app. For CLI tools keep the user's PATH first.
    if matches!(client_id, NativeClientId::ChatGpt) {
        #[cfg(target_os = "macos")]
        return macos_clients::find_installed(client_id);
        #[cfg(windows)]
        if let Some(app) = windows_clients::find_packaged_client(client_id) {
            return Some(app);
        }
    }
    if let Some(path_entries) = env::var_os("PATH") {
        for directory in env::split_paths(&path_entries).filter(|entry| entry.is_absolute()) {
            for candidate in client_id.executable_candidates() {
                let path = directory.join(candidate);
                // Windows npm shims use .cmd or .bat; POSIX launchers must be executable.
                #[cfg(windows)]
                if path.extension().is_none()
                    || !path.is_file()
                    || (matches!(client_id, NativeClientId::Codex)
                        && windows_clients::is_desktop_codex_shim(&path))
                {
                    continue;
                }
                #[cfg(unix)]
                {
                    use std::os::unix::fs::PermissionsExt;
                    if Path::new(candidate).extension().is_some()
                        || !path.metadata().is_ok_and(|meta| {
                            meta.is_file() && meta.permissions().mode() & 0o111 != 0
                        })
                    {
                        continue;
                    }
                }
                return Some(path);
            }
        }
    }
    #[cfg(target_os = "macos")]
    return macos_clients::find_installed(client_id);
    #[cfg(windows)]
    return match client_id {
        NativeClientId::ChatGpt => find_classic_chatgpt(),
        NativeClientId::Codex => windows_clients::find_packaged_client(client_id),
        _ => None,
    };
    #[cfg(not(any(windows, target_os = "macos")))]
    None
}

const CLIENT_PROBE_TIMEOUT: Duration = Duration::from_secs(4);
#[cfg(windows)]
const WINDOWS_METADATA_PROBE_TIMEOUT: Duration = Duration::from_secs(8);
const CLIENT_PROBE_OUTPUT_LIMIT: usize = 16 * 1024;

#[derive(Debug, PartialEq, Eq)]
enum ClientProbeFailure {
    Unavailable,
    TimedOut,
}

// The job owns only this probe and its descendants, never a running client.
#[cfg(windows)]
struct ClientProbeJob(isize);

#[cfg(windows)]
impl ClientProbeJob {
    fn attach(child: &std::process::Child) -> Result<Self, ClientProbeFailure> {
        use std::os::windows::io::AsRawHandle;
        use windows_sys::Win32::{Foundation::CloseHandle, System::JobObjects::*};
        unsafe {
            let job = CreateJobObjectW(std::ptr::null(), std::ptr::null());
            if job.is_null() {
                return Err(ClientProbeFailure::Unavailable);
            }
            let mut info: JOBOBJECT_EXTENDED_LIMIT_INFORMATION = std::mem::zeroed();
            info.BasicLimitInformation.LimitFlags = JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE;
            if SetInformationJobObject(
                job,
                JobObjectExtendedLimitInformation,
                &info as *const _ as *const _,
                std::mem::size_of_val(&info) as u32,
            ) == 0
                || AssignProcessToJobObject(job, child.as_raw_handle() as _) == 0
            {
                CloseHandle(job);
                return Err(ClientProbeFailure::Unavailable);
            }
            Ok(Self(job as isize))
        }
    }
}

#[cfg(windows)]
impl Drop for ClientProbeJob {
    fn drop(&mut self) {
        unsafe { windows_sys::Win32::Foundation::CloseHandle(self.0 as _) };
    }
}

fn run_client_probe(
    command: &mut Command,
    timeout: Duration,
) -> Result<String, ClientProbeFailure> {
    command
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::null());
    #[cfg(windows)]
    {
        use std::os::windows::process::CommandExt;
        command.creation_flags(0x0800_0000); // CREATE_NO_WINDOW
    }
    let deadline = Instant::now() + timeout;
    let mut child = command
        .spawn()
        .map_err(|_| ClientProbeFailure::Unavailable)?;
    #[cfg(windows)]
    let _job = match ClientProbeJob::attach(&child) {
        Ok(job) => Some(job),
        Err(_) if child.try_wait().ok().flatten().is_some() => None,
        Err(error) => {
            let _ = child.kill();
            return Err(error);
        }
    };
    let stdout = child.stdout.take().ok_or(ClientProbeFailure::Unavailable)?;
    let (send, receive) = mpsc::sync_channel(1);
    std::thread::spawn(move || {
        let mut bytes = Vec::new();
        let result = stdout
            .take((CLIENT_PROBE_OUTPUT_LIMIT + 1) as u64)
            .read_to_end(&mut bytes)
            .map(|_| bytes);
        let _ = send.send(result);
    });
    let mut output = None;
    loop {
        if output.is_none() {
            if let Ok(result) = receive.try_recv() {
                match result {
                    Ok(bytes) if bytes.len() <= CLIENT_PROBE_OUTPUT_LIMIT => output = Some(bytes),
                    _ => {
                        let _ = child.kill();
                        return Err(ClientProbeFailure::Unavailable);
                    }
                }
            }
        }
        match child.try_wait() {
            Ok(Some(status)) => {
                if !status.success() {
                    return Err(ClientProbeFailure::Unavailable);
                }
                let bytes = match output {
                    Some(bytes) => bytes,
                    None => receive
                        .recv_timeout(deadline.saturating_duration_since(Instant::now()))
                        .map_err(|_| ClientProbeFailure::TimedOut)?
                        .map_err(|_| ClientProbeFailure::Unavailable)?,
                };
                if bytes.len() > CLIENT_PROBE_OUTPUT_LIMIT {
                    return Err(ClientProbeFailure::Unavailable);
                }
                return String::from_utf8(bytes).map_err(|_| ClientProbeFailure::Unavailable);
            }
            Err(_) => {
                let _ = child.kill();
                return Err(ClientProbeFailure::Unavailable);
            }
            Ok(None) => {}
        }
        if Instant::now() >= deadline {
            let _ = child.kill();
            return Err(ClientProbeFailure::TimedOut);
        }
        std::thread::sleep(Duration::from_millis(20));
    }
}

fn codex_version_from_output(output: &str) -> Option<semver::Version> {
    output.lines().find_map(|line| {
        let version = line.trim().strip_prefix("codex-cli ")?.trim();
        semver::Version::parse(version).ok()
    })
}

fn native_installation_source(executable: &Path) -> &'static str {
    let path = executable
        .to_string_lossy()
        .replace('\\', "/")
        .to_ascii_lowercase();
    if path.starts_with("shell:appsfolder/") || path.contains("/windowsapps/openai.") {
        "windows_msix"
    } else if path.contains("/openai/codex/bin/") {
        "desktop_bundled"
    } else if path.contains("/npm/") || path.contains("/node_modules/") {
        "npm"
    } else {
        "path"
    }
}

#[cfg(windows)]
#[derive(Deserialize)]
struct WindowsClientMetadata {
    product_name: Option<String>,
    version: Option<String>,
    package_identity: Option<String>,
}

#[cfg(windows)]
fn apply_windows_client_metadata(
    identity: &mut NativeClientIdentity,
    metadata: WindowsClientMetadata,
) {
    if let Some(name) = metadata
        .product_name
        .filter(|value| !value.trim().is_empty())
    {
        identity.product_name = name;
    }
    identity.version = metadata.version.filter(|value| !value.trim().is_empty());
    identity.package_identity = metadata.package_identity;
    if identity.package_identity.is_some() {
        identity.installation_source = "windows_msix";
    }
    // MSIX build numbers do not identify a stable/preview release channel.
    if identity.version.is_some() {
        identity.probe_status = "detected";
    }
}

#[cfg(windows)]
fn chatgpt_metadata_script(executable: &Path) -> String {
    let requested = quote_powershell_arg(&executable.to_string_lossy());
    format!(
        r#"
$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
$requested = {requested}
$packages = @()
if ($requested -match '(?i)(?:^shell:AppsFolder\\|\\WindowsApps\\)(OpenAI\.[^_\\]+)_') {{
  $packages = @(Get-AppxPackage -Name $Matches[1] -ErrorAction SilentlyContinue)
}}
foreach ($package in $packages) {{
  $root = [string]$package.InstallLocation
  $shellPrefix = 'shell:AppsFolder\' + $package.PackageFamilyName + '!'
  if (!$requested.StartsWith($shellPrefix, [StringComparison]::OrdinalIgnoreCase) -and
      (!$root -or !$requested.StartsWith($root.TrimEnd('\') + '\', [StringComparison]::OrdinalIgnoreCase))) {{ continue }}
  $manifest = Get-AppxPackageManifest -Package $package
  foreach ($application in @($manifest.Package.Applications.Application)) {{
    $appId = 'shell:AppsFolder\' + $package.PackageFamilyName + '!' + $application.Id
    $appPath = Join-Path $root $application.Executable
    if ($requested -ine $appId -and $requested -ine $appPath) {{ continue }}
    $name = [string]$application.VisualElements.DisplayName
    if (!$name -or $name.StartsWith('ms-resource:')) {{ $name = [string]$manifest.Package.Properties.DisplayName }}
    if ($name.StartsWith('ms-resource:')) {{ $name = $null }}
    @{{ product_name=$name; version=[string]$package.Version; package_identity=[string]$package.Name }} | ConvertTo-Json -Compress
    exit 0
  }}
}}
if (Test-Path -LiteralPath $requested -PathType Leaf) {{
  $info = (Get-Item -LiteralPath $requested).VersionInfo
  @{{ product_name=[string]$info.ProductName; version=[string]$info.ProductVersion; package_identity=$null }} | ConvertTo-Json -Compress
  exit 0
}}
exit 1
"#
    )
}

fn detect_native_client_identity(
    client_id: &NativeClientId,
    executable: &Path,
) -> Option<NativeClientIdentity> {
    if !matches!(client_id, NativeClientId::ChatGpt | NativeClientId::Codex) {
        return None;
    }
    #[cfg(target_os = "macos")]
    if let Some(identity) = macos_clients::identity(client_id, executable) {
        return Some(identity);
    }
    let mut identity = NativeClientIdentity {
        product_name: client_id.label().into(),
        version: None,
        installation_source: native_installation_source(executable),
        release_channel: "unknown",
        package_identity: None,
        probe_status: "unavailable",
    };
    if matches!(client_id, NativeClientId::Codex) {
        #[cfg(windows)]
        if windows_clients::is_bundled_cli(executable) {
            identity.installation_source = "desktop_bundled";
            identity.package_identity = Some("OpenAI.Codex".into());
        }
        let mut command = Command::new(executable);
        command.arg("--version");
        #[cfg(windows)]
        if executable.extension().is_some_and(|ext| {
            let extension = ext.to_string_lossy();
            extension.eq_ignore_ascii_case("cmd") || extension.eq_ignore_ascii_case("bat")
        }) {
            command = Command::new(powershell_executable());
            command.args(["-NoLogo", "-NoProfile", "-NonInteractive", "-Command"]);
            command.arg(format!(
                "& {} '--version'",
                quote_powershell_arg(&executable.to_string_lossy())
            ));
        }
        match run_client_probe(&mut command, CLIENT_PROBE_TIMEOUT) {
            Ok(output) => {
                if let Some(version) = codex_version_from_output(&output) {
                    identity.release_channel = if version.pre.is_empty() {
                        "stable"
                    } else {
                        "prerelease"
                    };
                    identity.version = Some(version.to_string());
                    identity.probe_status = "detected";
                }
            }
            Err(ClientProbeFailure::TimedOut) => identity.probe_status = "timed_out",
            Err(ClientProbeFailure::Unavailable) => {}
        }
    } else {
        #[cfg(windows)]
        {
            let powershell = windows_powershell_candidates()
                .into_iter()
                .find(|path| path.is_file())
                .unwrap_or_else(powershell_executable);
            let mut command = Command::new(powershell);
            command
                .args(["-NoLogo", "-NoProfile", "-NonInteractive", "-Command"])
                .arg(chatgpt_metadata_script(executable));
            match run_client_probe(&mut command, WINDOWS_METADATA_PROBE_TIMEOUT) {
                Ok(output) => {
                    if let Ok(metadata) =
                        serde_json::from_str::<WindowsClientMetadata>(output.trim())
                    {
                        apply_windows_client_metadata(&mut identity, metadata);
                    }
                }
                Err(ClientProbeFailure::TimedOut) => identity.probe_status = "timed_out",
                Err(ClientProbeFailure::Unavailable) => {}
            }
        }
    }
    Some(identity)
}

#[cfg(windows)]
fn find_classic_chatgpt() -> Option<PathBuf> {
    let mut roots = Vec::new();
    for variable in ["LOCALAPPDATA", "PROGRAMFILES", "PROGRAMFILES(X86)"] {
        if let Some(value) = env::var_os(variable) {
            roots.push(PathBuf::from(value));
        }
    }
    let relative_paths = [
        Path::new("Programs/ChatGPT/ChatGPT.exe"),
        Path::new("Programs/OpenAI/ChatGPT/ChatGPT.exe"),
        Path::new("ChatGPT/ChatGPT.exe"),
        Path::new("Microsoft/WindowsApps/ChatGPT.exe"),
    ];
    roots
        .into_iter()
        .flat_map(|root| {
            relative_paths
                .iter()
                .map(move |relative| root.join(relative))
        })
        .find(|path| path.is_file())
}

#[cfg(windows)]
fn run_powershell_for_chatgpt_app_id(script: &str) -> Option<String> {
    let mut candidates = vec![powershell_executable()];
    if let Some(system_root) = env::var_os("SystemRoot") {
        let fallback = PathBuf::from(system_root)
            .join("System32")
            .join("WindowsPowerShell")
            .join("v1.0")
            .join("powershell.exe");
        if !candidates.contains(&fallback) {
            candidates.push(fallback);
        }
    }

    for executable in candidates {
        let mut command = Command::new(executable);
        command.args([
            "-NoLogo",
            "-NoProfile",
            "-NonInteractive",
            "-Command",
            script,
        ]);
        let output = match run_client_probe(&mut command, WINDOWS_METADATA_PROBE_TIMEOUT) {
            Ok(output) => output,
            _ => continue,
        };
        if let Some(app_id) = output
            .lines()
            .map(str::trim)
            .find(|line| windows_clients::is_known_app_id(line))
        {
            return Some(app_id.to_string());
        }
    }
    None
}

#[tauri::command]
pub fn native_working_directory() -> Result<String, String> {
    env::current_dir()
        .map(|path| path.display().to_string())
        .map_err(|error| format!("无法读取当前工作目录: {error}"))
}

#[tauri::command]
pub fn pick_native_working_directory() -> Result<Option<String>, String> {
    #[cfg(windows)]
    {
        let script = r#"
Add-Type -AssemblyName System.Windows.Forms
$dialog = New-Object System.Windows.Forms.FolderBrowserDialog
$dialog.Description = '选择客户端工作目录'
$dialog.ShowNewFolderButton = $true
if ($dialog.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) {
  [Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false)
  Write-Output $dialog.SelectedPath
}
"#;
        let output = Command::new(powershell_executable())
            .args([
                "-NoLogo",
                "-NoProfile",
                "-STA",
                "-WindowStyle",
                "Hidden",
                "-Command",
                script,
            ])
            .stdin(Stdio::null())
            .output()
            .map_err(|error| format!("无法打开目录选择器: {error}"))?;
        if !output.status.success() {
            let detail = String::from_utf8_lossy(&output.stderr).trim().to_string();
            return Err(if detail.is_empty() {
                "目录选择器启动失败".into()
            } else {
                format!("目录选择器启动失败: {detail}")
            });
        }
        let selected = String::from_utf8_lossy(&output.stdout).trim().to_string();
        if selected.is_empty() {
            return Ok(None);
        }
        let path = PathBuf::from(selected.trim());
        if !path.is_dir() {
            return Err(format!("所选路径不是有效目录: {}", path.display()));
        }
        return Ok(Some(path.display().to_string()));
    }

    #[cfg(target_os = "macos")]
    {
        let script = r#"try
return POSIX path of (choose folder with prompt "选择客户端工作目录")
on error number -128
return ""
end try"#;
        let output = Command::new("/usr/bin/osascript")
            .args(["-e", script])
            .output()
            .map_err(|error| format!("无法打开目录选择器: {error}"))?;
        if !output.status.success() {
            return Err(format!(
                "无法选择目录: {}",
                String::from_utf8_lossy(&output.stderr).trim()
            ));
        }
        let path = String::from_utf8(output.stdout).map_err(|error| error.to_string())?;
        let path = path.trim_end_matches('\n');
        Ok((!path.is_empty()).then(|| path.to_owned()))
    }
    #[cfg(not(any(windows, target_os = "macos")))]
    {
        Err("此平台没有原生目录选择器".into())
    }
}

fn unavailable_preview(
    request: &NativeClientLaunchRequest,
    working_directory: String,
    message: String,
) -> NativeClientLaunchPreview {
    NativeClientLaunchPreview {
        client_id: request.client_id.clone(),
        label: request.client_id.label().into(),
        executable: None,
        working_directory,
        display_command: String::new(),
        environment_keys: Vec::new(),
        available: false,
        message,
        identity: None,
    }
}

fn unavailable_message(client_id: &NativeClientId) -> String {
    if matches!(client_id, NativeClientId::ChatGpt) {
        "未找到 ChatGPT Desktop，请确认已安装官方桌面客户端".into()
    } else if matches!(client_id, NativeClientId::Codex) && cfg!(any(windows, target_os = "macos"))
    {
        "未找到 Codex CLI，请安装官方 ChatGPT 桌面客户端或将独立 Codex CLI 加入 PATH".into()
    } else {
        format!(
            "未在 PATH 中找到 {}，请确认客户端已安装并可从终端启动",
            client_id.label()
        )
    }
}

fn build_preview(request: &NativeClientLaunchRequest) -> NativeClientLaunchPreview {
    let working_directory = request.working_directory.trim().to_string();
    let executable = match find_executable(&request.client_id) {
        Some(path) => path,
        None => {
            return unavailable_preview(
                request,
                working_directory,
                unavailable_message(&request.client_id),
            )
        }
    };
    match build_launch_plan(request, executable.clone(), cfg!(windows)) {
        Ok(plan) => NativeClientLaunchPreview {
            client_id: request.client_id.clone(),
            label: request.client_id.label().into(),
            executable: Some(executable.display().to_string()),
            working_directory: plan.working_directory.display().to_string(),
            display_command: plan.display_command,
            environment_keys: plan
                .environment
                .iter()
                .map(|(key, _)| key.clone())
                .collect(),
            available: true,
            message: "客户端已就绪".into(),
            identity: detect_native_client_identity(&request.client_id, &executable),
        },
        Err(error) => unavailable_preview(request, working_directory, error),
    }
}

#[cfg(windows)]
fn powershell_command_line(plan: &LaunchPlan) -> String {
    std::iter::once(format!(
        "& {}",
        quote_powershell_arg(&plan.program.to_string_lossy())
    ))
    .chain(
        plan.args
            .iter()
            .map(|argument| quote_powershell_arg(argument)),
    )
    .collect::<Vec<_>>()
    .join(" ")
}

fn command_for_plan(plan: &LaunchPlan) -> Command {
    #[cfg(windows)]
    let mut command = if plan.open_terminal {
        let command_line = powershell_command_line(plan);
        let mut command = Command::new(powershell_executable());
        command.args([
            "-NoLogo",
            "-NoProfile",
            "-ExecutionPolicy",
            "Bypass",
            "-NoExit",
            "-Command",
            &command_line,
        ]);
        command
    } else {
        let mut command = Command::new(&plan.program);
        command.args(&plan.args);
        command
    };

    #[cfg(not(windows))]
    let mut command = {
        let mut command = Command::new(&plan.program);
        command.args(&plan.args);
        command
    };

    command.current_dir(&plan.working_directory);
    command.envs(plan.environment.iter().map(|(key, value)| (key, value)));
    if plan.open_terminal {
        command
            .stdin(Stdio::inherit())
            .stdout(Stdio::inherit())
            .stderr(Stdio::inherit());
    } else {
        command
            .stdin(Stdio::null())
            .stdout(Stdio::inherit())
            .stderr(Stdio::inherit());
    }

    #[cfg(windows)]
    {
        use std::os::windows::process::CommandExt;
        command.creation_flags(0x00000010);
    }
    command
}

#[cfg(windows)]
fn powershell_executable() -> PathBuf {
    powershell_core_candidates()
        .into_iter()
        .chain(windows_powershell_candidates())
        .find(|candidate| candidate.is_file())
        .unwrap_or_else(|| PathBuf::from("powershell.exe"))
}

#[cfg(windows)]
fn powershell_core_candidates() -> Vec<PathBuf> {
    let mut candidates = Vec::new();

    // PowerShell 7's stable MSI install location, including per-user installs.
    for variable in ["ProgramW6432", "ProgramFiles", "LOCALAPPDATA"] {
        if let Some(root) = env::var_os(variable) {
            let root = PathBuf::from(root);
            let candidate = if variable == "LOCALAPPDATA" {
                root.join("Microsoft")
                    .join("PowerShell")
                    .join("7")
                    .join("pwsh.exe")
            } else {
                root.join("PowerShell").join("7").join("pwsh.exe")
            };
            if !candidates.contains(&candidate) {
                candidates.push(candidate);
            }
        }
    }

    // Portable/package installs may only be discoverable through PATH.
    if let Some(path_entries) = env::var_os("PATH") {
        for directory in env::split_paths(&path_entries) {
            let candidate = directory.join("pwsh.exe");
            if !candidates.contains(&candidate) {
                candidates.push(candidate);
            }
        }
    }
    candidates
}

#[cfg(windows)]
fn windows_powershell_candidates() -> Vec<PathBuf> {
    let mut candidates = Vec::new();
    if let Some(system_root) = env::var_os("SystemRoot") {
        let candidate = PathBuf::from(system_root)
            .join("System32")
            .join("WindowsPowerShell")
            .join("v1.0")
            .join("powershell.exe");
        candidates.push(candidate);
    }
    candidates
}

#[cfg(windows)]
fn quote_powershell_arg(value: &str) -> String {
    format!("'{}'", value.replace('\'', "''"))
}

#[tauri::command]
pub async fn list_native_clients() -> Vec<NativeClientAvailability> {
    tauri::async_runtime::spawn_blocking(|| {
        [
            NativeClientId::ChatGpt,
            NativeClientId::Codex,
            NativeClientId::ClaudeCode,
            NativeClientId::Cursor,
            NativeClientId::OpenCode,
            NativeClientId::Grok,
        ]
        .into_iter()
        .map(|client_id| {
            let label = client_id.label().to_string();
            match find_executable(&client_id) {
                Some(path) => NativeClientAvailability {
                    identity: detect_native_client_identity(&client_id, &path),
                    client_id,
                    label,
                    executable: Some(path.display().to_string()),
                    available: true,
                    message: "客户端已就绪".into(),
                },
                None => {
                    let message = unavailable_message(&client_id);
                    NativeClientAvailability {
                        client_id,
                        label,
                        executable: None,
                        available: false,
                        message,
                        identity: None,
                    }
                }
            }
        })
        .collect()
    })
    .await
    .unwrap_or_default()
}

#[tauri::command]
pub async fn preview_native_client_launch(
    request: NativeClientLaunchRequest,
) -> NativeClientLaunchPreview {
    let fallback = request.clone();
    tauri::async_runtime::spawn_blocking(move || build_preview(&request))
        .await
        .unwrap_or_else(|_| {
            unavailable_preview(
                &fallback,
                fallback.working_directory.clone(),
                "客户端检查暂时不可用，请重试".into(),
            )
        })
}

#[tauri::command]
pub async fn launch_native_client(
    request: NativeClientLaunchRequest,
) -> Result<NativeClientLaunchReceipt, String> {
    tauri::async_runtime::spawn_blocking(move || launch_client(request))
        .await
        .map_err(|error| format!("启动客户端失败: {error}"))?
}

fn launch_client(request: NativeClientLaunchRequest) -> Result<NativeClientLaunchReceipt, String> {
    let executable = find_executable(&request.client_id)
        .ok_or_else(|| unavailable_message(&request.client_id))?;
    let executable_display = executable.display().to_string();
    let plan = build_launch_plan(&request, executable, cfg!(windows))?;
    let pid = spawn_client(&request.client_id, &plan)
        .map_err(|error| format!("启动 {} 失败: {error}", request.client_id.label()))?;
    Ok(NativeClientLaunchReceipt {
        client_id: request.client_id,
        pid,
        executable: executable_display,
        message: "客户端已启动".into(),
    })
}

fn spawn_client(_client_id: &NativeClientId, plan: &LaunchPlan) -> std::io::Result<u32> {
    #[cfg(target_os = "macos")]
    if matches!(_client_id, NativeClientId::Codex) {
        return macos_terminal::launch(plan);
    }
    let child = command_for_plan(plan).spawn()?;
    let pid = child.id();
    #[cfg(unix)]
    let mut child = child;
    #[cfg(target_os = "macos")]
    if matches!(_client_id, NativeClientId::ChatGpt) {
        if !child.wait()?.success() {
            return Err(std::io::Error::other("macOS 未能打开 ChatGPT"));
        }
        return Ok(pid);
    }
    #[cfg(unix)]
    std::thread::spawn(move || {
        let _ = child.wait();
    });
    Ok(pid)
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::path::PathBuf;

    #[test]
    fn codex_versions_distinguish_desktop_prerelease_from_stable_cli() {
        let stable = codex_version_from_output("codex-cli 0.167.0\n").unwrap();
        let desktop = codex_version_from_output("codex-cli 0.167.0-alpha.3.2\r\n").unwrap();
        assert!(stable.pre.is_empty());
        assert_eq!(desktop.pre.as_str(), "alpha.3.2");
        assert!(codex_version_from_output("ChatGPT 26.1103.4239.0").is_none());
        assert!(codex_version_from_output("codex-cli not-a-version").is_none());
    }

    #[test]
    fn installation_source_keeps_desktop_core_and_msix_distinct() {
        assert_eq!(
            native_installation_source(Path::new(
                r"C:\Users\sample\AppData\Local\OpenAI\Codex\bin\build\codex.exe"
            )),
            "desktop_bundled"
        );
        assert_eq!(
            native_installation_source(Path::new(r"C:\Users\sample\AppData\Roaming\npm\codex.cmd")),
            "npm"
        );
        assert_eq!(
            native_installation_source(Path::new(r"shell:AppsFolder\OpenAI.Codex_example!App")),
            "windows_msix"
        );
        assert_eq!(
            native_installation_source(Path::new(r"C:\tools\codex.exe")),
            "path"
        );
    }

    #[cfg(windows)]
    #[test]
    fn msix_display_name_does_not_infer_product_from_codex_package_id() {
        let mut identity = NativeClientIdentity {
            product_name: "ChatGPT Desktop".into(),
            version: None,
            installation_source: "path",
            release_channel: "unknown",
            package_identity: None,
            probe_status: "unavailable",
        };
        let metadata = serde_json::from_str::<WindowsClientMetadata>(
            r#"{"product_name":"ChatGPT","version":"26.1103.4239.0","package_identity":"OpenAI.Codex"}"#,
        ).unwrap();
        apply_windows_client_metadata(&mut identity, metadata);
        assert_eq!(identity.product_name, "ChatGPT");
        assert_eq!(identity.version.as_deref(), Some("26.1103.4239.0"));
        assert_eq!(identity.package_identity.as_deref(), Some("OpenAI.Codex"));
        assert_eq!(identity.installation_source, "windows_msix");
        assert_eq!(identity.release_channel, "unknown");
    }

    #[cfg(windows)]
    #[test]
    fn client_probe_times_out_without_waiting_for_command_completion() {
        let mut command = Command::new(powershell_executable());
        command.args([
            "-NoLogo",
            "-NoProfile",
            "-NonInteractive",
            "-Command",
            "Start-Sleep -Seconds 30",
        ]);
        let started = Instant::now();
        assert_eq!(
            run_client_probe(&mut command, Duration::from_millis(150)),
            Err(ClientProbeFailure::TimedOut)
        );
        assert!(started.elapsed() < Duration::from_secs(3));
    }

    #[cfg(windows)]
    #[test]
    fn client_probe_limits_output_and_accepts_normal_version_output() {
        let mut command = Command::new(powershell_executable());
        command.args([
            "-NoLogo",
            "-NoProfile",
            "-NonInteractive",
            "-Command",
            "[Console]::Write('x' * 20000)",
        ]);
        assert_eq!(
            run_client_probe(&mut command, CLIENT_PROBE_TIMEOUT),
            Err(ClientProbeFailure::Unavailable)
        );
        let mut version = Command::new(powershell_executable());
        version.args([
            "-NoLogo",
            "-NoProfile",
            "-NonInteractive",
            "-Command",
            "[Console]::WriteLine('codex-cli 0.167.0')",
        ]);
        assert_eq!(
            run_client_probe(&mut version, CLIENT_PROBE_TIMEOUT)
                .unwrap()
                .trim(),
            "codex-cli 0.167.0"
        );
    }

    #[test]
    fn codex_launch_plan_uses_process_scoped_credentials() {
        let request = NativeClientLaunchRequest {
            client_id: NativeClientId::Codex,
            gateway_profile: NativeGatewayProfile::OpenAi,
            base_url: "https://gateway.example.test/api".into(),
            api_key: "secret-value".into(),
            working_directory: ".".into(),
        };

        let plan = build_launch_plan(&request, PathBuf::from("C:\\tools\\codex.exe"), true)
            .expect("codex launch plan should be valid");

        assert_eq!(plan.program, PathBuf::from("C:\\tools\\codex.exe"));
        assert!(plan
            .args
            .windows(2)
            .any(|pair| pair[0] == "-c" && pair[1].contains("base_url")));
        assert!(plan.args.windows(2).any(|pair| pair[0] == "-c"
            && pair[1]
                == "model_providers.sub2api.model_catalog_url=\"https://gateway.example.test/api/models\""));
        assert!(plan
            .args
            .windows(2)
            .any(|pair| pair[0] == "-c" && pair[1] == "features.api_key_model_discovery=true"));
        for expected in [
            "web_search=\"live\"",
            "features.image_generation=true",
            "model_providers.sub2api.http_headers={\"x-openai-actor-authorization\"=\"local-image-extension\"}",
        ] {
            assert!(plan.args.windows(2).any(|pair| pair[0] == "-c" && pair[1] == expected));
        }
        assert!(!plan.args.iter().any(|arg| arg.starts_with("model=")));
        assert!(plan
            .environment
            .iter()
            .any(|(key, value)| key == "SUB2API_CODEX_API_KEY" && value == "secret-value"));
        assert!(!plan.display_command.contains("secret-value"));
    }

    #[test]
    fn chatgpt_desktop_plan_is_separate_and_does_not_consume_api_credentials() {
        let request = NativeClientLaunchRequest {
            client_id: NativeClientId::ChatGpt,
            gateway_profile: NativeGatewayProfile::Composite,
            base_url: "http://127.0.0.1:18765/v1".into(),
            api_key: String::new(),
            working_directory: ".".into(),
        };

        let plan = build_launch_plan(
            &request,
            PathBuf::from("C:\\Program Files\\ChatGPT\\ChatGPT.exe"),
            true,
        )
        .expect("ChatGPT Desktop launch plan should be valid without an API key");

        assert!(plan.args.is_empty());
        assert!(plan.environment.is_empty());
        assert_eq!(
            plan.program,
            PathBuf::from("C:\\Program Files\\ChatGPT\\ChatGPT.exe")
        );
    }

    #[cfg(target_os = "macos")]
    #[test]
    fn macos_desktop_opens_the_discovered_bundle_without_gateway_settings() {
        let request = NativeClientLaunchRequest {
            client_id: NativeClientId::ChatGpt,
            gateway_profile: NativeGatewayProfile::Composite,
            base_url: String::new(),
            api_key: String::new(),
            working_directory: ".".into(),
        };
        let bundle = PathBuf::from("/Applications/ChatGPT.app");
        let plan = build_launch_plan(&request, bundle, false).unwrap();
        assert_eq!(plan.program, PathBuf::from("/usr/bin/open"));
        assert_eq!(plan.args, ["-a", "/Applications/ChatGPT.app"]);
        assert!(plan.environment.is_empty());
        assert!(!plan.open_terminal);
    }

    #[test]
    fn chatgpt_msix_app_shell_target_uses_explorer_without_credentials() {
        let request = NativeClientLaunchRequest {
            client_id: NativeClientId::ChatGpt,
            gateway_profile: NativeGatewayProfile::Composite,
            base_url: "http://127.0.0.1:18765/v1".into(),
            api_key: String::new(),
            working_directory: ".".into(),
        };

        let plan = build_launch_plan(
            &request,
            PathBuf::from("shell:AppsFolder\\OpenAI.Codex_2p2nqsd0c76g0!App"),
            true,
        )
        .expect("ChatGPT MSIX launch plan should be valid");

        assert_eq!(plan.program, PathBuf::from("explorer.exe"));
        assert_eq!(
            plan.args,
            vec!["shell:AppsFolder\\OpenAI.Codex_2p2nqsd0c76g0!App"]
        );
        assert!(plan.environment.is_empty());
        assert!(!plan.open_terminal);
    }

    #[test]
    fn windows_client_candidates_prefer_exe_and_cmd_over_posix_shims() {
        assert_eq!(
            NativeClientId::OpenCode.executable_candidates()[0],
            "opencode.exe"
        );
        assert_eq!(
            NativeClientId::OpenCode.executable_candidates()[1],
            "opencode.cmd"
        );
        assert_eq!(
            NativeClientId::ClaudeCode.executable_candidates()[0],
            "claude.exe"
        );
        assert_eq!(
            NativeClientId::ClaudeCode.executable_candidates()[1],
            "claude.cmd"
        );
    }

    #[test]
    fn claude_code_plan_uses_anthropic_process_environment() {
        let request = NativeClientLaunchRequest {
            client_id: NativeClientId::ClaudeCode,
            gateway_profile: NativeGatewayProfile::Anthropic,
            base_url: "https://gateway.example.test/v1/messages".into(),
            api_key: "secret-value".into(),
            working_directory: ".".into(),
        };

        let plan = build_launch_plan(&request, PathBuf::from("C:\\tools\\claude.cmd"), true)
            .expect("claude launch plan should be valid");

        assert!(plan.args.is_empty());
        assert_eq!(
            plan.environment,
            vec![
                (
                    "ANTHROPIC_BASE_URL".to_string(),
                    "https://gateway.example.test/v1/messages".to_string()
                ),
                (
                    "ANTHROPIC_AUTH_TOKEN".to_string(),
                    "secret-value".to_string()
                )
            ]
        );
        assert!(!plan.display_command.contains("secret-value"));
    }

    #[test]
    fn launch_request_rejects_invalid_url_empty_key_and_missing_directory() {
        let base_request = || NativeClientLaunchRequest {
            client_id: NativeClientId::Codex,
            gateway_profile: NativeGatewayProfile::OpenAi,
            base_url: "https://gateway.example.test/api".into(),
            api_key: "secret-value".into(),
            working_directory: ".".into(),
        };

        let mut request = base_request();
        request.base_url = "file:///tmp/gateway".into();
        assert!(validate_request(&request).is_err());

        let mut request = base_request();
        request.api_key.clear();
        assert!(validate_request(&request).is_err());

        let mut request = base_request();
        request.working_directory = "C:\\path\\that\\does\\not\\exist".into();
        assert!(validate_request(&request).is_err());
    }

    #[test]
    fn unsupported_client_ids_are_rejected_before_launch() {
        let payload = r#"{
            "client_id": "unsupported-client",
            "gateway_profile": "openai",
            "base_url": "https://gateway.example.test/api",
            "api_key": "secret-value",
            "working_directory": "."
        }"#;
        assert!(serde_json::from_str::<NativeClientLaunchRequest>(payload).is_err());
    }

    #[test]
    fn cursor_plan_uses_endpoint_and_process_scoped_key() {
        let request = NativeClientLaunchRequest {
            client_id: NativeClientId::Cursor,
            gateway_profile: NativeGatewayProfile::OpenAi,
            base_url: "https://gateway.example.test/cursor".into(),
            api_key: "secret-value".into(),
            working_directory: ".".into(),
        };

        let plan = build_launch_plan(&request, PathBuf::from("C:\\tools\\cursor-agent.exe"), true)
            .expect("cursor launch plan should be valid");

        assert_eq!(
            plan.args,
            vec!["--endpoint", "https://gateway.example.test/cursor"]
        );
        assert!(plan
            .environment
            .iter()
            .any(|(key, value)| key == CURSOR_API_KEY_ENV && value == "secret-value"));
        assert!(!plan.display_command.contains("secret-value"));
    }

    #[test]
    fn opencode_plan_uses_isolated_config_with_environment_key_reference() {
        let request = NativeClientLaunchRequest {
            client_id: NativeClientId::OpenCode,
            gateway_profile: NativeGatewayProfile::Grok,
            base_url: "https://gateway.example.test/v1".into(),
            api_key: "secret-value".into(),
            working_directory: ".".into(),
        };

        let plan = build_launch_plan(&request, PathBuf::from("C:\\tools\\opencode.cmd"), true)
            .expect("opencode launch plan should be valid");
        let config = open_code_config(&request.gateway_profile, &request.base_url);

        assert!(config.contains("{env:SUB2API_OPENCODE_API_KEY}"));
        assert!(config.contains("@ai-sdk/openai-compatible"));
        assert!(config.contains("grok/grok-4.5"));
        assert!(!config.contains("secret-value"));
        assert!(plan
            .environment
            .iter()
            .any(|(key, value)| key == OPENCODE_API_KEY_ENV && value == "secret-value"));
        assert!(plan
            .environment
            .iter()
            .any(|(key, value)| key == OPENCODE_CONFIG_ENV && value == &config));
        assert!(!plan.display_command.contains("secret-value"));
    }

    #[test]
    fn opencode_runtime_config_selects_the_gateway_protocol() {
        let cases = [
            (NativeGatewayProfile::OpenAi, "openai/gpt-5.5", None),
            (
                NativeGatewayProfile::Anthropic,
                "anthropic/claude-sonnet-4-6",
                Some("@ai-sdk/anthropic"),
            ),
            (
                NativeGatewayProfile::Gemini,
                "gemini/gemini-3.1-pro-preview",
                Some("@ai-sdk/google"),
            ),
            (
                NativeGatewayProfile::Antigravity,
                "antigravity-claude/claude-sonnet-4-6",
                Some("@ai-sdk/anthropic"),
            ),
            (
                NativeGatewayProfile::Grok,
                "grok/grok-4.5",
                Some("@ai-sdk/openai-compatible"),
            ),
        ];

        for (profile, model, package) in cases {
            let config = open_code_config(&profile, "https://gateway.example.test/v1");
            let parsed: serde_json::Value =
                serde_json::from_str(&config).expect("runtime config should be valid JSON");

            assert_eq!(parsed["model"], model);
            assert_eq!(parsed["enabled_providers"].as_array().unwrap().len(), 1);
            assert_eq!(config.contains("\"npm\""), package.is_some());
            if let Some(package) = package {
                assert!(config.contains(package));
            }
            assert!(!config.contains("secret-value"));
        }
    }

    #[test]
    fn grok_plan_uses_custom_models_endpoint_and_process_scoped_key() {
        let request = NativeClientLaunchRequest {
            client_id: NativeClientId::Grok,
            gateway_profile: NativeGatewayProfile::Grok,
            base_url: "https://gateway.example.test/v1".into(),
            api_key: "secret-value".into(),
            working_directory: ".".into(),
        };

        let plan = build_launch_plan(&request, PathBuf::from("C:\\tools\\grok.cmd"), true)
            .expect("grok launch plan should be valid");

        assert_eq!(plan.args, vec!["--model", "grok-4.5"]);
        assert!(plan.environment.iter().any(|(key, value)| {
            key == GROK_BASE_URL_ENV && value == "https://gateway.example.test/v1"
        }));
        assert!(plan
            .environment
            .iter()
            .any(|(key, value)| key == GROK_API_KEY_ENV && value == "secret-value"));
        assert!(!plan.display_command.contains("secret-value"));
    }

    #[cfg(windows)]
    #[test]
    fn powershell_arguments_are_single_quoted_and_safe() {
        let quoted = quote_powershell_arg(r#"https://gateway.example.test/api?scope=a&b=1%'x"#);
        assert_eq!(
            quoted,
            r#"'https://gateway.example.test/api?scope=a&b=1%''x'"#
        );
    }

    #[cfg(windows)]
    #[test]
    fn powershell_command_line_invokes_the_client_program() {
        let plan = LaunchPlan {
            program: PathBuf::from(r#"C:\Users\reki\AppData\Roaming\npm\opencode.cmd"#),
            args: vec!["--version".into()],
            environment: Vec::new(),
            working_directory: PathBuf::from("."),
            display_command: String::new(),
            open_terminal: true,
        };
        assert_eq!(
            powershell_command_line(&plan),
            r#"& 'C:\Users\reki\AppData\Roaming\npm\opencode.cmd' '--version'"#
        );
    }

    #[cfg(windows)]
    #[test]
    fn powershell_selection_prefers_core_and_falls_back_to_desktop() {
        let executable = powershell_executable();
        let file_name = executable.file_name().and_then(|value| value.to_str());
        assert!(matches!(
            file_name,
            Some("pwsh.exe") | Some("powershell.exe")
        ));
        assert!(executable.is_file() || file_name == Some("powershell.exe"));
    }
}
