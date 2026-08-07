#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod desktop_runtime;
mod setup_environment;

use desktop_runtime::{
    check_core_update, desktop_backend_prepare_relaunch, desktop_backend_start,
    desktop_backend_status, desktop_backend_stop, initialize_backend, install_core_update,
    prepare_core_rollback, shutdown_backend, start_backend,
};
use setup_environment::{detect_setup_environment, provision_quick_setup};
use tauri::Manager;

fn main() {
    let app = tauri::Builder::default()
        .plugin(tauri_plugin_shell::init())
        .plugin(tauri_plugin_process::init())
        .plugin(tauri_plugin_updater::Builder::new().build())
        .setup(|app| {
            let handle = app.handle().clone();
            let supervisor = initialize_backend(&handle)
                .map_err(|error| format!("failed to initialize desktop backend: {error}"))?;
            app.manage(supervisor.clone());
            if let Err(error) = start_backend(handle, supervisor) {
                eprintln!("failed to start managed Sub2API backend: {error}");
            }
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
            install_core_update,
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
