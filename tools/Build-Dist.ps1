[CmdletBinding()]
param(
    [string]$Go = 'go',
    [string]$Flutter = 'flutter',
    [string]$ManagementEndpoint = 'https://vxziouwkf.top/gm/api'
)
$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
$dist = Join-Path $repoRoot 'dist'
$components = Join-Path $dist 'launcher-components'
$manager = Join-Path $dist 'GM管理器'
New-Item -ItemType Directory -Force $components, $manager | Out-Null
Push-Location $repoRoot
try {
    Push-Location 'server/go-server'
    try {
        & (Join-Path $PSScriptRoot 'Build-BridgeGo120.ps1') -Output "$components/OnlineBridge.exe"
        if ($LASTEXITCODE) { throw 'Go bridge build failed' }
        New-Item -ItemType Directory -Force "$dist/server" | Out-Null
        & $Go build -o "$dist/server/latency-probe.exe" ./cmd/latency
        if ($LASTEXITCODE) { throw 'Transport probe build failed' }
        & $Go build -ldflags "-H windowsgui -X kungfu.local/server/internal/desktop.DefaultManagementEndpoint=$ManagementEndpoint" -o "$manager/kungfu-desktop-admin.exe" ./cmd/desktop-admin
        if ($LASTEXITCODE) { throw 'Go administration build failed' }
    } finally { Pop-Location }
    & '.\launcher\client-adapter\build-login-skin.cmd'
    if ($LASTEXITCODE) { throw 'Login component build failed' }
    & dotnet publish tools/updater/Updater.csproj -c Release -o "$dist/updater"
    if ($LASTEXITCODE) { throw 'Update helper build failed' }
    Copy-Item -LiteralPath "$dist/updater/OpenKFO.Updater.exe" -Destination $manager -Force
    & dotnet publish launcher/launcher-online/OnlineLauncher.csproj -c Release -o "$dist/launcher"
    if ($LASTEXITCODE) { throw 'Launcher build failed' }
    & dotnet publish tools/local-server-monitor/LocalServerMonitor.csproj -c Release -o "$dist/local-server-monitor"
    if ($LASTEXITCODE) { throw 'Local server monitor build failed' }

    # Flutter/MSBuild cannot reliably read this project's Chinese workspace path.
    $stage = Join-Path ([System.IO.Path]::GetTempPath()) ('openkfo-build-' + [guid]::NewGuid().ToString('N'))
    if ($stage -match '[^\x00-\x7F]') { throw 'Set TEMP to an ASCII-only build directory before building Flutter.' }
    & robocopy 'gm' $stage /E /XD build .dart_tool .idea ephemeral /XF '*.iml' /NFL /NDL /NJH /NJS > $null
    if ($LASTEXITCODE -ge 8) { throw 'Flutter source staging failed' }
    Push-Location $stage
    try {
        & $Flutter pub get
        if ($LASTEXITCODE) { throw 'Flutter dependency resolution failed' }
        & $Flutter build windows --release
        if ($LASTEXITCODE) { throw 'Flutter build failed' }
    } finally { Pop-Location }
    Copy-Item -Path "$stage/build/windows/x64/runner/Release/*" -Destination $manager -Recurse -Force
    Move-Item -LiteralPath "$manager/kungfu_item_manager.exe" -Destination "$manager/GM管理器.exe" -Force
    Set-Content -LiteralPath "$dist/flutter-build-path.txt" -Value $stage -Encoding utf8
    Write-Host "Built: $dist"
} finally { Pop-Location }
