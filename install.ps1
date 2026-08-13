# AirBuild CLI installer — Windows (PowerShell)
#
# Usage:
#   irm https://raw.githubusercontent.com/airbuild/airbuild-cli/main/install.ps1 | iex
#
# Or with a specific version:
#   $env:AIRBUILD_VERSION = "v1.0.0"; irm https://raw.githubusercontent.com/airbuild/airbuild-cli/main/install.ps1 | iex
#
[CmdletBinding()]
param()

$ErrorActionPreference = "Stop"

$Repo = "airbuild/airbuild-cli"

# --- Detect platform ---
$Os = "windows"
$Arch = if ([Environment]::Is64BitOperatingSystem) { "amd64" } else { "arm64" }

# Check for ARM64
if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") {
    $Arch = "arm64"
}

$Binary = "airbuild-${Os}-${Arch}.exe"
Write-Host "Detected platform: ${Os}/${Arch}" -ForegroundColor Blue

# --- Determine install directory ---
# Prefer $env:AIRBUILD_INSTALL_DIR if set, otherwise use %LOCALAPPDATA%\AirBuild
# This is a per-user install (no admin required).
$InstallDir = if ($env:AIRBUILD_INSTALL_DIR) { $env:AIRBUILD_INSTALL_DIR } else { "$env:LOCALAPPDATA\AirBuild" }

if (-not (Test-Path $InstallDir)) {
    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
}

Write-Host "Install directory: $InstallDir" -ForegroundColor Blue

# --- Download ---
$DownloadUrl = "https://github.com/$Repo/releases/latest/download/$Binary"
$Target = Join-Path $InstallDir "airbuild.exe"
$TempFile = Join-Path $env:TEMP "airbuild-install.exe"

Write-Host "Downloading $Binary..." -ForegroundColor Blue

try {
    Invoke-WebRequest -Uri $DownloadUrl -OutFile $TempFile -UseBasicParsing
} catch {
    Write-Host "Download failed: $_" -ForegroundColor Red
    Write-Host "If no release exists yet, build from source:" -ForegroundColor Red
    Write-Host "  go install github.com/airbuild/airbuild-cli@latest" -ForegroundColor Red
    exit 1
}

# --- Install ---
Move-Item -Path $TempFile -Destination $Target -Force
Write-Host "Installed airbuild to $Target" -ForegroundColor Green

# --- PATH setup ---
# Check both the current session PATH and the persisted User PATH.
# We need to check exact directory match (not substring) to avoid false positives.
$UserPath = [Environment]::GetEnvironmentVariable("Path", "User") ?? ""
$UserPathDirs = $UserPath -split ";" | Where-Object { $_ -ne "" }
$AlreadyInPath = $UserPathDirs -contains $InstallDir

if (-not $AlreadyInPath) {
    Write-Host "Adding $InstallDir to user PATH..." -ForegroundColor Yellow
    $NewPath = if ($UserPath -eq "") { $InstallDir } else { "$UserPath;$InstallDir" }
    [Environment]::SetEnvironmentVariable("Path", $NewPath, "User")
    # Also update current session so airbuild works immediately
    $env:Path += ";$InstallDir"
    Write-Host "Added $InstallDir to user PATH." -ForegroundColor Green
    Write-Host "Restart your terminal for PATH changes to take full effect." -ForegroundColor Yellow
} else {
    Write-Host "$InstallDir is already in your PATH." -ForegroundColor Green
    # Ensure current session can find it too
    $SessionPathDirs = $env:Path -split ";" | Where-Object { $_ -ne "" }
    if ($SessionPathDirs -notcontains $InstallDir) {
        $env:Path += ";$InstallDir"
    }
}

# --- Verify ---
try {
    & $Target --help | Out-Null
    Write-Host "Verification: airbuild runs successfully" -ForegroundColor Green
    Write-Host "Ready! Run: airbuild --help" -ForegroundColor Green
} catch {
    Write-Host "Warning: airbuild --help returned an error. The binary may not be compatible." -ForegroundColor Yellow
}
