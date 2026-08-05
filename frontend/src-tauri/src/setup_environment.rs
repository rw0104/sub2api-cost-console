use rand::{distributions::Alphanumeric, Rng};
use serde::Serialize;
use std::{process::Stdio, time::Duration};
use tauri::{AppHandle, Emitter};
use tokio::{
    net::TcpStream,
    process::Command,
    time::{sleep, timeout},
};

const POSTGRES_IMAGE: &str = "postgres:16.14-alpine";
const VALKEY_IMAGE: &str = "valkey/valkey:8.1.9-alpine";
const POSTGRES_CONTAINER: &str = "sub2api-cost-postgres";
const VALKEY_CONTAINER: &str = "sub2api-cost-valkey";
const MANAGED_POSTGRES_PORT: u16 = 15_432;
const MANAGED_REDIS_PORT: u16 = 16_379;
const COMMAND_TIMEOUT: Duration = Duration::from_secs(120);

#[derive(Clone, Debug, Serialize)]
pub struct PortProbe {
    pub host: String,
    pub port: u16,
    pub reachable: bool,
}

#[derive(Clone, Debug, Serialize)]
pub struct DockerProbe {
    pub installed: bool,
    pub running: bool,
    pub version: String,
    pub message: String,
}

#[derive(Clone, Debug, Serialize)]
pub struct SetupEnvironment {
    pub desktop: bool,
    pub docker: DockerProbe,
    pub postgres: PortProbe,
    pub redis: PortProbe,
    pub managed_postgres: PortProbe,
    pub managed_redis: PortProbe,
}

#[derive(Clone, Debug, Serialize)]
pub struct ManagedDatabaseConfig {
    pub host: String,
    pub port: u16,
    pub user: String,
    pub password: String,
    pub dbname: String,
    pub sslmode: String,
}

#[derive(Clone, Debug, Serialize)]
pub struct ManagedRedisConfig {
    pub host: String,
    pub port: u16,
    pub username: String,
    pub password: String,
    pub db: u8,
    pub enable_tls: bool,
}

#[derive(Clone, Debug, Serialize)]
pub struct ManagedSetupConfig {
    pub database: ManagedDatabaseConfig,
    pub redis: ManagedRedisConfig,
    pub postgres_image: String,
    pub valkey_image: String,
}

#[derive(Clone, Debug, Serialize)]
struct ProvisionProgress {
    stage: String,
    message: String,
    percent: u8,
}

async fn probe_port(port: u16) -> PortProbe {
    let reachable = matches!(
        timeout(
            Duration::from_millis(550),
            TcpStream::connect(("127.0.0.1", port)),
        )
        .await,
        Ok(Ok(_))
    );
    PortProbe {
        host: "127.0.0.1".into(),
        port,
        reachable,
    }
}

fn configure_command(command: &mut Command) {
    command
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .kill_on_drop(true);
    #[cfg(target_os = "windows")]
    {
        use std::os::windows::process::CommandExt;
        command.as_std_mut().creation_flags(0x0800_0000);
    }
}

async fn run_docker(args: &[&str], limit: Duration) -> Result<String, String> {
    let mut command = Command::new("docker");
    command.args(args);
    configure_command(&mut command);
    let output = timeout(limit, command.output())
        .await
        .map_err(|_| format!("Docker 命令执行超时：docker {}", args.join(" ")))?
        .map_err(|error| format!("无法执行 Docker 命令：{error}"))?;
    if !output.status.success() {
        let stderr = String::from_utf8_lossy(&output.stderr).trim().to_string();
        let stdout = String::from_utf8_lossy(&output.stdout).trim().to_string();
        return Err(if stderr.is_empty() { stdout } else { stderr });
    }
    Ok(String::from_utf8_lossy(&output.stdout).trim().to_string())
}

async fn docker_probe() -> DockerProbe {
    let version = match run_docker(&["--version"], Duration::from_secs(4)).await {
        Ok(version) => version,
        Err(_) => {
            return DockerProbe {
                installed: false,
                running: false,
                version: String::new(),
                message: "未检测到 Docker。请手动准备 PostgreSQL 与 Redis/Valkey，或安装并启动 Docker Desktop。".into(),
            }
        }
    };
    match run_docker(
        &["info", "--format", "{{.ServerVersion}}"],
        Duration::from_secs(8),
    )
    .await
    {
        Ok(server_version) => DockerProbe {
            installed: true,
            running: true,
            version,
            message: format!("Docker 引擎可用（Server {server_version}）"),
        },
        Err(error) => DockerProbe {
            installed: true,
            running: false,
            version,
            message: format!("已安装 Docker，但引擎未就绪：{error}"),
        },
    }
}

