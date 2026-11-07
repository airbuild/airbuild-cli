## AirBuild

This project ships mobile builds to AirBuild — OTA app distribution for iOS and
Android (APK/IPA uploads, install links + QR codes, iOS ad-hoc UDID capture) and
CodePush OTA patches for Flutter and React Native. Everything is scriptable via
the `airbuild` CLI. Server: __SERVER__

### Skills — load the one that matches the task

- `airbuild-distribution` — upload builds, install links/QR, iOS devices & UDIDs,
  in-app update SDKs, CI uploads, webhooks.
- `airbuild-codepush` — OTA hotfixes: patch, rollout, rollback for Flutter and
  React Native.

Each skill tells you to run `airbuild guide <topic>` for the full runbook; the
CLI generates it, so it always matches the installed version. No skill support?
Run it yourself: `airbuild guide distribution|codepush|flutter|react-native`.

### Always

- Auth: `AIRBUILD_API_KEY` env var (Dashboard → Settings → API Keys), or
  `airbuild login --api-key $AIRBUILD_API_KEY`. NEVER print or commit it,
  `~/.airbuild/config.json`, or any `airbuild_*` key (`dk_*` keys are public).
- Project config is `.airbuild.json` (created by `airbuild init --yes`); commands
  default `--app` from it.
- Always pass `--json` to `airbuild` commands and parse stdout — progress and
  errors go to stderr, and failures exit non-zero. A 402/403 means a plan limit
  or disabled feature: report it, don't retry blindly.
- Ask the user before: promoting an OTA update to 100%, rolling back, shipping
  to production channels, or changing shared install links.
- Docs: https://docs.airbuild.dev (agent index: /llms.txt) — OpenAPI spec:
  __SERVER__/api/openapi.
