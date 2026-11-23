use super::{LaunchPlan, CODEX_API_KEY_ENV};
use std::{
    fs::{self, Permissions},
    io::{self, BufRead, BufReader, Read, Write},
    os::unix::{fs::PermissionsExt, net::UnixListener},
    path::Path,
    process::Command,
    thread,
    time::{Duration, Instant},
};

fn shell_argument(value: &str) -> String {
    format!("'{}'", value.replace('\'', "'\\''"))
}

pub(super) fn launch(plan: &LaunchPlan) -> io::Result<u32> {
    with_terminal(plan, |script| {
        let status = Command::new("/usr/bin/open")
            .args(["-b", "com.apple.Terminal"])
            .arg(script)
            .status()?;
        if status.success() {
            Ok(())
        } else {
            Err(io::Error::other("macOS 未能打开终端"))
        }
    })
}

// A Finder-launched application has no interactive terminal. Terminal opens the
// .command file, and its shell receives the key through a private, one-shot socket.
fn with_terminal(plan: &LaunchPlan, open: impl FnOnce(&Path) -> io::Result<()>) -> io::Result<u32> {
    let key = plan
        .environment
        .iter()
        .find(|(name, _)| name == CODEX_API_KEY_ENV)
        .map(|(_, value)| value)
        .ok_or_else(|| io::Error::other("缺少 Codex API Key"))?;
    // Keep the socket path below macOS's sockaddr_un limit even with a long TMPDIR.
    let dir = tempfile::Builder::new()
        .prefix("sub2api-codex-")
        .permissions(Permissions::from_mode(0o700))
        .tempdir_in("/tmp")?;
    let socket = dir.path().join("launch.sock");
    let listener = UnixListener::bind(&socket)?;
    listener.set_nonblocking(true)?;
    let args = plan
        .args
        .iter()
        .map(|arg| shell_argument(arg))
        .collect::<Vec<_>>()
        .join(" ");
    let cwd = std::path::absolute(&plan.working_directory)?;
    let program = std::path::absolute(&plan.program)?;
    let contents = format!(
        r#"#!/bin/sh
set -eu
cd -- {cwd}
{CODEX_API_KEY_ENV}=$(printf '%s\n' "$$" | /usr/bin/nc -w 5 -U {socket})
[ -n "${CODEX_API_KEY_ENV}" ]
export {CODEX_API_KEY_ENV}
exec {program} {args}
"#,
        cwd = shell_argument(&cwd.to_string_lossy()),
        socket = shell_argument(&socket.to_string_lossy()),
        program = shell_argument(&program.to_string_lossy()),
    );
    let script = dir.path().join("Codex.command");
    fs::write(&script, contents)?;
    fs::set_permissions(&script, Permissions::from_mode(0o700))?;
    open(&script)?;

    let deadline = Instant::now() + Duration::from_secs(30);
    let mut client = loop {
        match listener.accept() {
            Ok((stream, _)) => break stream,
            Err(error) if error.kind() == io::ErrorKind::WouldBlock => {
                if Instant::now() >= deadline {
                    return Err(io::Error::new(
                        io::ErrorKind::TimedOut,
                        "终端没有启动 Codex",
                    ));
                }
                thread::sleep(Duration::from_millis(40));
            }
            Err(error) => return Err(error),
        }
    };
    client.set_read_timeout(Some(Duration::from_secs(5)))?;
    client.set_write_timeout(Some(Duration::from_secs(5)))?;
    let mut pid = String::new();
    BufReader::new((&client).take(16)).read_line(&mut pid)?;
    let pid = pid.trim_end().parse().map_err(io::Error::other)?;
    client.write_all(key.as_bytes())?;
    Ok(pid)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn terminal_runs_in_requested_directory_and_passes_key_only_in_environment() {
        let work = tempfile::Builder::new()
            .prefix("codex test '")
            .tempdir_in(".")
            .unwrap();
        let key = "test ' \" $() ; key";
        let output = work.path().join("result");
        let plan = LaunchPlan {
            program: "/bin/sh".into(),
            args: vec![
                "-c".into(),
                r#"printf '%s\n%s' "$SUB2API_CODEX_API_KEY" "$1" > result"#.into(),
                "sh".into(),
                "literal ' $(exit 2)".into(),
            ],
            environment: vec![(CODEX_API_KEY_ENV.into(), key.into())],
            working_directory: work.path().file_name().unwrap().into(),
            display_command: String::new(),
            open_terminal: true,
        };
        let mut child = None;
        let pid = with_terminal(&plan, |script| {
            assert!(!fs::read_to_string(script)?.contains(key));
            assert_eq!(
                script.parent().unwrap().metadata()?.permissions().mode() & 0o777,
                0o700
            );
            child = Some(
                Command::new("/bin/sh")
                    .arg(script)
                    .current_dir("/")
                    .spawn()?,
            );
            Ok(())
        })
        .unwrap();
        let mut child = child.unwrap();
        assert_eq!(pid, child.id());
        assert!(child.wait().unwrap().success());
        assert_eq!(
            fs::read_to_string(output).unwrap(),
            format!("{key}\nliteral ' $(exit 2)")
        );
    }
}
