//! Build-time isolation; stable builds keep their existing identifiers/ports.
pub const PREVIEW: bool = cfg!(feature = "plugin-preview");
pub const BACKEND_PORT: u16 = if PREVIEW { 19765 } else { 18765 };
pub const POSTGRES_PORT: u16 = if PREVIEW { 25432 } else { 15432 };
pub const REDIS_PORT: u16 = if PREVIEW { 26379 } else { 16379 };
pub const POSTGRES_CONTAINER: &str = if PREVIEW {
    "sub2api-plugin-preview-postgres"
} else {
    "sub2api-cost-postgres"
};
pub const VALKEY_CONTAINER: &str = if PREVIEW {
    "sub2api-plugin-preview-valkey"
} else {
    "sub2api-cost-valkey"
};
pub const DATABASE_NAME: &str = if PREVIEW {
    "sub2api_plugin_preview"
} else {
    "sub2api"
};
pub const MANAGED_LABEL: &str = if PREVIEW {
    "com.sub2api.plugin-preview.managed=true"
} else {
    "com.sub2api.cost-console.managed=true"
};

pub fn allowed_preview_environment(name: &str) -> bool {
    matches!(
        name.to_ascii_uppercase().as_str(),
        "PATH"
            | "PATHEXT"
            | "SYSTEMROOT"
            | "WINDIR"
            | "COMSPEC"
            | "TEMP"
            | "TMP"
            | "APPDATA"
            | "LOCALAPPDATA"
            | "USERPROFILE"
            | "PROGRAMDATA"
            | "PROGRAMFILES"
            | "PROGRAMFILES(X86)"
            | "HOME"
            | "DOCKER_HOST"
            | "DOCKER_CONTEXT"
            | "DOCKER_CONFIG"
            | "HTTP_PROXY"
            | "HTTPS_PROXY"
            | "ALL_PROXY"
            | "NO_PROXY"
    )
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn preview_never_inherits_service_configuration() {
        for name in [
            "CONFIG_FILE",
            "DATA_DIR",
            "DATABASE_HOST",
            "DATABASE_PORT",
            "REDIS_HOST",
            "AUTO_SETUP",
            "SKIP_SETUP",
            "JWT_SECRET",
            "PLUGINS_DATA_DIR",
        ] {
            assert!(!allowed_preview_environment(name));
        }
        assert!(allowed_preview_environment("Path"));
    }
    #[test]
    fn profile_ports_and_names_are_consistent() {
        if PREVIEW {
            assert_eq!(
                (BACKEND_PORT, POSTGRES_PORT, REDIS_PORT),
                (19765, 25432, 26379)
            );
            assert!(POSTGRES_CONTAINER.contains("plugin-preview"));
            assert!(VALKEY_CONTAINER.contains("plugin-preview"));
            assert_eq!(DATABASE_NAME, "sub2api_plugin_preview");
            let config: serde_json::Value =
                serde_json::from_str(include_str!("../tauri.plugin-preview.conf.json")).unwrap();
            assert_eq!(config["identifier"], "com.sub2api.plugin-preview");
            assert_eq!(config["mainBinaryName"], "sub2api-plugin-preview");
            assert_eq!(config["bundle"]["createUpdaterArtifacts"], false);
        } else {
            assert_eq!(
                (BACKEND_PORT, POSTGRES_PORT, REDIS_PORT),
                (18765, 15432, 16379)
            );
        }
    }
}
