---
name: airbuild-codepush
description: Use when shipping an over-the-air (OTA/CodePush) update to an installed Flutter or React Native app without a store release — publishing or patching, staged rollout, promote, rollback, checking update status, or setting up AirBuild CodePush. Covers Android and iOS. Not for uploading new binaries (use airbuild-distribution).
---
<!-- airbuild:skill v=__VERSION__ -->
# AirBuild CodePush (OTA)

1. Run `airbuild guide codepush` (shared rules: patch vs new binary, lifecycle,
   channels, rollout, auto-pause).
2. Then run the framework runbook: `airbuild guide flutter` or
   `airbuild guide react-native`. The CLI generates them, so they always match
   the installed version.

If `airbuild` is missing: install it from https://docs.airbuild.dev/developer/cli,
then export `AIRBUILD_API_KEY` (never print or commit it).

Minimal fallback:
- Start with `airbuild codepush <flutter|react-native> doctor --json`.
- Patch/publish → DRAFT at 0% → `promote --rollout 10` → check `status` →
  50 → 100. Ask the user before 100% or any rollback.
- Only Dart/JS changes can be patched; native code and assets need a new build.
- Always pass `--json`.
