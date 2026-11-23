use super::{NativeClientId, NativeClientIdentity};
use serde::Deserialize;
use std::{
    env, fs,
    os::unix::fs::PermissionsExt,
    path::{Component, Path, PathBuf},
};

const CODEX_BUNDLE_ID: &str = "com.openai.codex";
const CLI_PACKAGE: &str = "Contents/Resources/codex-cli";

#[derive(Deserialize)]
struct BundleInfo {
    #[serde(rename = "CFBundleIdentifier")]
    identifier: String,
    #[serde(rename = "CFBundleExecutable")]
    executable: String,
    #[serde(rename = "CFBundleDisplayName")]
    display_name: Option<String>,
    #[serde(rename = "CFBundleName")]
    name: Option<String>,
    #[serde(rename = "CFBundleShortVersionString")]
    version: Option<String>,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct CliPackage {
    layout_version: u32,
    variant: String,
    entrypoint: PathBuf,
    version: String,
}

fn is_executable(path: &Path) -> bool {
    fs::metadata(path).is_ok_and(|file| file.is_file() && file.permissions().mode() & 0o111 != 0)
}

fn executable_in(root: &Path, relative: &Path) -> Option<PathBuf> {
    if !relative
        .components()
        .all(|c| matches!(c, Component::Normal(_)))
    {
        return None;
    }
    let path = root.join(relative);
    (is_executable(&path)
        && path
            .canonicalize()
            .ok()?
            .starts_with(root.canonicalize().ok()?))
    .then_some(path)
}

fn bundle_info(bundle: &Path) -> Option<BundleInfo> {
    let info: BundleInfo = plist::from_file(bundle.join("Contents/Info.plist")).ok()?;
    if info.identifier != CODEX_BUNDLE_ID || Path::new(&info.executable).components().count() != 1 {
        return None;
    }
    executable_in(bundle, &Path::new("Contents/MacOS").join(&info.executable))?;
    Some(info)
}

fn bundled_cli(bundle: &Path) -> Option<(PathBuf, semver::Version)> {
    let root = bundle.join(CLI_PACKAGE);
    if !root
        .canonicalize()
        .ok()?
        .starts_with(bundle.canonicalize().ok()?)
    {
        return None;
    }
    let manifest = fs::File::open(root.join("codex-package.json")).ok()?;
    let package: CliPackage = serde_json::from_reader(manifest).ok()?;
    if package.layout_version != 1 || package.variant != "codex" {
        return None;
    }
    Some((
        executable_in(&root, &package.entrypoint)?,
        package.version.parse().ok()?,
    ))
}

// A desktop-launched console can have a shorter PATH than the user's shell.
// Preserve the user's standalone CLI ahead of the desktop app's bundled CLI.
pub(super) fn find_installed(client: &NativeClientId) -> Option<PathBuf> {
    let home = env::var_os("HOME").map(PathBuf::from);
    let mut standalone = Vec::new();
    if matches!(client, NativeClientId::Codex) {
        if let Some(home) = &home {
            standalone.push(home.join(".local/bin/codex"));
        }
        standalone.extend(["/opt/homebrew/bin/codex", "/usr/local/bin/codex"].map(PathBuf::from));
    }
    let mut app_dirs = vec![PathBuf::from("/Applications")];
    if let Some(home) = home {
        app_dirs.push(home.join("Applications"));
    }
    find_in(client, &standalone, &app_dirs)
}

fn find_in(
    client: &NativeClientId,
    standalone: &[PathBuf],
    app_dirs: &[PathBuf],
) -> Option<PathBuf> {
    match client {
        NativeClientId::Codex => {
            if let Some(cli) = standalone.iter().find(|cli| is_executable(cli)) {
                return Some(cli.clone());
            }
        }
        NativeClientId::ChatGpt => {}
        _ => return None,
    }
    for app_name in ["ChatGPT.app", "Codex.app"] {
        for dir in app_dirs {
            let bundle = dir.join(app_name);
            if bundle_info(&bundle).is_some() {
                if matches!(client, NativeClientId::ChatGpt) {
                    return Some(bundle);
                }
                if let Some((cli, _)) = bundled_cli(&bundle) {
                    return Some(cli);
                }
            }
        }
    }
    None
}

pub(super) fn identity(client: &NativeClientId, executable: &Path) -> Option<NativeClientIdentity> {
    match client {
        NativeClientId::ChatGpt => {
            let info = bundle_info(executable)?;
            let product_name = info.display_name.or(info.name).unwrap_or(info.executable);
            let probe_status = if info.version.is_some() {
                "detected"
            } else {
                "unavailable"
            };
            Some(NativeClientIdentity {
                product_name,
                version: info.version,
                installation_source: "macos_bundle",
                release_channel: "unknown",
                package_identity: Some(info.identifier),
                probe_status,
            })
        }
        NativeClientId::Codex => {
            // A standalone PATH entry can also be a symlink into the app.
            let path = executable.canonicalize().ok()?;
            let bundle = path.ancestors().find(|dir| {
                dir.extension().is_some_and(|ext| ext == "app") && bundle_info(dir).is_some()
            })?;
            let (entrypoint, version) = bundled_cli(bundle)?;
            if entrypoint.canonicalize().ok()? != path {
                return None;
            }
            Some(NativeClientIdentity {
                product_name: "Codex CLI".into(),
                release_channel: if version.pre.is_empty() {
                    "stable"
                } else {
                    "prerelease"
                },
                version: Some(version.to_string()),
                installation_source: "desktop_bundled",
                package_identity: Some(CODEX_BUNDLE_ID.into()),
                probe_status: "detected",
            })
        }
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::os::unix::fs::symlink;
    use tempfile::TempDir;

    struct Fixture {
        dir: TempDir,
        bundle: PathBuf,
    }

    impl Fixture {
        fn new(app_name: &str, bundle_id: &str) -> Self {
            let dir = tempfile::tempdir().unwrap();
            let bundle = dir.path().join(app_name);
            let app_exe = bundle.join("Contents/MacOS/ChatGPT");
            fs::create_dir_all(app_exe.parent().unwrap()).unwrap();
            make_executable(&app_exe);
            let mut info = plist::Dictionary::new();
            for (key, value) in [
                ("CFBundleIdentifier", bundle_id),
                ("CFBundleExecutable", "ChatGPT"),
                ("CFBundleShortVersionString", "26.1117.41037"),
                ("CFBundleDisplayName", "ChatGPT"),
            ] {
                info.insert(key.into(), plist::Value::String(value.into()));
            }
            plist::Value::Dictionary(info)
                .to_file_binary(bundle.join("Contents/Info.plist"))
                .unwrap();
            Self { dir, bundle }
        }

        fn write_cli_package(&self, entry: &str) -> PathBuf {
            let root = self.bundle.join(CLI_PACKAGE);
            let cli = root.join("bin/codex");
            fs::create_dir_all(cli.parent().unwrap()).unwrap();
            make_executable(&cli);
            fs::write(
                root.join("codex-package.json"),
                serde_json::json!({
                    "layoutVersion": 1, "variant": "codex", "target": "aarch64-apple-darwin",
                    "version": "0.171.0-alpha.13", "entrypoint": entry
                })
                .to_string(),
            )
            .unwrap();
            cli
        }

        fn find(&self, client: &NativeClientId, standalone: &[PathBuf]) -> Option<PathBuf> {
            find_in(client, standalone, &[self.dir.path().to_owned()])
        }
    }

    fn make_executable(path: &Path) {
        fs::write(path, b"fixture").unwrap();
        fs::set_permissions(path, fs::Permissions::from_mode(0o755)).unwrap();
    }

    #[test]
    fn reads_desktop_and_cli_versions_separately_from_their_manifests() {
        let fixture = Fixture::new("ChatGPT.app", CODEX_BUNDLE_ID);
        let cli = fixture.write_cli_package("bin/codex");
        assert_eq!(
            fixture.find(&NativeClientId::ChatGpt, &[]).unwrap(),
            fixture.bundle
        );
        assert_eq!(fixture.find(&NativeClientId::Codex, &[]).unwrap(), cli);
        let app = identity(&NativeClientId::ChatGpt, &fixture.bundle).unwrap();
        assert_eq!(app.product_name, "ChatGPT");
        assert_eq!(app.installation_source, "macos_bundle");
        assert_eq!(app.version.as_deref(), Some("26.1117.41037"));
        assert_eq!(app.release_channel, "unknown");
        let link = fixture.dir.path().join("codex");
        symlink(cli, &link).unwrap();
        let bundled = identity(&NativeClientId::Codex, &link).unwrap();
        assert_eq!(bundled.version.as_deref(), Some("0.171.0-alpha.13"));
        assert_eq!(bundled.installation_source, "desktop_bundled");
        assert_eq!(bundled.release_channel, "prerelease");
        assert_eq!(bundled.package_identity.as_deref(), Some(CODEX_BUNDLE_ID));
    }

    #[test]
    fn prefers_standalone_cli_and_does_not_depend_on_cli_to_open_desktop() {
        let fixture = Fixture::new("Codex.app", CODEX_BUNDLE_ID);
        assert_eq!(
            fixture.find(&NativeClientId::ChatGpt, &[]).unwrap(),
            fixture.bundle
        );
        assert!(fixture.find(&NativeClientId::Codex, &[]).is_none());
        let bundled = fixture.write_cli_package("bin/codex");
        let standalone = fixture.dir.path().join("codex");
        fs::write(&standalone, b"not executable").unwrap();
        let candidates = std::slice::from_ref(&standalone);
        assert_eq!(
            fixture.find(&NativeClientId::Codex, candidates).unwrap(),
            bundled
        );
        make_executable(&standalone);
        assert_eq!(
            fixture.find(&NativeClientId::Codex, candidates).unwrap(),
            standalone
        );
        assert!(identity(&NativeClientId::Codex, &standalone).is_none());
    }

    #[test]
    fn rejects_unrelated_apps_and_package_entrypoints_outside_the_bundle() {
        let unrelated = Fixture::new("ChatGPT.app", "org.example.other");
        unrelated.write_cli_package("bin/codex");
        assert!(unrelated.find(&NativeClientId::ChatGpt, &[]).is_none());
        assert!(unrelated.find(&NativeClientId::Codex, &[]).is_none());

        let fixture = Fixture::new("ChatGPT.app", CODEX_BUNDLE_ID);
        let outside = fixture.dir.path().join("outside");
        make_executable(&outside);
        fixture.write_cli_package(outside.to_str().unwrap());
        assert!(fixture.find(&NativeClientId::Codex, &[]).is_none());
        fixture.write_cli_package("../../MacOS/ChatGPT");
        assert!(fixture.find(&NativeClientId::Codex, &[]).is_none());
        let cli = fixture.write_cli_package("bin/codex");
        fs::remove_file(&cli).unwrap();
        symlink(outside, cli).unwrap();
        assert!(fixture.find(&NativeClientId::Codex, &[]).is_none());
    }
}
