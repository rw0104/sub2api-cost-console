#requires -Version 7.0
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$KeyFile,
    [Parameter(Mandatory = $true)][string]$KeyId,
    [string]$Output = '.build/release/ccodex-sleep-state-0.5.1.s2plugin',
    [string]$Targets = 'windows-amd64,linux-amd64,linux-arm64',
    [switch]$GenerateKey
)

$ErrorActionPreference = 'Stop'
$root = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
Push-Location $root
try {
    & go test -count=1 ./...
    if ($LASTEXITCODE -ne 0) { throw 'go test failed' }
    & go vet ./...
    if ($LASTEXITCODE -ne 0) { throw 'go vet failed' }

    $args = @('run', './cmd/pack', '-key', [IO.Path]::GetFullPath($KeyFile), '-key-id', $KeyId, '-targets', $Targets, '-out', $Output)
    if ($GenerateKey) { $args += '-generate-key' }
    & go @args
    if ($LASTEXITCODE -ne 0) { throw 'package build failed' }

    $packagePath = [IO.Path]::GetFullPath($Output)
    $publicKeyPath = Join-Path ([IO.Path]::GetDirectoryName($packagePath)) 'publisher-public-key.txt'
    & go run ./cmd/verify -package $packagePath -public-key $publicKeyPath -key-id $KeyId
    if ($LASTEXITCODE -ne 0) { throw 'package verification failed' }
} finally {
    Pop-Location
}
