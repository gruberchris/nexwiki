<#
.SYNOPSIS
  Removes the installed nexwiki binary on Windows.

.DESCRIPTION
  Removes nexwiki.exe from the install directory (default
  $env:LOCALAPPDATA\Programs\nexwiki, plus $HOME\bin and any explicit path)
  and drops the directory from the user PATH when it becomes empty.
  Your wiki content in %AppData%\nexwiki\nexwiki-data is left untouched.

.PARAMETER InstallDir
  Explicit install directory (or full path to nexwiki.exe) to remove.
  When omitted, the standard locations are searched.

.EXAMPLE
  .\scripts\uninstall.ps1
.EXAMPLE
  .\scripts\uninstall.ps1 -InstallDir "$HOME\bin"
#>
param(
  [string]$InstallDir = ""
)

$ErrorActionPreference = "Stop"
$found = $false

function Remove-NexwikiAt([string]$dir) {
  $target = Join-Path $dir "nexwiki.exe"
  if (Test-Path $target) {
    Write-Host "==> Removing '$target'..."
    Remove-Item -Force $target
    Write-Host "==> Successfully removed '$target'"
    $script:found = $true
  }
  # Tidy the user PATH entry when the directory no longer holds the binary.
  if ((Test-Path $dir) -and -not (Test-Path $target)) {
    $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
    $parts = $userPath -split ";" | Where-Object { $_ -ne "" -and $_ -ne $dir }
    if ($parts.Count -lt ($userPath -split ";").Count) {
      [Environment]::SetEnvironmentVariable("Path", ($parts -join ";"), "User")
      Write-Host "==> Removed '$dir' from your user PATH."
    }
  }
}

if (-not [string]::IsNullOrWhiteSpace($InstallDir)) {
  if (Test-Path $InstallDir -PathType Container) {
    Remove-NexwikiAt $InstallDir
  } elseif (Test-Path $InstallDir -PathType Leaf) {
    Write-Host "==> Removing '$InstallDir'..."
    Remove-Item -Force $InstallDir
    $found = $true
  } else {
    Write-Warning "'$InstallDir' does not exist."
  }
  if ($found) {
    Write-Host "==> NexWiki uninstallation complete (wiki data was left in place)."
    return
  }
  Write-Host "==> Not found at specified target; checking standard locations..."
}

$candidates = @(
  (Join-Path $env:LOCALAPPDATA "Programs\nexwiki"),
  (Join-Path $HOME "bin"),
  (Join-Path $env:LOCALAPPDATA "Programs\nexwiki\bin")
)
foreach ($dir in $candidates) {
  if (Test-Path $dir -PathType Container) {
    Remove-NexwikiAt $dir
  }
}

if (-not $found) {
  $onPath = Get-Command nexwiki -ErrorAction SilentlyContinue
  if ($onPath -and (Test-Path $onPath.Source)) {
    Write-Host "==> Found nexwiki in PATH at '$($onPath.Source)'"
    Remove-Item -Force $onPath.Source
    $found = $true
  }
}

if ($found) {
  Write-Host "==> NexWiki uninstallation complete (wiki data was left in place)."
} else {
  Write-Host "==> No nexwiki.exe found in default locations."
}
