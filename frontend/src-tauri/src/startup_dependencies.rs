use serde::{Deserialize, Serialize};
use std::{collections::HashMap, path::Path, time::Duration};
use tokio::{net::TcpStream, time::timeout};

#[derive(Clone, Debug, Serialize, PartialEq, Eq)]
pub struct StartupProblem {
    pub code: String,
    pub title: String,
    pub message: String,
    pub retryable: bool,
}

impl StartupProblem {
    pub fn new(code: &str, title: &str, message: impl Into<String>, retryable: bool) -> Self {
        Self {
            code: code.into(),
            title: title.into(),
            message: message.into(),
            retryable,
        }
    }
}

#[derive(Clone, Debug, Deserialize)]
pub struct Endpoint {
    #[serde(default = "local_host")]
    pub host: String,
    pub port: u16,
}

fn local_host() -> String {
    "127.0.0.1".into()
}

#[derive(Clone, Debug)]
pub struct Dependency {
    pub name: &'static str,
    pub endpoint: Endpoint,
    pub container: Option<(&'static str, &'static str)>,
}

#[derive(Default, Deserialize)]
struct ConfigEndpoint {
    host: Option<String>,
    port: Option<u16>,
}
impl ConfigEndpoint {
    fn resolve(self, port: u16) -> Endpoint {
        Endpoint {
            host: self.host.unwrap_or_else(local_host),
            port: self.port.unwrap_or(port),
        }
    }
}
#[derive(Deserialize)]
struct Config {
    #[serde(default)]
    database: ConfigEndpoint,
    #[serde(default)]
    redis: ConfigEndpoint,
}

fn is_local(host: &str) -> bool {
    matches!(host, "127.0.0.1" | "localhost" | "::1")
}

fn dependencies_from_yaml(yaml: &str) -> Result<Vec<Dependency>, StartupProblem> {
    let config: Config = serde_yaml_ng::from_str(yaml).map_err(|_| {
        StartupProblem::new(
            "configuration_invalid",
            "连接配置需要检查",
            "无法读取数据库连接配置，请检查 config.yaml 的格式。",
            false,
        )
    })?;
    let database = config.database.resolve(5432);
    let redis = config.redis.resolve(6379);
    Ok(vec![
        Dependency {
            name: "PostgreSQL",
            container: (is_local(&database.host) && database.port == 15432)
                .then_some(("sub2api-cost-postgres", "5432/tcp")),
            endpoint: database,
        },
        Dependency {
            name: "Redis / Valkey",
            container: (is_local(&redis.host) && redis.port == 16379)
                .then_some(("sub2api-cost-valkey", "6379/tcp")),
            endpoint: redis,
        },
    ])
}

pub fn configured_dependencies(data_dir: &Path) -> Result<Vec<Dependency>, StartupProblem> {
    let path = data_dir.join("config.yaml");
    if !path.exists() && !data_dir.join(".installed").exists() {
        return Ok(vec![]);
    }
    let yaml = std::fs::read_to_string(path).map_err(|_| {
        StartupProblem::new(
            "configuration_missing",
            "连接配置无法读取",
            "请检查数据目录中的 config.yaml；现有数据不会被重新初始化。",
            false,
        )
    })?;
    let mut dependencies = dependencies_from_yaml(&yaml)?;
    for (dependency, prefix) in dependencies.iter_mut().zip(["DATABASE", "REDIS"]) {
        if let Ok(host) = std::env::var(format!("{prefix}_HOST")) {
            dependency.endpoint.host = host;
        }
        if let Ok(port) = std::env::var(format!("{prefix}_PORT")) {
            dependency.endpoint.port = port.parse().map_err(|_| {
                StartupProblem::new(
                    "configuration_invalid",
                    "连接配置需要检查",
                    "数据库环境变量中的端口无效。",
                    false,
                )
            })?;
        }
        // Environment overrides must never cause unrelated containers to start.
        if !is_local(&dependency.endpoint.host)
            || dependency.endpoint.port != if prefix == "DATABASE" { 15432 } else { 16379 }
        {
            dependency.container = None;
        }
    }
    Ok(dependencies)
}

#[derive(Deserialize)]
pub struct ContainerInfo {
    managed: Option<String>,
    running: bool,
    ports: HashMap<String, Option<Vec<PortBinding>>>,
}
#[derive(Deserialize)]
struct PortBinding {
    #[serde(rename = "HostIp")]
    host_ip: String,
    #[serde(rename = "HostPort")]
    host_port: String,
}

