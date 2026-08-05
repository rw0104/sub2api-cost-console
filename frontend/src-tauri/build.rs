fn main() {
    let core_version =
        std::fs::read_to_string("../CORE_VERSION").expect("frontend/CORE_VERSION must exist");
    let algorithm_version = std::fs::read_to_string("../ALGORITHM_VERSION")
        .expect("frontend/ALGORITHM_VERSION must exist");
    let upstream_commit = std::fs::read_to_string("../UPSTREAM_SUB2API_COMMIT")
        .expect("frontend/UPSTREAM_SUB2API_COMMIT must exist");
    println!("cargo:rerun-if-changed=../CORE_VERSION");
    println!("cargo:rerun-if-changed=../ALGORITHM_VERSION");
    println!("cargo:rerun-if-changed=../UPSTREAM_SUB2API_COMMIT");
    println!("cargo:rerun-if-env-changed=SUB2API_CORE_MANIFEST_URL");
    println!("cargo:rerun-if-env-changed=SUB2API_CORE_MANIFEST_SIGNATURE_URL");
    println!(
        "cargo:rustc-env=SUB2API_CORE_VERSION={}",
        core_version.trim()
    );
    println!(
        "cargo:rustc-env=SUB2API_ALGORITHM_VERSION={}",
        algorithm_version.trim()
    );
    println!(
        "cargo:rustc-env=SUB2API_UPSTREAM_COMMIT={}",
        upstream_commit.trim()
    );
    tauri_build::build()
}
