use reqwest::Url;
use serde::{Deserialize, Serialize};
use std::{
    env, fs,
    path::{Path, PathBuf},
    process::{Command, Stdio},
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
    let base_url = request.base_url.trim();
    let parsed = Url::parse(base_url).map_err(|_| "Base URL 必须是有效的 URL".to_string())?;
    if !matches!(parsed.scheme(), "http" | "https") || parsed.host_str().is_none() {
        return Err("Base URL 只支持带主机名的 http 或 https 地址".into());
    }
    if !matches!(request.client_id, NativeClientId::ChatGpt) && request.api_key.trim().is_empty() {
        return Err("API Key 不能为空".into());
    }
    if !matches!(request.client_id, NativeClientId::ChatGpt)
        && request
            .api_key
            .chars()
            .any(|character| character.is_control())
    {
        return Err("API Key 不能包含控制字符".into());
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
        "model_providers.sub2api.name=\"Sub2API\"".into(),
        "-c".into(),
        format!("model_providers.sub2api.base_url={}", toml_string(base_url)),
        "-c".into(),
        format!(
            "model_providers.sub2api.env_key={}",
            toml_string(CODEX_API_KEY_ENV)
        ),
        "-c".into(),
        "model_providers.sub2api.wire_api=\"responses\"".into(),
        "-c".into(),
        "model_providers.sub2api.requires_openai_auth=false".into(),
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
    if let Some(path_entries) = env::var_os("PATH") {
        for directory in env::split_paths(&path_entries) {
            for candidate in client_id.executable_candidates() {
                let path = directory.join(candidate);
                // Windows cannot execute npm's extensionless #!/bin/sh shim. Prefer the
                // adjacent .cmd/.bat launcher instead of returning a path that fails with
                // ERROR_BAD_EXE_FORMAT (193).
                if cfg!(windows) && Path::new(candidate).extension().is_none() {
                    continue;
                }
                if path.is_file() {
                    return Some(path);
                }
            }
        }
    }
    find_installed_executable(client_id)
}

fn find_installed_executable(client_id: &NativeClientId) -> Option<PathBuf> {
    if !matches!(client_id, NativeClientId::ChatGpt) {
        return None;
    }

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
        Path::new("ChatGPT/ChatGPT.exe"),
    ];
    let classic_install = roots
        .into_iter()
        .flat_map(|root| {
            relative_paths
                .iter()
                .map(move |relative| root.join(relative))
        })
        .find(|path| path.is_file());
    classic_install
        .or_else(find_msix_chatgpt_executable)
        .or_else(|| {
            find_msix_chatgpt_app_id()
                .map(|app_id| PathBuf::from(format!("{CHATGPT_APP_SHELL_PREFIX}{app_id}")))
        })
}

fn is_openai_chatgpt_package(name: &str) -> bool {
    let normalized = name.to_ascii_lowercase();
    normalized.starts_with("openai.codex_") || normalized.starts_with("openai.chatgpt")
}

fn find_msix_chatgpt_executable() -> Option<PathBuf> {
    let roots = ["ProgramW6432", "PROGRAMFILES", "PROGRAMFILES(X86)"]
        .into_iter()
        .filter_map(|variable| env::var_os(variable).map(PathBuf::from));

    roots
        .flat_map(|root| {
            let windows_apps = root.join("WindowsApps");
            fs::read_dir(windows_apps)
                .into_iter()
                .flatten()
                .filter_map(Result::ok)
                .collect::<Vec<_>>()
        })
        .filter_map(|entry| {
            let name = entry.file_name().to_string_lossy().to_string();
            is_openai_chatgpt_package(&name).then_some(entry.path())
        })
        .flat_map(|package_root| {
            [
                Path::new("app/ChatGPT.exe"),
                Path::new("app/Codex.exe"),
                Path::new("ChatGPT.exe"),
                Path::new("Codex.exe"),
            ]
            .into_iter()
            .map(move |relative| package_root.join(relative))
        })
        .find(|path| path.is_file())
}

fn is_valid_chatgpt_app_id(app_id: &str) -> bool {
    let normalized = app_id.to_ascii_lowercase();
    (normalized.starts_with("openai.codex_") || normalized.starts_with("openai.chatgpt"))
        && normalized.ends_with("!app")
        && app_id
            .chars()
            .all(|character| character.is_ascii_alphanumeric() || "._-!".contains(character))
}

