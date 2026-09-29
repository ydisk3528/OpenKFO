param([string]$Python = 'python')
$ErrorActionPreference = 'Stop'
$repository = Split-Path $PSScriptRoot -Parent
$environment = Join-Path $repository 'runtime-local\oss-publisher-venv'
if (-not (Test-Path (Join-Path $environment 'Scripts\python.exe'))) {
    & $Python -m venv $environment
    if ($LASTEXITCODE -ne 0) { throw '创建构建环境失败' }
}
$interpreter = Join-Path $environment 'Scripts\python.exe'
& $interpreter -m pip install -r (Join-Path $repository 'tools\oss-publisher\requirements.txt') 'pyinstaller==6.22.3'
if ($LASTEXITCODE -ne 0) { throw '安装构建依赖失败' }
Push-Location (Join-Path $repository 'tools\oss-publisher')
try {
    & $interpreter -m unittest -v test_publisher
    if ($LASTEXITCODE -ne 0) { throw '发布工具测试失败' }
    & $interpreter -m PyInstaller --noconfirm --clean --onefile --windowed --name 'OSS发布工具' --collect-all alibabacloud_oss_v2 --distpath (Join-Path $repository 'dist\oss-publisher') --workpath (Join-Path $repository 'build\oss-publisher') --specpath (Join-Path $repository 'build\oss-publisher') app.py
    if ($LASTEXITCODE -ne 0) { throw '发布工具构建失败' }
} finally {
    Pop-Location
}