async fn detect_environment() -> SetupEnvironment {
    let (docker, postgres, redis, managed_postgres, managed_redis) = tokio::join!(
        docker_probe(),
        probe_port(5432),
        probe_port(6379),
        probe_port(MANAGED_POSTGRES_PORT),
        probe_port(MANAGED_REDIS_PORT),
    );
    SetupEnvironment {
        desktop: true,
        docker,
        postgres,
        redis,
        managed_postgres,
        managed_redis,
    }
}

fn random_secret(length: usize) -> String {
    rand::thread_rng()
        .sample_iter(&Alphanumeric)
        .take(length)
        .map(char::from)
        .collect()
}

fn emit_progress(app: &AppHandle, stage: &str, message: &str, percent: u8) {
    let _ = app.emit(
        "setup-provision-progress",
        ProvisionProgress {
            stage: stage.into(),
            message: message.into(),
            percent,
        },
    );
}

async fn container_exists(name: &str) -> bool {
    run_docker(&["container", "inspect", name], Duration::from_secs(6))
        .await
        .is_ok()
}

async fn remove_container(name: &str) {
    let _ = run_docker(&["rm", "--force", name], Duration::from_secs(15)).await;
}

async fn remove_volume(name: &str) {
    let _ = run_docker(&["volume", "rm", name], Duration::from_secs(15)).await;
}

async fn wait_for_port(port: u16, attempts: usize) -> Result<(), String> {
    for _ in 0..attempts {
        if probe_port(port).await.reachable {
            return Ok(());
        }
        sleep(Duration::from_secs(1)).await;
    }
    Err(format!("本地端口 {port} 未在预期时间内就绪"))
}

#[tauri::command]
pub async fn detect_setup_environment() -> SetupEnvironment {
    detect_environment().await
}

