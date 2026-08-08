#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod desktop_proxy;
mod desktop_runtime;
mod desktop_shell;
mod managed_core_process;
mod setup_environment;

use desktop_runtime::{
    check_core_update, desktop_backend_prepare_relaunch, desktop_backend_start,
    desktop_backend_status, desktop_backend_stop, initialize_backend, inspect_core_identity,
    install_core_update, prepare_core_rollback, restore_bundled_core, shutdown_backend,
    start_backend,
};
use desktop_shell::{handle_main_window_event, setup_desktop_shell};
use setup_environment::{detect_setup_environment, provision_quick_setup};
use tauri::Manager;

fn main() {
    let app = tauri::Builder::default()
        .plugin(tauri_plugin_shell::init())
        .plugin(tauri_plugin_opener::init())
        .plugin(tauri_plugin_process::init())
        .plugin(tauri_plugin_updater::Builder::new().build())
        .on_window_event(handle_main_window_event)
        .setup(|app| {
            let handle = app.handle().clone();
            let supervisor = initialize_backend(&handle)
                .map_err(|error| format!("failed to initialize desktop backend: {error}"))?;
            app.manage(supervisor.clone());
            if let Err(error) = start_backend(handle, supervisor) {
                eprintln!("failed to start managed Sub2API backend: {error}");
            }
            setup_desktop_shell(app)
                .map_err(|error| format!("failed to initialize system tray: {error}"))?;
            Ok(())
        })
        .invoke_handler(tauri::generate_handler![
            desktop_backend_status,
            desktop_backend_start,
            desktop_backend_stop,
            desktop_backend_prepare_relaunch,
            detect_setup_environment,
            provision_quick_setup,
            check_core_update,
            inspect_core_identity,
            install_core_update,
            restore_bundled_core,
            prepare_core_rollback,
        ])
        .build(tauri::generate_context!())
        .expect("error while building Sub2API Cost Console");

    app.run(|app_handle, event| {
        if matches!(
            event,
            tauri::RunEvent::Exit | tauri::RunEvent::ExitRequested { .. }
        ) {
            shutdown_backend(app_handle);
        }
    });
}

#[cfg(test)]
mod tests {
    #[test]
    fn desktop_capabilities_allow_the_fullscreen_control() {
        let capabilities: serde_json::Value =
            serde_json::from_str(include_str!("../capabilities/default.json"))
                .expect("desktop capabilities must be valid JSON");
        let permissions = capabilities["permissions"]
            .as_array()
            .expect("desktop capabilities must declare permissions");

        assert!(
            permissions
                .iter()
                .any(|permission| permission.as_str() == Some("core:window:allow-set-fullscreen")),
            "the cost-center fullscreen control requires set_fullscreen permission"
        );
    }
}
