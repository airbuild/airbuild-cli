# AirBuild CLI installer — Windows (PowerShell)
#
# Usage:
#   irm https://raw.githubusercontent.com/airbuild/cli/main/install.ps1 | iex
#
# Or with a specific version:
#   $env:AIRBUILD_VERSION = "v1.0.0"; irm https://raw.githubusercontent.com/airbuild/cli/main/install.ps1 | iex
#
[CmdletBinding()]
param()

$ErrorActionPreference = "Stop"

$Repo = "airbuild/cli"

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
    Write-Host "  go install github.com/airbuild/cli@latest" -ForegroundColor Red
    exit 1
}

# --- Install ---
Move-Item -Path $TempFile -Destination $Target -Force
Write-Host "Installed airbuild to $Target" -ForegroundColor Green

# --- PATH check ---
$PathDirs = $env:Path -split ";"
if ($PathDirs -notcontains $InstallDir) {
    Write-Host "$InstallDir is not in your PATH." -ForegroundColor Yellow
    Write-Host "Adding to user PATH..." -ForegroundColor Yellow

    $CurrentPath = [Environment]::GetEnvironmentVariable("Path", "User")
    if ($CurrentPath -notlike "*$InstallDir*") {
        [Environment]::SetEnvironmentVariable("Path", "$CurrentPath;$InstallDir", "User")
        $env:Path += ";$InstallDir"
        Write-Host "Added $InstallDir to user PATH." -ForegroundColor Green
        Write-Host "Restart your terminal for PATH changes to take effect." -ForegroundColor Yellow
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
