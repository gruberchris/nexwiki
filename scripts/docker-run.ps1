<#
.SYNOPSIS
  Pulls the latest released nexwiki image and (re)creates the container.

.DESCRIPTION
  Windows counterpart to scripts/docker-run.sh:
  1. Resolves the wiki data directory (%AppData%\nexwiki\nexwiki-data,
     matching the binary's defaultDataDir).
  2. Pulls the latest RELEASED image (ghcr.io/gruberchris/nexwiki:latest) —
     never a local unreleased build.
  3. Recreates the 'nexwiki' container with -p 5808:5808 and the data dir
     mounted at /app/data.

.PARAMETER Image
  Container image. Defaults to ghcr.io/gruberchris/nexwiki.

.PARAMETER Tag
  Image tag. Defaults to latest (always the latest release).

.PARAMETER ContainerName
  Container name. Defaults to nexwiki.

.PARAMETER HostPort
  Host port mapped to the container's 5808. Defaults to 5808.

.EXAMPLE
  .\scripts\docker-run.ps1
.EXAMPLE
  .\scripts\docker-run.ps1 -Tag v0.2.0 -HostPort 5808
#>
param(
  [string]$Image = $env:NEXWIKI_IMAGE,
  [string]$Tag = $env:NEXWIKI_TAG,
  [string]$ContainerName = $env:NEXWIKI_CONTAINER_NAME,
  [string]$HostPort = $env:NEXWIKI_HOST_PORT
)

$ErrorActionPreference = "Stop"

if ([string]::IsNullOrWhiteSpace($Image)) { $Image = "ghcr.io/gruberchris/nexwiki" }
if ([string]::IsNullOrWhiteSpace($Tag)) { $Tag = "latest" }
if ([string]::IsNullOrWhiteSpace($ContainerName)) { $ContainerName = "nexwiki" }
if ([string]::IsNullOrWhiteSpace($HostPort)) { $HostPort = "5808" }

if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
  throw "Docker is not installed or not on PATH."
}

$dataDir = Join-Path $env:APPDATA "nexwiki\nexwiki-data"
Write-Host "==> Wiki data directory: $dataDir"
New-Item -ItemType Directory -Force -Path $dataDir | Out-Null

Write-Host "==> Pulling ${Image}:${Tag}..."
docker pull "${Image}:${Tag}"

$existing = docker ps -a --format '{{.Names}}' | Where-Object { $_ -eq $ContainerName }
if ($existing) {
  Write-Host "==> Removing existing container '$ContainerName'..."
  docker rm -f $ContainerName | Out-Null
}

Write-Host "==> Starting container '$ContainerName'..."
docker run -d `
  --name $ContainerName `
  -p "${HostPort}:5808" `
  -v "${dataDir}:/app/data" `
  --restart unless-stopped `
  "${Image}:${Tag}" | Out-Null

Write-Host "==> NexWiki is running at http://localhost:$HostPort"
Write-Host "==> Wiki content is stored in $dataDir"
