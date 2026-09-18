"""Build the public, source-only plugin SDK kit attached to a desktop release.

Only tracked SDK files, public docs and pinned Go dependencies are selected.
Private workspace plugins, signing keys and compiled plugin packages are excluded.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile
from urllib.parse import quote, unquote, urlsplit
import zipfile


def run(args: list[str], cwd: Path, env: dict[str, str] | None = None) -> str:
    result = subprocess.run(args, cwd=cwd, env=env, text=True, encoding="utf-8", errors="replace", capture_output=True)
    if result.returncode:
        raise RuntimeError(f"Command failed: {args[0]} {args[1:]}\n{result.stdout}\n{result.stderr}")
    return result.stdout.strip()


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", help="New output directory; refuses to overwrite existing files")
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[2]
    config = json.loads((root / "frontend/src-tauri/tauri.conf.json").read_text(encoding="utf-8"))
    version = config["version"]
    if not re.fullmatch(r"\d+\.\d+\.\d+(?:-[a-zA-Z0-9.-]+)?", version):
        raise ValueError("Invalid desktop version")
    guide = (root / "docs/PLUGIN_DEVELOPMENT.md").read_text(encoding="utf-8")
    if f"正式版 v{version}" not in guide:
        raise ValueError("Update PLUGIN_DEVELOPMENT.md to the desktop release before packaging")
    source_commit = run(["git", "rev-parse", "HEAD"], root)
    published_source = run(["git", "ls-files", "backend/pkg/pluginapi"], root).splitlines()
    if not published_source:
        raise ValueError("No public SDK files found")
    output = Path(args.output).resolve() if args.output else root / "frontend/release-assets/plugin-devkit"
    output.mkdir(parents=True, exist_ok=True)
    archive = output / f"sub2api-plugin-devkit-v{version}.zip"
    standalone = output / "PLUGIN_DEVELOPMENT.md"
    checksums = output / "PLUGIN_DEVKIT_SHA256SUMS.txt"
    for target in (archive, standalone, checksums):
        if target.exists():
            raise FileExistsError(target)

    with tempfile.TemporaryDirectory(prefix="sub2api-public-devkit-") as scratch:
        kit = Path(scratch) / f"sub2api-plugin-devkit-v{version}"
        kit.mkdir()
        selected = [*published_source, "docs/PLUGIN_DEVELOPMENT.md", "LICENSE", "NOTICE.md", "backend/go.mod", "backend/go.sum"]
        for name in selected:
            source = root / name
            if source.is_symlink() or source.suffix.lower() in {".private", ".key", ".pem", ".exe", ".s2plugin", ".zip"}:
                raise ValueError(f"Unexpected SDK source: {name}")
            target = kit / name
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(source, target)

        # Keep the host's dependency versions while reducing the module to SDK packages.
        # The vendor tree enables builds with an already installed Go toolchain offline.
        run(["go", "mod", "tidy"], kit / "backend")
        run(["go", "mod", "vendor"], kit / "backend")
        print("Prepared SDK module and vendored dependencies", flush=True)

        # Retain offline links inside the kit. Host-only implementation references
        # point to the exact public source revision instead of missing local files.
        markdown_link = re.compile(r"\]\(([^\s)]+)\)")
        for document in [kit / "docs/PLUGIN_DEVELOPMENT.md", *(kit / "backend/pkg/pluginapi").rglob("*.md")]:
            def resolve_link(match: re.Match[str]) -> str:
                url = match.group(1)
                parts = urlsplit(url)
                if parts.scheme or parts.netloc or not parts.path:
                    return match.group(0)
                local = (document.parent / unquote(parts.path)).resolve()
                if local.exists() and local.is_relative_to(kit):
                    return match.group(0)
                repository_target = (root / document.relative_to(kit).parent / unquote(parts.path)).resolve()
                if not repository_target.is_relative_to(root) or not repository_target.exists():
                    raise ValueError(f"Broken documentation link: {document.relative_to(kit)} -> {url}")
                kind = "tree" if repository_target.is_dir() else "blob"
                path = quote(repository_target.relative_to(root).as_posix())
                anchor = f"#{parts.fragment}" if parts.fragment else ""
                return f"](https://github.com/rw0104/sub2api-cost-console/{kind}/{source_commit}/{path}{anchor})"
            document.write_text(markdown_link.sub(resolve_link, document.read_text(encoding="utf-8")), encoding="utf-8", newline="\n")

        (kit / "START_HERE.md").write_text(f"""# Sub2API 插件开发包 v{version}

对应正式桌面 v{version}、内核 {(root / 'frontend/CORE_VERSION').read_text().strip()}、扩展 {(root / 'frontend/CORE_EXTENSION_VERSION').read_text().strip()}。

1. 安装 Go 1.27.0，进入本开发包的 `backend` 目录。
2. 按[开发指南第 2 节](docs/PLUGIN_DEVELOPMENT.md)编译和签名公开示例。
3. 用自己的插件 ID、业务逻辑和配置 UI 替换示例，同步更新运行时与清单。
4. 将已编译的 `.s2plugin` 交给用户；用户在正式主程序首次导入时确认发布者即可安装，不需要编译或修改配置。

