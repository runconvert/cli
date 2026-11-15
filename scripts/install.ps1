# Installs the RunConvert CLI on Windows (PowerShell):
#   irm https://www.runconvert.com/install.ps1 | iex
#
# Settings: $env:RUNCONVERT_VERSION (e.g. 0.1.0), $env:RUNCONVERT_INSTALL_DIR,
# $env:RUNCONVERT_DOWNLOAD_URL.
$ErrorActionPreference = 'Stop'

$repo = if ($env:RUNCONVERT_DOWNLOAD_URL) { $env:RUNCONVERT_DOWNLOAD_URL } else { 'https://github.com/runconvert/runconvert-cli/releases' }
$dir = if ($env:RUNCONVERT_INSTALL_DIR) { $env:RUNCONVERT_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'runconvert\bin' }
$arch = if ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture -eq 'Arm64') { 'arm64' } else { 'amd64' }
$base = if ($env:RUNCONVERT_VERSION) { "$repo/download/v$($env:RUNCONVERT_VERSION.TrimStart('v'))" } else { "$repo/latest/download" }
$name = "runconvert_windows_$arch.zip"

$tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("runconvert-" + [guid]::NewGuid())
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
  Write-Host "Downloading runconvert for windows/$arch..."
  Invoke-WebRequest -UseBasicParsing "$base/$name" -OutFile (Join-Path $tmp $name)
  Invoke-WebRequest -UseBasicParsing "$base/checksums.txt" -OutFile (Join-Path $tmp 'checksums.txt')

  # Check the download against the published checksum before installing it.
  $line = Get-Content (Join-Path $tmp 'checksums.txt') | Where-Object { $_ -match " $([regex]::Escape($name))$" }
  if (-not $line) { throw "$name is not in checksums.txt" }
  $want = ($line -split ' ')[0].ToLower()
  $got = (Get-FileHash (Join-Path $tmp $name) -Algorithm SHA256).Hash.ToLower()
  if ($got -ne $want) { throw "checksum mismatch for $name" }

  Expand-Archive (Join-Path $tmp $name) -DestinationPath $tmp -Force
  New-Item -ItemType Directory -Path $dir -Force | Out-Null
  Copy-Item (Join-Path $tmp 'runconvert.exe') (Join-Path $dir 'runconvert.exe') -Force
} finally {
  Remove-Item $tmp -Recurse -Force -ErrorAction SilentlyContinue
}

$path = [Environment]::GetEnvironmentVariable('Path', 'User')
if (($path -split ';') -notcontains $dir) {
  [Environment]::SetEnvironmentVariable('Path', "$dir;$path", 'User')
  Write-Host "Added $dir to your PATH (open a new terminal to use it)."
}
Write-Host "Installed $(& (Join-Path $dir 'runconvert.exe') --version)"
Write-Host ""
Write-Host "Next:  runconvert login    then    runconvert"