fn find_msix_chatgpt_app_id() -> Option<String> {
    #[cfg(windows)]
    {
        // Get-StartApps is fast, but its availability differs between Windows
        // PowerShell and PowerShell 7. Query the package manifest as the primary
        // source so MSIX installs work even when the StartApps module is absent.
        let script = r#"
$packages = @(Get-AppxPackage -Name OpenAI.Codex -ErrorAction SilentlyContinue; Get-AppxPackage -Name OpenAI.ChatGPT -ErrorAction SilentlyContinue)
foreach ($package in $packages) {
  try { $manifest = Get-AppxPackageManifest -Package $package -ErrorAction Stop } catch { continue }
  foreach ($application in @($manifest.Package.Applications.Application)) {
    if ($application.Id -eq 'App' -or $application.Id -match '(?i)chatgpt|codex') {
      $appId = "$($package.PackageFamilyName)!$($application.Id)"
      if ($appId -match '^(?i)OpenAI\.(Codex|ChatGPT)_[A-Za-z0-9._-]+![A-Za-z0-9._-]+$') { Write-Output $appId; exit 0 }
    }
  }
}
"#;
        if let Some(app_id) = run_powershell_for_chatgpt_app_id(script) {
            return Some(app_id);
        }

        // Older installations may be registered in the Start menu without a
        // package query result. Keep this as a compatibility fallback.
        let start_apps_script = r#"$app = Get-StartApps | Where-Object { $_.Name -eq 'ChatGPT' -or $_.AppID -like 'OpenAI.Codex_*!App' -or $_.AppID -like 'OpenAI.ChatGPT*!App' } | Select-Object -First 1; if ($app) { $app.AppID }"#;
        run_powershell_for_chatgpt_app_id(start_apps_script)
    }

    #[cfg(not(windows))]
    {
        None
    }
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
        let output = match Command::new(executable)
            .args([
                "-NoLogo",
                "-NoProfile",
                "-NonInteractive",
                "-Command",
                script,
            ])
            .output()
        {
            Ok(output) if output.status.success() => output,
            _ => continue,
        };
        if let Some(app_id) = String::from_utf8_lossy(&output.stdout)
            .lines()
            .map(str::trim)
            .find(|line| is_valid_chatgpt_app_id(line))
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

    #[cfg(not(windows))]
    {
        Err("目录选择器仅在 Windows 桌面应用中可用".into())
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
    }
}

fn unavailable_message(client_id: &NativeClientId) -> String {
    if matches!(client_id, NativeClientId::ChatGpt) {
        "未找到 ChatGPT Desktop，请确认已安装官方 Windows 客户端".into()
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
            executable: Some(plan.program.display().to_string()),
            working_directory: plan.working_directory.display().to_string(),
            display_command: plan.display_command,
            environment_keys: plan
                .environment
                .iter()
                .map(|(key, _)| key.clone())
                .collect(),
            available: true,
            message: "客户端已就绪".into(),
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
pub fn list_native_clients() -> Vec<NativeClientAvailability> {
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
                }
            }
        }
    })
    .collect()
}

#[tauri::command]
pub fn preview_native_client_launch(
    request: NativeClientLaunchRequest,
) -> NativeClientLaunchPreview {
    build_preview(&request)
}

#[tauri::command]
pub fn launch_native_client(
    request: NativeClientLaunchRequest,
) -> Result<NativeClientLaunchReceipt, String> {
    let executable = find_executable(&request.client_id)
        .ok_or_else(|| unavailable_message(&request.client_id))?;
    let plan = build_launch_plan(&request, executable, cfg!(windows))?;
    let executable_display = plan.program.display().to_string();
    let child = command_for_plan(&plan)
        .spawn()
        .map_err(|error| format!("启动 {} 失败: {error}", request.client_id.label()))?;
    let pid = child.id();
    drop(child);
    Ok(NativeClientLaunchReceipt {
        client_id: request.client_id,
        pid,
        executable: executable_display,
        message: "客户端已启动".into(),
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::path::PathBuf;

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

    #[test]
    fn msix_chatgpt_package_names_cover_current_and_legacy_openai_ids() {
        assert!(is_openai_chatgpt_package(
            "OpenAI.Codex_26.810.7004.0_x64__2p2nqsd0c76g0"
        ));
        assert!(is_openai_chatgpt_package(
            "OpenAI.ChatGPT-Desktop_1.0.0_x64__example"
        ));
        assert!(!is_openai_chatgpt_package(
            "Microsoft.WindowsCalculator_1.0.0_x64__example"
        ));
    }

    #[test]
    fn chatgpt_app_ids_are_strictly_validated_before_shell_launch() {
        assert!(is_valid_chatgpt_app_id("OpenAI.Codex_2p2nqsd0c76g0!App"));
        assert!(is_valid_chatgpt_app_id("OpenAI.ChatGPT_1.0.0!App"));
        assert!(!is_valid_chatgpt_app_id(
            "Microsoft.WindowsCalculator_1.0!App"
        ));
        assert!(!is_valid_chatgpt_app_id(
            "OpenAI.Codex_foo!App; Remove-Item"
        ));
    }

    #[cfg(windows)]
    #[test]
    fn installed_msix_chatgpt_path_is_valid_when_present() {
        if let Some(path) = find_msix_chatgpt_executable() {
            assert!(path.is_file());
            assert!(path.ends_with("ChatGPT.exe") || path.ends_with("Codex.exe"));
        }
    }

    #[cfg(windows)]
    #[test]
    fn installed_msix_chatgpt_app_id_is_valid_when_present() {
        if let Some(app_id) = find_msix_chatgpt_app_id() {
            assert!(is_valid_chatgpt_app_id(&app_id));
            assert!(app_id.ends_with("!App"));
        }
    }
}
