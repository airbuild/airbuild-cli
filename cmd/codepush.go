package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/airbuild/airbuild-cli/internal/api"
	"github.com/airbuild/airbuild-cli/internal/project"
	"github.com/airbuild/airbuild-cli/internal/ui"
	"github.com/spf13/cobra"
)

// codepushCmd is the root `airbuild codepush` command tree.
var codepushCmd = &cobra.Command{
	Use:   "codepush",
	Short: "Push OTA code updates (React Native bundles, Flutter patches)",
}

// codepushFlutterCmd groups the Flutter-specific subcommands, which drive
// Shorebird's open-source toolchain locally (no Shorebird account needed).
var codepushFlutterCmd = &cobra.Command{
	Use:   "flutter",
	Short: "Flutter CodePush — Dart-only OTA patches via Shorebird's updater",
	Long: `Flutter CodePush pushes Dart-only code patches to your app without a full
store re-submission, built on Shorebird's open-source updater runtime.

AirBuild is the control plane: it stores releases/patches and decides which
patch each device receives (channels, staged rollout, rollback). Builds and
binary diffs are produced locally using Shorebird's bundled Flutter SDK and
patch tool, installed by ` + "`airbuild codepush flutter install`" + ` — fully
self-hosted: no Shorebird account, nothing leaves AirBuild.

Typical flow:
  airbuild codepush flutter release android --version 1.0.0+1
  # ...fix a Dart bug...
  airbuild codepush flutter patch android --release-version 1.0.0+1
  airbuild codepush flutter promote --patch 1 --channel production --rollout 25

(If you've run ` + "`airbuild init`" + `, --app is read from .airbuild.json automatically.)`,
}

// codepushReactNativeCmd groups the React Native subcommands, which wrap
// the (separately installed) Expo CLI to produce a JS bundle + assets.
var codepushReactNativeCmd = &cobra.Command{
	Use:   "react-native",
	Short: "React Native CodePush — implements the Expo Updates protocol",
	Long: `React Native CodePush pushes JS bundle updates to your app without a full
store re-submission, using the open Expo Updates v1 protocol
(https://docs.expo.dev/technical-specs/expo-updates-1/). No AirBuild client
SDK is required — just the standard "expo-updates" package, which works in
both Expo-managed and bare React Native apps.

The bundle itself is produced locally by the Expo CLI ("npx expo export").

Typical flow:
  airbuild codepush react-native publish --runtime-version 1.0.0
  airbuild codepush react-native promote --platform android --runtime-version 1.0.0 --channel production --rollout 25

(If you've run ` + "`airbuild init`" + `, --app is read from .airbuild.json automatically.)`,
}

func init() {
	rootCmd.AddCommand(codepushCmd)
	codepushCmd.AddCommand(codepushFlutterCmd)
	codepushCmd.AddCommand(codepushReactNativeCmd)
}

// --- helpers ---

// resolveAppID returns the --app flag value if set, otherwise falls back to
// the AppID stored in .airbuild.json by `airbuild init`. On failure it prints
// an error and returns ok=false so the caller can `return`.
func resolveAppID(flagValue string) (string, bool) {
	if flagValue != "" {
		return flagValue, true
	}
	projCfg, err := project.Load()
	if err != nil {
		ui.Error("--app is required (or run `airbuild init` to set a default): %v", err)
		return "", false
	}
	ui.Muted("Using app %s from .airbuild.json", projCfg.AppID)
	return projCfg.AppID, true
}

func normalizeFlutterPlatform(p string) (string, error) {
	switch strings.ToLower(p) {
	case "android":
		return "ANDROID", nil
	case "ios":
		return "IOS", nil
	default:
		return "", fmt.Errorf("platform must be android or ios, got: %s", p)
	}
}

// normalizePlatformFlag validates an optional --platform flag shared by the
// promote/rollback commands (Flutter and React Native). An empty string is
// passed through as-is (the caller is expected to be using --update-id
// instead, which doesn't need a platform). A non-empty value must be
// "android" or "ios" (case-insensitive) — this catches typos early instead
// of silently forwarding a garbage value (e.g. "andriod" -> "ANDRIOD") to
// the server, where it would fail with a less obvious error.
func normalizePlatformFlag(p string) (string, error) {
	if p == "" {
		return "", nil
	}
	return normalizeFlutterPlatform(p)
}

// validateRolloutPercent checks that a --rollout value is in the valid
// 0-100 range before sending it to the server, so mistakes (e.g. a typo
// like --rollout 1000) are caught immediately with a clear message.
func validateRolloutPercent(pct int) error {
	if pct < 0 || pct > 100 {
		return fmt.Errorf("--rollout must be between 0 and 100, got: %d", pct)
	}
	return nil
}

// patchSigningEnv returns the SHOREBIRD_PUBLIC_KEY env map for flutter
// builds. Shorebird's forked flutter_tools reads that env var and writes
// `patch_public_key` into the compiled shorebird.yaml asset (the yaml's own
// `patch_public_key` key is NOT copied — env only), which makes the
// on-device updater enforce `hash_signature` on every patch. Best-effort:
// returns nil when the key can't be fetched, keeping today's unsigned
// behavior.
func patchSigningEnv(appID string) map[string]string {
	cfg, err := loadConfig()
	if err != nil {
		return nil
	}
	client := api.New(cfg.APIURL, cfg.APIKey)
	key, err := client.CodePushFlutterSigningKey(appID)
	if err != nil || key.RSAPublicKeyDerBase64 == "" {
		ui.Muted("Patch signing key unavailable — building without patch_public_key")
		return nil
	}
	ui.Info("Embedding patch signing key (patch_public_key) into build")
	return map[string]string{"SHOREBIRD_PUBLIC_KEY": key.RSAPublicKeyDerBase64}
}

// dartDefineArgs converts --dart-define / --dart-define-from-file flag
// values into `flutter build` arguments.
func dartDefineArgs() []string {
	var args []string
	for _, d := range cpDartDefines {
		args = append(args, "--dart-define="+d)
	}
	for _, f := range cpDartDefineFiles {
		args = append(args, "--dart-define-from-file="+f)
	}
	return args
}

// sha256File returns the lowercase hex sha256 of a file's contents.
func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close() //nolint:errcheck
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func valueOr(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
