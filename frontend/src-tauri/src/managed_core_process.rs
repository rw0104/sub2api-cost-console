use std::path::{Path, PathBuf};

#[derive(Clone, Debug, PartialEq, Eq)]
pub(crate) struct ListenerProcess {
    pub pid: u32,
    pub executable: PathBuf,
}

pub(crate) trait ListenerProcessControl {
    fn listener_on_port(&self, port: u16) -> Result<Option<ListenerProcess>, String>;
    fn terminate_and_wait(&self, pid: u32) -> Result<(), String>;
}

pub(crate) fn stop_owned_listener_with(
    control: &impl ListenerProcessControl,
    port: u16,
    expected_executable: &Path,
) -> Result<bool, String> {
    let listener = match control.listener_on_port(port) {
        Ok(listener) => listener,
        Err(first_error) => match control.listener_on_port(port) {
            Ok(None) => None,
            Ok(Some(_)) | Err(_) => return Err(first_error),
        },
    };
    let Some(listener) = listener else {
        return Ok(false);
    };
    if !same_executable_path(&listener.executable, expected_executable) {
        return Err(format!(
            "端口 {port} 由外部进程 {}（PID {}）占用，桌面端不会停止该进程",
            listener.executable.display(),
            listener.pid
        ));
    }
    match control.terminate_and_wait(listener.pid) {
        Ok(()) => Ok(true),
        Err(error) => match control.listener_on_port(port)? {
            None => Ok(true),
            Some(current) if current.pid != listener.pid => Err(format!(
                "停止受管内核时端口 {port} 已切换到进程 {}（PID {}），桌面端不会停止新进程；原错误: {error}",
                current.executable.display(),
                current.pid
            )),
            Some(_) => Err(error),
        },
    }
}

