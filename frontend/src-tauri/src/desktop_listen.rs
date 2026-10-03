//! User-configurable listen address of the managed backend. The saved value is
//! read once per launch, so a change takes effect after the desktop restarts.
use crate::desktop_profile::{BACKEND_PORT, POSTGRES_PORT, PREVIEW, REDIS_PORT};
use serde::{Deserialize, Serialize};
use std::{
    fs,
    net::{Ipv4Addr, SocketAddr, TcpListener, TcpStream},
    path::{Path, PathBuf},
    sync::RwLock,
    time::Duration,
};
use tauri::{AppHandle, Manager};

const SETTINGS_FILE: &str = "desktop-listen.json";

#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
pub struct ListenSettings {
    pub host: String,
    pub port: u16,
}

impl Default for ListenSettings {
    fn default() -> Self {
        Self {
            host: Ipv4Addr::LOCALHOST.to_string(),
            port: BACKEND_PORT,
        }
    }
}

impl ListenSettings {
    fn bind_ip(&self) -> Ipv4Addr {
        self.host.parse().unwrap_or(Ipv4Addr::LOCALHOST)
    }

    /// Address local clients use to reach the backend; a wildcard bind is not
    /// a connectable address on Windows.
    pub fn connect_address(&self) -> SocketAddr {
        let ip = self.bind_ip();
        let ip = if ip.is_unspecified() {
            Ipv4Addr::LOCALHOST
        } else {
            ip
        };
        SocketAddr::from((ip, self.port))
    }
}

#[derive(Clone, Debug, Serialize)]
pub struct ListenSettingsView {
    /// Saved value, applied on the next launch.
    pub host: String,
    pub port: u16,
    /// Value the running backend was started with.
    pub active_host: String,
    pub active_port: u16,
    pub default_port: u16,
    pub editable: bool,
    pub restart_required: bool,
}

static ACTIVE: RwLock<Option<ListenSettings>> = RwLock::new(None);

pub fn active() -> ListenSettings {
    ACTIVE
        .read()
        .expect("listen settings poisoned")
        .clone()
        .unwrap_or_default()
}

pub fn validate(host: &str, port: u16) -> Result<ListenSettings, String> {
    let host = host.trim();
    let ip = if host.eq_ignore_ascii_case("localhost") {
        Ipv4Addr::LOCALHOST
    } else {
        host.parse::<Ipv4Addr>().map_err(|_| {
            "监听地址只支持 localhost 或 IPv4 地址，例如 127.0.0.1、0.0.0.0、192.168.1.10"
                .to_string()
        })?
    };
    if port < 1024 {
        return Err("端口必须在 1024 到 65535 之间".into());
    }
    if port == POSTGRES_PORT || port == REDIS_PORT {
        return Err(format!("端口 {port} 已保留给本机数据服务，请换一个端口"));
    }
    Ok(ListenSettings {
        host: ip.to_string(),
        port,
    })
}

fn settings_path(app_data_dir: &Path) -> PathBuf {
    app_data_dir.join(SETTINGS_FILE)
}

fn load(app_data_dir: &Path) -> ListenSettings {
    fs::read(settings_path(app_data_dir))
        .ok()
        .and_then(|bytes| serde_json::from_slice::<ListenSettings>(&bytes).ok())
        .and_then(|saved| validate(&saved.host, saved.port).ok())
        .unwrap_or_default()
}

/// Resolves the address for this launch. Until the setup wizard has written
/// its config the setup endpoints are unauthenticated, so the backend stays
/// on loopback regardless of the saved host. A saved LAN address that this
/// machine no longer owns also falls back to loopback, since the settings
/// panel is only reachable once the backend is up.
fn resolve(app_data_dir: &Path, setup_complete: bool) -> ListenSettings {
    if PREVIEW {
        return ListenSettings::default();
    }
    let mut settings = load(app_data_dir);
    let ip = settings.bind_ip();
    let assignable = ip.is_unspecified() || TcpListener::bind((ip, 0)).is_ok();
    if (!setup_complete && !ip.is_loopback()) || !assignable {
        settings.host = Ipv4Addr::LOCALHOST.to_string();
    }
    settings
}

pub fn initialize(app_data_dir: &Path, setup_complete: bool) {
    *ACTIVE.write().expect("listen settings poisoned") =
        Some(resolve(app_data_dir, setup_complete));
}

fn view(app_data_dir: &Path) -> ListenSettingsView {
    let saved = if PREVIEW {
        ListenSettings::default()
    } else {
        load(app_data_dir)
    };
    let current = active();
    ListenSettingsView {
        restart_required: saved != current,
        host: saved.host,
        port: saved.port,
        active_host: current.host,
        active_port: current.port,
        default_port: BACKEND_PORT,
        editable: !PREVIEW,
    }
}

