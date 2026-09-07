# AirBuild CLI

A standalone command-line tool for uploading builds to AirBuild and managing
your apps, builds, and install links — no Node.js or npm required.

## Install

### One-liner (macOS & Linux)

```bash
curl -fsSL https://raw.githubusercontent.com/airbuild/airbuild-cli/main/install.sh | bash
```

### Windows (PowerShell)

```powershell
irm https://raw.githubusercontent.com/airbuild/airbuild-cli/main/install.ps1 | iex
```

### From binary (manual)

Download the latest binary for your platform from
[GitHub Releases](https://github.com/airbuild/airbuild-cli/releases):

```bash
# macOS (Apple Silicon)
curl -L https://github.com/airbuild/airbuild-cli/releases/latest/download/airbuild-darwin-arm64 -o airbuild
chmod +x airbuild && sudo mv airbuild /usr/local/bin/

# macOS (Intel)
curl -L https://github.com/airbuild/airbuild-cli/releases/latest/download/airbuild-darwin-amd64 -o airbuild
chmod +x airbuild && sudo mv airbuild /usr/local/bin/

# Linux (x86_64)
curl -L https://github.com/airbuild/airbuild-cli/releases/latest/download/airbuild-linux-amd64 -o airbuild
chmod +x airbuild && sudo mv airbuild /usr/local/bin/

# Linux (ARM64)
curl -L https://github.com/airbuild/airbuild-cli/releases/latest/download/airbuild-linux-arm64 -o airbuild
chmod +x airbuild && sudo mv airbuild /usr/local/bin/
```

```powershell
# Windows (PowerShell)
$dir = "$env:LOCALAPPDATA\AirBuild"
New-Item -ItemType Directory -Path $dir -Force
Invoke-WebRequest "https://github.com/airbuild/airbuild-cli/releases/latest/download/airbuild-windows-amd64.exe" -OutFile "$dir\airbuild.exe"
# Add to PATH:
[Environment]::SetEnvironmentVariable("Path", "$env:Path;$dir", "User")
```

### From source

```bash
go install github.com/airbuild/airbuild-cli@latest
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

## CodePush / OTA updates

Push code-only updates to your apps without a store re-submission. AirBuild
CodePush supports **Flutter** (via Shorebird's updater) and **React Native**
(via the Expo Updates protocol) with independent feature flags, channels,
staged rollout, and instant rollback.

> **Feature flag:** CodePush must be enabled for your organization by an
> admin (Admin → Feature Flags → `codepush_flutter` / `codepush_react_native`).
> All CodePush endpoints return `403` when the flag is disabled.

### Flutter CodePush

Wraps the [Shorebird CLI](https://pub.dev/packages/shorebird_cli) — install
it first with `dart pub global activate shorebird_cli`.

#### `airbuild codepush flutter release`

Register a Flutter release (runs `shorebird release`, then uploads the artifact).

```bash
airbuild codepush flutter release android \
  --app app_xxx \
  --version 1.0.0+1 \
  --channel production
```

| Flag | Required | Default | Description |
| ---- | -------- | ------- | ----------- |
| `--app` | yes | — | App ID |
| `--version` | yes | — | App version, e.g. `1.0.0+1` |
| `--architecture` | no | auto | Target architecture, e.g. `arm64-v8a` |
| `--channel` | no | `production` | Distribution channel |
| `--flutter-revision` | no | — | Flutter SDK version used |
| `--shorebird-app-id` | no | — | Shorebird app_id |
| `--release-notes` | no | — | Release notes |
| `--artifact` | no | auto-detect | Path to libapp.so (skips build) |
| `--skip-build` | no | `false` | Don't run `shorebird release` |

#### `airbuild codepush flutter patch`

Create a Flutter patch (runs `shorebird patch`, then uploads the diff).

```bash
shorebird patch android --no-confirm
airbuild codepush flutter patch android \
  --app app_xxx \
  --release-version 1.0.0+1 \
  --artifact path/to/patch.diff \
  --release-notes "Fixed login crash"
```

| Flag | Required | Default | Description |
| ---- | -------- | ------- | ----------- |
| `--app` | yes | — | App ID |
| `--release-version` | yes | — | Release version this patch targets |
| `--architecture` | no | — | Target architecture |
| `--channel` | no | `production` | Distribution channel |
| `--release-notes` | no | — | Patch notes |
| `--artifact` | yes | — | Path to the patch diff file |
| `--skip-build` | no | `false` | Don't run `shorebird patch` |

#### `airbuild codepush flutter promote`

Promote a patch to a channel at a rollout percentage.

```bash
airbuild codepush flutter promote \
  --app app_xxx \
  --patch 1 \
  --channel production \
  --rollout 25
```

| Flag | Required | Default | Description |
| ---- | -------- | ------- | ----------- |
| `--app` | yes | — | App ID |
| `--update-id` | no | — | Update ID (alternative to patch number) |
| `--release-version` | no | — | Release version (with `--platform` + `--patch`) |
| `--platform` | no | — | `ANDROID` or `IOS` |
| `--patch` | no | — | Patch number (from `status`) |
| `--channel` | no | `production` | Channel to promote to |
| `--rollout` | no | `100` | Rollout percentage (0–100) |

#### `airbuild codepush flutter rollback`

Rollback a patch — devices revert on next check-in.

```bash
airbuild codepush flutter rollback --app app_xxx --patch 1
```

| Flag | Required | Default | Description |
| ---- | -------- | ------- | ----------- |
| `--app` | yes | — | App ID |
| `--update-id` | no | — | Update ID (alternative to patch number) |
| `--release-version` | no | — | Release version |
| `--platform` | no | — | `ANDROID` or `IOS` |
| `--patch` | no | — | Patch number |
| `--channel` | no | `production` | Channel |

#### `airbuild codepush flutter status`

Show all releases and patches for an app.

```bash
airbuild codepush flutter status --app app_xxx
```

| Flag | Required | Default | Description |
| ---- | -------- | ------- | ----------- |
| `--app` | yes | — | App ID |

### React Native CodePush

Uses the [Expo Updates v1 protocol](https://docs.expo.dev/technical-specs/expo-updates-1/).
Requires `expo-updates` in your app and `npx` on your PATH.

#### `airbuild codepush react-native publish`

Publish an update (runs `npx expo export`, then uploads bundle + assets).

```bash
airbuild codepush react-native publish \
  --app app_xxx \
  --platform android \
  --runtime-version 1.0.0 \
  --release-notes "Fixed navigation bug"
```

| Flag | Required | Default | Description |
| ---- | -------- | ------- | ----------- |
| `--app` | yes | — | App ID |
| `--platform` | yes | — | `android` or `ios` |
| `--runtime-version` | yes | — | Must match `expo.updates.runtimeVersion` |
| `--channel` | no | `production` | Distribution channel |
| `--release-notes` | no | — | Release notes |
| `--output-dir` | no | `dist` | Export directory |
| `--skip-export` | no | `false` | Don't run `expo export` — read `--output-dir` |

#### `airbuild codepush react-native promote`

Promote an update to a channel at a rollout percentage.

```bash
airbuild codepush react-native promote \
  --app app_xxx \
  --update-id update_xxx \
  --channel production \
  --rollout 25
```

| Flag | Required | Default | Description |
| ---- | -------- | ------- | ----------- |
| `--app` | yes | — | App ID |
| `--update-id` | no | — | Update ID (alternative to `--platform` + `--runtime-version`) |
| `--platform` | no | — | `ANDROID` or `IOS` |
| `--runtime-version` | no | — | Runtime version |
| `--channel` | no | `production` | Channel to promote to |
| `--rollout` | no | `100` | Rollout percentage (0–100) |

#### `airbuild codepush react-native rollback`

Rollback an update — devices revert on next check-in.

```bash
airbuild codepush react-native rollback --app app_xxx --update-id update_xxx
```

| Flag | Required | Default | Description |
| ---- | -------- | ------- | ----------- |
| `--app` | yes | — | App ID |
| `--update-id` | yes | — | Update ID |

#### `airbuild codepush react-native status`

Show all releases and updates for an app.

```bash
airbuild codepush react-native status --app app_xxx
```

| Flag | Required | Default | Description |
| ---- | -------- | ------- | ----------- |
| `--app` | yes | — | App ID |

### Typical workflow

```bash
# Flutter
airbuild codepush flutter release android --app app_xxx --version 1.0.0+1
# ...fix a Dart bug...
airbuild codepush flutter patch android --app app_xxx --release-version 1.0.0+1 --artifact patch.diff
airbuild codepush flutter promote --app app_xxx --patch 1 --channel production --rollout 25
airbuild codepush flutter rollback --app app_xxx --patch 1

# React Native
airbuild codepush react-native publish --app app_xxx --platform android --runtime-version 1.0.0
airbuild codepush react-native promote --app app_xxx --update-id update_xxx --rollout 25
airbuild codepush react-native rollback --app app_xxx --update-id update_xxx
```

See the [CodePush documentation](https://docs.airbuild.dev/guides/codepush/) for
setup instructions, device-side configuration, and CI/CD examples.

## CI/CD integration

The CLI is designed for CI/CD pipelines. With `airbuild init` + `airbuild push`,
your pipeline just runs `airbuild push` — no file paths or app IDs to manage.

### GitHub Actions (with init + push)

```yaml
- name: Install AirBuild CLI
  run: curl -fsSL https://raw.githubusercontent.com/airbuild/airbuild-cli/main/install.sh | bash

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
    curl -fsSL https://raw.githubusercontent.com/airbuild/airbuild-cli/main/install.sh | bash

- name: Login
  run: airbuild login --api-key ${{ secrets.AIRBUILD_API_KEY }}

- name: Upload build
  run: airbuild upload ./app/build/outputs/apk/release/app-release.apk --app-id ${{ secrets.AIRBUILD_APP_ID }}
```

### GitLab CI

```yaml
upload:
  script:
    - curl -fsSL https://raw.githubusercontent.com/airbuild/airbuild-cli/main/install.sh | bash
    - airbuild login --api-key $AIRBUILD_API_KEY
    - airbuild push --json
```

### Azure Pipelines (Windows)

```yaml
- script: |
    irm https://raw.githubusercontent.com/airbuild/airbuild-cli/main/install.ps1 | iex
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

### Windows Defender false positive

Go binaries are not code-signed by default, which can cause Windows Defender
or other antivirus software to flag them as suspicious. This is a **false
positive** — the AirBuild CLI is open source and contains no malicious code.

To resolve:

1. **Verify the checksum** — compare the SHA-256 of your downloaded binary
   with `checksums.txt` from the [release page](https://github.com/airbuild/airbuild-cli/releases).
2. **Add an exclusion** — in Windows Security > Virus & threat protection >
   Manage settings > Add or remove exclusions, add the `airbuild.exe` path.
3. **Build from source** — if you prefer, build from source with Go:
   ```bash
   go install github.com/airbuild/airbuild-cli@latest
   ```

We are working on code signing for future releases to eliminate this issue.

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