impl ContainerInfo {
    fn owns_endpoint(&self, dependency: &Dependency, container_port: &str) -> bool {
        self.managed.as_deref() == Some("true")
            && self
                .ports
                .get(container_port)
                .and_then(Option::as_ref)
                .is_some_and(|bindings| {
                    bindings.iter().any(|binding| {
                        is_local(&binding.host_ip)
                            && binding.host_port == dependency.endpoint.port.to_string()
                    })
                })
    }
}

pub trait DependencyControl {
    async fn reachable(&self, endpoint: &Endpoint) -> bool;
    async fn docker_running(&self) -> bool;
    async fn container_info(&self, name: &str) -> Result<ContainerInfo, String>;
    async fn start_container(&self, name: &str) -> Result<(), String>;
}

pub struct SystemDependencies;
impl DependencyControl for SystemDependencies {
    async fn reachable(&self, endpoint: &Endpoint) -> bool {
        matches!(
            timeout(
                Duration::from_millis(650),
                TcpStream::connect((endpoint.host.as_str(), endpoint.port))
            )
            .await,
            Ok(Ok(_))
        )
    }
    async fn docker_running(&self) -> bool {
        crate::setup_environment::run_docker(
            &["info", "--format", "{{.ServerVersion}}"],
            Duration::from_secs(4),
        )
        .await
        .is_ok()
    }
    async fn container_info(&self, name: &str) -> Result<ContainerInfo, String> {
        // Only inspect ownership and bindings; credentials never enter diagnostics.
        let format = r#"{"managed":{{json (index .Config.Labels "com.sub2api.cost-console.managed")}},"running":{{json .State.Running}},"ports":{{json .HostConfig.PortBindings}}}"#;
        let json = crate::setup_environment::run_docker(
            &["container", "inspect", "--format", format, name],
            Duration::from_secs(4),
        )
        .await?;
        serde_json::from_str(&json).map_err(|_| "无法读取数据容器状态".into())
    }
    async fn start_container(&self, name: &str) -> Result<(), String> {
        crate::setup_environment::run_docker(&["start", name], Duration::from_secs(15))
            .await
            .map(|_| ())
    }
}

