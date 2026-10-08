# Install the latest silo release on Windows.
#
#   irm https://raw.githubusercontent.com/nonex-code/silo/main/install.ps1 | iex
$ErrorActionPreference = 'Stop'

$arch = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64' -or $env:PROCESSOR_ARCHITEW6432 -eq 'ARM64') { 'arm64' } else { 'amd64' }
$url = "https://github.com/nonex-code/silo/releases/latest/download/silo_windows_$arch.zip"
$tmp = Join-Path $env:TEMP ("silo-" + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
    $zip = Join-Path $tmp 'silo.zip'
    Write-Host "downloading $url"
    Invoke-WebRequest -Uri $url -OutFile $zip
    Expand-Archive -Path $zip -DestinationPath $tmp
    $dest = Join-Path $env:LOCALAPPDATA 'Programs\silo'
    New-Item -ItemType Directory -Force -Path $dest | Out-Null
    Copy-Item (Join-Path $tmp 'silo.exe') (Join-Path $dest 'silo.exe') -Force
    Write-Host "installed $dest\silo.exe"
    if ($env:Path -notlike "*$dest*") {
        Write-Host "note: add $dest to your PATH"
    }
}
finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}