use super::{NativeClientId, CHATGPT_APP_SHELL_PREFIX};
use serde::Deserialize;
use std::path::{Path, PathBuf};

const CODEX_APP_ID: &str = "OpenAI.Codex_2p2nqsd0c76g0!App";
const CLASSIC_APP_ID: &str = "OpenAI.ChatGPT-Desktop_2p2nqsd0c76g0!ChatGPT";
const LEGACY_APP_ID: &str = "OpenAI.ChatGPT_2p2nqsd0c76g0!App";

pub(super) fn is_known_app_id(id: &str) -> bool {
    [CODEX_APP_ID, CLASSIC_APP_ID, LEGACY_APP_ID]
        .iter()
        .any(|known| id.eq_ignore_ascii_case(known))
}

// PackageFullName has five fields: name_version_architecture_resourceId_publisherId.
// Resource packs and bundles do not contain the executable that we launch.
fn package_info(full_name: &str) -> Option<(&'static str, [u16; 4])> {
    let mut fields = full_name.split('_');
    let name = fields.next()?;
    let mut version_parts = fields.next()?.split('.');
    let mut version = [0; 4];
    for part in &mut version {
        *part = version_parts.next()?.parse().ok()?;
    }
    if version_parts.next().is_some()
        || !matches!(fields.next()?, "x64" | "arm64" | "x86" | "neutral")
        || !fields.next()?.is_empty()
        || !fields.next()?.eq_ignore_ascii_case("2p2nqsd0c76g0")
        || fields.next().is_some()
    {
        return None;
    }
    let id = if name.eq_ignore_ascii_case("OpenAI.Codex") {
        CODEX_APP_ID
    } else if name.eq_ignore_ascii_case("OpenAI.ChatGPT-Desktop") {
        CLASSIC_APP_ID
    } else if name.eq_ignore_ascii_case("OpenAI.ChatGPT") {
        LEGACY_APP_ID
    } else {
        return None;
    };
    Some((id, version))
}

fn package_at_executable(executable: &Path, relative: &[&str]) -> Option<&'static str> {
    let mut path = executable;
    for name in relative.iter().rev() {
        if !path
            .file_name()?
            .eq_ignore_ascii_case(std::ffi::OsStr::new(name))
        {
            return None;
        }
        path = path.parent()?;
    }
    package_info(path.file_name()?.to_str()?).map(|(id, _)| id)
}

pub(super) fn is_bundled_cli(path: &Path) -> bool {
    package_at_executable(path, &["app", "resources", "codex.exe"]) == Some(CODEX_APP_ID)
}

pub(super) fn is_desktop_codex_shim(path: &Path) -> bool {
    package_at_executable(path, &["app", "codex.exe"]) == Some(CODEX_APP_ID)
}

#[derive(Deserialize)]
struct RegisteredPackage {
    full_name: String,
    install_location: PathBuf,
    trusted: bool,
}

fn select_client(
    client_id: &NativeClientId,
    packages: &[RegisteredPackage],
    is_file: impl Fn(&Path) -> bool,
) -> Option<PathBuf> {
    let mut candidates = Vec::new();
    for package in packages.iter().filter(|package| package.trusted) {
        let Some((id, version)) = package_info(&package.full_name) else {
            continue;
        };
        let executable = match client_id {
            NativeClientId::ChatGpt => PathBuf::from(format!("{CHATGPT_APP_SHELL_PREFIX}{id}")),
            NativeClientId::Codex if id == CODEX_APP_ID => {
                // Query the precise OS-registered location, including other Appx volumes.
                // Listing WindowsApps normally fails for an unprivileged user.
                if !package.install_location.is_absolute() {
                    continue;
                }
                let cli = package.install_location.join("app/resources/codex.exe");
                if !is_file(&cli) {
                    continue;
                }
                cli
            }
            _ => continue,
        };
        candidates.push(((id == CODEX_APP_ID, version), executable));
    }
    candidates
        .into_iter()
        .max_by_key(|(rank, _)| *rank)
        .map(|(_, path)| path)
}

