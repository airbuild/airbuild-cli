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

# AirBuild release-signing public key (RSA-3072, XML form for .NET). The private
# key lives only in the GitHub release workflow's secrets. checksums.txt.sig is
# an RSA-SHA256 signature over checksums.txt — a tampered release, including a
# swapped checksums.txt, fails verification.
$ReleasePubXml = "$PUBXML"

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

# --- Resolve the release (latest, or $env:AIRBUILD_VERSION = "vX.Y.Z") ---
if ($env:AIRBUILD_VERSION) {
    $Tag = $env:AIRBUILD_VERSION -replace '^cli-', ''
    if (-not $Tag.StartsWith("v")) { $Tag = "v$Tag" }
    $BaseUrl = "https://github.com/$Repo/releases/download/cli-$Tag"
    Write-Host "Installing pinned version $Tag" -ForegroundColor Blue
} else {
    $BaseUrl = "https://github.com/$Repo/releases/latest/download"
}

# --- Download binary + checksums ---
$Target = Join-Path $InstallDir "airbuild.exe"
$TempFile = Join-Path $env:TEMP "airbuild-install.exe"
$SumFile = Join-Path $env:TEMP "airbuild-checksums.txt"

Write-Host "Downloading $Binary..." -ForegroundColor Blue

try {
    Invoke-WebRequest -Uri "$BaseUrl/$Binary" -OutFile $TempFile -UseBasicParsing
} catch {
    Write-Host "Download failed: $_" -ForegroundColor Red
    Write-Host "If no release exists yet, build from source:" -ForegroundColor Red
    Write-Host "  go install github.com/airbuild/airbuild-cli@latest" -ForegroundColor Red
    exit 1
}
try {
    Invoke-WebRequest -Uri "$BaseUrl/checksums.txt" -OutFile $SumFile -UseBasicParsing
} catch {
    Remove-Item $TempFile -Force -ErrorAction SilentlyContinue
    Write-Host "Could not download checksums.txt - refusing to install an unverified binary." -ForegroundColor Red
    exit 1
}
$SigFile = Join-Path $env:TEMP "airbuild-checksums.sig"
try {
    Invoke-WebRequest -Uri "$BaseUrl/checksums.txt.sig" -OutFile $SigFile -UseBasicParsing
} catch {
    Remove-Item $TempFile, $SumFile -Force -ErrorAction SilentlyContinue
    Write-Host "Could not download checksums.txt.sig - refusing to install an unsigned release." -ForegroundColor Red
    exit 1
}

# --- Verify authenticity: checksums.txt was signed by AirBuild's release key ---
$SumBytes = [IO.File]::ReadAllBytes($SumFile)
$SigBytes = [IO.File]::ReadAllBytes($SigFile)
$Rsa = New-Object System.Security.Cryptography.RSACryptoServiceProvider
$Rsa.FromXmlString($ReleasePubXml)
$SigOk = $Rsa.VerifyData($SumBytes, "SHA256", $SigBytes)
Remove-Item $SigFile -Force -ErrorAction SilentlyContinue
if (-not $SigOk) {
    Remove-Item $TempFile, $SumFile -Force -ErrorAction SilentlyContinue
    Write-Host "Release signature verification failed - this release was not signed by AirBuild." -ForegroundColor Red
    Write-Host "Refusing to install." -ForegroundColor Red
    exit 1
}
Write-Host "Release signature verified" -ForegroundColor Green

# --- Verify integrity (SHA-256) ---
$Expected = $null
foreach ($Line in Get-Content $SumFile) {
    $Parts = $Line -split '\s+', 2
    if ($Parts.Count -eq 2 -and ($Parts[1].TrimStart('*').Trim()) -eq $Binary) { $Expected = $Parts[0].ToLower(); break }
}
$Actual = (Get-FileHash -Algorithm SHA256 -Path $TempFile).Hash.ToLower()
Remove-Item $SumFile -Force -ErrorAction SilentlyContinue
if (-not $Expected) {
    Remove-Item $TempFile -Force -ErrorAction SilentlyContinue
    Write-Host "No checksum listed for $Binary - refusing to install." -ForegroundColor Red
    exit 1
}
if ($Expected -ne $Actual) {
    Remove-Item $TempFile -Force -ErrorAction SilentlyContinue
    Write-Host "Checksum mismatch for $Binary (expected $Expected, got $Actual) - refusing to install." -ForegroundColor Red
    exit 1
}
Write-Host "Checksum verified (sha256 $Actual)" -ForegroundColor Green

# --- Install ---
Move-Item -Path $TempFile -Destination $Target -Force
Write-Host "Installed airbuild to $Target" -ForegroundColor Green

# --- PATH setup ---
# Check both the current session PATH and the persisted User PATH.
# We need to check exact directory match (not substring) to avoid false positives.
$UserPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($null -eq $UserPath) { $UserPath = "" }
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
