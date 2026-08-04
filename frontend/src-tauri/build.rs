fn main() {
    let core_version =
        std::fs::read_to_string("../CORE_VERSION").expect("frontend/CORE_VERSION must exist");
    let algorithm_version = std::fs::read_to_string("../ALGORITHM_VERSION")
        .expect("frontend/ALGORITHM_VERSION must exist");
    println!("cargo:rerun-if-changed=../CORE_VERSION");
    println!("cargo:rerun-if-changed=../ALGORITHM_VERSION");
    println!(
        "cargo:rustc-env=SUB2API_CORE_VERSION={}",
        core_version.trim()
    );
    println!(
        "cargo:rustc-env=SUB2API_ALGORITHM_VERSION={}",
        algorithm_version.trim()
    );
    tauri_build::build()
}
