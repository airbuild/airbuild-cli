# AirBuild CLI

A standalone command-line tool for uploading builds to AirBuild and managing
your apps, builds, and install links — no Node.js or npm required.

## Install

### From binary (recommended)

Download the latest binary for your platform from
[GitHub Releases](https://github.com/airbuild/airbuild/releases):

```bash
# macOS (Apple Silicon)
curl -L https://github.com/airbuild/airbuild/releases/latest/download/airbuild-darwin-arm64 -o airbuild
chmod +x airbuild && sudo mv airbuild /usr/local/bin/

# macOS (Intel)
curl -L https://github.com/airbuild/airbuild/releases/latest/download/airbuild-darwin-amd64 -o airbuild
chmod +x airbuild && sudo mv airbuild /usr/local/bin/

# Linux
curl -L https://github.com/airbuild/airbuild/releases/latest/download/airbuild-linux-amd64 -o airbuild
chmod +x airbuild && sudo mv airbuild /usr/local/bin/
```

### From source

```bash
go install github.com/airbuild/cli@latest
```

### Build from this repo

```bash
cd packages/cli
make build
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
- name: Upload to AirBuild
  run: |
    curl -L https://github.com/airbuild/airbuild/releases/latest/download/airbuild-linux-amd64 -o airbuild
    chmod +x airbuild
    ./airbuild config set --api-key ${{ secrets.AIRBUILD_API_KEY }}
    ./airbuild upload ./build/app-release.apk --app-id ${{ secrets.AIRBUILD_APP_ID }}
```

### GitLab CI

```yaml
upload:
  script:
    - curl -L https://github.com/airbuild/airbuild/releases/latest/download/airbuild-linux-amd64 -o airbuild
    - chmod +x airbuild
    - ./airbuild config set --api-key $AIRBUILD_API_KEY
    - ./airbuild upload ./build/app-release.apk --app-id $AIRBUILD_APP_ID
```

## Configuration

Config is stored at `~/.airbuild/config.json`:

```json
{
  "apiKey": "airbuild_xxx",
  "apiUrl": "https://airbuild.dev",
  "orgId": "xxx",
  "orgName": "My Org"
}
```

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
