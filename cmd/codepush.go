package cmd

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

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
	cpReleaseNoCodesign      bool
	cpReleaseFormat          string
	cpDartDefines            []string
	cpDartDefineFiles        []string
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
		// Per-arch artifacts to register — multi-ABI Android builds upload
		// one libapp.so per architecture into the same release row.
		type archUpload struct{ arch, path string }
		var uploads []archUpload
		if artifact == "" {
			if !cpReleaseSkipBuild {
				if _, err := shorebird.BundledFlutter(); err != nil {
					ui.Error("%v", err)
					return
				}
				if platform == "ANDROID" {
					target := "apk"
					if cpReleaseFormat == "aab" || cpReleaseFormat == "appbundle" {
						target = "appbundle"
					}
					ui.Info("Building %s with Shorebird's bundled Flutter (fully local — no Shorebird account needed)...", target)
					buildArgs := append([]string{"build", target, "--release"}, dartDefineArgs()...)
					if err := shorebird.RunFlutter(".", patchSigningEnv(cpReleaseAppID), buildArgs...); err != nil {
						ui.Error("%v", err)
						return
					}
				} else {
					// iOS: the release artifact MUST come from the exact
					// build that ships to devices — independent compilations
					// produce different AOT snapshots, so the on-device diff
					// base would never match a separately-built binary.
					// Codesigning does NOT affect the extracted snapshot,
					// so the signed build produced here doubles as the
					// installable app (re-signing it later is safe too).
					if cpReleaseNoCodesign {
						ui.Info("Building iOS app with Shorebird's bundled Flutter (--no-codesign)...")
						buildArgs := append([]string{"build", "ios", "--release", "--no-codesign"}, dartDefineArgs()...)
						if err := shorebird.RunFlutter(".", patchSigningEnv(cpReleaseAppID), buildArgs...); err != nil {
							ui.Error("%v", err)
							return
						}
						ui.Muted("Note: sign/install the .app this build produced — patches can't apply to a separately-built binary.")
					} else {
						ui.Info("Building signed iOS app with Shorebird's bundled Flutter...")
						buildArgs := append([]string{"build", "ios", "--release"}, dartDefineArgs()...)
						if err := shorebird.RunFlutter(".", patchSigningEnv(cpReleaseAppID), buildArgs...); err != nil {
							ui.Error("%v", err)
							return
						}
					}
				}
			}
			if platform == "ANDROID" {
				libapps, err := shorebird.FindAndroidLibapps(".")
				if err != nil {
					ui.Error("%v", err)
					return
				}
				var arches []string
				for a := range libapps {
					arches = append(arches, a)
				}
				sort.Strings(arches)
				if cpReleaseArchitecture != "" {
					if _, ok := libapps[cpReleaseArchitecture]; !ok {
						ui.Error("architecture %s not in build (found: %s)", cpReleaseArchitecture, strings.Join(arches, ", "))
						return
					}
					arches = []string{cpReleaseArchitecture}
				}
				for _, a := range arches {
					uploads = append(uploads, archUpload{arch: a, path: libapps[a]})
				}
				ui.Muted("Auto-detected %d artifact(s): %s", len(uploads), strings.Join(arches, ", "))
			} else {
				// iOS release artifact = zip of the App binary plus its
				// link supplements (App.* files aot-tools needs at patch
				// time), the same layout Shorebird stores per-release.
				packed, err := packageIosReleaseArtifact("", ".")
				if err != nil {
					ui.Error("%v", err)
					return
				}
				artifact = packed
				ui.Muted("Packaged iOS release artifact: %s", artifact)
				if cpReleaseArchitecture == "" {
					cpReleaseArchitecture = "aarch64"
				}
			}
		} else if platform == "IOS" && !strings.HasSuffix(artifact, ".zip") {
			// User-supplied iOS artifact: accept .app/.xcarchive dirs and raw
			// App binaries — repackage into the zip layout patch expects.
			packed, err := packageIosReleaseArtifact(artifact, ".")
			if err != nil {
				ui.Error("%v", err)
				return
			}
			artifact = packed
			if cpReleaseArchitecture == "" {
				cpReleaseArchitecture = "aarch64"
			}
		}

		if len(uploads) == 0 {
			if _, err := os.Stat(artifact); err != nil {
				ui.Error("Artifact not found: %s", artifact)
				return
			}
			uploads = []archUpload{{arch: cpReleaseArchitecture, path: artifact}}
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

		ui.Info("Uploading release artifact(s)...")
		var resp *api.CodePushReleaseResponse
		for _, u := range uploads {
			resp, err = client.CodePushFlutterRelease(
				u.path, cpReleaseAppID, platform, cpReleaseVersion, u.arch,
				cpReleaseFlutterRevision, cpReleaseShorebirdAppID, cpReleaseChannel, cpReleaseNotes,
			)
			if err != nil {
				ui.Error("Failed to register release: %v", err)
				return
			}
		}

		// Record the packaged build's contents so `patch` can warn about
		// non-Dart changes (native code, assets, engine) it can't ship.
		// Only when this command produced the build — a --skip-build or
		// --artifact upload may not match the local build dir.
		if !cpReleaseSkipBuild && cpReleaseArtifact == "" {
			writeBuildManifest(".", platform, cpReleaseVersion)
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
	codepushFlutterReleaseCmd.Flags().BoolVar(&cpReleaseNoCodesign, "no-codesign", false, "iOS: build unsigned (for CI where the same .app is signed later — never recompile)")
	codepushFlutterReleaseCmd.Flags().StringVar(&cpReleaseFormat, "format", "apk", "Android build format: apk (install links) or aab (Play Store)")
	codepushFlutterReleaseCmd.Flags().StringArrayVar(&cpDartDefines, "dart-define", nil, "Dart compile-time constant KEY=VALUE (repeatable), passed to flutter build")
	codepushFlutterReleaseCmd.Flags().StringArrayVar(&cpDartDefineFiles, "dart-define-from-file", nil, "JSON file of dart-define values (repeatable), passed to flutter build")
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

		var diffs []api.FlutterArchDiff

		if cpPatchArtifact == "" {
			// --- Auto-diff flow ---
			// When --artifact is omitted, the CLI builds the patch and
			// locates the resulting diff automatically.
			//
			// Android auto-diff:
			//   1. Build libapp.so with Shorebird's bundled Flutter (a patch
			//      is just the same release build with new Dart code — the
			//      updater ships the binary diff, not a build recipe).
			//   2. Locate the freshly built libapp.so per architecture
			//   3. Download the release's per-arch libapp.so from AirBuild
			//   4. Use Shorebird's `patch` binary (public artifact download)
			//      to create the bidiff+zstd diff for each arch
			//   5. Upload the diffs to AirBuild in one patch

			if _, err := shorebird.BundledFlutter(); err != nil {
				ui.Error("%v", err)
				return
			}

			if platform == "ANDROID" {
				diffs, err = runAndroidAutoDiff(cpPatchReleaseVersion, cpPatchSkipBuild, cpPatchArchitecture, cpPatchChannel, cpPatchAppID)
			} else {
				var diffPath, hash string
				diffPath, hash, err = runIOSAutoDiff(cpPatchReleaseVersion, cpPatchSkipBuild, cpPatchChannel, cpPatchAppID)
				if cpPatchArchitecture == "" {
					cpPatchArchitecture = "aarch64"
				}
				if err == nil {
					diffs = []api.FlutterArchDiff{{Arch: cpPatchArchitecture, Path: diffPath, ArtifactSha256: hash}}
				}
			}
			if err != nil {
				ui.Error("%v", err)
				return
			}
		} else {
			artifact := cpPatchArtifact
			if _, err := os.Stat(artifact); err != nil {
				ui.Error("Artifact not found: %s", artifact)
				return
			}
			if platform == "IOS" && cpPatchArchitecture == "" {
				cpPatchArchitecture = "aarch64"
			}
			diffs = []api.FlutterArchDiff{{Arch: cpPatchArchitecture, Path: artifact, ArtifactSha256: cpPatchArtifactHash}}
		}

		for _, d := range diffs {
			if _, err := os.Stat(d.Path); err != nil {
				ui.Error("Artifact not found: %s", d.Path)
				return
			}
		}

		if diffs[0].ArtifactSha256 == "" {
			// Without the patched-artifact hash the server can't send a
			// `hash` the updater accepts — devices will download the patch
			// then reject it ("Update rejected: hash mismatch").
			ui.Muted("No patched-artifact hash supplied — pass --artifact-sha256 (sha256 of the new libapp.so) or use auto-diff.")
		}

		cfg := mustLoadConfig()
		client := api.New(cfg.APIURL, cfg.APIKey)

		ui.Info("Uploading patch...")
		resp, err := client.CodePushFlutterPatchMulti(
			diffs, cpPatchAppID, platform, cpPatchReleaseVersion, cpPatchChannel, cpPatchNotes,
		)
		if err != nil {
			ui.Error("Failed to create patch: %v", err)
			return
		}

		ui.Success("Patch #%d created (DRAFT, 0%% rollout)", resp.Update.PatchNumber)
		// Auto-generated diffs are temp files — clean up after upload.
		if cpPatchArtifact == "" {
			for _, d := range diffs {
				os.Remove(d.Path) //nolint:errcheck
			}
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
	codepushFlutterPatchCmd.Flags().StringArrayVar(&cpDartDefines, "dart-define", nil, "Dart compile-time constant KEY=VALUE (repeatable), passed to flutter build")
	codepushFlutterPatchCmd.Flags().StringArrayVar(&cpDartDefineFiles, "dart-define-from-file", nil, "JSON file of dart-define values (repeatable), passed to flutter build")
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

// patchSigningEnv returns the SHOREBIRD_PUBLIC_KEY env map for flutter
// builds. Shorebird's forked flutter_tools reads that env var and writes
// `patch_public_key` into the compiled shorebird.yaml asset (the yaml's own
// `patch_public_key` key is NOT copied — env only), which makes the
// on-device updater enforce `hash_signature` on every patch. Best-effort:
// returns nil when the key can't be fetched, keeping today's unsigned
// behavior.
func patchSigningEnv(appID string) map[string]string {
	cfg := mustLoadConfig()
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

// runAndroidAutoDiff implements the full Android auto-diff flow: rebuild
// libapp.so with Shorebird's bundled Flutter, download the release's
// per-architecture libapp.so from AirBuild, and create one binary diff per
// architecture using Shorebird's `patch` binary. Returns the diffs with
// their patched-artifact sha256s — the hashes on-device updaters verify
// after applying the diff.
func runAndroidAutoDiff(releaseVersion string, skipBuild bool, architecture, channel, appID string) ([]api.FlutterArchDiff, error) {
	// Step 1: Build the patch's libapp.so — the same release build with the
	// new Dart code, using the bundled (updater-capable) Flutter SDK.
	if !skipBuild {
		ui.Info("Building patch artifact with Shorebird's bundled Flutter...")
		buildArgs := append([]string{"build", "apk", "--release"}, dartDefineArgs()...)
		if err := shorebird.RunFlutter(".", patchSigningEnv(appID), buildArgs...); err != nil {
			return nil, fmt.Errorf("patch build failed: %w", err)
		}
	}

	// Warn if the patch build contains non-Dart changes the OTA can't ship
	// (native code, assets, or a different engine revision).
	warnNonDartChanges(".", "ANDROID", releaseVersion)

	// Step 2: Locate the freshly built patch libapp.so for every packaged
	// architecture.
	libapps, err := shorebird.FindAndroidLibapps(".")
	if err != nil {
		return nil, err
	}
	if architecture != "" {
		found, ok := libapps[architecture]
		if !ok {
			var have []string
			for a := range libapps {
				have = append(have, a)
			}
			return nil, fmt.Errorf("architecture %s not in build (found: %s)", architecture, strings.Join(have, ", "))
		}
		libapps = map[string]string{architecture: found}
	}
	var localArches []string
	for a := range libapps {
		localArches = append(localArches, a)
	}
	sort.Strings(localArches)
	ui.Muted("Found patch libapp.so for: %s", strings.Join(localArches, ", "))

	// Step 3: Locate Shorebird's `patch` binary (downloads it from
	// Shorebird's public artifact bucket on first use — no auth needed).
	patchBinary, err := shorebird.EnsurePatchBinary()
	if err != nil {
		return nil, err
	}
	ui.Muted("Using Shorebird patch binary: %s", patchBinary)

	// Step 4: Fetch the release's architecture list, then download each
	// arch's artifact and diff locally-built vs release libapp.so.
	cfg := mustLoadConfig()
	client := api.New(cfg.APIURL, cfg.APIKey)

	dlResp, err := client.CodePushFlutterReleaseDownload(
		appID, releaseVersion, "ANDROID", architecture, channel,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get release download URL: %w", err)
	}
	releaseArches := map[string]bool{}
	for _, a := range dlResp.Release.Architectures {
		releaseArches[a] = true
	}

	// Diff the arches the release actually has artifacts for — diffing
	// against an artifact of another arch produces a patch that can never
	// apply (its base doesn't match the device's libapp.so).
	var targets []string
	for _, a := range localArches {
		if len(releaseArches) == 0 || releaseArches[a] {
			targets = append(targets, a)
		} else {
			ui.Muted("Skipping %s — release %s has no artifact for it", a, releaseVersion)
		}
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("no architectures in common between this build (%s) and release %s (%v)",
			strings.Join(localArches, ", "), releaseVersion, dlResp.Release.Architectures)
	}
	for a := range releaseArches {
		if _, ok := libapps[a]; !ok {
			ui.Muted("Note: release covers %s but this build doesn't include it — devices on %s won't get this patch", a, a)
		}
	}

	tmpDir, err := os.MkdirTemp("", "airbuild-patch-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tmpDir) //nolint:errcheck

	var diffs []api.FlutterArchDiff
	for _, arch := range targets {
		ui.Info("Diffing %s...", arch)
		resp, err := client.CodePushFlutterReleaseDownload(
			appID, releaseVersion, "ANDROID", arch, channel,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to get release download URL for %s: %w", arch, err)
		}
		releaseLibappPath := filepath.Join(tmpDir, "release-libapp-"+arch+".so")
		if err := client.DownloadFile(resp.DownloadUrl, releaseLibappPath); err != nil {
			return nil, fmt.Errorf("failed to download release libapp.so for %s: %w", arch, err)
		}

		// The diff goes in its own temp file (not tmpDir — that's deleted
		// on return, and the caller needs the path to survive the upload).
		diffFile, err := os.CreateTemp("", "airbuild-patch-*.diff")
		if err != nil {
			return nil, fmt.Errorf("failed to create diff file: %w", err)
		}
		diffPath := diffFile.Name()
		diffFile.Close() //nolint:errcheck
		if err := shorebird.CreateDiff(patchBinary, releaseLibappPath, libapps[arch], diffPath); err != nil {
			return nil, fmt.Errorf("failed to create diff for %s: %w", arch, err)
		}
		if err := shorebird.ValidateDiffFile(diffPath); err != nil {
			return nil, fmt.Errorf("generated %s diff failed validation: %w", arch, err)
		}

		// The updater verifies the *patched artifact* hash (the new
		// libapp.so) after applying the diff — send it alongside.
		artifactHash, err := sha256File(libapps[arch])
		if err != nil {
			return nil, fmt.Errorf("failed to hash %s patch artifact: %w", arch, err)
		}
		ui.Muted("Created %s diff: %s", arch, diffPath)
		diffs = append(diffs, api.FlutterArchDiff{Arch: arch, Path: diffPath, ArtifactSha256: artifactHash})
	}
	return diffs, nil
}

// packageIosReleaseArtifact produces the zip release artifact AirBuild
// stores for iOS: the App Mach-O binary plus its link supplements. When
// src is empty, the binary is auto-detected from the latest build output
// under projectDir; otherwise src may be an .app/.xcarchive directory or
// a raw App binary. Returns the path to a temp zip.
func packageIosReleaseArtifact(src, projectDir string) (string, error) {
	var appPath string
	var supplements []string
	if src == "" {
		found, err := shorebird.FindIosAppBinary(projectDir)
		if err != nil {
			return "", err
		}
		appPath = found
		supplements = shorebird.CollectIosSupplements(shorebird.IosSupplementDir(projectDir))
	} else {
		info, err := os.Stat(src)
		if err != nil {
			return "", fmt.Errorf("artifact not found: %s", src)
		}
		switch {
		case info.IsDir() && (strings.HasSuffix(src, ".app") || strings.HasSuffix(src, ".xcarchive")):
			matches, err := filepath.Glob(filepath.Join(src, "**", "Frameworks", "App.framework", "App"))
			if err != nil || len(matches) == 0 {
				return "", fmt.Errorf("no Frameworks/App.framework/App found inside %s", src)
			}
			appPath = matches[0]
			// Supplements may sit beside the App binary or under
			// build/ios/shorebird — collect what exists.
			supplements = append(supplements, shorebird.CollectIosSupplements(filepath.Dir(appPath))...)
			supplements = append(supplements, shorebird.CollectIosSupplements(shorebird.IosSupplementDir(projectDir))...)
		case info.IsDir():
			return "", fmt.Errorf("unsupported artifact directory %s — pass a .app, .xcarchive, zip, or App binary", src)
		default:
			appPath = src
			supplements = shorebird.CollectIosSupplements(filepath.Dir(src))
		}
	}

	out, err := os.CreateTemp("", "airbuild-ios-release-*.zip")
	if err != nil {
		return "", err
	}
	out.Close() //nolint:errcheck
	if err := shorebird.CreateIosReleaseArtifact(appPath, supplements, out.Name()); err != nil {
		os.Remove(out.Name()) //nolint:errcheck
		return "", err
	}
	if len(supplements) == 0 {
		ui.Muted("No link supplements found — iOS patches built against this release may link less efficiently")
	}
	return out.Name(), nil
}

// runIOSAutoDiff implements Shorebird's iOS patch pipeline fully locally:
//
//  1. Rebuild the app with the bundled Flutter (`flutter build ios
//     --release --no-codesign`) — an unsigned build is fine since the
//     Dart snapshot regions are signature-independent.
//  2. gen_snapshot recompiles app.dill into an ELF AOT snapshot (out.aot).
//  3. aot-tools `link` minimizes the diff between the release snapshot and
//     the new one, producing the vmcode the device will run.
//  4. aot-tools `dump_blobs` extracts the release's snapshot regions —
//     the same bytes the on-device updater uses as its patch base.
//  5. Shorebird's `patch` binary diffs (dump_blobs → vmcode).
//
// Returns the diff path and sha256(vmcode) — the artifact hash devices
// verify after applying the patch.
func runIOSAutoDiff(releaseVersion string, skipBuild bool, channel, appID string) (string, string, error) {
	// Step 1: build, remembering when it started so we can find the app.dill
	// it produced (Shorebird looks for an app.dill newer than the build).
	buildStart := time.Now()
	if !skipBuild {
		ui.Info("Building iOS patch with Shorebird's bundled Flutter (--no-codesign)...")
		buildArgs := append([]string{"build", "ios", "--release", "--no-codesign"}, dartDefineArgs()...)
		if err := shorebird.RunFlutter(".", patchSigningEnv(appID), buildArgs...); err != nil {
			return "", "", fmt.Errorf("patch build failed: %w", err)
		}
		warnNonDartChanges(".", "IOS", releaseVersion)
	} else {
		buildStart = time.Time{} // accept any existing app.dill
	}

	// Step 2: resolve the toolchain — all local, no Shorebird account.
	appDill, err := shorebird.FindRecentAppDill(".", buildStart)
	if err != nil {
		return "", "", err
	}
	// aot-tools runs gen_snapshot with cwd=workDir — the kernel path must
	// be absolute or it resolves relative to the wrong directory.
	if appDill, err = filepath.Abs(appDill); err != nil {
		return "", "", err
	}
	ui.Muted("Kernel snapshot: %s", appDill)
	genSnapshot, err := shorebird.IosEngineTool("gen_snapshot_arm64")
	if err != nil {
		return "", "", err
	}
	analyzeSnapshot, err := shorebird.IosEngineTool("analyze_snapshot_arm64")
	if err != nil {
		return "", "", err
	}
	aotTools, err := shorebird.EnsureAotTools()
	if err != nil {
		return "", "", err
	}
	patchBinary, err := shorebird.EnsurePatchBinary()
	if err != nil {
		return "", "", err
	}

	// Step 3: fetch the release artifact (App + supplements) from AirBuild.
	cfg := mustLoadConfig()
	client := api.New(cfg.APIURL, cfg.APIKey)
	ui.Info("Downloading release %s App binary from AirBuild...", releaseVersion)
	dlResp, err := client.CodePushFlutterReleaseDownload(appID, releaseVersion, "IOS", "aarch64", channel)
	if err != nil {
		return "", "", fmt.Errorf("failed to get release download URL: %w", err)
	}

	workDir, err := os.MkdirTemp("", "airbuild-ios-patch-*")
	if err != nil {
		return "", "", err
	}
	defer os.RemoveAll(workDir) //nolint:errcheck

	zipPath := filepath.Join(workDir, "release.zip")
	if err := client.DownloadFile(dlResp.DownloadUrl, zipPath); err != nil {
		return "", "", fmt.Errorf("failed to download release artifact: %w", err)
	}
	releaseDir := filepath.Join(workDir, "release")
	if err := os.MkdirAll(releaseDir, 0o755); err != nil {
		return "", "", err
	}
	releaseApp, err := shorebird.ExtractIosReleaseArtifact(zipPath, releaseDir)
	if err != nil {
		return "", "", fmt.Errorf("failed to extract release artifact: %w", err)
	}
	ui.Muted("Release App: %s", releaseApp)

	// Step 4: recompile the kernel into an ELF AOT snapshot.
	aotOut := filepath.Join(workDir, "out.aot")
	ui.Info("Compiling AOT snapshot (gen_snapshot)...")
	if err := shorebird.RunGenSnapshot(genSnapshot, appDill, aotOut); err != nil {
		return "", "", err
	}

	// Step 5: place the patch build's link supplements beside out.aot,
	// renamed to out.* — aot-tools looks them up by snapshot basename.
	// The release's supplements already sit beside its App binary inside
	// releaseDir (they were zipped in at release time).
	supplementDir := shorebird.IosSupplementDir(".")
	if n, err := shorebird.CopyIosSupplements(supplementDir, workDir, "out"); err != nil {
		return "", "", err
	} else if n == 0 {
		ui.Muted("No link supplements under %s — link percentage may be low", supplementDir)
	}

	// Step 6: link — produces the vmcode the device will execute.
	vmcode := filepath.Join(workDir, "out.vmcode")
	ui.Info("Linking AOT snapshots (aot-tools link)...")
	if err := shorebird.RunAotTools(aotTools, workDir,
		"link",
		"--base="+releaseApp,
		"--patch="+aotOut,
		"--analyze-snapshot="+analyzeSnapshot,
		"--output="+vmcode,
		"--gen-snapshot="+genSnapshot,
		"--kernel="+appDill,
		"--reporter=json",
		"--redirect-to="+filepath.Join(workDir, "link.jsonl"),
	); err != nil {
		return "", "", err
	}

	// Step 7: diff base = the release's snapshot regions, matching what the
	// on-device updater extracts from the installed app.
	diffBase := filepath.Join(workDir, "diff_base")
	if ok, err := shorebird.AotToolsSupports(aotTools, workDir, "dump_blobs"); err != nil {
		return "", "", err
	} else if ok {
		ui.Info("Generating diff base (aot-tools dump_blobs)...")
		if err := shorebird.RunAotTools(aotTools, workDir,
			"dump_blobs",
			"--analyze-snapshot="+analyzeSnapshot,
			"--output="+diffBase,
			"--snapshot="+releaseApp,
		); err != nil {
			return "", "", err
		}
	} else {
		return "", "", fmt.Errorf("aot-tools doesn't support dump_blobs — update Shorebird (`shorebird upgrade`) and retry")
	}

	// Step 8: binary diff + artifact hash (sha256 of the vmcode the device
	// produces after applying the patch).
	diffFile, err := os.CreateTemp("", "airbuild-patch-*.diff")
	if err != nil {
		return "", "", err
	}
	diffPath := diffFile.Name()
	diffFile.Close() //nolint:errcheck
	ui.Info("Creating binary diff...")
	if err := shorebird.CreateDiff(patchBinary, diffBase, vmcode, diffPath); err != nil {
		return "", "", fmt.Errorf("failed to create diff: %w", err)
	}
	if err := shorebird.ValidateDiffFile(diffPath); err != nil {
		return "", "", fmt.Errorf("generated diff failed validation: %w", err)
	}
	artifactHash, err := sha256File(vmcode)
	if err != nil {
		return "", "", fmt.Errorf("failed to hash vmcode: %w", err)
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

// --- Build manifests: native/asset change detection ---
//
// A Dart patch can only replace the compiled Dart snapshot (libapp.so /
// App binary). Native code, assets, manifests, and engine changes are NOT
// patchable — shipping a patch whose other files changed produces a silent
// no-op or a subtle mismatch. At release time we record a manifest of the
// packaged build's contents; at patch time we compare and warn.

type buildManifest map[string]string // archive-relative path → sha256

func manifestPath(projectDir, platform, version string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			return r
		}
		return '_'
	}, version)
	return filepath.Join(projectDir, ".airbuild", "manifests", strings.ToLower(platform)+"-"+safe+".json")
}

// classifyManifestEntry buckets a packaged file by what an OTA patch can
// and cannot change. "" means "ignore" (packaging/signing churn that
// legitimately differs between builds).
func classifyManifestEntry(platform, name string) string {
	switch platform {
	case "ANDROID":
		switch {
		case strings.HasPrefix(name, "META-INF/"):
			return "" // signing output
		case strings.HasSuffix(name, "/libapp.so"), name == "libapp.so":
			return "dart"
		case strings.HasSuffix(name, "/libflutter.so"), strings.HasSuffix(name, "/libflutter_package.so"):
			return "engine"
		case strings.HasPrefix(name, "assets/flutter_assets/"):
			return "asset"
		case strings.HasSuffix(name, ".dex"),
			name == "AndroidManifest.xml", name == "resources.arsc",
			strings.HasPrefix(name, "res/"), strings.HasPrefix(name, "kotlin/"),
			strings.HasPrefix(name, "lib/"):
			return "native"
		default:
			return "asset"
		}
	case "IOS":
		switch {
		case strings.HasPrefix(name, "_CodeSignature/"),
			name == "embedded.mobileprovision", name == "PkgInfo",
			name == ".DS_Store", strings.HasSuffix(name, "/.DS_Store"):
			return ""
		case strings.HasSuffix(name, "App.framework/App"):
			return "dart"
		case strings.Contains(name, "App.framework/flutter_assets/"):
			return "asset"
		case strings.Contains(name, "Flutter.framework/"):
			return "engine"
		default:
			return "native"
		}
	}
	return "native"
}

// zipManifest hashes every entry of a zip archive (APK/AAB).
func zipManifest(path string) (buildManifest, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer zr.Close() //nolint:errcheck
	m := buildManifest{}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		h := sha256.New()
		if _, err := io.Copy(h, rc); err != nil {
			rc.Close() //nolint:errcheck
			return nil, err
		}
		rc.Close() //nolint:errcheck
		m[f.Name] = hex.EncodeToString(h.Sum(nil))
	}
	return m, nil
}

// dirManifest hashes every file under root, keyed by path relative to root.
func dirManifest(root string) (buildManifest, error) {
	m := buildManifest{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr
		}
		h, err := sha256File(path)
		if err != nil {
			return nil // skip unreadable files
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		m[filepath.ToSlash(rel)] = h
		return nil
	})
	return m, err
}

// writeBuildManifest persists the release build's manifest for the patch
// command to compare against. Best-effort — silently skipped when the
// packaged artifact can't be found (e.g. --artifact/--skip-build flows).
func writeBuildManifest(projectDir, platform, version string) {
	var m buildManifest
	var err error
	if platform == "ANDROID" {
		for _, candidate := range []string{
			filepath.Join(projectDir, "build", "app", "outputs", "flutter-apk", "app-release.apk"),
			filepath.Join(projectDir, "build", "app", "outputs", "bundle", "release", "app-release.aab"),
		} {
			if m, err = zipManifest(candidate); err == nil {
				break
			}
		}
	} else {
		// iOS: hash the built .app directory.
		for _, candidate := range []string{
			filepath.Join(projectDir, "build", "ios", "iphoneos", "Runner.app"),
			filepath.Join(projectDir, "build", "ios", "ipa", "*.app"),
		} {
			matches, _ := filepath.Glob(candidate)
			for _, dir := range matches {
				if m, err = dirManifest(dir); err == nil && len(m) > 0 {
					break
				}
			}
			if len(m) > 0 {
				break
			}
		}
	}
	if len(m) == 0 {
		return
	}
	path := manifestPath(projectDir, platform, version)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o644)
}

// loadBuildManifest reads the manifest recorded at release time. Returns
// nil when absent (foreign machine, --skip-build release, etc).
func loadBuildManifest(projectDir, platform, version string) buildManifest {
	data, err := os.ReadFile(manifestPath(projectDir, platform, version))
	if err != nil {
		return nil
	}
	var m buildManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil
	}
	return m
}

