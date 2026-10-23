// Package agents writes AI-assistant instruction files (AGENTS.md,
// CLAUDE.md, …) that teach coding agents how to use the AirBuild CLI
// inside a project.
package agents

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Markers delimit the AirBuild-managed block so re-running
// `airbuild init --agents` replaces it in place instead of appending a
// duplicate.
const (
	startMarker = "<!-- airbuild:start -->"
	endMarker   = "<!-- airbuild:end -->"
)

// Files are the agent instruction files we maintain. AGENTS.md is the
// emerging cross-tool standard (Claude Code, Cursor, Devin, Codex, Jules
// all read it); CLAUDE.md is kept in sync for tools that only read it.
var Files = []string{"AGENTS.md", "CLAUDE.md"}

// DefaultServer is the hosted AirBuild origin, used when the caller
// doesn't have a configured API URL.
const DefaultServer = "https://airbuild.dev"

// block builds the instruction text written between the markers. Keep it
// task-oriented and terse — agents follow it best when it reads like
// runbook steps, not prose. serverBase is the AirBuild origin the CLI is
// configured against (config.apiUrl) so self-hosted deployments produce
// correct install/spec URLs instead of hardcoding airbuild.dev.
func block(serverBase string) string {
	base := strings.TrimSuffix(serverBase, "/")
	if base == "" {
		base = DefaultServer
	}
	return startMarker + `
## AirBuild

This project ships mobile builds to AirBuild (OTA distribution: APK/IPA
uploads, install links, and CodePush OTA patches for Flutter / React Native).
Server: ` + base + `

### Setup

- CLI: https://docs.airbuild.dev/developer/cli — install once, then
  ` + "`airbuild login --api-key $AIRBUILD_API_KEY`" + ` or just export the key.
- Auth: ` + "`AIRBUILD_API_KEY`" + ` env var (Dashboard → Settings → API Keys).
  NEVER commit it, ` + "`~/.airbuild/config.json`" + `, or any ` + "`airbuild_*`" + ` key.
- Project config lives in ` + "`.airbuild.json`" + ` (app ID + build paths,
  created by ` + "`airbuild init`" + `; use ` + "`--yes`" + ` non-interactively).
- Verify environment before shipping: ` + "`airbuild codepush flutter doctor`" + `.

### In-app SDKs (update prompts inside the app)

When the task is to check for updates inside the app itself, use the
AirBuild SDK — it shows testers a newer-build dialog and reports install
analytics. Auth is a PUBLIC distribution key (` + "`dk_xxx`" + `, from the app's
Settings page — safe to embed; NOT the ` + "`airbuild_*`" + ` API key).

- Flutter: ` + "`flutter pub add airbuild`" + ` → ` + "`AirBuild.configure(distributionKey: 'dk_…', baseUrl: '" + base + "')`" + `,
  then ` + "`await AirBuild.checkForUpdates(currentVersion: 'x.y.z')`" + `.
- React Native: ` + "`npm i @airbuild/react-native`" + ` → ` + "`AirBuild.configure({ distributionKey: 'dk_…', baseUrl: '" + base + "' })`" + `,
  then ` + "`await AirBuild.checkForUpdates({ currentVersion: 'x.y.z' })`" + `.
- iOS: pod ` + "`AirBuild`" + ` or SPM → ` + "`AirBuild.configure(distributionKey: 'dk_…', baseUrl: '" + base + "')`" + `.
- Android: Gradle dep ` + "`dev.airbuild:airbuild-android`" + ` →
  ` + "`AirBuild.configure(distributionKey = \"dk_…\", baseUrl = \"" + base + "\")`" + `, then ` + "`AirBuild.checkForUpdates(context, version)`" + `.
- Version rules: a new build is an update when its version name is newer,
  or the version matches and its build number (versionCode) is higher.
  "Latest" = most recently uploaded READY build — upload in version order.
- Full guide: https://docs.airbuild.dev/developer/sdks/
- Source repos: github.com/airbuild/{airbuild-cli, airbuild-flutter,
  airbuild-react-native, airbuild-ios, airbuild-android} — file SDK bugs
  against the matching repo.

### CodePush (OTA patches, no re-install)

Ships Dart/JS-only fixes on top of an installed build. NOT for native
code, assets, manifest, or dependency changes — those need a new binary.

- One-time per project: ` + "`airbuild codepush flutter init`" + ` writes
  ` + "`shorebird.yaml`" + ` (app_id, base_url, auto_update). Toolchain:
  ` + "`airbuild codepush flutter install`" + ` → ~/.shorebird.
- Patches only apply to builds made with ` + "`codepush flutter release`" + `
  (embeds the updater runtime) — plain ` + "`flutter build`" + ` APKs can't patch.
- Flutter in-app: ` + "`auto_update: false`" + ` in shorebird.yaml +
  ` + "`AirBuildCodePush()`" + ` from package:airbuild → ` + "`checkForUpdate()`" + `,
  ` + "`update()`" + `, ` + "`currentPatch()`" + ` (patch number for "About" UIs),
  or wrap the app in ` + "`AirBuildCodePushOverlay`" + ` for banner + restart UX.
  Patches apply on next full app restart; ` + "`defaultChannel`" + ` picks the
  rollout channel (boot auto-update always checks production).
- React Native: ` + "`airbuild codepush react-native init|publish|promote`" + ` —
  implements the Expo Updates protocol end-to-end.

### Rules for agents

- Always pass ` + "`--json`" + ` to ` + "`airbuild`" + ` commands and parse stdout — progress and
  errors go to stderr, and failures exit non-zero.
- Ship a build: ` + "`airbuild push --json`" + ` (uses .airbuild.json), or
  ` + "`airbuild upload <file.apk|.ipa> --app-id <id> --json`" + `.
- New install link: ` + "`airbuild links create --build-id <id> --json`" + ` → the
  install URL is ` + "`" + base + "/i/<slug>`" + `.
- Inspect state: ` + "`airbuild apps list --json`" + `,
  ` + "`airbuild builds list --app-id <id> --json`" + ` — poll builds until
  ` + "`status`" + ` is ` + "`READY`" + ` before sharing a link.
- CodePush Flutter: ` + "`airbuild codepush flutter release|patch|promote|rollback|status`" + `.
  React Native: ` + "`airbuild codepush react-native publish|promote|rollback|status`" + `.
- Prefer staged rollout: ` + "`--rollout 10`" + `, verify, then 50, then 100. Ask the
  user before promoting a patch to 100% or rolling back.
- Docs: https://docs.airbuild.dev (agent index: /llms.txt) — OpenAPI spec:
  ` + base + `/api/openapi.
` + endMarker
}

// WriteAll upserts the AirBuild block into every file in Files inside dir.
// Existing content is preserved — the block is appended when a file has no
// markers, replaced in place when it does. Returns the paths written.
func WriteAll(dir, serverBase string) ([]string, error) {
	var written []string
	for _, name := range Files {
		path := filepath.Join(dir, name)
		if err := upsert(path, serverBase); err != nil {
			return written, fmt.Errorf("writing %s: %w", name, err)
		}
		written = append(written, path)
	}
	return written, nil
}

func upsert(path, serverBase string) error {
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	content := string(existing)
	switch {
	case err != nil:
		// New file.
		content = "# Project — AirBuild setup\n\n" + block(serverBase) + "\n"
	case strings.Contains(content, startMarker) && strings.Contains(content, endMarker):
		// Replace the existing block in place.
		start := strings.Index(content, startMarker)
		end := strings.Index(content, endMarker) + len(endMarker)
		content = content[:start] + block(serverBase) + content[end:]
	default:
		// Append after existing content.
		sep := "\n\n"
		if strings.HasSuffix(content, "\n") {
			sep = "\n"
		}
		content = content + sep + block(serverBase) + "\n"
	}

	return os.WriteFile(path, []byte(content), 0o644)
}
