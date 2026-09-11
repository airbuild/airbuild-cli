package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/airbuild/cli/internal/api"
	"github.com/airbuild/cli/internal/expo"
	"github.com/airbuild/cli/internal/project"
	"github.com/airbuild/cli/internal/shorebird"
	"github.com/airbuild/cli/internal/ui"
	"github.com/spf13/cobra"
)

// codepushCmd is the root `airbuild codepush` command tree.
var codepushCmd = &cobra.Command{
	Use:   "codepush",
	Short: "Push OTA code updates (React Native bundles, Flutter patches)",
}

// codepushFlutterCmd groups the Flutter-specific subcommands, which wrap the
// (separately installed) Shorebird CLI.
var codepushFlutterCmd = &cobra.Command{
	Use:   "flutter",
	Short: "Flutter CodePush — wraps the Shorebird CLI",
	Long: `Flutter CodePush pushes Dart-only code patches to your app without a full
store re-submission, built on Shorebird's open-source updater runtime.

AirBuild is the control plane: it stores releases/patches and decides which
patch each device receives (channels, staged rollout, rollback). The actual
build + binary diff is still produced locally by the Shorebird CLI
(https://pub.dev/packages/shorebird_cli — "dart pub global activate shorebird_cli").

Typical flow:
  airbuild codepush flutter release android --version 1.0.0+1
  # ...fix a Dart bug...
  airbuild codepush flutter patch android --release-version 1.0.0+1 --artifact patch.diff
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

// --- flutter release ---

var (
	cpReleaseAppID           string
	cpReleaseVersion         string
	cpReleaseArchitecture    string
	cpReleaseChannel         string
	cpReleaseFlutterRevision string
	cpReleaseShorebirdAppID  string
	cpReleaseNotes           string
	cpReleaseArtifact        string
	cpReleaseSkipBuild       bool
)

var codepushFlutterReleaseCmd = &cobra.Command{
	Use:   "release <android|ios>",
	Short: "Register a Flutter release (runs `shorebird release`, then uploads the artifact)",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		platformArg := strings.ToLower(args[0])
		platform, err := normalizeFlutterPlatform(platformArg)
		if err != nil {
			ui.Error("%v", err)
			return
		}

		var ok bool
		cpReleaseAppID, ok = resolveAppID(cpReleaseAppID)
		if !ok {
			return
		}
		if cpReleaseVersion == "" {
			ui.Error("--version is required")
			return
		}

		artifact := cpReleaseArtifact
		if artifact == "" {
			if !cpReleaseSkipBuild {
				if !shorebird.IsInstalled() {
					ui.Error("shorebird CLI not found on PATH. Install it with `dart pub global activate shorebird_cli`, or pass --artifact to skip the build step.")
					return
				}
				ui.Info("Running `shorebird release %s`...", platformArg)
				if err := shorebird.Run(".", "release", platformArg, "--no-confirm"); err != nil {
					ui.Error("%v", err)
					return
				}
			}
			if platform == "ANDROID" {
				found, err := shorebird.FindAndroidLibapp(".", cpReleaseArchitecture)
				if err != nil {
					ui.Error("%v", err)
					return
				}
				artifact = found
				ui.Muted("Auto-detected artifact: %s", artifact)
			} else {
				ui.Error("Auto-detection of the iOS release artifact isn't supported yet — pass --artifact <path to App binary/framework>.")
				return
			}
		}

		if _, err := os.Stat(artifact); err != nil {
			ui.Error("Artifact not found: %s", artifact)
			return
		}

		cfg := mustLoadConfig()
		client := api.New(cfg.APIURL, cfg.APIKey)

		ui.Info("Uploading release artifact...")
		resp, err := client.CodePushFlutterRelease(
			artifact, cpReleaseAppID, platform, cpReleaseVersion, cpReleaseArchitecture,
			cpReleaseFlutterRevision, cpReleaseShorebirdAppID, cpReleaseChannel, cpReleaseNotes,
		)
		if err != nil {
			ui.Error("Failed to register release: %v", err)
			return
		}

		ui.Success("Release registered")
		ui.Table([]string{"Field", "Value"}, [][]string{
			{"Release ID", resp.Release.ID},
			{"Version", resp.Release.Version},
			{"Platform", resp.Release.Platform},
			{"Channel", resp.Release.Channel},
			{"Architectures", strings.Join(resp.Release.Architectures, ", ")},
		})
	},
}

func init() {
	codepushFlutterReleaseCmd.Flags().StringVar(&cpReleaseAppID, "app", "", "App ID (defaults to .airbuild.json)")
	codepushFlutterReleaseCmd.Flags().StringVar(&cpReleaseVersion, "version", "", "App version, e.g. 1.0.0+1 (required)")
	codepushFlutterReleaseCmd.Flags().StringVar(&cpReleaseArchitecture, "architecture", "", "Target architecture, e.g. arm64-v8a (Android)")
	codepushFlutterReleaseCmd.Flags().StringVar(&cpReleaseChannel, "channel", "production", "Distribution channel")
	codepushFlutterReleaseCmd.Flags().StringVar(&cpReleaseFlutterRevision, "flutter-revision", "", "Flutter SDK version used to build this release")
	codepushFlutterReleaseCmd.Flags().StringVar(&cpReleaseShorebirdAppID, "shorebird-app-id", "", "Shorebird app_id, if you're tracking one")
	codepushFlutterReleaseCmd.Flags().StringVar(&cpReleaseNotes, "release-notes", "", "Release notes")
	codepushFlutterReleaseCmd.Flags().StringVar(&cpReleaseArtifact, "artifact", "", "Path to the built libapp.so / release artifact (skips auto-detection)")
	codepushFlutterReleaseCmd.Flags().BoolVar(&cpReleaseSkipBuild, "skip-build", false, "Don't run `shorebird release` — just upload --artifact")
	codepushFlutterCmd.AddCommand(codepushFlutterReleaseCmd)
}

// --- flutter patch ---

var (
	cpPatchAppID          string
	cpPatchReleaseVersion string
	cpPatchArchitecture   string
	cpPatchChannel        string
	cpPatchNotes          string
	cpPatchArtifact       string
	cpPatchSkipBuild      bool
)

var codepushFlutterPatchCmd = &cobra.Command{
	Use:   "patch <android|ios>",
	Short: "Create a Flutter patch (auto-diffs against the release, then uploads)",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		platformArg := strings.ToLower(args[0])
		platform, err := normalizeFlutterPlatform(platformArg)
		if err != nil {
			ui.Error("%v", err)
			return
		}

		var ok bool
		cpPatchAppID, ok = resolveAppID(cpPatchAppID)
		if !ok {
			return
		}
		if cpPatchReleaseVersion == "" {
			ui.Error("--release-version is required")
			return
		}

		artifact := cpPatchArtifact

		if artifact == "" {
			// --- Auto-diff flow ---
			// When --artifact is omitted, the CLI builds the patch and
			// locates the resulting diff automatically.
			//
			// Android (Phase 2 — full auto-diff):
			//   1. Run `shorebird patch android --dry-run` (builds libapp.so)
			//   2. Locate the freshly built libapp.so via FindAndroidLibapp
			//   3. Download the release's original libapp.so from AirBuild
			//   4. Use Shorebird's cached `patch` binary to create the diff
			//   5. Upload the diff to AirBuild
			//
			// iOS (Phase 1 — temp dir scan):
			//   1. Record the current timestamp
			//   2. Run `shorebird patch ios --dry-run` (builds + creates diff
			//      in a random temp dir, but doesn't upload to Shorebird)
			//   3. Scan the OS temp dir for a recently-created diff.patch
			//   4. Upload the located diff to AirBuild
			//
			// iOS Phase 1 is best-effort: Shorebird creates the diff in a
			// random temp directory, so we locate it by scanning for files
			// named "diff.patch" modified after the build started. If the
			// scan fails (temp cleanup, concurrent builds), the user falls
			// back to --artifact.

			if !shorebird.IsInstalled() {
				ui.Error("shorebird CLI not found on PATH. Install it with `dart pub global activate shorebird_cli`, or pass --artifact to skip the build step.")
				return
			}

			if platform == "ANDROID" {
				artifact, err = runAndroidAutoDiff(platformArg, cpPatchReleaseVersion, cpPatchSkipBuild, cpPatchArchitecture, cpPatchChannel, cpPatchAppID)
				if err != nil {
					ui.Error("%v", err)
					return
				}
			} else {
				// iOS — Phase 1: temp dir scan.
				artifact, err = runIOSAutoDiff(platformArg, cpPatchReleaseVersion, cpPatchSkipBuild)
				if err != nil {
					ui.Error(`%v

Could not auto-locate the iOS patch diff. You can still create a patch
manually:
  1. Run "shorebird patch ios" yourself
  2. Locate the resulting diff.patch file
  3. Pass it via --artifact`, err)
					return
				}
			}
		} else {
			// --- Explicit --artifact flow (original behavior) ---
			if !cpPatchSkipBuild && shorebird.IsInstalled() {
				ui.Info("Running `shorebird patch %s` (for its build/diff side effects)...", platformArg)
				if err := shorebird.Run(".", "patch", platformArg, "--no-confirm"); err != nil {
					ui.Warn("shorebird patch reported an error (continuing with --artifact anyway): %v", err)
				}
			}
		}

		if _, err := os.Stat(artifact); err != nil {
			ui.Error("Artifact not found: %s", artifact)
			return
		}

		cfg := mustLoadConfig()
		client := api.New(cfg.APIURL, cfg.APIKey)

		ui.Info("Uploading patch...")
		resp, err := client.CodePushFlutterPatch(
			artifact, cpPatchAppID, platform, cpPatchReleaseVersion, cpPatchArchitecture, cpPatchChannel, cpPatchNotes,
		)
		if err != nil {
			ui.Error("Failed to create patch: %v", err)
			return
		}

		ui.Success("Patch #%d created (DRAFT, 0%% rollout)", resp.Update.PatchNumber)
		ui.Info("Promote it with: airbuild codepush flutter promote --patch %d --channel %s --rollout 25",
			resp.Update.PatchNumber, valueOr(cpPatchChannel, "production"))
	},
}

func init() {
	codepushFlutterPatchCmd.Flags().StringVar(&cpPatchAppID, "app", "", "App ID (defaults to .airbuild.json)")
	codepushFlutterPatchCmd.Flags().StringVar(&cpPatchReleaseVersion, "release-version", "", "The release version this patch targets, e.g. 1.0.0+1 (required)")
	codepushFlutterPatchCmd.Flags().StringVar(&cpPatchArchitecture, "architecture", "", "Target architecture, e.g. arm64-v8a (Android)")
	codepushFlutterPatchCmd.Flags().StringVar(&cpPatchChannel, "channel", "production", "Distribution channel")
	codepushFlutterPatchCmd.Flags().StringVar(&cpPatchNotes, "release-notes", "", "Patch notes")
	codepushFlutterPatchCmd.Flags().StringVar(&cpPatchArtifact, "artifact", "", "Path to a pre-built patch diff file (optional — auto-diffs if omitted)")
	codepushFlutterPatchCmd.Flags().BoolVar(&cpPatchSkipBuild, "skip-build", false, "Don't run `shorebird patch` — use existing build output or --artifact")
	codepushFlutterCmd.AddCommand(codepushFlutterPatchCmd)
}

// --- flutter promote ---

var (
	cpPromoteAppID          string
	cpPromoteUpdateID       string
	cpPromoteReleaseVersion string
	cpPromotePlatform       string
	cpPromotePatchNumber    int
	cpPromoteChannel        string
	cpPromoteRollout        int
)

var codepushFlutterPromoteCmd = &cobra.Command{
	Use:   "promote",
	Short: "Promote a Flutter patch to a channel at a rollout percentage",
	Run: func(cmd *cobra.Command, args []string) {
		var ok bool
		cpPromoteAppID, ok = resolveAppID(cpPromoteAppID)
		if !ok {
			return
		}
		if cpPromoteUpdateID == "" && (cpPromoteReleaseVersion == "" || cpPromotePlatform == "" || cpPromotePatchNumber == 0) {
			ui.Error("Provide --update-id, or --release-version + --platform + --patch")
			return
		}

		cfg := mustLoadConfig()
		client := api.New(cfg.APIURL, cfg.APIKey)

		ref := api.CodePushFlutterPatchRef{
			AppID:          cpPromoteAppID,
			UpdateID:       cpPromoteUpdateID,
			ReleaseVersion: cpPromoteReleaseVersion,
			Platform:       strings.ToUpper(cpPromotePlatform),
			PatchNumber:    cpPromotePatchNumber,
			Channel:        cpPromoteChannel,
		}
		resp, err := client.CodePushFlutterPromote(ref, cpPromoteRollout)
		if err != nil {
			ui.Error("Failed to promote patch: %v", err)
			return
		}

		ui.Success("Patch #%d promoted to %s at %d%% rollout", resp.Update.PatchNumber, resp.Update.Channel, resp.Update.RolloutPercent)
	},
}

func init() {
	codepushFlutterPromoteCmd.Flags().StringVar(&cpPromoteAppID, "app", "", "App ID (defaults to .airbuild.json)")
	codepushFlutterPromoteCmd.Flags().StringVar(&cpPromoteUpdateID, "update-id", "", "Update ID (alternative to --release-version/--platform/--patch)")
	codepushFlutterPromoteCmd.Flags().StringVar(&cpPromoteReleaseVersion, "release-version", "", "Release version the patch targets")
	codepushFlutterPromoteCmd.Flags().StringVar(&cpPromotePlatform, "platform", "", "ANDROID or IOS")
	codepushFlutterPromoteCmd.Flags().IntVar(&cpPromotePatchNumber, "patch", 0, "Patch number (from `airbuild codepush flutter status`)")
	codepushFlutterPromoteCmd.Flags().StringVar(&cpPromoteChannel, "channel", "production", "Channel to promote to")
	codepushFlutterPromoteCmd.Flags().IntVar(&cpPromoteRollout, "rollout", 100, "Rollout percentage (0-100)")
	codepushFlutterCmd.AddCommand(codepushFlutterPromoteCmd)
}

// --- flutter rollback ---

var (
	cpRollbackAppID          string
	cpRollbackUpdateID       string
	cpRollbackReleaseVersion string
	cpRollbackPlatform       string
	cpRollbackPatchNumber    int
	cpRollbackChannel        string
)

var codepushFlutterRollbackCmd = &cobra.Command{
	Use:   "rollback",
	Short: "Rollback a Flutter patch",
	Run: func(cmd *cobra.Command, args []string) {
		var ok bool
		cpRollbackAppID, ok = resolveAppID(cpRollbackAppID)
		if !ok {
			return
		}
		if cpRollbackUpdateID == "" && (cpRollbackReleaseVersion == "" || cpRollbackPlatform == "" || cpRollbackPatchNumber == 0) {
			ui.Error("Provide --update-id, or --release-version + --platform + --patch")
			return
		}

		cfg := mustLoadConfig()
		client := api.New(cfg.APIURL, cfg.APIKey)

		ref := api.CodePushFlutterPatchRef{
			AppID:          cpRollbackAppID,
			UpdateID:       cpRollbackUpdateID,
			ReleaseVersion: cpRollbackReleaseVersion,
			Platform:       strings.ToUpper(cpRollbackPlatform),
			PatchNumber:    cpRollbackPatchNumber,
			Channel:        cpRollbackChannel,
		}
		resp, err := client.CodePushFlutterRollback(ref)
		if err != nil {
			ui.Error("Failed to rollback patch: %v", err)
			return
		}

		ui.Success("Patch #%d rolled back", resp.Update.PatchNumber)
	},
}

func init() {
	codepushFlutterRollbackCmd.Flags().StringVar(&cpRollbackAppID, "app", "", "App ID (defaults to .airbuild.json)")
	codepushFlutterRollbackCmd.Flags().StringVar(&cpRollbackUpdateID, "update-id", "", "Update ID (alternative to --release-version/--platform/--patch)")
	codepushFlutterRollbackCmd.Flags().StringVar(&cpRollbackReleaseVersion, "release-version", "", "Release version the patch targets")
	codepushFlutterRollbackCmd.Flags().StringVar(&cpRollbackPlatform, "platform", "", "ANDROID or IOS")
	codepushFlutterRollbackCmd.Flags().IntVar(&cpRollbackPatchNumber, "patch", 0, "Patch number")
	codepushFlutterRollbackCmd.Flags().StringVar(&cpRollbackChannel, "channel", "production", "Channel")
	codepushFlutterCmd.AddCommand(codepushFlutterRollbackCmd)
}

// --- flutter status ---

var cpStatusAppID string

var codepushFlutterStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show Flutter release/patch status for an app",
	Run: func(cmd *cobra.Command, args []string) {
		var ok bool
		cpStatusAppID, ok = resolveAppID(cpStatusAppID)
		if !ok {
			return
		}

		cfg := mustLoadConfig()
		client := api.New(cfg.APIURL, cfg.APIKey)

		resp, err := client.CodePushFlutterStatus(cpStatusAppID)
		if err != nil {
			ui.Error("Failed to fetch status: %v", err)
			return
		}

		if len(resp.Channels) > 0 {
			ui.Header("Channels")
			rows := make([][]string, 0, len(resp.Channels))
			for _, c := range resp.Channels {
				rows = append(rows, []string{c.Name, c.CurrentUpdateID})
			}
			ui.Table([]string{"Channel", "Current Update ID"}, rows)
			fmt.Println()
		}

		if len(resp.Releases) == 0 {
			ui.Muted("No releases yet.")
			return
		}

		for _, r := range resp.Releases {
			ui.Header("Release %s (%s, %s)", r.Version, r.Platform, r.Channel)
			if len(r.Updates) == 0 {
				ui.Muted("  No patches yet.")
				continue
			}
			rows := make([][]string, 0, len(r.Updates))
			for _, u := range r.Updates {
				rows = append(rows, []string{
					fmt.Sprintf("#%d", u.PatchNumber),
					u.Status,
					fmt.Sprintf("%d%%", u.RolloutPercent),
					u.Channel,
					u.CreatedAt,
				})
			}
			ui.Table([]string{"Patch", "Status", "Rollout", "Channel", "Created"}, rows)
			fmt.Println()
		}
	},
}

func init() {
	codepushFlutterStatusCmd.Flags().StringVar(&cpStatusAppID, "app", "", "App ID (defaults to .airbuild.json)")
	codepushFlutterCmd.AddCommand(codepushFlutterStatusCmd)
}

// --- react-native publish ---

var (
	cpRnAppID          string
	cpRnPlatform       string
	cpRnRuntimeVersion string
	cpRnChannel        string
	cpRnNotes          string
	cpRnOutputDir      string
	cpRnSkipExport     bool
)

var codepushReactNativePublishCmd = &cobra.Command{
	Use:   "publish",
	Short: "Publish a React Native update (runs `npx expo export`, then uploads the bundle + assets)",
	Run: func(cmd *cobra.Command, args []string) {
		var ok bool
		cpRnAppID, ok = resolveAppID(cpRnAppID)
		if !ok {
			return
		}
		if cpRnPlatform == "" || cpRnRuntimeVersion == "" {
			ui.Error("--platform and --runtime-version are required")
			return
		}
		platform, err := normalizeFlutterPlatform(cpRnPlatform) // ANDROID/IOS normalization is framework-agnostic
		if err != nil {
			ui.Error("%v", err)
			return
		}
		expoPlatform := strings.ToLower(cpRnPlatform)

		if !cpRnSkipExport {
			if !expo.IsInstalled() {
				ui.Error("npx not found on PATH. Install Node.js, or pass --skip-export if you already ran `expo export`.")
				return
			}
			ui.Info("Running `npx expo export --output-dir %s --platform %s`...", cpRnOutputDir, expoPlatform)
			if err := expo.Run(".", "export", "--output-dir", cpRnOutputDir, "--platform", expoPlatform); err != nil {
				ui.Error("%v", err)
				return
			}
		}

		meta, err := expo.ParseMetadata(cpRnOutputDir)
		if err != nil {
			ui.Error("%v", err)
			return
		}
		entries, err := expo.BuildManifestEntries(cpRnOutputDir, meta, expoPlatform)
		if err != nil {
			ui.Error("%v", err)
			return
		}

		apiEntries := make([]api.ReactNativeManifestEntry, 0, len(entries))
		for _, e := range entries {
			if _, err := os.Stat(e.FilePath); err != nil {
				ui.Error("File referenced in metadata.json not found: %s", e.FilePath)
				return
			}
			apiEntries = append(apiEntries, api.ReactNativeManifestEntry{
				FieldName:     e.FieldName,
				FilePath:      e.FilePath,
				Key:           e.Key,
				IsLaunchAsset: e.IsLaunchAsset,
				ContentType:   e.ContentType,
				FileExtension: e.FileExtension,
			})
		}

		cfg := mustLoadConfig()
		client := api.New(cfg.APIURL, cfg.APIKey)

		ui.Info("Uploading %d file(s) (bundle + assets)...", len(apiEntries))
		resp, err := client.CodePushReactNativePublish(apiEntries, cpRnAppID, platform, cpRnRuntimeVersion, cpRnChannel, cpRnNotes)
		if err != nil {
			ui.Error("Failed to publish update: %v", err)
			return
		}

		ui.Success("Update published (DRAFT, 0%% rollout)")
		ui.Table([]string{"Field", "Value"}, [][]string{
			{"Update ID", resp.Update.ID},
			{"Channel", resp.Update.Channel},
			{"Assets", fmt.Sprintf("%d", len(resp.Update.Assets))},
		})
		ui.Info("Promote it with: airbuild codepush react-native promote --update-id %s --channel %s --rollout 25",
			resp.Update.ID, valueOr(cpRnChannel, "production"))
	},
}

func init() {
	codepushReactNativePublishCmd.Flags().StringVar(&cpRnAppID, "app", "", "App ID (defaults to .airbuild.json)")
	codepushReactNativePublishCmd.Flags().StringVar(&cpRnPlatform, "platform", "", "android or ios (required)")
	codepushReactNativePublishCmd.Flags().StringVar(&cpRnRuntimeVersion, "runtime-version", "", "Runtime version, must match expo.updates.runtimeVersion in app.json (required)")
	codepushReactNativePublishCmd.Flags().StringVar(&cpRnChannel, "channel", "production", "Distribution channel")
	codepushReactNativePublishCmd.Flags().StringVar(&cpRnNotes, "release-notes", "", "Release notes")
	codepushReactNativePublishCmd.Flags().StringVar(&cpRnOutputDir, "output-dir", "dist", "Directory to export to / read from")
	codepushReactNativePublishCmd.Flags().BoolVar(&cpRnSkipExport, "skip-export", false, "Don't run `npx expo export` — just read --output-dir")
	codepushReactNativeCmd.AddCommand(codepushReactNativePublishCmd)
}

// --- react-native promote ---

var (
	cpRnPromoteAppID          string
	cpRnPromoteUpdateID       string
	cpRnPromotePlatform       string
	cpRnPromoteRuntimeVersion string
	cpRnPromoteChannel        string
	cpRnPromoteRollout        int
)

var codepushReactNativePromoteCmd = &cobra.Command{
	Use:   "promote",
	Short: "Promote a React Native update to a channel at a rollout percentage",
	Run: func(cmd *cobra.Command, args []string) {
		var ok bool
		cpRnPromoteAppID, ok = resolveAppID(cpRnPromoteAppID)
		if !ok {
			return
		}
		if cpRnPromoteUpdateID == "" && (cpRnPromotePlatform == "" || cpRnPromoteRuntimeVersion == "") {
			ui.Error("Provide --update-id, or --platform + --runtime-version")
			return
		}

		cfg := mustLoadConfig()
		client := api.New(cfg.APIURL, cfg.APIKey)

		resp, err := client.CodePushReactNativePromote(
			cpRnPromoteAppID, cpRnPromoteUpdateID, strings.ToUpper(cpRnPromotePlatform), cpRnPromoteRuntimeVersion,
			cpRnPromoteChannel, cpRnPromoteRollout,
		)
		if err != nil {
			ui.Error("Failed to promote update: %v", err)
			return
		}

		ui.Success("Update promoted to %s at %d%% rollout", resp.Update.Channel, resp.Update.RolloutPercent)
	},
}

func init() {
	codepushReactNativePromoteCmd.Flags().StringVar(&cpRnPromoteAppID, "app", "", "App ID (defaults to .airbuild.json)")
	codepushReactNativePromoteCmd.Flags().StringVar(&cpRnPromoteUpdateID, "update-id", "", "Update ID (alternative to --platform/--runtime-version)")
	codepushReactNativePromoteCmd.Flags().StringVar(&cpRnPromotePlatform, "platform", "", "ANDROID or IOS")
	codepushReactNativePromoteCmd.Flags().StringVar(&cpRnPromoteRuntimeVersion, "runtime-version", "", "Runtime version the update targets")
	codepushReactNativePromoteCmd.Flags().StringVar(&cpRnPromoteChannel, "channel", "production", "Channel to promote to")
	codepushReactNativePromoteCmd.Flags().IntVar(&cpRnPromoteRollout, "rollout", 100, "Rollout percentage (0-100)")
	codepushReactNativeCmd.AddCommand(codepushReactNativePromoteCmd)
}

// --- react-native rollback ---

var (
	cpRnRollbackAppID    string
	cpRnRollbackUpdateID string
)

var codepushReactNativeRollbackCmd = &cobra.Command{
	Use:   "rollback",
	Short: "Rollback a React Native update",
	Run: func(cmd *cobra.Command, args []string) {
		var ok bool
		cpRnRollbackAppID, ok = resolveAppID(cpRnRollbackAppID)
		if !ok {
			return
		}
		if cpRnRollbackUpdateID == "" {
			ui.Error("--update-id is required")
			return
		}

		cfg := mustLoadConfig()
		client := api.New(cfg.APIURL, cfg.APIKey)

		resp, err := client.CodePushReactNativeRollback(cpRnRollbackAppID, cpRnRollbackUpdateID)
		if err != nil {
			ui.Error("Failed to rollback update: %v", err)
			return
		}

		ui.Success("Update %s rolled back", resp.Update.ID)
	},
}

func init() {
	codepushReactNativeRollbackCmd.Flags().StringVar(&cpRnRollbackAppID, "app", "", "App ID (defaults to .airbuild.json)")
	codepushReactNativeRollbackCmd.Flags().StringVar(&cpRnRollbackUpdateID, "update-id", "", "Update ID (required)")
	codepushReactNativeCmd.AddCommand(codepushReactNativeRollbackCmd)
}

// --- react-native status ---

var cpRnStatusAppID string

var codepushReactNativeStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show React Native release/update status for an app",
	Run: func(cmd *cobra.Command, args []string) {
		var ok bool
		cpRnStatusAppID, ok = resolveAppID(cpRnStatusAppID)
		if !ok {
			return
		}

		cfg := mustLoadConfig()
		client := api.New(cfg.APIURL, cfg.APIKey)

		resp, err := client.CodePushReactNativeStatus(cpRnStatusAppID)
		if err != nil {
			ui.Error("Failed to fetch status: %v", err)
			return
		}

		if len(resp.Channels) > 0 {
			ui.Header("Channels")
			rows := make([][]string, 0, len(resp.Channels))
			for _, c := range resp.Channels {
				rows = append(rows, []string{c.Name, c.CurrentUpdateID})
			}
			ui.Table([]string{"Channel", "Current Update ID"}, rows)
			fmt.Println()
		}

		if len(resp.Releases) == 0 {
			ui.Muted("No releases yet.")
			return
		}

		for _, r := range resp.Releases {
			ui.Header("Runtime %s (%s, %s)", r.Version, r.Platform, r.Channel)
			if len(r.Updates) == 0 {
				ui.Muted("  No updates yet.")
				continue
			}
			rows := make([][]string, 0, len(r.Updates))
			for _, u := range r.Updates {
				rows = append(rows, []string{
					u.ID,
					u.Status,
					fmt.Sprintf("%d%%", u.RolloutPercent),
					u.Channel,
					fmt.Sprintf("%d", len(u.Assets)),
					u.CreatedAt,
				})
			}
			ui.Table([]string{"Update ID", "Status", "Rollout", "Channel", "Assets", "Created"}, rows)
			fmt.Println()
		}
	},
}

func init() {
	codepushReactNativeStatusCmd.Flags().StringVar(&cpRnStatusAppID, "app", "", "App ID (defaults to .airbuild.json)")
	codepushReactNativeCmd.AddCommand(codepushReactNativeStatusCmd)
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

// runAndroidAutoDiff implements the full Android auto-diff flow (Phase 2):
// build via shorebird patch --dry-run, locate libapp.so, download the release
// libapp.so from AirBuild, and create the diff using Shorebird's cached
// patch binary. Returns the path to the generated diff file.
func runAndroidAutoDiff(platformArg, releaseVersion string, skipBuild bool, architecture, channel, appID string) (string, error) {
	// Step 1: Build the patch's libapp.so (dry-run = build but don't upload
	// to Shorebird's cloud).
	if !skipBuild {
		ui.Info("Running `shorebird patch %s --dry-run` (building patch artifact)...", platformArg)
		releaseVersionArgs := []string{"patch", platformArg, "--dry-run"}
		if releaseVersion != "" {
			releaseVersionArgs = append(releaseVersionArgs, "--release-version", releaseVersion)
		}
		if err := shorebird.Run(".", releaseVersionArgs...); err != nil {
			return "", fmt.Errorf("shorebird patch --dry-run failed: %w", err)
		}
	}

	// Step 2: Locate the freshly built patch libapp.so.
	patchLibapp, err := shorebird.FindAndroidLibapp(".", architecture)
	if err != nil {
		return "", err
	}
	ui.Muted("Found patch libapp.so: %s", patchLibapp)

	// Step 3: Locate Shorebird's cached `patch` binary (for diffing).
	patchBinary, err := shorebird.FindPatchBinary()
	if err != nil {
		return "", err
	}
	ui.Muted("Using Shorebird patch binary: %s", patchBinary)

	// Step 4: Download the release's original libapp.so from AirBuild.
	cfg := mustLoadConfig()
	client := api.New(cfg.APIURL, cfg.APIKey)

	ui.Info("Downloading release %s libapp.so from AirBuild...", releaseVersion)
	dlResp, err := client.CodePushFlutterReleaseDownload(
		appID, releaseVersion, "ANDROID", architecture, channel,
	)
	if err != nil {
		return "", fmt.Errorf("failed to get release download URL: %w", err)
	}

	tmpDir, err := os.MkdirTemp("", "airbuild-patch-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tmpDir) //nolint:errcheck

	releaseLibappPath := filepath.Join(tmpDir, "release-libapp.so")
	if err := client.DownloadFile(dlResp.DownloadUrl, releaseLibappPath); err != nil {
		return "", fmt.Errorf("failed to download release libapp.so: %w", err)
	}
	ui.Muted("Downloaded release libapp.so (%s) to %s", dlResp.Release.Version, releaseLibappPath)

	// Step 5: Create the binary diff using Shorebird's patch binary.
	diffPath := filepath.Join(tmpDir, "patch.diff")
	ui.Info("Creating binary diff...")
	if err := shorebird.CreateDiff(patchBinary, releaseLibappPath, patchLibapp, diffPath); err != nil {
		return "", fmt.Errorf("failed to create diff: %w", err)
	}

	ui.Muted("Created diff: %s", diffPath)
	return diffPath, nil
}

// runIOSAutoDiff implements the iOS Phase 1 auto-diff flow: run
// `shorebird patch ios --dry-run`, then scan the OS temp directory for the
// diff.patch file that Shorebird creates in a random temp dir. This is
// best-effort — if the scan fails, the user falls back to --artifact.
//
// Phase 2 (full iOS auto-diff using aot_tools + analyze_snapshot + patch
// binaries) is tracked as a future task — see docs/CHECKLIST.md §5.2d.
func runIOSAutoDiff(platformArg, releaseVersion string, skipBuild bool) (string, error) {
	// Record the timestamp before building so we can filter temp files.
	buildStart := time.Now()

	if !skipBuild {
		ui.Info("Running `shorebird patch %s --dry-run` (building + creating diff)...", platformArg)
		releaseVersionArgs := []string{"patch", platformArg, "--dry-run"}
		if releaseVersion != "" {
			releaseVersionArgs = append(releaseVersionArgs, "--release-version", releaseVersion)
		}
		if err := shorebird.Run(".", releaseVersionArgs...); err != nil {
			return "", fmt.Errorf("shorebird patch --dry-run failed: %w", err)
		}
	}

	// Scan the OS temp dir for a diff.patch modified after buildStart.
	ui.Info("Scanning temp directory for the generated diff...")
	diffPath, err := shorebird.FindRecentDiffPatch(buildStart)
	if err != nil {
		return "", err
	}

	ui.Muted("Found diff: %s", diffPath)
	return diffPath, nil
}

func valueOr(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