// warnNonDartChanges compares the release build's manifest against the
// patch build's and warns about changes an OTA patch cannot deliver:
// native code, bundled assets, and (critically) engine changes.
func warnNonDartChanges(projectDir, platform, releaseVersion string) {
	old := loadBuildManifest(projectDir, platform, releaseVersion)
	if len(old) == 0 {
		return // nothing to compare — stay quiet
	}
	var fresh buildManifest
	var err error
	if platform == "ANDROID" {
		fresh, err = zipManifest(filepath.Join(projectDir, "build", "app", "outputs", "flutter-apk", "app-release.apk"))
		if err != nil {
			fresh, err = zipManifest(filepath.Join(projectDir, "build", "app", "outputs", "bundle", "release", "app-release.aab"))
		}
	} else {
		fresh, err = dirManifest(filepath.Join(projectDir, "build", "ios", "iphoneos", "Runner.app"))
	}
	if err != nil || len(fresh) == 0 {
		return
	}

	var engineChanged, nativeChanged, assetChanged []string
	names := map[string]bool{}
	for n := range old {
		names[n] = true
	}
	for n := range fresh {
		names[n] = true
	}
	for name := range names {
		if old[name] == fresh[name] {
			continue
		}
		switch classifyManifestEntry(platform, name) {
		case "":
			continue // packaging churn — not a real change
		case "dart":
			continue // the point of the patch
		case "engine":
			engineChanged = append(engineChanged, name)
		case "native":
			nativeChanged = append(nativeChanged, name)
		case "asset":
			assetChanged = append(assetChanged, name)
		}
	}

	show := func(label string, files []string) {
		sort.Strings(files)
		ui.Error("%s", label)
		const max = 8
		for i, f := range files {
			if i >= max {
				ui.Muted("    ... and %d more", len(files)-max)
				break
			}
			ui.Muted("    %s", f)
		}
	}
	if len(engineChanged) > 0 {
		show("⚠ Flutter engine changed since this release — patches built on a different engine revision will NOT apply. Re-register the release with this engine, or rebuild with the release's flutter-revision.", engineChanged)
	}
	if len(nativeChanged) > 0 {
		show("⚠ Native files changed since the release — OTA patches only ship Dart code; these changes will NOT reach devices:", nativeChanged)
	}
	if len(assetChanged) > 0 {
		show("⚠ Asset files changed since the release — assets are not patchable; they will NOT reach devices:", assetChanged)
	}
}