fn same_executable_path(left: &Path, right: &Path) -> bool {
    fn key(path: &Path) -> String {
        let resolved = path.canonicalize().unwrap_or_else(|_| path.to_path_buf());
        resolved
            .to_string_lossy()
            .trim_start_matches(r"\\?\")
            .replace('/', r"\")
            .to_ascii_lowercase()
    }
    key(left) == key(right)
}

#[cfg(windows)]
mod windows {
    use super::{ListenerProcess, ListenerProcessControl};
    use std::{ffi::c_void, path::PathBuf, ptr, slice};
    use windows_sys::Win32::{
        Foundation::{
            CloseHandle, ERROR_INSUFFICIENT_BUFFER, ERROR_SUCCESS, WAIT_OBJECT_0, WAIT_TIMEOUT,
        },
        NetworkManagement::IpHelper::{
            GetExtendedTcpTable, MIB_TCPROW_OWNER_PID, MIB_TCPTABLE_OWNER_PID,
            TCP_TABLE_OWNER_PID_LISTENER,
        },
        Networking::WinSock::AF_INET,
        System::Threading::{
            OpenProcess, QueryFullProcessImageNameW, TerminateProcess, WaitForSingleObject,
            PROCESS_QUERY_LIMITED_INFORMATION, PROCESS_SYNCHRONIZE, PROCESS_TERMINATE,
        },
    };

    pub(super) struct WindowsListenerProcessControl;

    impl ListenerProcessControl for WindowsListenerProcessControl {
        fn listener_on_port(&self, port: u16) -> Result<Option<ListenerProcess>, String> {
            let mut size = 0_u32;
            let first = unsafe {
                GetExtendedTcpTable(
                    ptr::null_mut(),
                    &mut size,
                    0,
                    AF_INET as u32,
                    TCP_TABLE_OWNER_PID_LISTENER,
                    0,
                )
            };
            if first != ERROR_INSUFFICIENT_BUFFER {
                return Err(format!(
                    "无法读取端口 {port} 的监听进程，GetExtendedTcpTable 返回 {first}"
                ));
            }

            let word_count = (size as usize).div_ceil(size_of::<u32>());
            let mut buffer = vec![0_u32; word_count];
            let result = unsafe {
                GetExtendedTcpTable(
                    buffer.as_mut_ptr().cast::<c_void>(),
                    &mut size,
                    0,
                    AF_INET as u32,
                    TCP_TABLE_OWNER_PID_LISTENER,
                    0,
                )
            };
            if result != ERROR_SUCCESS {
                return Err(format!(
                    "无法读取端口 {port} 的监听进程，GetExtendedTcpTable 返回 {result}"
                ));
            }

            let table = unsafe { &*(buffer.as_ptr().cast::<MIB_TCPTABLE_OWNER_PID>()) };
            let rows: &[MIB_TCPROW_OWNER_PID] =
                unsafe { slice::from_raw_parts(table.table.as_ptr(), table.dwNumEntries as usize) };
            let Some(row) = rows
                .iter()
                .find(|row| u16::from_be(row.dwLocalPort as u16) == port)
            else {
                return Ok(None);
            };
            Ok(Some(ListenerProcess {
                pid: row.dwOwningPid,
                executable: process_image_path(row.dwOwningPid)?,
            }))
        }

        fn terminate_and_wait(&self, pid: u32) -> Result<(), String> {
            let handle = unsafe {
                OpenProcess(
                    PROCESS_QUERY_LIMITED_INFORMATION | PROCESS_TERMINATE | PROCESS_SYNCHRONIZE,
                    0,
                    pid,
                )
            };
            if handle.is_null() {
                return Err(format!(
                    "无法打开残留的受管内核进程 PID {pid}: {}",
                    std::io::Error::last_os_error()
                ));
            }
            let terminated = unsafe { TerminateProcess(handle, 0) };
            if terminated == 0 {
                let error = std::io::Error::last_os_error();
                unsafe { CloseHandle(handle) };
                return Err(format!("无法停止残留的受管内核进程 PID {pid}: {error}"));
            }
            let wait = unsafe { WaitForSingleObject(handle, 5_000) };
            unsafe { CloseHandle(handle) };
            match wait {
                WAIT_OBJECT_0 => Ok(()),
                WAIT_TIMEOUT => Err(format!("残留的受管内核进程 PID {pid} 未在 5 秒内退出")),
                other => Err(format!("等待受管内核进程 PID {pid} 退出失败，状态 {other}")),
            }
        }
    }

    fn process_image_path(pid: u32) -> Result<PathBuf, String> {
        let handle = unsafe { OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, 0, pid) };
        if handle.is_null() {
            return Err(format!(
                "无法读取端口监听进程 PID {pid}: {}",
                std::io::Error::last_os_error()
            ));
        }
        let mut buffer = vec![0_u16; 32_768];
        let mut length = buffer.len() as u32;
        let queried =
            unsafe { QueryFullProcessImageNameW(handle, 0, buffer.as_mut_ptr(), &mut length) };
        unsafe { CloseHandle(handle) };
        if queried == 0 {
            return Err(format!(
                "无法读取端口监听进程 PID {pid} 的文件路径: {}",
                std::io::Error::last_os_error()
            ));
        }
        Ok(PathBuf::from(String::from_utf16_lossy(
            &buffer[..length as usize],
        )))
    }
}

#[cfg(windows)]
pub(crate) fn stop_owned_listener(port: u16, expected_executable: &Path) -> Result<bool, String> {
    stop_owned_listener_with(
        &windows::WindowsListenerProcessControl,
        port,
        expected_executable,
    )
}

#[cfg(not(windows))]
pub(crate) fn stop_owned_listener(_port: u16, _expected_executable: &Path) -> Result<bool, String> {
    Ok(false)
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::{
        atomic::{AtomicUsize, Ordering},
        Mutex,
    };

    struct FakeControl {
        listener: Option<ListenerProcess>,
        terminated: Mutex<Vec<u32>>,
    }

    impl ListenerProcessControl for FakeControl {
        fn listener_on_port(&self, _port: u16) -> Result<Option<ListenerProcess>, String> {
            Ok(self.listener.clone())
        }

        fn terminate_and_wait(&self, pid: u32) -> Result<(), String> {
            self.terminated.lock().unwrap().push(pid);
            Ok(())
        }
    }

    #[test]
    fn stops_only_the_listener_running_the_managed_active_core() {
        let active = PathBuf::from(
            r"C:\Users\tester\AppData\Roaming\com.sub2api.cost-console\core\active\sub2api-backend.exe",
        );
        let control = FakeControl {
            listener: Some(ListenerProcess {
                pid: 42,
                executable: active.clone(),
            }),
            terminated: Mutex::new(Vec::new()),
        };

        assert!(stop_owned_listener_with(&control, 18_765, &active).unwrap());
        assert_eq!(*control.terminated.lock().unwrap(), vec![42]);
    }

    #[test]
    fn refuses_to_stop_an_external_listener() {
        let active = PathBuf::from(
            r"C:\Users\tester\AppData\Roaming\com.sub2api.cost-console\core\active\sub2api-backend.exe",
        );
        let control = FakeControl {
            listener: Some(ListenerProcess {
                pid: 77,
                executable: PathBuf::from(r"D:\ExternalSub2API\sub2api.exe"),
            }),
            terminated: Mutex::new(Vec::new()),
        };

        let error = stop_owned_listener_with(&control, 18_765, &active).unwrap_err();
        assert!(error.contains("外部"));
        assert!(control.terminated.lock().unwrap().is_empty());
    }

    struct ExitedDuringStopControl {
        listener: ListenerProcess,
        queries: AtomicUsize,
    }

    impl ListenerProcessControl for ExitedDuringStopControl {
        fn listener_on_port(&self, _port: u16) -> Result<Option<ListenerProcess>, String> {
            if self.queries.fetch_add(1, Ordering::SeqCst) == 0 {
                Ok(Some(self.listener.clone()))
            } else {
                Ok(None)
            }
        }

        fn terminate_and_wait(&self, _pid: u32) -> Result<(), String> {
            Err("无法打开已经退出的进程: 拒绝访问".into())
        }
    }

    #[test]
    fn accepts_listener_that_exits_between_inspection_and_termination() {
        let active = PathBuf::from(
            r"C:\Users\tester\AppData\Roaming\com.sub2api.cost-console\core\active\sub2api-backend.exe",
        );
        let control = ExitedDuringStopControl {
            listener: ListenerProcess {
                pid: 88,
                executable: active.clone(),
            },
            queries: AtomicUsize::new(0),
        };

        assert!(stop_owned_listener_with(&control, 18_765, &active).unwrap());
        assert_eq!(control.queries.load(Ordering::SeqCst), 2);
    }

    struct StaleListenerRowControl {
        queries: AtomicUsize,
    }

    impl ListenerProcessControl for StaleListenerRowControl {
        fn listener_on_port(&self, _port: u16) -> Result<Option<ListenerProcess>, String> {
            if self.queries.fetch_add(1, Ordering::SeqCst) == 0 {
                Err("无法读取已经退出的监听进程路径: 拒绝访问".into())
            } else {
                Ok(None)
            }
        }

        fn terminate_and_wait(&self, _pid: u32) -> Result<(), String> {
            panic!("an already-exited listener must not be terminated")
        }
    }

    #[test]
    fn accepts_stale_listener_row_after_process_has_exited() {
        let active = PathBuf::from(
            r"C:\Users\tester\AppData\Roaming\com.sub2api.cost-console\core\active\sub2api-backend.exe",
        );
        let control = StaleListenerRowControl {
            queries: AtomicUsize::new(0),
        };

        assert!(!stop_owned_listener_with(&control, 18_765, &active).unwrap());
        assert_eq!(control.queries.load(Ordering::SeqCst), 2);
    }
}
