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

# 2. Initialize your project (creates .airbuild.json with build paths)
airbuild init

# 3. Build your app, then push
flutter build apk --release    # or your framework's build command
airbuild push                  # uploads and prints the install link
```

### Direct upload (no project config needed)

```bash
# Upload a specific file to a specific app
airbuild upload ./build/app-release.apk --app-id app_xxx
```

## Commands

### `airbuild login`
Authenticate with your AirBuild API key. Saves credentials to `~/.airbuild/config.json`.

```bash
airbuild login --api-key airbuild_xxx
airbuild login --api-key airbuild_xxx --api-url https://staging.airbuild.dev
```

### `airbuild init`
Create a `.airbuild.json` config file for your project. Enables `airbuild push`.

```bash
airbuild init                    # Interactive setup
airbuild init --app-id app_xxx   # Link an existing app
```

The interactive flow detects your framework (Flutter, React Native, Android
native, iOS native) and suggests build output paths. You can accept or
override them.

The resulting `.airbuild.json`:

```json
{
  "appId": "clxxxx...",
  "builds": {
    "android": {
      "debug": "build/app/outputs/flutter-apk/app-debug.apk",
      "release": "build/app/outputs/flutter-apk/app-release.apk"
    },
    "ios": {
      "debug": "build/ios/ipa/app-debug.ipa",
      "release": "build/ios/ipa/app-release.ipa"
    }
  }
}
```

### `airbuild push`
Upload a build using the `.airbuild.json` config file.

```bash
airbuild push                              # Push release (auto platform)
airbuild push --platform android           # Push Android release
airbuild push --platform ios --debug       # Push iOS debug
airbuild push --all                        # Push both platforms
airbuild push --release --expiry 30        # Push with 30-day link expiry
airbuild push --json                       # JSON output for CI/CD
airbuild push --release-notes "Bug fixes"  # Include release notes
```

| Flag              | Description                                        | Default  |
| ----------------- | -------------------------------------------------- | -------- |
| `--platform`      | `android` or `ios` (required if both configured)   | auto     |
| `--release`       | Upload the release build                           | yes      |
| `--debug`         | Upload the debug build                             | no       |
| `--all`           | Upload all configured platforms                    | no       |
| `--expiry`        | Install link expiry in days (0 = plan default)     | 0        |
| `--json`          | Output results as JSON (for CI/CD)                | no       |
| `--release-notes` | Release notes for this build                       | none     |

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

### `airbuild version`
Print the current CLI version and platform info.

```bash
airbuild version
```

### `airbuild upgrade`
Check for a newer version and upgrade the CLI in place.

```bash
airbuild upgrade           # Upgrade to the latest version
airbuild upgrade --check   # Only check if an update is available
```

The command queries the GitHub Releases API, downloads the correct binary
for your OS and architecture, and atomically replaces the running binary.
No manual download or PATH changes needed.

## CI/CD integration

The CLI is designed for CI/CD pipelines. With `airbuild init` + `airbuild push`,
your pipeline just runs `airbuild push` — no file paths or app IDs to manage.

### GitHub Actions (with init + push)

```yaml
- name: Install AirBuild CLI
  run: curl -fsSL https://raw.githubusercontent.com/airbuild/cli/main/install.sh | bash

- name: Login
  run: airbuild login --api-key ${{ secrets.AIRBUILD_API_KEY }}

- name: Build
  run: flutter build apk --release

- name: Push to AirBuild
  run: airbuild push --json
```

### GitHub Actions (direct upload)

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
    - airbuild push --json
```

### Azure Pipelines (Windows)

```yaml
- script: |
    irm https://raw.githubusercontent.com/airbuild/cli/main/install.ps1 | iex
    airbuild login --api-key $(AIRBUILD_API_KEY)
    airbuild push --platform android --release
```

## Configuration

### CLI config (`~/.airbuild/config.json`)

Stores your API key and organization info. Created by `airbuild login`.

```json
{
  "apiKey": "airbuild_xxx",
  "apiUrl": "https://airbuild.dev",
  "orgId": "xxx",
  "orgName": "My Org"
}
```

### Project config (`.airbuild.json`)

Stores the app ID and build output paths for the current project. Created by
`airbuild init`. Used by `airbuild push`.

```json
{
  "appId": "clxxxx...",
  "builds": {
    "android": {
      "debug": "build/app/outputs/flutter-apk/app-debug.apk",
      "release": "build/app/outputs/flutter-apk/app-release.apk"
    }
  }
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
