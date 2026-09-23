#requires -Version 7.0
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$Package,
    [Parameter(Mandatory = $true)][string]$PublicKey,
    [Parameter(Mandatory = $true)][string]$KeyId
)

$ErrorActionPreference = 'Stop'
$root = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
Push-Location $root
try {
    & go run ./cmd/verify -package ([IO.Path]::GetFullPath($Package)) -public-key ([IO.Path]::GetFullPath($PublicKey)) -key-id $KeyId
    if ($LASTEXITCODE -ne 0) { throw 'package verification failed' }
} finally {
    Pop-Location
}
