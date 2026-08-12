# AirBuild CLI

A standalone command-line tool for uploading builds to AirBuild and managing
your apps, builds, and install links — no Node.js or npm required.

## Install

### One-liner (macOS & Linux)

```bash
curl -fsSL https://raw.githubusercontent.com/airbuild/cli/main/install.sh | bash
```

### Windows (PowerShell)

```powershell
irm https://raw.githubusercontent.com/airbuild/cli/main/install.ps1 | iex
```

### From binary (manual)

Download the latest binary for your platform from
[GitHub Releases](https://github.com/airbuild/cli/releases):

```bash
# macOS (Apple Silicon)
curl -L https://github.com/airbuild/cli/releases/latest/download/airbuild-darwin-arm64 -o airbuild
chmod +x airbuild && sudo mv airbuild /usr/local/bin/

# macOS (Intel)
curl -L https://github.com/airbuild/cli/releases/latest/download/airbuild-darwin-amd64 -o airbuild
chmod +x airbuild && sudo mv airbuild /usr/local/bin/

# Linux (x86_64)
curl -L https://github.com/airbuild/cli/releases/latest/download/airbuild-linux-amd64 -o airbuild
chmod +x airbuild && sudo mv airbuild /usr/local/bin/

# Linux (ARM64)
curl -L https://github.com/airbuild/cli/releases/latest/download/airbuild-linux-arm64 -o airbuild
chmod +x airbuild && sudo mv airbuild /usr/local/bin/
```

```powershell
# Windows (PowerShell)
$dir = "$env:LOCALAPPDATA\AirBuild"
New-Item -ItemType Directory -Path $dir -Force
Invoke-WebRequest "https://github.com/airbuild/cli/releases/latest/download/airbuild-windows-amd64.exe" -OutFile "$dir\airbuild.exe"
# Add to PATH:
[Environment]::SetEnvironmentVariable("Path", "$env:Path;$dir", "User")
```

### From source

```bash
go install github.com/airbuild/cli@latest
```

### Build from this repo

```bash
cd packages/cli
make build      # current platform
make dist       # all platforms
```

## Quick start

```bash
# 1. Log in with your API key (from Dashboard > Settings > API Keys)
airbuild login --api-key airbuild_xxx

# 2. List your apps
airbuild apps list

# 3. Upload a build
airbuild upload ./build/app-release.apk --app-id app_xxx

# 4. List builds
airbuild builds list --app-id app_xxx

# 5. Create an install link
airbuild links create --build-id build_xxx

# 6. List install links
airbuild links list --app-id app_xxx
```

## Commands

### `airbuild login`
Authenticate with your AirBuild API key. Saves credentials to `~/.airbuild/config.json`.

```bash
airbuild login --api-key airbuild_xxx
airbuild login --api-key airbuild_xxx --api-url https://staging.airbuild.dev
```

### `airbuild upload <file>`
Upload an IPA or APK build. Platform is auto-detected from the file extension.

```bash
airbuild upload ./MyApp.ipa --app-id app_xxx
airbuild upload ./app-release.apk --app-id app_xxx --release-notes "Bug fixes"
airbuild upload ./MyApp.ipa --app-id app_xxx --platform IOS
```

### `airbuild apps list`
List all apps in your organization.

### `airbuild builds list`
List builds for a specific app.

```bash
airbuild builds list --app-id app_xxx
```

### `airbuild links list`
List install links for a specific app.

```bash
airbuild links list --app-id app_xxx
```

### `airbuild links create`
Create a new install link for a build.

```bash
airbuild links create --build-id build_xxx
```

### `airbuild config`
Manage CLI configuration.

```bash
airbuild config show                          # Show current config
airbuild config set --api-key airbuild_xxx    # Set API key
airbuild config set --api-url https://...     # Set API URL
```

## CI/CD integration

The CLI is designed for CI/CD pipelines. Set the API key via config and run
the upload command:

### GitHub Actions

```yaml
- name: Install AirBuild CLI
  run: |
    curl -fsSL https://raw.githubusercontent.com/airbuild/cli/main/install.sh | bash

- name: Login
  run: airbuild login --api-key ${{ secrets.AIRBUILD_API_KEY }}

- name: Upload build
  run: airbuild upload ./app/build/outputs/apk/release/app-release.apk --app-id ${{ secrets.AIRBUILD_APP_ID }}
```

### GitLab CI

```yaml
upload:
  script:
    - curl -fsSL https://raw.githubusercontent.com/airbuild/cli/main/install.sh | bash
    - airbuild login --api-key $AIRBUILD_API_KEY
    - airbuild upload ./app/build/outputs/apk/release/app-release.apk --app-id $AIRBUILD_APP_ID
```

### Azure Pipelines (Windows)

```yaml
- script: |
    irm https://raw.githubusercontent.com/airbuild/cli/main/install.ps1 | iex
    airbuild login --api-key $(AIRBUILD_API_KEY)
    airbuild upload ./app/build/outputs/apk/release/app-release.apk --app-id $(AIRBUILD_APP_ID)
```

## Configuration

Config is stored at `~/.airbuild/config.json` (on Windows: `%USERPROFILE%\.airbuild\config.json`):

```json
{
  "apiKey": "airbuild_xxx",
  "apiUrl": "https://airbuild.dev",
  "orgId": "xxx",
  "orgName": "My Org"
}
```

## Cross-platform support

Pre-built binaries are available for:

- **macOS** — Apple Silicon (`darwin-arm64`) and Intel (`darwin-amd64`)
- **Linux** — x86_64 (`linux-amd64`) and ARM64 (`linux-arm64`)
- **Windows** — x86_64 (`windows-amd64`) and ARM64 (`windows-arm64`)

The CLI auto-enables ANSI colors on Windows 10+ (VT processing) and falls
back to plain text on legacy terminals. Unicode symbols are rendered using
UTF-8 code page on Windows.

## Cross-compilation

Build for all platforms:

```bash
make dist
```

Produces binaries in `dist/`:
- `airbuild-darwin-amd64`
- `airbuild-darwin-arm64`
- `airbuild-linux-amd64`
- `airbuild-linux-arm64`
- `airbuild-windows-amd64.exe`
- `airbuild-windows-arm64.exe`

## Releasing

Releases are automated via GitHub Actions. To create a new release:

```bash
git tag cli-v1.0.0
git push origin cli-v1.0.0
```

This triggers the [release workflow](.github/workflows/release.yml) which:
1. Cross-compiles binaries for all 6 platforms
2. Generates SHA-256 checksums
3. Creates a GitHub Release with all binaries attached
