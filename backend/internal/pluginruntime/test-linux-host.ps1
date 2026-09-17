# Run the integration test from a Linux host process against the local daemon.
# The trusted test host sees source/modules read-only; uploaded plugins never see
# those mounts or the Docker socket.
$ErrorActionPreference = "Stop"
$repoRoot = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot "../../..")).Path
$moduleCache = (& go env GOMODCACHE).Trim()
if ($LASTEXITCODE -ne 0) { throw "Unable to locate Go module cache" }
foreach ($source in @($repoRoot, $moduleCache)) {
    if ($source.Contains(",") -or $source.Contains("`n") -or $source.Contains("`r")) { throw "Unsupported bind path" }
}
docker build -f (Join-Path $repoRoot "deploy/Dockerfile.plugin-sandbox") -t sub2api-plugin-sandbox:1 $repoRoot
if ($LASTEXITCODE -ne 0) { throw "Sandbox image build failed" }
docker build -f (Join-Path $repoRoot "deploy/Dockerfile.plugin-sandbox-test") -t sub2api-plugin-sandbox-test:1 $repoRoot
if ($LASTEXITCODE -ne 0) { throw "Test host image build failed" }
$nonce = [guid]::NewGuid().ToString("N")
$volume = "sub2api-sandbox-validation-$nonce"
docker volume create --label "org.sub2api.plugin.test=$nonce" $volume | Out-Null
if ($LASTEXITCODE -ne 0) { throw "Test volume creation failed" }
$result = 1
try {
    $info = (docker volume inspect $volume | ConvertFrom-Json)[0]
    if ($info.Name -ne $volume -or $info.Labels.'org.sub2api.plugin.test' -ne $nonce -or $info.Driver -ne "local") { throw "Unexpected test volume identity" }
    $mount = $info.Mountpoint
    if (-not $mount.StartsWith("/") -or -not $mount.EndsWith("/$volume/_data") -or $mount.Contains(",") -or $mount.Contains("..")) { throw "Unexpected test volume path" }
    $arguments = @("run", "--rm", "--name", "sub2api-sandbox-test-$nonce", "--memory", "2g", "--cpus", "2", "--pids-limit", "256",
        "--mount", "type=bind,src=/var/run/docker.sock,dst=/var/run/docker.sock",
        "--mount", "type=bind,src=$mount,dst=$mount",
        "--mount", "type=bind,src=$repoRoot,dst=/workspace,readonly",
        "--mount", "type=bind,src=$moduleCache,dst=/go/pkg/mod,readonly",
        "--env", "TMPDIR=$mount", "--env", "GOPROXY=off", "--env", "GOSUMDB=off", "--env", "SUB2API_TEST_SANDBOX_CONTAINER=1",
        "sub2api-plugin-sandbox-test:1", "go", "test", "./internal/pluginruntime", "-run", "^TestContainerIsolationIntegration$", "-count=1", "-v", "-timeout=8m")
    docker @arguments
    $result = $LASTEXITCODE
} finally {
    $info = (docker volume inspect $volume | ConvertFrom-Json)[0]
    if ($info.Name -eq $volume -and $info.Labels.'org.sub2api.plugin.test' -eq $nonce) { docker volume rm $volume }
}
if ($result -ne 0) { throw "Linux host validation failed ($result)" }
