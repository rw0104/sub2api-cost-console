//! Native Windows regression: run explicitly with an interactive desktop and WebView2.
//! Uses an isolated blank window; never starts the backend or loads account data.
use crate::desktop_shell;
use std::{
    sync::mpsc,
    thread,
    time::{Duration, Instant},
};
use tauri::{WebviewUrl, WebviewWindow, WebviewWindowBuilder};

type Hwnd = *mut std::ffi::c_void;

#[repr(C)]
struct NativeIconIdentifier {
    size: u32,
    hwnd: Hwnd,
    id: u32,
    guid: [u8; 16],
}

#[repr(C)]
#[derive(Debug, Default)]
struct NativeRect {
    left: i32,
    top: i32,
    right: i32,
    bottom: i32,
}

#[link(name = "user32")]
extern "system" {
    fn EnumWindows(callback: unsafe extern "system" fn(Hwnd, isize) -> i32, data: isize) -> i32;
    fn GetWindowThreadProcessId(hwnd: Hwnd, pid: *mut u32) -> u32;
    fn GetClassNameW(hwnd: Hwnd, class: *mut u16, capacity: i32) -> i32;
    fn PostMessageW(hwnd: Hwnd, message: u32, wparam: usize, lparam: isize) -> i32;
}

#[link(name = "shell32")]
extern "system" {
    fn Shell_NotifyIconGetRect(icon: *const NativeIconIdentifier, rect: *mut NativeRect) -> i32;
}

fn native_tray_window() -> usize {
    unsafe extern "system" fn visit(hwnd: Hwnd, found: isize) -> i32 {
        let mut pid = 0;
        GetWindowThreadProcessId(hwnd, &mut pid);
        if pid == std::process::id() {
            let mut class = [0u16; 64];
            let length = GetClassNameW(hwnd, class.as_mut_ptr(), class.len() as i32);
            if String::from_utf16_lossy(&class[..length.max(0) as usize]) == "tray_icon_app" {
                *(found as *mut usize) = hwnd as usize;
                return 0;
            }
        }
        1
    }
    let mut found = 0usize;
    unsafe { EnumWindows(visit, &mut found as *mut usize as isize) };
    assert_ne!(found, 0, "the isolated test owns a real Windows tray icon");
    found
}

fn click_native_tray(hwnd: usize, double_click: bool) {
    // Protocol constants from pinned tray-icon 0.24.2. A fresh builder consumes
    // counter 1 for its default public ID and counter 2 for the native icon.
    let icon = NativeIconIdentifier {
        size: std::mem::size_of::<NativeIconIdentifier>() as u32,
        hwnd: hwnd as Hwnd,
        id: 2,
        guid: [0; 16],
    };
    let mut rect = NativeRect::default();
    let result = unsafe { Shell_NotifyIconGetRect(&icon, &mut rect) };
    assert!(result >= 0, "test tray registration failed: {result:#x}");
    eprintln!("native tray rectangle result={result:#x}, rect={rect:?}");
    let mouse_message = if double_click { 0x203 } else { 0x202 };
    assert_ne!(
        unsafe { PostMessageW(hwnd as Hwnd, 6002, 2, mouse_message) },
        0,
        "post notification-area click to this test's own tray window"
    );
}

fn await_window_state(window: &WebviewWindow, visible: bool, minimized: bool, stage: &str) {
    let deadline = Instant::now() + Duration::from_secs(3);
    loop {
        let actual = (
            window.is_visible().expect("read visibility"),
            window.is_minimized().expect("read minimized state"),
        );
        if actual == (visible, minimized) {
            return;
        }
        assert!(
            Instant::now() < deadline,
            "{stage}: expected visible/minimized={:?}, got {actual:?}",
            (visible, minimized)
        );
        thread::sleep(Duration::from_millis(20));
    }
}

fn restore_from_event_loop(app: &tauri::AppHandle) {
    let (sender, receiver) = mpsc::channel();
    let handle = app.clone();
    app.run_on_main_thread(move || {
        sender
            .send(desktop_shell::show_main_window(&handle))
            .expect("restore result channel");
    })
    .expect("queue tray restore on the event loop");
    receiver
        .recv_timeout(Duration::from_secs(3))
        .expect("tray restore must not block the event loop")
        .expect("tray restore succeeds");
}

#[test]
#[ignore = "requires Windows desktop and WebView2; creates only an isolated blank window"]
fn minimized_and_closed_windows_restore_from_tray() {
    let profile = std::env::temp_dir().join(format!("sub2api-tray-test-{}", std::process::id()));
    let mut context = tauri::generate_context!();
    context.config_mut().identifier = "com.sub2api.tray-regression".into();
    context.config_mut().app.windows.clear();
    let (result_sender, result_receiver) = mpsc::channel();
    let app = tauri::Builder::default()
        .any_thread()
        .on_window_event(desktop_shell::handle_main_window_event)
        .setup(move |app| {
            let window = WebviewWindowBuilder::new(
                app,
                "main",
                WebviewUrl::External("about:blank".parse().expect("blank URL")),
            )
            .title("Sub2API tray restore regression")
            .inner_size(420.0, 240.0)
            .decorations(false)
            .visible(false)
            .data_directory(profile)
            .build()?;
            desktop_shell::setup_desktop_shell(app)?;
            let tray_hwnd = native_tray_window();
            let handle = app.handle().clone();
            thread::spawn(move || {
                let result = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| {
                    for cycle in 0..5 {
                        restore_from_event_loop(&handle);
                        await_window_state(&window, true, false, "initial restore");
                        // Only this test's icon is changed. Hiding it makes the
                        // shell return the observed S_FALSE/overflow rectangle;
                        // inject the already-delivered notification to prove it
                        // is not discarded by rectangle lookup. Alternate icon
                        // visibility; the shell may still return S_FALSE for a
                        // visible icon in overflow. Unit tests cover S_OK too.
                        handle
                            .tray_by_id("sub2api-cost-console-tray")
                            .expect("test tray exists")
                            .set_visible(cycle % 2 == 0)
                            .expect("set isolated tray visibility");
                        window.minimize().expect("minimize");
                        await_window_state(&window, false, true, "minimize to tray");
                        click_native_tray(tray_hwnd, false);
                        await_window_state(&window, true, false, "restore after minimizing");
                        window.close().expect("close to tray");
                        await_window_state(&window, false, false, "close intercepted");
                        click_native_tray(tray_hwnd, true);
                        await_window_state(&window, true, false, "restore after closing");
                        eprintln!("native tray restore cycle {cycle}: passed");
                    }
                }));
                let failed = result.is_err();
                result_sender.send(result).expect("test result channel");
                handle.exit(if failed { 1 } else { 0 });
            });
            Ok(())
        })
        .build(context)
        .expect("build isolated tray test app");
    app.run_return(|_, _| {});
    if let Err(error) = result_receiver
        .recv_timeout(Duration::from_secs(1))
        .expect("native test completed")
    {
        std::panic::resume_unwind(error);
    }
}
