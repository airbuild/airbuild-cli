# AirBuild CodePush (OTA) runbook — shared rules

Ships Dart (Flutter) or JS (React Native) fixes to installed apps with no store
review. Server: __SERVER__. Pass `--json` to every command.

Framework runbooks (read the one you need):
- `airbuild guide flutter` — Flutter, Android + iOS
- `airbuild guide react-native` — React Native / Expo Updates, Android + iOS

## Patch or new binary?

If `git diff` touches anything outside Dart/JS code — native code (Kotlin, Java,
Swift, ObjC), permissions, AndroidManifest/Info.plist, assets or fonts, Flutter/
engine upgrades, native plugin changes — ship a NEW store build instead. Patches
can't deliver those (the CLI warns, but the patch still uploads).

## Prerequisites

- OTA enabled for the org (feature flags `codepush_flutter` /
  `codepush_react_native`; disabled = 403 on every endpoint) and enabled on the
  app: dashboard → app → Enable OTA Updates → pick the framework. That issues
  the distribution key.
- `airbuild login` / `AIRBUILD_API_KEY` and `airbuild init` first (`.airbuild.json`
  lets commands skip `--app`).
- Plans meter OTA publishes, unique devices (MAU) and patch downloads per
  month; Free has a small quota. Hitting a limit returns 402/403 — report it.
- Start with `airbuild codepush <framework> doctor` (read-only; non-zero exit if
  anything is missing; safe in CI). `install` and `init` change the project and
  prompt unless you pass `--yes`.

## Lifecycle

1. Base build registered with AirBuild (Flutter `release`; React Native: the
   store build that already contains `expo-updates`).
2. `patch` (Flutter) / `publish` (React Native) → lands as a DRAFT at 0% rollout
   — nothing reaches devices yet.
3. `promote --rollout N` activates it on a channel.
4. `status` to watch checks, downloads, installs and failures.
5. `rollback` if bad. Rollback is device-side: installed devices revert on their
   next launch.

Each platform (android, ios) is its own patch/publish and its own promote.

## Channels and staged rollout

- Default channel `production`; apps also get `staging`. Promoting to a new name
  creates a channel.
- Roll out in steps: `--rollout 10`, check `status`, then 50, then 100.
  Re-promoting the same update only changes the percentage. Devices are
  bucketed deterministically, so ramping up only adds devices.
- ASK THE USER before going to 100% or rolling back.

## Safety nets

- Patches are RSA-signed; devices verify hash + signature.
- Auto-pause: at ≥30% failure across ≥10 install reports the update stops being
  served (downloads 410) and webhook `codepush.update_auto_paused` fires;
  `status` shows it as auto-paused; re-promoting resumes/resets it.
- Webhooks: `codepush.release_created`, `update_published`, `update_promoted`,
  `update_rolled_back`, `update_auto_paused`.
- Docs: https://docs.airbuild.dev/guides/codepush/
