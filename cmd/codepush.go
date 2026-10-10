package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/airbuild/airbuild-cli/internal/api"
	"github.com/airbuild/airbuild-cli/internal/expo"
	"github.com/airbuild/airbuild-cli/internal/project"
	"github.com/airbuild/airbuild-cli/internal/shorebird"
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
	cpReleaseFormat          string
)

var codepushFlutterReleaseCmd = &cobra.Command{
	Use:   "release <android|ios>",
	Short: "Build a Flutter release locally and register it with AirBuild",
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
		switch cpReleaseFormat {
		case "apk", "aab", "appbundle":
		default:
			ui.Error("--format must be apk or aab, got: %s", cpReleaseFormat)
			return
		}

		artifact := cpReleaseArtifact
		if artifact == "" {
			if !cpReleaseSkipBuild {
				if platform != "ANDROID" {
					ui.Error("Local iOS release builds aren't supported yet — pass --artifact <path to built artifact>.")
					return
				}
				if _, err := shorebird.BundledFlutter(); err != nil {
					ui.Error("%v", err)
					return
				}
				target := "apk"
				if cpReleaseFormat == "aab" || cpReleaseFormat == "appbundle" {
					target = "appbundle"
				}
				ui.Info("Building %s with Shorebird's bundled Flutter (fully local — no Shorebird account needed)...", target)
				if err := shorebird.RunFlutter(".", nil, "build", target, "--release"); err != nil {
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
				// The ABI is the parent dir of libapp.so (e.g. .../arm64-v8a/libapp.so).
				if cpReleaseArchitecture == "" {
					cpReleaseArchitecture = filepath.Base(filepath.Dir(artifact))
				}
			} else {
				ui.Error("Auto-detection of the iOS release artifact isn't supported yet — pass --artifact <path to App binary/framework>.")
				return
			}
		}

		if _, err := os.Stat(artifact); err != nil {
			ui.Error("Artifact not found: %s", artifact)
			return
		}

		// Record which bundled Flutter built this release — patches must be
		// built with the same engine revision.
		if cpReleaseFlutterRevision == "" {
			if dir, err := shorebird.FlutterDir(); err == nil {
				cpReleaseFlutterRevision = filepath.Base(dir)
			}
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
	codepushFlutterReleaseCmd.Flags().BoolVar(&cpReleaseSkipBuild, "skip-build", false, "Don't build — just upload --artifact or auto-detected output")
	codepushFlutterReleaseCmd.Flags().StringVar(&cpReleaseFormat, "format", "apk", "Android build format: apk (install links) or aab (Play Store)")
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
	cpPatchArtifactHash   string
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
		artifactHash := cpPatchArtifactHash

		if artifact == "" {
			// --- Auto-diff flow ---
			// When --artifact is omitted, the CLI builds the patch and
			// locates the resulting diff automatically.
			//
			// Android auto-diff:
			//   1. Build libapp.so with Shorebird's bundled Flutter (a patch
			//      is just the same release build with new Dart code — the
			//      updater ships the binary diff, not a build recipe).
			//   2. Locate the freshly built libapp.so via FindAndroidLibapp
			//   3. Download the release's original libapp.so from AirBuild
			//   4. Use Shorebird's `patch` binary (public artifact download)
			//      to create the bidiff+zstd diff
			//   5. Upload the diff to AirBuild

			if _, err := shorebird.BundledFlutter(); err != nil {
				ui.Error("%v", err)
				return
			}

			if platform == "ANDROID" {
				artifact, artifactHash, err = runAndroidAutoDiff(cpPatchReleaseVersion, cpPatchSkipBuild, cpPatchArchitecture, cpPatchChannel, cpPatchAppID)
				if err != nil {
					ui.Error("%v", err)
					return
				}
			} else {
				ui.Error(`iOS auto-diff isn't supported yet — iOS patches need aot_tools + analyze_snapshot.
You can still create a patch manually:
  1. Build the iOS patch artifact yourself
  2. Produce the diff.patch file
  3. Pass it via --artifact`)
				return
			}
		}

		if _, err := os.Stat(artifact); err != nil {
			ui.Error("Artifact not found: %s", artifact)
			return
		}

		if artifactHash == "" {
			// Without the patched-artifact hash the server can't send a
			// `hash` the updater accepts — devices will download the patch
			// then reject it ("Update rejected: hash mismatch").
			ui.Muted("No patched-artifact hash supplied — pass --artifact-sha256 (sha256 of the new libapp.so) or use auto-diff.")
		}

		cfg := mustLoadConfig()
		client := api.New(cfg.APIURL, cfg.APIKey)

		ui.Info("Uploading patch...")
		resp, err := client.CodePushFlutterPatch(
			artifact, cpPatchAppID, platform, cpPatchReleaseVersion, cpPatchArchitecture, cpPatchChannel, cpPatchNotes, artifactHash,
		)
		if err != nil {
			ui.Error("Failed to create patch: %v", err)
			return
		}

		ui.Success("Patch #%d created (DRAFT, 0%% rollout)", resp.Update.PatchNumber)
		// Auto-generated diffs are temp files — clean up after upload.
		if cpPatchArtifact == "" {
			os.Remove(artifact) //nolint:errcheck
		}
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
	codepushFlutterPatchCmd.Flags().StringVar(&cpPatchArtifactHash, "artifact-sha256", "", "sha256 of the patched libapp.so (required when passing --artifact so devices can verify the applied patch)")
	codepushFlutterPatchCmd.Flags().BoolVar(&cpPatchSkipBuild, "skip-build", false, "Don't rebuild — diff from the existing build output")
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
		platform, err := normalizePlatformFlag(cpPromotePlatform)
		if err != nil {
			ui.Error("%v", err)
			return
		}
		if err := validateRolloutPercent(cpPromoteRollout); err != nil {
			ui.Error("%v", err)
			return
		}

		cfg := mustLoadConfig()
		client := api.New(cfg.APIURL, cfg.APIKey)

		ref := api.CodePushFlutterPatchRef{
			AppID:          cpPromoteAppID,
			UpdateID:       cpPromoteUpdateID,
			ReleaseVersion: cpPromoteReleaseVersion,
			Platform:       platform,
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
		platform, err := normalizePlatformFlag(cpRollbackPlatform)
		if err != nil {
			ui.Error("%v", err)
			return
		}

		cfg := mustLoadConfig()
		client := api.New(cfg.APIURL, cfg.APIKey)

		ref := api.CodePushFlutterPatchRef{
			AppID:          cpRollbackAppID,
			UpdateID:       cpRollbackUpdateID,
			ReleaseVersion: cpRollbackReleaseVersion,
			Platform:       platform,
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
			ui.Muted("No releases yet. Register one with: airbuild codepush flutter release android --version 1.0.0+1")
			return
		}

		for _, r := range resp.Releases {
			ui.Header("Release %s (%s, %s)", r.Version, r.Platform, r.Channel)
			if len(r.Updates) == 0 {
				ui.Muted("  No patches yet. Create one with: airbuild codepush flutter patch %s --release-version %s", strings.ToLower(r.Platform), r.Version)
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
		platform, err := normalizePlatformFlag(cpRnPromotePlatform)
		if err != nil {
			ui.Error("%v", err)
			return
		}
		if err := validateRolloutPercent(cpRnPromoteRollout); err != nil {
			ui.Error("%v", err)
			return
		}

		cfg := mustLoadConfig()
		client := api.New(cfg.APIURL, cfg.APIKey)

		resp, err := client.CodePushReactNativePromote(
			cpRnPromoteAppID, cpRnPromoteUpdateID, platform, cpRnPromoteRuntimeVersion,
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
			ui.Muted("No releases yet. Publish one with: airbuild codepush react-native publish --platform android --runtime-version 1.0.0")
			return
		}

		for _, r := range resp.Releases {
			ui.Header("Runtime %s (%s, %s)", r.Version, r.Platform, r.Channel)
			if len(r.Updates) == 0 {
				ui.Muted("  No updates yet. Publish one with: airbuild codepush react-native publish --platform %s --runtime-version %s", strings.ToLower(r.Platform), r.Version)
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

// runAndroidAutoDiff implements the full Android auto-diff flow: rebuild
// libapp.so with Shorebird's bundled Flutter, download the release's
// libapp.so from AirBuild, and create the diff using Shorebird's `patch`
// binary. Returns the path to the generated diff file and the sha256 of
// the patched libapp.so — the artifact hash the on-device updater
// verifies after applying the diff.
func runAndroidAutoDiff(releaseVersion string, skipBuild bool, architecture, channel, appID string) (string, string, error) {
	// Step 1: Build the patch's libapp.so — the same release build with the
	// new Dart code, using the bundled (updater-capable) Flutter SDK.
	if !skipBuild {
		ui.Info("Building patch artifact with Shorebird's bundled Flutter...")
		if err := shorebird.RunFlutter(".", nil, "build", "apk", "--release"); err != nil {
			return "", "", fmt.Errorf("patch build failed: %w", err)
		}
	}

	// Step 2: Locate the freshly built patch libapp.so.
	patchLibapp, err := shorebird.FindAndroidLibapp(".", architecture)
	if err != nil {
		return "", "", err
	}
	ui.Muted("Found patch libapp.so: %s", patchLibapp)

	// Step 3: Locate Shorebird's `patch` binary (downloads it from
	// Shorebird's public artifact bucket on first use — no auth needed).
	patchBinary, err := shorebird.EnsurePatchBinary()
	if err != nil {
		return "", "", err
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
		return "", "", fmt.Errorf("failed to get release download URL: %w", err)
	}

	tmpDir, err := os.MkdirTemp("", "airbuild-patch-*")
	if err != nil {
		return "", "", fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tmpDir) //nolint:errcheck

	releaseLibappPath := filepath.Join(tmpDir, "release-libapp.so")
	if err := client.DownloadFile(dlResp.DownloadUrl, releaseLibappPath); err != nil {
		return "", "", fmt.Errorf("failed to download release libapp.so: %w", err)
	}
	ui.Muted("Downloaded release libapp.so (%s) to %s", dlResp.Release.Version, releaseLibappPath)

	// Step 5: Create the binary diff using Shorebird's patch binary. The
	// diff goes in its own temp file (not tmpDir — that's deleted on
	// return, and the caller needs the path to survive for the upload).
	diffFile, err := os.CreateTemp("", "airbuild-patch-*.diff")
	if err != nil {
		return "", "", fmt.Errorf("failed to create diff file: %w", err)
	}
	diffPath := diffFile.Name()
	diffFile.Close() //nolint:errcheck
	ui.Info("Creating binary diff...")
	if err := shorebird.CreateDiff(patchBinary, releaseLibappPath, patchLibapp, diffPath); err != nil {
		return "", "", fmt.Errorf("failed to create diff: %w", err)
	}
	if err := shorebird.ValidateDiffFile(diffPath); err != nil {
		return "", "", fmt.Errorf("generated diff failed validation: %w", err)
	}

	// The updater verifies the *patched artifact* hash (the new libapp.so)
	// after applying the diff — send it alongside the upload.
	artifactHash, err := sha256File(patchLibapp)
	if err != nil {
		return "", "", fmt.Errorf("failed to hash patch artifact: %w", err)
	}

	ui.Muted("Created diff: %s", diffPath)
	return diffPath, artifactHash, nil
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
