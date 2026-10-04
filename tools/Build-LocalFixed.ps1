$ErrorActionPreference = 'Stop'
$repo = Split-Path -Parent $PSScriptRoot
$out = Join-Path (Split-Path -Parent $repo) 'dist'
$go = Join-Path $repo 'runtime-local/go-sdk/go/bin/go.exe'
function Run($exe, $arguments) { & $exe @arguments; if($LASTEXITCODE -ne 0){throw "Build failed: $exe"} }
Push-Location (Join-Path $repo 'server/go-server')
try {
 Run $go @('build','-o',"$out/服务器/kungfu-server.exe",'./cmd/server')
 & (Join-Path $PSScriptRoot 'Build-BridgeGo120.ps1') -Output "$out/登录器/launcher-files/OnlineBridge.exe"
 Run $go @('build','-ldflags','-H windowsgui','-o',"$out/登录器/LauncherSupport.exe",'./cmd/launcher-support')
 Run $go @('build','-ldflags','-H windowsgui -X kungfu.local/server/internal/desktop.DefaultManagementEndpoint=https://vxfnqfjdr.top/gm/api','-o',"$out/GM管理器/kungfu-desktop-admin.exe",'./cmd/desktop-admin')
} finally {Pop-Location}
Run 'dotnet' @('publish',"$repo/tools/local-server-monitor/LocalServerMonitor.csproj",'-c','Release','-o',"$out/服务器")
Run 'dotnet' @('publish',"$repo/tools/updater/Updater.csproj",'-c','Release','-o',"$out/GM管理器")
Run 'cmd.exe' @('/c',"$repo/tools/game-mod/build.cmd")
Copy-Item -LiteralPath "$repo/tools/game-mod/GameMod.exe" -Destination "$out/登录器/launcher-files/GameMod.exe" -Force
foreach($job in @(@('gm','E:/OpenKFO-GMBuildCurrent','F:/flutter/bin/flutter.bat','GM管理器','kungfu_item_manager.exe','GM管理器.exe'),@('launcher/launcher-flutter','E:/OpenKFO-ModLauncherBuild','E:/OpenKFO-Flutter3169/flutter/bin/flutter.bat','登录器','openkfo_launcher.exe','线下启动器.exe'))) {
 & robocopy "$repo/$($job[0])" $job[1] /E /XD build .dart_tool .git ephemeral /XF .flutter-plugins /NFL /NDL /NJH /NJS | Out-Null
 if($LASTEXITCODE -ge 8){throw 'Flutter staging failed'}
 Push-Location $job[1]
 try {Run $job[2] @('pub','get','--offline');Run $job[2] @('build','windows','--release')} finally {Pop-Location}
 $release="$($job[1])/build/windows/x64/runner/Release"
 Copy-Item -Path "$release/*" -Destination "$out/$($job[3])" -Recurse -Force
 Move-Item -LiteralPath "$out/$($job[3])/$($job[4])" -Destination "$out/$($job[3])/$($job[5])" -Force
}
$manifest="$out/登录器/launcher-files/files.json"
$m=Get-Content -LiteralPath $manifest -Raw | ConvertFrom-Json
foreach($name in @('OnlineBridge.exe','GameMod.exe')) {$m | Add-Member -NotePropertyName $name -NotePropertyValue ((Get-FileHash -LiteralPath "$out/登录器/launcher-files/$name").Hash.ToLower()) -Force}
$m | ConvertTo-Json | Set-Content -LiteralPath $manifest -Encoding utf8
Write-Output "Fixed output: $out"
