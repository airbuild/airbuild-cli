# AirBuild CodePush — Flutter runbook (Android + iOS)

Server: __SERVER__. Shared rules: `airbuild guide codepush`. Always `--json`.

## One-time setup

1. `airbuild codepush flutter doctor` — checks dart, flutter, toolchain,
   `shorebird.yaml`, `.airbuild.json`, API key, API reachability.
2. `airbuild codepush flutter install` — installs the build toolchain into
   `~/.shorebird` (no Shorebird account; everything runs locally).
3. `airbuild codepush flutter init` — writes `shorebird.yaml` (app_id, base_url,
   distribution_key, channel, auto_update) and adds it to pubspec assets.
Android builds need Java 17+; iOS builds need macOS + Xcode.

## Release (base build — do this once per shipped version)

`airbuild codepush flutter release <android|ios> --version 1.0.0+1`
- `--format apk|aab` (default apk) · `--architecture arm64-v8a` (default: all
  ABIs detected) · `--channel` · `--release-notes` ·
  `--dart-define K=V` / `--dart-define-from-file f.json` (repeatable) ·
  `--artifact <path> --skip-build` (upload a pre-built artifact) ·
  `--flutter-revision`.
- iOS: builds a signed `.app`. Install THAT exact build on devices — a build
  from a different compile can never be patched. For CI that signs later use
  `--no-codesign` and re-sign the same `.app`; never recompile.
- Patches only apply to builds made by `codepush flutter release` (it embeds the
  updater). A plain `flutter build` can't be patched.

## Patch

`airbuild codepush flutter patch <android|ios> --release-version 1.0.0+1`
- Rebuilds, diffs against the release, uploads as a DRAFT at 0%.
- Android diffs every ABI into ONE patch. Same `--dart-define*` flags; the CLI
  warns if defines drift from the release (defines ARE patchable).
- Also `--architecture`, `--channel`, `--release-notes`, `--skip-build`,
  `--artifact <diff> --artifact-sha256 <hash>`.
- Read the CLI's warnings about native/asset/engine differences: those parts
  will NOT reach devices — ship a new binary instead.

## Promote, roll back, inspect

- `airbuild codepush flutter promote --release-version 1.0.0+1 --platform android --patch 1 --channel production --rollout 10`
  (or `--update-id <id>`). Default rollout is 100 — always pass `--rollout`.
- `airbuild codepush flutter rollback --release-version 1.0.0+1 --platform android --patch 1`
  (or `--update-id <id>`).
- `airbuild codepush flutter status` — releases, patches, channels, rollout %,
  check-ins/downloads/installs/failures per patch. Patch numbers come from here.

## Channels from the device

`channel:` in shorebird.yaml is stripped at build time: automatic (boot-time)
updates always come from `production`. To test `staging` on a device set
`auto_update: false` and check manually:

- `final codePush = AirBuildCodePush(defaultChannel: 'staging');`
  `if (await codePush.checkForUpdate() == UpdateStatus.outdated) await codePush.update();`
  `await codePush.currentPatch();` (patch number for About screens)
- Or wrap the app in `AirBuildCodePushOverlay(config: AirBuildCodePushConfig(channel: 'staging', onRestart: …), child: …)`
  for check-on-launch/resume → download → restart banner.
- `disableOnPlayStoreInstalls: true` skips patch checks on Play-installed devices.
- Patches apply on the NEXT full app restart.

## Gotchas

- Rebuild after a signing key change: `cd android && ./gradlew --stop` first
  (Gradle daemon caches `SHOREBIRD_PUBLIC_KEY`).
- Multi-ABI: devices get the diff matching their architecture automatically.
- Docs: https://docs.airbuild.dev/guides/codepush-flutter/