pub async fn ensure_dependencies(
    dependencies: &[Dependency],
    control: &impl DependencyControl,
) -> Result<(), StartupProblem> {
    let mut waiting = None;
    let mut docker_checked = false;
    for dependency in dependencies {
        if control.reachable(&dependency.endpoint).await {
            continue;
        }
        if let Some((name, container_port)) = dependency.container {
            if !docker_checked {
                if !control.docker_running().await {
                    return Err(StartupProblem::new("docker_unavailable", "Docker 尚未启动", "请启动 Docker Desktop，并等待引擎就绪。应用会自动继续启动，也可点击“重新检测并启动”。", true));
                }
                docker_checked = true;
            }
            let info = control.container_info(name).await.map_err(|_| {
                StartupProblem::new(
                    "container_missing",
                    "找不到本地数据容器",
                    format!("请在 Docker Desktop 中检查 {name}。应用不会重建容器或覆盖现有数据。"),
                    true,
                )
            })?;
            if !info.owns_endpoint(dependency, container_port) {
                return Err(StartupProblem::new(
                    "container_unverified",
                    "数据容器配置不匹配",
                    format!("{name} 的归属或端口配置与应用不一致，请检查 Docker 容器配置。"),
                    false,
                ));
            }
            if !info.running {
                control.start_container(name).await.map_err(|_| {
                    StartupProblem::new(
                        "container_start_failed",
                        "数据服务暂时无法启动",
                        format!("请在 Docker Desktop 中查看 {name} 的状态。应用会稍后重试。"),
                        true,
                    )
                })?;
            }
        }
        waiting = Some(StartupProblem::new(
            "dependency_unavailable",
            "正在等待数据服务",
            format!(
                "{}（{}:{}）尚未就绪。数据服务恢复后，应用会自动继续启动。",
                dependency.name, dependency.endpoint.host, dependency.endpoint.port,
            ),
            true,
        ));
    }
    waiting.map_or(Ok(()), Err)
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::{
        atomic::{AtomicBool, Ordering},
        Mutex,
    };
    struct Fake {
        engine: AtomicBool,
        ports: AtomicBool,
        owned: bool,
        calls: Mutex<Vec<String>>,
    }
    impl DependencyControl for Fake {
        async fn reachable(&self, _: &Endpoint) -> bool {
            self.ports.load(Ordering::SeqCst)
        }
        async fn docker_running(&self) -> bool {
            self.calls.lock().unwrap().push("docker".into());
            self.engine.load(Ordering::SeqCst)
        }
        async fn container_info(&self, name: &str) -> Result<ContainerInfo, String> {
            let (port, host) = if name.ends_with("postgres") {
                ("5432/tcp", "15432")
            } else {
                ("6379/tcp", "16379")
            };
            Ok(ContainerInfo {
                managed: Some(self.owned.to_string()),
                running: false,
                ports: HashMap::from([(
                    port.into(),
                    Some(vec![PortBinding {
                        host_ip: "127.0.0.1".into(),
                        host_port: host.into(),
                    }]),
                )]),
            })
        }
        async fn start_container(&self, name: &str) -> Result<(), String> {
            self.calls.lock().unwrap().push(name.into());
            Ok(())
        }
    }
    fn fixture() -> Fake {
        Fake {
            engine: AtomicBool::new(false),
            ports: AtomicBool::new(false),
            owned: true,
            calls: Mutex::new(vec![]),
        }
    }
    fn dependencies() -> Vec<Dependency> {
        dependencies_from_yaml(
            "database: {host: 127.0.0.1, port: 15432}\nredis: {host: 127.0.0.1, port: 16379}",
        )
        .unwrap()
    }
    #[tokio::test]
    async fn docker_off_then_on_restores_existing_containers_without_reprovisioning() {
        let fake = fixture();
        let deps = dependencies();
        assert_eq!(
            ensure_dependencies(&deps, &fake).await.unwrap_err().code,
            "docker_unavailable"
        );
        assert_eq!(*fake.calls.lock().unwrap(), vec!["docker"]);
        fake.engine.store(true, Ordering::SeqCst);
        assert_eq!(
            ensure_dependencies(&deps, &fake).await.unwrap_err().code,
            "dependency_unavailable"
        );
        assert!(fake
            .calls
            .lock()
            .unwrap()
            .ends_with(&["sub2api-cost-postgres".into(), "sub2api-cost-valkey".into()]));
        fake.ports.store(true, Ordering::SeqCst);
        assert!(ensure_dependencies(&deps, &fake).await.is_ok());
    }
    #[tokio::test]
    async fn remote_database_failure_does_not_touch_docker() {
        let fake = fixture();
        let deps = dependencies_from_yaml(
            "database: {host: db.example, port: 15432}\nredis: {host: cache.example, port: 16379}",
        )
        .unwrap();
        assert_eq!(
            ensure_dependencies(&deps, &fake).await.unwrap_err().code,
            "dependency_unavailable"
        );
        assert!(fake.calls.lock().unwrap().is_empty());
    }
    #[tokio::test]
    async fn refuses_to_start_a_container_without_ownership_and_matching_bindings() {
        let mut fake = fixture();
        fake.owned = false;
        fake.engine.store(true, Ordering::SeqCst);
        assert_eq!(
            ensure_dependencies(&dependencies(), &fake)
                .await
                .unwrap_err()
                .code,
            "container_unverified"
        );
        assert_eq!(*fake.calls.lock().unwrap(), vec!["docker"]);
        let mut info = fake.container_info("sub2api-cost-postgres").await.unwrap();
        info.managed = Some("true".into());
        info.ports.get_mut("5432/tcp").unwrap().as_mut().unwrap()[0].host_port = "9999".into();
        assert!(!info.owns_endpoint(&dependencies()[0], "5432/tcp"));
    }
    #[test]
    fn yaml_errors_do_not_expose_configuration_secrets() {
        let error =
            dependencies_from_yaml("database: {password: secret-do-not-show, port: invalid}")
                .unwrap_err();
        assert!(!error.message.contains("secret-do-not-show"));
    }

    #[test]
    fn omitted_fields_follow_backend_database_and_cache_defaults() {
        let deps =
            dependencies_from_yaml("database: {host: localhost}\nredis: {password: fixture}")
                .unwrap();
        assert_eq!(deps[0].endpoint.port, 5432);
        assert_eq!(deps[1].endpoint.port, 6379);
        assert!(deps.iter().all(|dependency| dependency.container.is_none()));
    }

    #[tokio::test]
    #[ignore = "opt-in: starts existing owned local Docker data containers"]
    async fn recover_existing_local_managed_containers() {
        assert_eq!(std::env::var("SUB2API_RECOVERY_SMOKE").as_deref(), Ok("1"));
        let deps = dependencies();
        for _ in 0..30 {
            match ensure_dependencies(&deps, &SystemDependencies).await {
                Ok(()) => return,
                Err(problem) => assert!(problem.retryable, "{}", problem.message),
            }
            tokio::time::sleep(Duration::from_secs(1)).await;
        }
        panic!("owned data services did not recover within the bounded smoke test");
    }
}
