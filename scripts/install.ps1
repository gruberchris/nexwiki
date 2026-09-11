<#
.SYNOPSIS
  Installs the latest released nexwiki binary on Windows.

.DESCRIPTION
  Downloads the latest gruberchris/nexwiki GitHub release asset
  (nexwiki-<version>-windows-amd64.exe, never an unreleased main-branch
  build) and installs it as nexwiki.exe. Your wiki content lives in
  %AppData%\nexwiki\nexwiki-data and is created on first launch.

.PARAMETER InstallDir
  Target directory. Defaults to $env:LOCALAPPDATA\Programs\nexwiki.

.PARAMETER Version
  Pin a specific release tag (e.g. -Version v0.2.0). Defaults to latest.

.EXAMPLE
  .\scripts\install.ps1
.EXAMPLE
  .\scripts\install.ps1 -InstallDir "$HOME\bin" -Version v0.2.0
#>
param(
  [string]$InstallDir = (Join-Path $env:LOCALAPPDATA "Programs\nexwiki"),
  [string]$Version = $env:NEXWIKI_VERSION
)

$ErrorActionPreference = "Stop"
$Repo = "gruberchris/nexwiki"

if ([string]::IsNullOrWhiteSpace($Version)) {
  Write-Host "==> Fetching latest release tag..."
  $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest"
  $Version = $release.tag_name
  if ([string]::IsNullOrWhiteSpace($Version)) {
    throw "Could not determine the latest release. Retry later or pass -Version v0.2.0."
  }
  Write-Host "==> Latest release: $Version"
} else {
  Write-Host "==> Using pinned version: $Version"
}

$number = $Version.TrimStart("v")
$asset = "nexwiki-$number-windows-amd64.exe"
$downloadUrl = "https://github.com/$Repo/releases/download/$Version/$asset"

New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
$dest = Join-Path $InstallDir "nexwiki.exe"
$tmp = Join-Path ([IO.Path]::GetTempPath()) "nexwiki-$number-windows-amd64.exe"

Write-Host "==> Downloading $asset..."
Invoke-WebRequest -Uri $downloadUrl -OutFile $tmp

# Verify SHA256 when the release publishes checksums (warn-only on mismatch
# data absence; hard-fail on a real mismatch).
try {
  $sums = Invoke-WebRequest -Uri "https://github.com/$Repo/releases/download/$Version/SHA256SUMS.txt" -UseBasicParsing |
    Select-Object -ExpandProperty Content
  $expected = ($sums -split "`n" | Where-Object { $_ -match "\s$asset\s*$" } | ForEach-Object { ($_ -split '\s+')[0] } | Select-Object -First 1)
  if ($expected) {
    $actual = (Get-FileHash -Path $tmp -Algorithm SHA256).Hash.ToLower()
    if ($actual -ne $expected.ToLower()) {
      throw "Checksum mismatch for $asset (expected $expected, got $actual)."
    }
    Write-Host "==> Checksum OK."
  } else {
    Write-Warning "No checksum entry for $asset; skipping verification."
  }
} catch [System.Management.Automation.RuntimeException] {
  throw
} catch {
  Write-Warning "SHA256SUMS.txt unavailable; skipping verification."
}

Move-Item -Force -Path $tmp -Destination $dest
Write-Host "==> Successfully installed nexwiki $Version to $dest"

# Ensure the install dir is on the user's PATH.
$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
if (($userPath -split ";" | Where-Object { $_ -eq $InstallDir }).Count -eq 0) {
  [Environment]::SetEnvironmentVariable("Path", "$userPath;$InstallDir", "User")
  Write-Host "==> Added $InstallDir to your user PATH (restart the terminal to pick it up)."
} else {
  Write-Host "==> Verify installation: nexwiki --help"
}