#[tauri::command]
pub async fn provision_quick_setup(app: AppHandle) -> Result<ManagedSetupConfig, String> {
    emit_progress(&app, "checking", "正在检查 Docker 与本地端口", 5);
    let environment = detect_environment().await;
    if !environment.docker.installed {
        return Err(
            "快速安装需要 Docker Desktop。请安装并启动 Docker，或选择高级连接手动配置。".into(),
        );
    }
    if !environment.docker.running {
        return Err(
            "Docker 已安装但引擎未运行。请启动 Docker Desktop 后重试，或选择高级连接。".into(),
        );
    }
    if environment.managed_postgres.reachable || environment.managed_redis.reachable {
        return Err(format!(
            "快速安装端口 {} 或 {} 已被占用。请停止冲突服务，或选择高级连接。",
            MANAGED_POSTGRES_PORT, MANAGED_REDIS_PORT
        ));
    }
    if container_exists(POSTGRES_CONTAINER).await || container_exists(VALKEY_CONTAINER).await {
        return Err(
            "检测到同名的 Sub2API 数据容器。为避免覆盖现有数据，请选择高级连接并使用已有容器。"
                .into(),
        );
    }

    let postgres_password = random_secret(32);
    let redis_password = random_secret(32);
    let suffix = random_secret(8).to_lowercase();
    let postgres_volume = format!("sub2api-cost-postgres-data-{suffix}");
    let valkey_volume = format!("sub2api-cost-valkey-data-{suffix}");
    let postgres_publish = format!("127.0.0.1:{MANAGED_POSTGRES_PORT}:5432");
    let redis_publish = format!("127.0.0.1:{MANAGED_REDIS_PORT}:6379");
    let postgres_password_env = format!("POSTGRES_PASSWORD={postgres_password}");
    let postgres_volume_mount = format!("{postgres_volume}:/var/lib/postgresql/data");
    let valkey_volume_mount = format!("{valkey_volume}:/data");

    emit_progress(
        &app,
        "pulling_postgres",
        "正在下载 PostgreSQL 16 数据组件",
        15,
    );
    run_docker(&["pull", POSTGRES_IMAGE], COMMAND_TIMEOUT)
        .await
        .map_err(|error| format!("无法下载 PostgreSQL 镜像：{error}"))?;
    emit_progress(&app, "pulling_valkey", "正在下载 Valkey 缓存组件", 35);
    run_docker(&["pull", VALKEY_IMAGE], COMMAND_TIMEOUT)
        .await
        .map_err(|error| format!("无法下载 Valkey 镜像：{error}"))?;

    emit_progress(
        &app,
        "starting_postgres",
        "正在创建 PostgreSQL 本地容器",
        55,
    );
    let postgres_result = run_docker(
        &[
            "run",
            "--detach",
            "--name",
            POSTGRES_CONTAINER,
            "--label",
            "com.sub2api.cost-console.managed=true",
            "--restart",
            "unless-stopped",
            "--publish",
            &postgres_publish,
            "--env",
            "POSTGRES_USER=sub2api",
            "--env",
            "POSTGRES_DB=sub2api",
            "--env",
            &postgres_password_env,
            "--volume",
            &postgres_volume_mount,
            POSTGRES_IMAGE,
        ],
        COMMAND_TIMEOUT,
    )
    .await;
    if let Err(error) = postgres_result {
        remove_volume(&postgres_volume).await;
        return Err(format!("无法创建 PostgreSQL 容器：{error}"));
    }

    emit_progress(&app, "starting_valkey", "正在创建 Valkey 本地容器", 70);
    let valkey_result = run_docker(
        &[
            "run",
            "--detach",
            "--name",
            VALKEY_CONTAINER,
            "--label",
            "com.sub2api.cost-console.managed=true",
            "--restart",
            "unless-stopped",
            "--publish",
            &redis_publish,
            "--volume",
            &valkey_volume_mount,
            VALKEY_IMAGE,
            "valkey-server",
            "--appendonly",
            "yes",
            "--requirepass",
            &redis_password,
        ],
        COMMAND_TIMEOUT,
    )
    .await;
    if let Err(error) = valkey_result {
        remove_container(POSTGRES_CONTAINER).await;
        remove_volume(&postgres_volume).await;
        remove_volume(&valkey_volume).await;
        return Err(format!("无法创建 Valkey 容器：{error}"));
    }

    emit_progress(&app, "waiting", "正在等待本地数据服务完成初始化", 85);
    if let Err(error) = wait_for_port(MANAGED_POSTGRES_PORT, 60).await {
        remove_container(POSTGRES_CONTAINER).await;
        remove_container(VALKEY_CONTAINER).await;
        remove_volume(&postgres_volume).await;
        remove_volume(&valkey_volume).await;
        return Err(error);
    }
    if let Err(error) = wait_for_port(MANAGED_REDIS_PORT, 30).await {
        remove_container(POSTGRES_CONTAINER).await;
        remove_container(VALKEY_CONTAINER).await;
        remove_volume(&postgres_volume).await;
        remove_volume(&valkey_volume).await;
        return Err(error);
    }

    emit_progress(&app, "ready", "本地 PostgreSQL 与 Valkey 已准备完成", 100);
    Ok(ManagedSetupConfig {
        database: ManagedDatabaseConfig {
            host: "127.0.0.1".into(),
            port: MANAGED_POSTGRES_PORT,
            user: "sub2api".into(),
            password: postgres_password,
            dbname: "sub2api".into(),
            sslmode: "disable".into(),
        },
        redis: ManagedRedisConfig {
            host: "127.0.0.1".into(),
            port: MANAGED_REDIS_PORT,
            username: String::new(),
            password: redis_password,
            db: 0,
            enable_tls: false,
        },
        postgres_image: POSTGRES_IMAGE.into(),
        valkey_image: VALKEY_IMAGE.into(),
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn generated_secrets_are_long_and_distinct() {
        let first = random_secret(32);
        let second = random_secret(32);
        assert_eq!(first.len(), 32);
        assert_eq!(second.len(), 32);
        assert_ne!(first, second);
        assert!(first
            .chars()
            .all(|character| character.is_ascii_alphanumeric()));
    }

    #[test]
    fn managed_ports_do_not_replace_standard_database_ports() {
        assert_ne!(MANAGED_POSTGRES_PORT, 5432);
        assert_ne!(MANAGED_REDIS_PORT, 6379);
    }
}