```powershell
cd backend
go test ./pkg/pluginapi/... -count=1
go build -o preprocess.exe ./pkg/pluginapi/examples/preprocess
```

源码与 vendor 已包含，不需要 Rust、Node.js、完整宿主仓库或主程序编译。已有 Go 1.27.0 工具链时上述步骤可离线运行。打包安装的完整命令见开发指南；不要直接运行插件 exe，宿主负责协议握手。

- [SDK 总览](backend/pkg/pluginapi/README.md)
- [请求预处理示例](backend/pkg/pluginapi/examples/preprocess/README.md)
- [保护传输接口](backend/pkg/pluginapi/docs/protection-transport.md)
- [UI Bridge](backend/pkg/pluginapi/docs/ui-bridge.md)
- [Host API](backend/pkg/pluginapi/docs/host-api.md)

本包是公开开发资料，不是可直接上传的插件。无私有插件、编译后的插件、测试密钥或主程序内核。宿主集成测试需要另外检出完整仓库；开发包只包含 SDK 测试。源码基线和文件摘要见 `DEVKIT_MANIFEST.json`。许可见 `LICENSE`、`NOTICE.md` 及 `backend/vendor/` 中的第三方许可文件。
""", encoding="utf-8", newline="\n")

        # Verify the extracted developer workflow without placing generated keys,
        # binaries or plugin packages in the source archive.
        test_env = dict(os.environ, GOPROXY="off", CGO_ENABLED="0")
        for key in ("GOOS", "GOARCH"):
            test_env.pop(key, None)
        run(["go", "test", "./pkg/pluginapi/...", "-count=1", "-timeout=2m"], kit / "backend", test_env)
        binary = Path(scratch) / ("preprocess.exe" if os.name == "nt" else "preprocess")
        run(["go", "build", "-trimpath", "-o", str(binary), "./pkg/pluginapi/examples/preprocess"], kit / "backend", test_env)
        key_prefix = Path(scratch) / "verification-publisher"
        run(["go", "run", "./pkg/pluginapi/tools/keygen", "-out", str(key_prefix)], kit / "backend", test_env)
        package = Path(scratch) / "verification.s2plugin"
        run(["go", "run", "./pkg/pluginapi/examples/preprocess/pack", "-binary", str(binary), "-signing-key", str(key_prefix)+".private", "-key-id", "devkit-verification", "-out", str(package)], kit / "backend", test_env)
        with zipfile.ZipFile(package) as packaged:
            package_manifest = json.loads(packaged.read("manifest.json"))
            if "signature.json" not in packaged.namelist():
                raise ValueError("Example package is not signed")
            for name, digest in package_manifest["files"].items():
                if hashlib.sha256(packaged.read(name)).hexdigest() != digest:
                    raise ValueError(f"Example package hash mismatch: {name}")
        print(run(["go", "run", str(root / "frontend/scripts/verify-plugin-devkit.go"), "-binary", str(binary), "-package", str(package), "-public-key", str(key_prefix)+".public"], kit / "backend", test_env), flush=True)
        print("SDK tests, offline example compilation and signed package generation passed", flush=True)

        files = {p.relative_to(kit).as_posix(): hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(kit.rglob("*")) if p.is_file()}
        manifest = {"desktop_version": version, "core_version": (root / "frontend/CORE_VERSION").read_text().strip(), "extension_version": (root / "frontend/CORE_EXTENSION_VERSION").read_text().strip(), "source_commit": source_commit, "files": files, "verified": ["sdk_tests", "offline_example_build", "signed_example_package_hashes", "ed25519_signature", "real_process_mtls_rpc"], "contains_private_plugins": False}
        (kit / "DEVKIT_MANIFEST.json").write_text(json.dumps(manifest, ensure_ascii=False, indent=2)+"\n", encoding="utf-8")
        with zipfile.ZipFile(archive, "x", zipfile.ZIP_DEFLATED) as zipped:
            for file in sorted(kit.rglob("*")):
                if file.is_file():
                    zipped.write(file, f"{kit.name}/{file.relative_to(kit).as_posix()}")

    # A standalone guide must work without the archive's relative directory tree.
    def online_link(match: re.Match[str]) -> str:
        url = match.group(1)
        if urlsplit(url).scheme or url.startswith("#"):
            return match.group(0)
        parts = urlsplit(url)
        target = (root / "docs" / unquote(parts.path)).resolve()
        if not target.is_relative_to(root) or not target.exists():
            raise ValueError(f"Broken standalone guide link: {url}")
        kind = "tree" if target.is_dir() else "blob"
        anchor = f"#{parts.fragment}" if parts.fragment else ""
        return f"](https://github.com/rw0104/sub2api-cost-console/{kind}/{source_commit}/{quote(target.relative_to(root).as_posix())}{anchor})"
    standalone.write_text(markdown_link.sub(online_link, guide), encoding="utf-8", newline="\n")
    checksums.write_text("".join(f"{hashlib.sha256(p.read_bytes()).hexdigest()}  {p.name}\n" for p in (archive, standalone)), encoding="utf-8")
    print(json.dumps({"version": version, "source_commit": source_commit, "files": [str(archive), str(standalone), str(checksums)], "archive_bytes": archive.stat().st_size}, ensure_ascii=False))


if __name__ == "__main__":
    main()
