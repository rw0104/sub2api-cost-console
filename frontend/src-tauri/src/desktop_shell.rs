use tauri::{
    menu::{Menu, MenuItem},
    tray::{MouseButton, MouseButtonState, TrayIconBuilder, TrayIconEvent},
    App, AppHandle, Manager, Runtime, Window, WindowEvent,
};

const MAIN_WINDOW_LABEL: &str = "main";
const TRAY_ICON_ID: &str = "sub2api-cost-console-tray";
const TRAY_SHOW_ID: &str = "tray-show";
const TRAY_QUIT_ID: &str = "tray-quit";

// Keep native tests in the binary target so they inherit Tauri's Windows
// Common Controls v6 manifest, which WebView2 window creation requires.
#[cfg(all(test, windows))]
#[path = "desktop_shell/native_tests.rs"]
mod native_tests;

// Exercise the patched dependency's real Windows predicate in the normal host
// test suite, without enabling the dependency's unrelated example/dev stack.
#[cfg(all(test, windows))]
#[path = "../vendor/tray-icon/src/platform_impl/windows/rect_result.rs"]
mod tray_rect_contract;

#[derive(Debug, PartialEq, Eq)]
enum TrayMenuAction {
    ShowMainWindow,
    QuitApplication,
}

fn tray_menu_action(menu_id: &str) -> Option<TrayMenuAction> {
    match menu_id {
        TRAY_SHOW_ID => Some(TrayMenuAction::ShowMainWindow),
        TRAY_QUIT_ID => Some(TrayMenuAction::QuitApplication),
        _ => None,
    }
}

pub(crate) fn show_main_window<R: Runtime>(app: &AppHandle<R>) -> tauri::Result<()> {
    if let Some(window) = app.get_webview_window(MAIN_WINDOW_LABEL) {
        window.show()?;
        window.unminimize()?;
        window.set_focus()?;
    }
    Ok(())
}

fn is_tray_window_activation(event: &TrayIconEvent) -> bool {
    matches!(
        event,
        TrayIconEvent::Click {
            button: MouseButton::Left,
            button_state: MouseButtonState::Up,
            ..
        } | TrayIconEvent::DoubleClick {
            button: MouseButton::Left,
            ..
        }
    )
}

pub fn setup_desktop_shell<R: Runtime>(app: &App<R>) -> tauri::Result<()> {
    let show = MenuItem::with_id(app, TRAY_SHOW_ID, "显示主窗口", true, None::<&str>)?;
    let quit = MenuItem::with_id(app, TRAY_QUIT_ID, "退出 Sub2API", true, None::<&str>)?;
    let menu = Menu::with_items(app, &[&show, &quit])?;

    let mut tray = TrayIconBuilder::with_id(TRAY_ICON_ID)
        .tooltip(if crate::desktop_profile::PREVIEW {
            "Sub2API Plugin Preview — 独立测试版"
        } else {
            "Sub2API Cost Console"
        })
        .menu(&menu)
        .show_menu_on_left_click(false)
        .on_menu_event(|app, event| match tray_menu_action(event.id().as_ref()) {
            Some(TrayMenuAction::ShowMainWindow) => {
                if let Err(error) = show_main_window(app) {
                    eprintln!("failed to show main window from tray: {error}");
                }
            }
            Some(TrayMenuAction::QuitApplication) => app.exit(0),
            None => {}
        })
        .on_tray_icon_event(|tray, event| {
            if is_tray_window_activation(&event) {
                if let Err(error) = show_main_window(tray.app_handle()) {
                    eprintln!("failed to show main window from tray click: {error}");
                }
            }
        });

    if let Some(icon) = app.default_window_icon() {
        tray = tray.icon(icon.clone());
    }
    tray.build(app)?;
    Ok(())
}

pub fn handle_main_window_event<R: Runtime>(window: &Window<R>, event: &WindowEvent) {
    if window.label() != MAIN_WINDOW_LABEL {
        return;
    }

    match event {
        WindowEvent::CloseRequested { api, .. } => {
            api.prevent_close();
            if let Err(error) = window.hide() {
                eprintln!("failed to hide main window to tray: {error}");
            }
        }
        WindowEvent::Resized(_) if window.is_minimized().unwrap_or(false) => {
            if let Err(error) = window.hide() {
                eprintln!("failed to minimize main window to tray: {error}");
            }
        }
        _ => {}
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn tray_menu_exposes_show_and_explicit_quit_actions() {
        assert_eq!(
            tray_menu_action(TRAY_SHOW_ID),
            Some(TrayMenuAction::ShowMainWindow)
        );
        assert_eq!(
            tray_menu_action(TRAY_QUIT_ID),
            Some(TrayMenuAction::QuitApplication)
        );
        assert_eq!(tray_menu_action("unknown"), None);
    }

    #[test]
    fn tray_activation_includes_windows_double_click() {
        // Keep the event policy explicit: both the normal left-button release
        // and Windows' dedicated double-click event restore the main window.
        assert!(is_tray_window_activation(&TrayIconEvent::Click {
            id: TRAY_ICON_ID.into(),
            position: (0.0, 0.0).into(),
            rect: Default::default(),
            button: MouseButton::Left,
            button_state: MouseButtonState::Up,
        }));
        assert!(is_tray_window_activation(&TrayIconEvent::DoubleClick {
            id: TRAY_ICON_ID.into(),
            position: (0.0, 0.0).into(),
            rect: Default::default(),
            button: MouseButton::Left,
        }));
        assert!(!is_tray_window_activation(&TrayIconEvent::Click {
            id: TRAY_ICON_ID.into(),
            position: (0.0, 0.0).into(),
            rect: Default::default(),
            button: MouseButton::Right,
            button_state: MouseButtonState::Up,
        }));
    }
}
