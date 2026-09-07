use tauri_plugin_shell::process::CommandChild;

/// Closing the desktop's job kills its sidecar, including on an abnormal exit.
pub struct ManagedChild {
    child: CommandChild,
    lifetime: ProcessLifetime,
}

impl ManagedChild {
    pub fn new(child: CommandChild) -> Result<Self, String> {
        match ProcessLifetime::attach(child.pid()) {
            Ok(lifetime) => Ok(Self { child, lifetime }),
            Err(error) => {
                let _ = child.kill();
                Err(error)
            }
        }
    }
    pub fn pid(&self) -> u32 {
        self.child.pid()
    }
    pub fn kill(self) -> Result<(), String> {
        let Self { child, lifetime } = self;
        // The process may have exited before its termination event was consumed.
        let result = child.kill();
        lifetime
            .wait_for_exit()
            .map_err(|error| format!("无法确认内核已退出：{error}；终止结果：{result:?}"))
    }
}

#[cfg(windows)]
struct ProcessLifetime {
    job: isize,
    process: isize,
}

#[cfg(windows)]
impl ProcessLifetime {
    fn attach(pid: u32) -> Result<Self, String> {
        use windows_sys::Win32::{
            Foundation::CloseHandle,
            System::{JobObjects::*, Threading::*},
        };
        unsafe {
            let job = CreateJobObjectW(std::ptr::null(), std::ptr::null());
            if job.is_null() {
                return Err("无法创建内核进程监管任务".into());
            }
            let mut info: JOBOBJECT_EXTENDED_LIMIT_INFORMATION = std::mem::zeroed();
            info.BasicLimitInformation.LimitFlags = JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE;
            if SetInformationJobObject(
                job,
                JobObjectExtendedLimitInformation,
                &info as *const _ as *const _,
                std::mem::size_of_val(&info) as u32,
            ) == 0
            {
                CloseHandle(job);
                return Err("无法配置内核退出清理".into());
            }
            let process = OpenProcess(
                PROCESS_SET_QUOTA | PROCESS_TERMINATE | PROCESS_SYNCHRONIZE,
                0,
                pid,
            );
            if process.is_null() {
                CloseHandle(job);
                return Err("内核在建立进程监管前已退出".into());
            }
            if AssignProcessToJobObject(job, process) == 0 {
                let error = std::io::Error::last_os_error();
                CloseHandle(process);
                CloseHandle(job);
                return Err(format!("无法监管内核进程：{error}"));
            }
            Ok(Self {
                job: job as isize,
                process: process as isize,
            })
        }
    }
    fn wait_for_exit(&self) -> Result<(), String> {
        use windows_sys::Win32::{
            Foundation::WAIT_OBJECT_0, System::Threading::WaitForSingleObject,
        };
        if unsafe { WaitForSingleObject(self.process as _, 5_000) } == WAIT_OBJECT_0 {
            Ok(())
        } else {
            Err("内核进程未在 5 秒内退出".into())
        }
    }
}
#[cfg(windows)]
impl Drop for ProcessLifetime {
    fn drop(&mut self) {
        unsafe {
            windows_sys::Win32::Foundation::CloseHandle(self.job as _);
            windows_sys::Win32::Foundation::CloseHandle(self.process as _);
        }
    }
}
#[cfg(not(windows))]
struct ProcessLifetime;
#[cfg(not(windows))]
impl ProcessLifetime {
    fn attach(_: u32) -> Result<Self, String> {
        Ok(Self)
    }
    fn wait_for_exit(&self) -> Result<(), String> {
        Ok(())
    }
}

#[cfg(all(test, windows))]
mod tests {
    use super::*;
    use std::{
        os::windows::process::CommandExt,
        process::{Command, Stdio},
        time::{Duration, Instant},
    };
    #[test]
    fn closing_desktop_process_guard_terminates_a_real_child() {
        let mut child = Command::new("ping.exe")
            .args(["-n", "60", "127.0.0.1"])
            .creation_flags(0x0800_0000)
            .stdout(Stdio::null())
            .stderr(Stdio::null())
            .spawn()
            .unwrap();
        let guard = ProcessLifetime::attach(child.id()).unwrap();
        assert!(child.try_wait().unwrap().is_none());
        drop(guard);
        let deadline = Instant::now() + Duration::from_secs(5);
        loop {
            if child.try_wait().unwrap().is_some() {
                break;
            }
            if Instant::now() > deadline {
                let _ = child.kill();
                panic!("sidecar survived desktop guard cleanup");
            }
            std::thread::sleep(Duration::from_millis(20));
        }
    }

    #[test]
    fn retry_waits_for_a_real_child_even_when_it_never_opens_a_port() {
        let mut child = Command::new("ping.exe")
            .args(["-n", "60", "127.0.0.1"])
            .creation_flags(0x0800_0000)
            .stdout(Stdio::null())
            .stderr(Stdio::null())
            .spawn()
            .unwrap();
        let guard = ProcessLifetime::attach(child.id()).unwrap();
        child.kill().unwrap();
        guard.wait_for_exit().unwrap();
        assert!(child.try_wait().unwrap().is_some());
    }
}
