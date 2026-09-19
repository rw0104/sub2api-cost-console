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
    pub fn stop(self) -> Result<(), String> {
        let Self {
            mut child,
            lifetime,
        } = self;
        #[cfg(windows)]
        let processes = lifetime.process_handles()?;
        // Windows cannot deliver SIGTERM to the Go sidecar. Its inherited
        // stdin requests the normal server/plugin cleanup path instead.
        #[cfg(windows)]
        if child.write(b"sub2api:desktop:shutdown:v1\n").is_ok()
            && lifetime.wait_for_exit_with_timeout(15_000).is_ok()
        {
            return lifetime.terminate_tree(processes);
        }
        // The process may have exited before its termination event was consumed.
        let result = child.kill();
        #[cfg(windows)]
        lifetime.terminate_tree(processes)?;
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
        self.wait_for_exit_with_timeout(5_000)
    }
    fn wait_for_exit_with_timeout(&self, milliseconds: u32) -> Result<(), String> {
        use windows_sys::Win32::{
            Foundation::WAIT_OBJECT_0, System::Threading::WaitForSingleObject,
        };
        if unsafe { WaitForSingleObject(self.process as _, milliseconds) } == WAIT_OBJECT_0 {
            Ok(())
        } else {
            Err("内核进程未在等待期限内退出".into())
        }
    }

    fn terminate_tree(&self, mut processes: Vec<ProcessWaitHandle>) -> Result<(), String> {
        use windows_sys::Win32::System::JobObjects::*;
        processes.extend(self.process_handles()?);
        unsafe {
            if TerminateJobObject(self.job as _, 1) == 0 {
                return Err(format!(
                    "无法停止内核和插件进程：{}",
                    std::io::Error::last_os_error()
                ));
            }
            let deadline = std::time::Instant::now() + std::time::Duration::from_secs(5);
            loop {
                let mut info: JOBOBJECT_BASIC_ACCOUNTING_INFORMATION = std::mem::zeroed();
                if QueryInformationJobObject(
                    self.job as _,
                    JobObjectBasicAccountingInformation,
                    &mut info as *mut _ as *mut _,
                    std::mem::size_of_val(&info) as u32,
                    std::ptr::null_mut(),
                ) == 0
                {
                    return Err(format!(
                        "无法确认插件进程已退出：{}",
                        std::io::Error::last_os_error()
                    ));
                }
                if info.ActiveProcesses == 0 {
                    break;
                }
                if std::time::Instant::now() >= deadline {
                    return Err("内核或插件进程尚未退出，无法安全更新".into());
                }
                std::thread::sleep(std::time::Duration::from_millis(20));
            }
            // ActiveProcesses can reach zero before process objects become
            // signaled. Keep handles acquired before termination so an update
            // cannot race descendants still releasing executable files.
            for process in &processes {
                let remaining = deadline
                    .saturating_duration_since(std::time::Instant::now())
                    .as_millis() as u32;
                if windows_sys::Win32::System::Threading::WaitForSingleObject(
                    process.0 as _,
                    remaining,
                ) != windows_sys::Win32::Foundation::WAIT_OBJECT_0
                {
                    return Err("插件进程尚未完全退出，无法安全更新".into());
                }
            }
            Ok(())
        }
    }

    fn process_handles(&self) -> Result<Vec<ProcessWaitHandle>, String> {
        use windows_sys::Win32::{
            Foundation::{ERROR_INVALID_PARAMETER, ERROR_MORE_DATA},
            System::{
                JobObjects::*,
                Threading::{OpenProcess, PROCESS_SYNCHRONIZE},
            },
        };
        // usize storage provides the native alignment for the variable-length
        // PID list. Retry if a process was added while querying the job.
        let mut capacity = 16;
        loop {
            let mut storage = vec![0usize; capacity + 2];
            let list = storage.as_mut_ptr() as *mut JOBOBJECT_BASIC_PROCESS_ID_LIST;
            let ok = unsafe {
                QueryInformationJobObject(
                    self.job as _,
                    JobObjectBasicProcessIdList,
                    list as _,
                    (storage.len() * std::mem::size_of::<usize>()) as u32,
                    std::ptr::null_mut(),
                )
            };
            if ok == 0 {
                let error = std::io::Error::last_os_error();
                if error.raw_os_error() == Some(ERROR_MORE_DATA as i32) && capacity < 65536 {
                    capacity *= 2;
                    continue;
                }
                return Err(format!("无法读取插件进程列表：{error}"));
            }
            let pids = unsafe {
                std::slice::from_raw_parts(
                    (*list).ProcessIdList.as_ptr(),
                    (*list).NumberOfProcessIdsInList as usize,
                )
            };
            let mut handles = Vec::new();
            for pid in pids {
                let handle = unsafe { OpenProcess(PROCESS_SYNCHRONIZE, 0, *pid as u32) };
                if handle.is_null() {
                    let error = std::io::Error::last_os_error();
                    if error.raw_os_error() != Some(ERROR_INVALID_PARAMETER as i32) {
                        return Err(format!("无法等待插件进程退出：{error}"));
                    }
                } else {
                    handles.push(ProcessWaitHandle(handle as isize));
                }
            }
            return Ok(handles);
        }
    }
}

