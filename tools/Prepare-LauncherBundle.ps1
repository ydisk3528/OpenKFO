[CmdletBinding()]
param([Parameter(Mandatory = $true)][string]$ClientDirectory)
$ErrorActionPreference = 'Stop'
$client = (Resolve-Path -LiteralPath $ClientDirectory).Path
$source = Get-Content -LiteralPath (Join-Path $client 'bridge.json') -Raw | ConvertFrom-Json
$nativeLogin = Join-Path $client 'SDError.dll'
if ((Get-FileHash -LiteralPath $nativeLogin -Algorithm SHA256).Hash -ne '8B50DB6CEAD1C84BCF1ECD360B86C91E989EE58FDC2F87F965DB14024BF969BF') {
    throw 'Use a verified client with the custom native login component, not the original SDError.dll.'
}
$components = Join-Path (Split-Path -Parent $PSScriptRoot) 'dist/launcher-components'
New-Item -ItemType Directory -Force -Path $components | Out-Null
if ((Get-FileHash -LiteralPath (Join-Path $client 'gfld.dat')).Hash -ne '98C43BE72AC7600B368D4E185D75205376F79E938EA42E4B16CE8F8C4BAE827B') { throw 'Unsupported gfld.dat.' }
foreach ($name in 'gfld.dat','SDError.dll','lqbz.dll','libssl-1_1.dll','libcrypto-1_1.dll') {
    Copy-Item -LiteralPath (Join-Path $client $name) -Destination (Join-Path $components $name) -Force
}
$bundle = Join-Path (Split-Path -Parent $PSScriptRoot) 'runtime-local/launcher-package'
$stage = Join-Path $bundle 'contents'
$certificates = Join-Path $stage 'launcher-certificates/online'
New-Item -ItemType Directory -Force -Path $certificates | Out-Null
$config = [ordered]@{}
foreach ($key in 'client_sha256','config_hash','single_client','shared_client','trace_protocol','credentials_scope','url','update_base_url') {
    if ($source.PSObject.Properties.Name -contains $key) { $config[$key] = $source.$key }
}
$endpoint = [Uri]$source.url
if ($endpoint.Scheme -ne 'tls' -or $endpoint.IsLoopback -or $source.config_hash -notmatch '^[0-9a-fA-F]{64}$') { throw 'Use a verified online client configuration.' }
$config['client_directory'] = '.'
foreach ($entry in @(@('server_certificate','origin.crt'),@('login_certificate','login.crt'),@('login_key','login.key'))) {
    $path = [IO.Path]::GetFullPath([string]$source.($entry[0]), $client)
    if ($entry[0] -ne 'login_key' -and (Get-Content -LiteralPath $path -Raw) -match 'PRIVATE KEY') { throw 'A public certificate must not contain the server private key.' }
    Copy-Item -LiteralPath $path -Destination (Join-Path $certificates $entry[1]) -Force
    $config[$entry[0]] = 'launcher-certificates/online/' + $entry[1]
}
$config | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $stage 'bridge.json') -Encoding utf8
Add-Type -AssemblyName System.IO.Compression.FileSystem
$stream = [IO.File]::Create((Join-Path $bundle 'bootstrap.zip'))
$archive = [IO.Compression.ZipArchive]::new($stream, [IO.Compression.ZipArchiveMode]::Create)
try {
    foreach ($name in 'bridge.json','launcher-certificates/online/origin.crt','launcher-certificates/online/login.crt','launcher-certificates/online/login.key') {
        [IO.Compression.ZipFileExtensions]::CreateEntryFromFile($archive, (Join-Path $stage $name), $name, [IO.Compression.CompressionLevel]::Optimal) | Out-Null
    }
} finally { $archive.Dispose(); $stream.Dispose() }