#[cfg(windows)]
pub(super) fn find_packaged_client(client_id: &NativeClientId) -> Option<PathBuf> {
    use super::{run_client_probe, windows_powershell_candidates, WINDOWS_METADATA_PROBE_TIMEOUT};
    use std::process::Command;

    if !matches!(client_id, NativeClientId::ChatGpt | NativeClientId::Codex) {
        return None;
    }
    // Get-AppxPackage returns the current user's registrations. Store signature and
    // developer-mode checks exclude loose registrations with a copied publisher name.
    let query = r#"
$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
$packages = @(Get-AppxPackage -Name 'OpenAI.*' | Where-Object {
  $_.Name -in @('OpenAI.Codex', 'OpenAI.ChatGPT-Desktop', 'OpenAI.ChatGPT')
} | ForEach-Object {
  @{ full_name=[string]$_.PackageFullName; install_location=[string]$_.InstallLocation;
     trusted=($_.SignatureKind -eq 'Store' -and !$_.IsDevelopmentMode -and $_.Status -eq 'Ok') }
})
ConvertTo-Json -Compress -InputObject $packages
"#;
    let packages = windows_powershell_candidates()
        .into_iter()
        .filter(|exe| exe.is_file())
        .find_map(|exe| {
            let mut command = Command::new(exe);
            command.args([
                "-NoLogo",
                "-NoProfile",
                "-NonInteractive",
                "-Command",
                query,
            ]);
            let output = run_client_probe(&mut command, WINDOWS_METADATA_PROBE_TIMEOUT).ok()?;
            serde_json::from_str::<Vec<RegisteredPackage>>(output.trim()).ok()
        })
        .unwrap_or_default();

    select_client(client_id, &packages, Path::is_file).or_else(|| {
        if !matches!(client_id, NativeClientId::ChatGpt) {
            return None;
        }
        // Older systems may expose the AUMID only through StartApps. Shell activation
        // needs no gateway key and does not require access to the package directory.
        let id = super::run_powershell_for_chatgpt_app_id(r#"
$ids = @(Get-StartApps | Select-Object -ExpandProperty AppID)
foreach ($id in @('OpenAI.Codex_2p2nqsd0c76g0!App', 'OpenAI.ChatGPT-Desktop_2p2nqsd0c76g0!ChatGPT', 'OpenAI.ChatGPT_2p2nqsd0c76g0!App')) {
  if ($ids -contains $id) { Write-Output $id; exit 0 }
}
"#)?;
        if !is_known_app_id(&id) || packages.iter().any(|p| {
            !p.trusted && package_info(&p.full_name).is_some_and(|(app, _)| app.eq_ignore_ascii_case(&id))
        }) {
            return None;
        }
        Some(PathBuf::from(format!("{CHATGPT_APP_SHELL_PREFIX}{id}")))
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    fn registered(full_name: &str) -> RegisteredPackage {
        RegisteredPackage {
            full_name: full_name.to_owned(),
            install_location: std::env::temp_dir().join(full_name),
            trusted: true,
        }
    }

    #[test]
    fn registered_versions_rank_numerically_and_cli_stays_inside_resources() {
        let current = registered("OpenAI.Codex_26.1117.4957.0_x64__2p2nqsd0c76g0");
        let old = registered("OpenAI.Codex_26.917.9999.0_x64__2p2nqsd0c76g0");
        let newest_cli = current.install_location.join("app/resources/codex.exe");
        let old_cli = old.install_location.join("app/resources/codex.exe");
        let desktop_shim = current.install_location.join("app/Codex.exe");
        let packages = [current, old];
        assert_eq!(
            select_client(&NativeClientId::Codex, &packages, |_| true),
            Some(newest_cli.clone())
        );
        assert_eq!(
            select_client(&NativeClientId::Codex, &packages, |p| p != newest_cli),
            Some(old_cli)
        );
        assert!(is_bundled_cli(&newest_cli));
        assert!(!is_bundled_cli(&desktop_shim));
        assert!(is_desktop_codex_shim(&desktop_shim));
        assert!(!is_desktop_codex_shim(&newest_cli));
    }

    #[test]
    fn registered_desktop_activates_without_read_access_and_prefers_current_app() {
        let classic = registered("OpenAI.ChatGPT-Desktop_2026.709.1617.0_x64__2p2nqsd0c76g0");
        let current = registered("OpenAI.Codex_26.1117.4957.0_arm64__2p2nqsd0c76g0");
        let packages = [classic, current];
        let shell = |id| PathBuf::from(format!("{CHATGPT_APP_SHELL_PREFIX}{id}"));
        assert_eq!(
            select_client(&NativeClientId::ChatGpt, &packages, |_| false),
            Some(shell(CODEX_APP_ID))
        );
        assert_eq!(
            select_client(&NativeClientId::ChatGpt, &packages[..1], |_| false),
            Some(shell(CLASSIC_APP_ID))
        );
        assert_eq!(
            select_client(&NativeClientId::Codex, &packages, |_| false),
            None
        );
        assert_eq!(
            select_client(&NativeClientId::Codex, &packages[..1], |_| true),
            None
        );
    }

    #[test]
    fn rejects_untrusted_package_identities_and_non_app_entries() {
        let mut unsigned = registered("OpenAI.Codex_26.1117.4957.0_x64__2p2nqsd0c76g0");
        unsigned.trusted = false;
        let packages = [
            unsigned,
            registered("OpenAI.Codex_26.1117.4957.0_x64__example"),
            registered("OpenAI.ChatGPT-Copy_26.1117.4957.0_x64__2p2nqsd0c76g0"),
            registered("OpenAI.Codex_26.1117.4957.0_neutral_split.language-zh_2p2nqsd0c76g0"),
        ];
        for client in [NativeClientId::ChatGpt, NativeClientId::Codex] {
            assert_eq!(select_client(&client, &packages, |_| true), None);
        }
        assert!(is_known_app_id(CLASSIC_APP_ID));
        assert!(!is_known_app_id(
            "OpenAI.Codex_2p2nqsd0c76g0!CodexCoreCommandRunner"
        ));
        assert!(!is_known_app_id("OpenAI.Codex_other!App"));
    }
}
