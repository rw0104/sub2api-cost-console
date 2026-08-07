use std::collections::{BTreeMap, BTreeSet};

#[derive(Clone, Debug, Default, Eq, PartialEq)]
struct WindowsProxySettings {
    enabled: bool,
    server: String,
    bypass: String,
}

pub(crate) fn backend_proxy_environment() -> BTreeMap<String, String> {
    if explicit_proxy_environment_present() {
        return BTreeMap::new();
    }

    load_windows_proxy_settings()
        .filter(|settings| settings.enabled)
        .map(|settings| proxy_environment_from_windows(&settings))
        .unwrap_or_default()
}

fn explicit_proxy_environment_present() -> bool {
    ["HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY"]
        .iter()
        .any(|name| std::env::var_os(name).is_some())
}

#[cfg(windows)]
fn load_windows_proxy_settings() -> Option<WindowsProxySettings> {
    use winreg::{enums::HKEY_CURRENT_USER, RegKey};

    let internet_settings = RegKey::predef(HKEY_CURRENT_USER)
        .open_subkey("Software\\Microsoft\\Windows\\CurrentVersion\\Internet Settings")
        .ok()?;
    let enabled = internet_settings
        .get_value::<u32, _>("ProxyEnable")
        .unwrap_or(0)
        != 0;
    let server = internet_settings
        .get_value::<String, _>("ProxyServer")
        .unwrap_or_default();
    let bypass = internet_settings
        .get_value::<String, _>("ProxyOverride")
        .unwrap_or_default();
    Some(WindowsProxySettings {
        enabled,
        server,
        bypass,
    })
}

#[cfg(not(windows))]
fn load_windows_proxy_settings() -> Option<WindowsProxySettings> {
    None
}

fn proxy_environment_from_windows(settings: &WindowsProxySettings) -> BTreeMap<String, String> {
    let mut environment = parse_proxy_server(&settings.server);
    if environment.is_empty() {
        return environment;
    }

    environment.insert("NO_PROXY".into(), normalize_proxy_bypass(&settings.bypass));
    environment
}

fn parse_proxy_server(server: &str) -> BTreeMap<String, String> {
    let mut environment = BTreeMap::new();
    let server = server.trim();
    if server.is_empty() {
        return environment;
    }

    if !server.contains('=') {
        let proxy = normalize_proxy_endpoint(server, "http");
        environment.insert("HTTP_PROXY".into(), proxy.clone());
        environment.insert("HTTPS_PROXY".into(), proxy);
        return environment;
    }

    let mut socks_proxy = None;
    for entry in server.split(';').map(str::trim).filter(|entry| !entry.is_empty()) {
        let Some((kind, endpoint)) = entry.split_once('=') else {
            continue;
        };
        match kind.trim().to_ascii_lowercase().as_str() {
            "http" => {
                environment.insert(
                    "HTTP_PROXY".into(),
                    normalize_proxy_endpoint(endpoint, "http"),
                );
            }
            "https" => {
                environment.insert(
                    "HTTPS_PROXY".into(),
                    normalize_proxy_endpoint(endpoint, "http"),
                );
            }
            "socks" | "socks5" => {
                socks_proxy = Some(normalize_proxy_endpoint(endpoint, "socks5"));
            }
            _ => {}
        }
    }

    if let Some(proxy) = socks_proxy {
        environment
            .entry("HTTP_PROXY".into())
            .or_insert_with(|| proxy.clone());
        environment
            .entry("HTTPS_PROXY".into())
            .or_insert(proxy);
    }
    environment
}

fn normalize_proxy_endpoint(endpoint: &str, default_scheme: &str) -> String {
    let endpoint = endpoint.trim();
    if endpoint.contains("://") {
        endpoint.to_string()
    } else {
        format!("{default_scheme}://{endpoint}")
    }
}

fn normalize_proxy_bypass(bypass: &str) -> String {
    let mut values = BTreeSet::from([
        "localhost".to_string(),
        "127.0.0.1".to_string(),
        "::1".to_string(),
    ]);
    for raw in bypass.split(';').map(str::trim).filter(|value| !value.is_empty()) {
        if raw.eq_ignore_ascii_case("<local>") {
            continue;
        }
        values.insert(normalize_bypass_entry(raw));
    }
    values.into_iter().collect::<Vec<_>>().join(",")
}

fn normalize_bypass_entry(entry: &str) -> String {
    if let Some(prefix) = entry.strip_suffix(".*") {
        let octets = prefix.split('.').collect::<Vec<_>>();
        if !octets.is_empty()
            && octets.len() < 4
            && octets.iter().all(|part| part.parse::<u8>().is_ok())
        {
            let mut address = octets.join(".");
            for _ in octets.len()..4 {
                address.push_str(".0");
            }
            return format!("{address}/{}", octets.len() * 8);
        }
    }
    entry.strip_prefix("*.").unwrap_or(entry).to_string()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn maps_single_windows_proxy_to_http_and_https() {
        let environment = proxy_environment_from_windows(&WindowsProxySettings {
            enabled: true,
            server: "127.0.0.1:10808".into(),
            bypass: "<local>;localhost;127.*;192.168.*".into(),
        });

        assert_eq!(environment.get("HTTP_PROXY").unwrap(), "http://127.0.0.1:10808");
        assert_eq!(environment.get("HTTPS_PROXY").unwrap(), "http://127.0.0.1:10808");
        let no_proxy = environment.get("NO_PROXY").unwrap();
        assert!(no_proxy.contains("127.0.0.0/8"));
        assert!(no_proxy.contains("192.168.0.0/16"));
    }

    #[test]
    fn maps_protocol_specific_and_socks_windows_proxies() {
        let environment = parse_proxy_server(
            "http=127.0.0.1:8080;https=127.0.0.1:8443;socks=127.0.0.1:1080",
        );

        assert_eq!(environment.get("HTTP_PROXY").unwrap(), "http://127.0.0.1:8080");
        assert_eq!(environment.get("HTTPS_PROXY").unwrap(), "http://127.0.0.1:8443");
    }

    #[test]
    fn uses_socks_when_no_protocol_proxy_exists() {
        let environment = parse_proxy_server("socks=127.0.0.1:1080");

        assert_eq!(environment.get("HTTP_PROXY").unwrap(), "socks5://127.0.0.1:1080");
        assert_eq!(environment.get("HTTPS_PROXY").unwrap(), "socks5://127.0.0.1:1080");
    }
}
