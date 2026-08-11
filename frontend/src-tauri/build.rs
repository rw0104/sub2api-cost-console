use std::{env, fs, path::PathBuf, process::Command};

fn stage_go_zoneinfo() {
    println!("cargo:rerun-if-env-changed=SUB2API_ZONEINFO_PATH");

    let source = env::var_os("SUB2API_ZONEINFO_PATH")
        .map(PathBuf::from)
        .unwrap_or_else(|| {
            let output = Command::new("go")
                .args(["env", "GOROOT"])
                .output()
                .expect("Go is required to stage the desktop IANA timezone database");
            if !output.status.success() {
                panic!("`go env GOROOT` failed while staging the desktop timezone database");
            }
            let goroot = String::from_utf8(output.stdout)
                .expect("`go env GOROOT` returned non-UTF-8 output");
            PathBuf::from(goroot.trim())
                .join("lib")
                .join("time")
                .join("zoneinfo.zip")
        });
    if !source.is_file() {
        panic!(
            "Go IANA timezone database does not exist at {}",
            source.display()
        );
    }

    let output = PathBuf::from(env::var_os("OUT_DIR").expect("Cargo OUT_DIR must exist"))
        .join("zoneinfo.zip");
    fs::copy(&source, &output).unwrap_or_else(|error| {
        panic!(
            "failed to stage Go timezone database {} to {}: {error}",
            source.display(),
            output.display()
        )
    });
    println!("cargo:rerun-if-changed={}", source.display());
}

fn main() {
    stage_go_zoneinfo();
    println!("cargo:rerun-if-changed=binaries/sub2api-backend-x86_64-pc-windows-msvc.exe");
    let core_version =
        std::fs::read_to_string("../CORE_VERSION").expect("frontend/CORE_VERSION must exist");
    let algorithm_version = std::fs::read_to_string("../ALGORITHM_VERSION")
        .expect("frontend/ALGORITHM_VERSION must exist");
    let extension_version = std::fs::read_to_string("../CORE_EXTENSION_VERSION")
        .expect("frontend/CORE_EXTENSION_VERSION must exist");
    let core_capabilities = std::fs::read_to_string("../CORE_CAPABILITIES")
        .expect("frontend/CORE_CAPABILITIES must exist")
        .split_whitespace()
        .collect::<Vec<_>>()
        .join(" ");
    let upstream_commit = std::fs::read_to_string("../UPSTREAM_SUB2API_COMMIT")
        .expect("frontend/UPSTREAM_SUB2API_COMMIT must exist");
    println!("cargo:rerun-if-changed=../CORE_VERSION");
    println!("cargo:rerun-if-changed=../ALGORITHM_VERSION");
    println!("cargo:rerun-if-changed=../CORE_EXTENSION_VERSION");
    println!("cargo:rerun-if-changed=../CORE_CAPABILITIES");
    println!("cargo:rerun-if-changed=../UPSTREAM_SUB2API_COMMIT");
    println!(
        "cargo:rustc-env=SUB2API_CORE_VERSION={}",
        core_version.trim()
    );
    println!(
        "cargo:rustc-env=SUB2API_ALGORITHM_VERSION={}",
        algorithm_version.trim()
    );
    println!(
        "cargo:rustc-env=SUB2API_CORE_EXTENSION_VERSION={}",
        extension_version.trim()
    );
    println!(
        "cargo:rustc-env=SUB2API_CORE_CAPABILITIES={}",
        core_capabilities
    );
    println!(
        "cargo:rustc-env=SUB2API_UPSTREAM_COMMIT={}",
        upstream_commit.trim()
    );
    println!(
        "cargo:rustc-env=SUB2API_BUNDLED_CORE_COMMIT={}",
        upstream_commit.trim()
    );
    tauri_build::build()
}
