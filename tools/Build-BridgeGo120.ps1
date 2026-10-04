[CmdletBinding()]
param(
    [string]$Go = '',
    [string]$Output = ''
)
$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
if (!$Go) { $Go = Join-Path $repoRoot 'runtime-local/go120/go/bin/go.exe' }
if (!$Output) { $Output = Join-Path $repoRoot 'dist/launcher-components/OnlineBridge.exe' }
$version = & $Go version
if ($LASTEXITCODE -ne 0 -or $version -notmatch '^go version go1\.20\.') { throw 'OnlineBridge requires the Go 1.20 toolchain for Windows 7 compatibility.' }
$oldOS, $oldArch, $oldCGO = $env:GOOS, $env:GOARCH, $env:CGO_ENABLED
Push-Location (Join-Path $repoRoot 'server/go-server')
try {
    $env:GOOS = 'windows'
    $env:GOARCH = '386'
    $env:CGO_ENABLED = '0'
    & $Go test -mod=readonly '-modfile=bridge-go120.mod' ./internal/bridge ./internal/tunnel ./cmd/bridge
    if ($LASTEXITCODE) { throw 'Go 1.20 bridge tests failed' }
    New-Item -ItemType Directory -Force (Split-Path -Parent $Output) | Out-Null
    & $Go build -mod=readonly '-modfile=bridge-go120.mod' -trimpath '-ldflags=-s -w -H=windowsgui' -o $Output ./cmd/bridge
    if ($LASTEXITCODE) { throw 'Go 1.20 bridge build failed' }
    & $Go version -m $Output
} finally {
    Pop-Location
    $env:GOOS, $env:GOARCH, $env:CGO_ENABLED = $oldOS, $oldArch, $oldCGO
}
