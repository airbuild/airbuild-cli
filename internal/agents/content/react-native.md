# AirBuild CodePush — React Native runbook (Android + iOS)

Server: __SERVER__. Shared rules: `airbuild guide codepush`. Always `--json`.
Uses the open Expo Updates v1 protocol with the standard `expo-updates` client
(Expo-managed and bare apps). No AirBuild runtime SDK is required.

## One-time setup

1. `airbuild codepush react-native doctor` — checks node/npx, `expo-updates`,
   app.json/app.config manifest URL, `ios.bundleIdentifier`, the telemetry
   module, `.airbuild.json`, API key, API reachability.
2. `airbuild codepush react-native install` — installs `expo-updates` (via
   `npx expo install` for Expo, `npm install` for bare) and generates
   `airbuild-telemetry.js`.
3. `airbuild codepush react-native init` — sets `expo.updates.url` (with the
   distribution key) and `expo.updates.runtimeVersion` in app.json; derives
   `ios.bundleIdentifier` if missing. For `app.config.js/ts` it prints a snippet
   to paste. Add `--yes` for non-interactive use.
4. Import telemetry ONCE, early in the entry file (`App.js`, `index.js` or
   `app/_layout.tsx`): `import './airbuild-telemetry';` — it powers install
   counts, crash-free rate and auto-pause.
5. Ship ONE store build that contains `expo-updates`. Devices on older binaries
   receive nothing. A new app version = a new runtime version.

## Publish

`airbuild codepush react-native publish --platform android|ios --runtime-version 1.0.0`
- `--runtime-version` MUST equal `expo.updates.runtimeVersion` in app.json
  (with `policy: appVersion` it is the app version string).
- Runs `npx expo export --output-dir dist --platform <p>` then uploads the bundle
  + assets; the update lands as a DRAFT at 0%.
- Flags: `--channel production` · `--release-notes "…"` · `--output-dir dist` ·
  `--skip-export` (upload an already exported `--output-dir`).
- Publish once PER platform (android, then ios). Needs Node + npx.

## Promote, roll back, inspect

- `airbuild codepush react-native promote --platform android --runtime-version 1.0.0 --channel production --rollout 10`
  (or `--update-id <id>`). Default rollout is 100 — always pass `--rollout`.
- `airbuild codepush react-native rollback --update-id <id>` (id required).
- `airbuild codepush react-native status` — releases/updates, rollout %, installs,
  failures; auto-paused updates show as `ACTIVE (auto-paused)`.

## How devices behave

- Channel is the `expo-channel-name` request header in app.json
  (default `production`).
- The app checks on launch (`checkAutomatically: ON_LOAD`), downloads, and
  loads the new bundle on the NEXT launch.
- Devices are counted by `airbuild-device-id` (MAU). The telemetry module sets a
  persistent id; without it counting falls back to IP and is approximate.
- A startup crash that kills the app before it can report is detected on the
  next launch and counted as a failure.

## Gotchas

- Runtime version mismatch = update never applies (most common failure).
- Expo Go / dev builds have updates disabled; telemetry no-ops there.
- Docs: https://docs.airbuild.dev/guides/codepush-react-native/
