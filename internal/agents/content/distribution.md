# AirBuild distribution runbook

Server: __SERVER__ — all commands below take `--json` (parse stdout; logs and
errors go to stderr; failures exit non-zero).

## What you can do

- Upload an .ipa/.apk and share an install page + QR code. iOS installs over
  the air (itms-services); Android downloads the APK directly.
- Links can be password-protected, expire by date or install count, and be
  revoked or regenerated without re-uploading. Per-app/per-build install
  analytics and CSV export are in the dashboard.
- iOS ad-hoc: capture tester device UDIDs automatically and register them on the
  Apple Developer account.
- In-app update prompts through SDKs (Flutter, React Native, iOS, Android).
- Teams (owner/admin/developer/tester/guest), audit logs, webhooks, REST API.

## Ship a build

1. Build the app with the framework's normal command (agent runs it).
2. `airbuild push --json` — uploads the paths in `.airbuild.json`
   (release by default). Flags: `--platform android|ios`, `--all`, `--debug`,
   `--release-notes "…"`.
   Or: `airbuild upload <file.apk|.ipa> --app-id <id> [--platform IOS|ANDROID] [--release-notes "…"] --json`.
3. `airbuild builds list --app-id <id> --json` — poll until the build `status`
   is `READY` before sharing anything.
4. `airbuild links create --build-id <id> --json` → install URL is
   `__SERVER__/i/<slug>`. `airbuild links list --app-id <id>` lists existing ones.
5. Password / expiry / install caps are not CLI flags: set them in the dashboard
   or via the API, e.g. `PATCH /api/builds/{buildId}/password` with
   `{"password":"…"}` and `Authorization: Bearer $AIRBUILD_API_KEY`.

Setup: `airbuild init --yes [--app-id <id>]` writes `.airbuild.json` (app ID +
build output paths). Other commands: `airbuild apps list`, `airbuild config
show|set`, `airbuild version`, `airbuild upgrade [--check] [--yes]`.

## iOS ad-hoc devices and UDIDs

- An ad-hoc .ipa only installs on devices whose UDID is in its provisioning
  profile. Testers get a device invite (dashboard → app → Devices), install a
  .mobileconfig profile, and the UDID lands in the app's device registry.
- With an Apple Developer (App Store Connect) API key connected in Settings,
  captured UDIDs register on the Apple account automatically (paid plans);
  otherwise register them manually in the Apple portal.
- Registering a UDID does NOT add it to a provisioning profile. After new
  devices register: update the profile, rebuild/re-sign, re-upload, then share
  the new link.
- Docs: https://docs.airbuild.dev/guides/udid-capture/ and
  /guides/apple-developer-integration/.

## In-app SDKs (update prompts inside the app)

Use an SDK when the app itself should detect and offer a newer build. Auth is a
PUBLIC distribution key (`dk_xxx`, app Settings page — safe to embed; NOT the
`airbuild_*` API key).

- Flutter: `flutter pub add airbuild` → `AirBuild.configure(distributionKey: 'dk_…', baseUrl: '__SERVER__')`,
  then `await AirBuild.checkForUpdates(currentVersion: 'x.y.z')`.
- React Native: `npm i @airbuild/react-native` → `AirBuild.configure({ distributionKey: 'dk_…', baseUrl: '__SERVER__' })`,
  then `await AirBuild.checkForUpdates({ currentVersion: 'x.y.z' })`.
- iOS: pod `AirBuild` or SPM → `AirBuild.configure(distributionKey: 'dk_…', baseUrl: '__SERVER__')`.
- Android: Gradle dep `dev.airbuild:airbuild-android` →
  `AirBuild.configure(distributionKey = "dk_…", baseUrl = "__SERVER__")`, then `AirBuild.checkForUpdates(context, version)`.
- A new build is an update when its version name is newer, or the version
  matches and its build number (versionCode) is higher. "Latest" = most recently
  uploaded READY build — upload in version order.
- Guide: https://docs.airbuild.dev/developer/sdks/ · repos:
  github.com/airbuild/{airbuild-cli, airbuild-flutter, airbuild-react-native,
  airbuild-ios, airbuild-android} — file SDK bugs against the matching repo.

## CI, webhooks, API

- CI: export `AIRBUILD_API_KEY` (no `login` needed), build, `airbuild push --json`
  (or `upload`), read `installUrl` from the JSON.
- Webhooks (HMAC-SHA256 signed): `build.uploaded`, `build.deleted`,
  `install_link.created|revoked|expired`, `download.started`, member and
  billing events. https://docs.airbuild.dev/developer/webhooks/
- REST API + OpenAPI spec: __SERVER__/api/openapi ·
  https://docs.airbuild.dev/developer/api-reference/

## Guardrails

- Never print or commit `airbuild_*` keys. Ask before revoking, regenerating or
  publicly sharing install links for non-test builds.
- 402/403 = plan limit or disabled feature (build size, apps, downloads/month):
  report it to the user.
- Need OTA code patches instead of a new binary? Run `airbuild guide codepush`.
