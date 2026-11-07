---
name: airbuild-distribution
description: Use when uploading iOS/Android builds to AirBuild, creating install links or QR codes, distributing to testers, capturing iOS device UDIDs for ad-hoc builds, adding in-app update-check SDKs, sending builds from CI, or wiring AirBuild webhooks. Not for OTA code patches — use airbuild-codepush for those.
---
<!-- airbuild:skill v=__VERSION__ -->
# AirBuild distribution

Run `airbuild guide distribution` and follow its output. The CLI generates it,
so it always matches the installed version.

If `airbuild` is missing: install it from https://docs.airbuild.dev/developer/cli,
then export `AIRBUILD_API_KEY` (never print or commit it).

Minimal fallback (full runbook has the details):
- `airbuild push --json` uploads the build paths in `.airbuild.json`;
  `airbuild upload <file> --app-id <id> --json` uploads one file.
- Poll `airbuild builds list --app-id <id> --json` until `READY`, then
  `airbuild links create --build-id <id> --json` → `<server>/i/<slug>`.
- Always pass `--json`. A 402/403 means a plan limit — report it.