fn app_data_dir(app: &AppHandle) -> Result<PathBuf, String> {
    app.path()
        .app_data_dir()
        .map_err(|error| format!("无法定位应用数据目录: {error}"))
}

#[tauri::command]
pub fn desktop_listen_settings(app: AppHandle) -> Result<ListenSettingsView, String> {
    Ok(view(&app_data_dir(&app)?))
}

#[tauri::command]
pub fn desktop_listen_settings_save(
    app: AppHandle,
    host: String,
    port: u16,
) -> Result<ListenSettingsView, String> {
    if PREVIEW {
        return Err("测试版使用固定的隔离端口，不支持修改监听地址".into());
    }
    let settings = validate(&host, port)?;
    let current = active();
    if settings.port != current.port
        && TcpStream::connect_timeout(&settings.connect_address(), Duration::from_millis(250))
            .is_ok()
    {
        return Err(format!(
            "端口 {} 已被其他程序占用，请换一个端口",
            settings.port
        ));
    }
    let directory = app_data_dir(&app)?;
    fs::create_dir_all(&directory).map_err(|error| format!("无法创建应用数据目录: {error}"))?;
    let bytes = serde_json::to_vec_pretty(&settings)
        .map_err(|error| format!("无法序列化监听设置: {error}"))?;
    fs::write(settings_path(&directory), bytes)
        .map_err(|error| format!("无法保存监听设置: {error}"))?;
    Ok(view(&directory))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn validate_accepts_localhost_alias_and_ipv4_binds() {
        assert_eq!(validate(" LocalHost ", 18765).unwrap().host, "127.0.0.1");
        assert_eq!(validate("0.0.0.0", 28000).unwrap().host, "0.0.0.0");
        assert_eq!(validate("192.168.1.10", 28000).unwrap().port, 28000);
    }

    #[test]
    fn validate_rejects_hostnames_ipv6_and_reserved_ports() {
        assert!(validate("example.com", 18765).is_err());
        assert!(validate("::1", 18765).is_err());
        assert!(validate("127.0.0.1", 80).is_err());
        assert!(validate("127.0.0.1", POSTGRES_PORT).is_err());
        assert!(validate("127.0.0.1", REDIS_PORT).is_err());
    }

    #[test]
    fn wildcard_bind_is_reached_through_loopback() {
        let settings = validate("0.0.0.0", 28000).unwrap();
        assert_eq!(
            settings.connect_address(),
            SocketAddr::from(([127, 0, 0, 1], 28000))
        );
        let lan = validate("192.168.1.10", 28000).unwrap();
        assert_eq!(lan.connect_address().to_string(), "192.168.1.10:28000");
    }

    #[test]
    fn saved_settings_round_trip_and_fall_back_to_defaults() {
        let directory = std::env::temp_dir().join(format!(
            "sub2api-listen-test-{}-{:?}",
            std::process::id(),
            std::thread::current().id()
        ));
        fs::create_dir_all(&directory).unwrap();
        assert_eq!(load(&directory), ListenSettings::default());

        let saved = validate("0.0.0.0", 28001).unwrap();
        fs::write(
            settings_path(&directory),
            serde_json::to_vec(&saved).unwrap(),
        )
        .unwrap();
        assert_eq!(load(&directory), saved);

        fs::write(settings_path(&directory), b"{\"host\":\"bad\",\"port\":1}").unwrap();
        assert_eq!(load(&directory), ListenSettings::default());
        fs::remove_dir_all(&directory).unwrap();
    }

    #[cfg(not(feature = "plugin-preview"))]
    #[test]
    fn non_loopback_bind_waits_for_completed_setup() {
        let directory = std::env::temp_dir().join(format!(
            "sub2api-listen-setup-test-{}-{:?}",
            std::process::id(),
            std::thread::current().id()
        ));
        fs::create_dir_all(&directory).unwrap();
        let saved = validate("0.0.0.0", 28002).unwrap();
        fs::write(
            settings_path(&directory),
            serde_json::to_vec(&saved).unwrap(),
        )
        .unwrap();

        let before_setup = resolve(&directory, false);
        assert_eq!(before_setup.host, "127.0.0.1");
        assert_eq!(before_setup.port, 28002);
        assert_eq!(resolve(&directory, true), saved);

        // TEST-NET-1 is never assigned to a local interface.
        let unowned = validate("192.0.2.1", 28002).unwrap();
        fs::write(
            settings_path(&directory),
            serde_json::to_vec(&unowned).unwrap(),
        )
        .unwrap();
        assert_eq!(resolve(&directory, true).host, "127.0.0.1");
        fs::remove_dir_all(&directory).unwrap();
    }
}