#[cfg(windows)]
struct ProcessWaitHandle(isize);
#[cfg(windows)]
impl Drop for ProcessWaitHandle {
    fn drop(&mut self) {
        unsafe {
            windows_sys::Win32::Foundation::CloseHandle(self.0 as _);
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

    #[test]
    fn stopping_job_waits_for_plugin_descendants_too() {
        use std::io::{BufRead, BufReader, Write};
        use windows_sys::Win32::{
            Foundation::{CloseHandle, WAIT_OBJECT_0},
            System::{
                JobObjects::IsProcessInJob,
                Threading::{
                    OpenProcess, WaitForSingleObject, PROCESS_QUERY_LIMITED_INFORMATION,
                    PROCESS_SYNCHRONIZE,
                },
            },
        };
        let mut parent = Command::new("powershell.exe")
            .args(["-NoProfile", "-NonInteractive", "-Command",
                "$null = [Console]::ReadLine(); $s = New-Object System.Diagnostics.ProcessStartInfo; $s.FileName = 'ping.exe'; $s.Arguments = '-n 60 127.0.0.1'; $s.UseShellExecute = $false; $s.CreateNoWindow = $true; $p = [System.Diagnostics.Process]::Start($s); [Console]::WriteLine($p.Id); $p.WaitForExit()"])
            .creation_flags(0x0800_0000)
            .stdin(Stdio::piped()).stdout(Stdio::piped()).stderr(Stdio::null())
            .spawn().unwrap();
        let guard = ProcessLifetime::attach(parent.id()).unwrap();
        // Spawn the descendant only after the supervisor job owns its parent.
        parent.stdin.take().unwrap().write_all(b"spawn\n").unwrap();
        let output = parent.stdout.take().unwrap();
        let (tx, rx) = std::sync::mpsc::channel();
        std::thread::spawn(move || {
            let mut line = String::new();
            BufReader::new(output).read_line(&mut line).unwrap();
            let _ = tx.send(line.trim().parse::<u32>().unwrap());
        });
        let plugin_pid = rx.recv_timeout(Duration::from_secs(30)).unwrap();
        let plugin = unsafe {
            OpenProcess(
                PROCESS_SYNCHRONIZE | PROCESS_QUERY_LIMITED_INFORMATION,
                0,
                plugin_pid,
            )
        };
        assert!(!plugin.is_null());
        let mut in_job = 0;
        assert_ne!(
            unsafe { IsProcessInJob(plugin, guard.job as _, &mut in_job) },
            0
        );
        assert_ne!(
            in_job, 0,
            "fixture must inherit the same job as a Go exec child"
        );
        let processes = guard.process_handles().unwrap();
        // The fallback kills the parent first; retain descendants' handles
        // across that exit rather than discovering them only afterwards.
        parent.kill().unwrap();
        guard.wait_for_exit().unwrap();
        let stopped = guard.terminate_tree(processes);
        let exited = unsafe { WaitForSingleObject(plugin, 0) };
        unsafe {
            CloseHandle(plugin);
        }
        stopped.unwrap();
        assert_eq!(exited, WAIT_OBJECT_0, "plugin descendant survived stop");
        parent.wait().unwrap();
    }
}
