package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/airbuild/airbuild-cli/internal/api"
	"github.com/airbuild/airbuild-cli/internal/shorebird"
	"github.com/airbuild/airbuild-cli/internal/ui"
	"github.com/spf13/cobra"
)

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
	RunE: func(cmd *cobra.Command, args []string) error {
		platformArg := strings.ToLower(args[0])
		platform, err := normalizeFlutterPlatform(platformArg)
		if err != nil {
			return failf("%v", err)
		}

		var ok bool
		cpReleaseAppID, ok = resolveAppID(cpReleaseAppID)
		if !ok {
			return errSilent
		}
		if cpReleaseVersion == "" {
			return failf("--version is required")
		}
		switch cpReleaseFormat {
		case "apk", "aab", "appbundle":
		default:
			return failf("--format must be apk or aab, got: %s", cpReleaseFormat)
		}

		artifact := cpReleaseArtifact
		// Per-arch artifacts to register — multi-ABI Android builds upload
		// one libapp.so per architecture into the same release row.
		type archUpload struct{ arch, path string }
		var uploads []archUpload
		if artifact == "" {
			if !cpReleaseSkipBuild {
				if _, err := shorebird.BundledFlutter(); err != nil {
					return failf("%v", err)
				}
				if platform == "ANDROID" {
					target := "apk"
					if cpReleaseFormat == "aab" || cpReleaseFormat == "appbundle" {
						target = "appbundle"
					}
					ui.Info("Building %s with Shorebird's bundled Flutter (fully local — no Shorebird account needed)...", target)
					buildArgs := append([]string{"build", target, "--release"}, dartDefineArgs()...)
					if err := shorebird.RunFlutter(".", patchSigningEnv(cpReleaseAppID), buildArgs...); err != nil {
						return failf("%v", err)
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
							return failf("%v", err)
						}
						ui.Muted("Note: sign/install the .app this build produced — patches can't apply to a separately-built binary.")
					} else {
						ui.Info("Building signed iOS app with Shorebird's bundled Flutter...")
						buildArgs := append([]string{"build", "ios", "--release"}, dartDefineArgs()...)
						if err := shorebird.RunFlutter(".", patchSigningEnv(cpReleaseAppID), buildArgs...); err != nil {
							return failf("%v", err)
						}
					}
				}
			}
			if platform == "ANDROID" {
				libapps, err := shorebird.FindAndroidLibapps(".")
				if err != nil {
					return failf("%v", err)
				}
				var arches []string
				for a := range libapps {
					arches = append(arches, a)
				}
				sort.Strings(arches)
				if cpReleaseArchitecture != "" {
					if _, ok := libapps[cpReleaseArchitecture]; !ok {
						return failf("architecture %s not in build (found: %s)", cpReleaseArchitecture, strings.Join(arches, ", "))
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
					return failf("%v", err)
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
				return failf("%v", err)
			}
			artifact = packed
			if cpReleaseArchitecture == "" {
				cpReleaseArchitecture = "aarch64"
			}
		}

		if len(uploads) == 0 {
			if _, err := os.Stat(artifact); err != nil {
				return failf("Artifact not found: %s", artifact)
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

		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		client := api.New(cfg.APIURL, cfg.APIKey)

		ui.Info("Uploading release artifact(s)...")
		var resp *api.CodePushReleaseResponse
		for _, u := range uploads {
			resp, err = client.CodePushFlutterRelease(
				u.path, cpReleaseAppID, platform, cpReleaseVersion, u.arch,
				cpReleaseFlutterRevision, cpReleaseShorebirdAppID, cpReleaseChannel, cpReleaseNotes,
			)
			if err != nil {
				return failf("Failed to register release: %v", err)
			}
		}

		// Record the packaged build's contents so `patch` can warn about
		// non-Dart changes (native code, assets, engine) it can't ship.
		// Only when this command produced the build — a --skip-build or
		// --artifact upload may not match the local build dir.
		if !cpReleaseSkipBuild && cpReleaseArtifact == "" {
			writeBuildManifest(".", platform, cpReleaseVersion)
			writeDefinesManifest(".", platform, cpReleaseVersion)
		}

		if jsonOutput {
			ui.PrintJSON(resp)
			return nil
		}

		ui.Success("Release registered")
		ui.Table([]string{"Field", "Value"}, [][]string{
			{"Release ID", resp.Release.ID},
			{"Version", resp.Release.Version},
			{"Platform", resp.Release.Platform},
			{"Channel", resp.Release.Channel},
			{"Architectures", strings.Join(resp.Release.Architectures, ", ")},
		})
		return nil
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
	RunE: func(cmd *cobra.Command, args []string) error {
		platformArg := strings.ToLower(args[0])
		platform, err := normalizeFlutterPlatform(platformArg)
		if err != nil {
			return failf("%v", err)
		}

		var ok bool
		cpPatchAppID, ok = resolveAppID(cpPatchAppID)
		if !ok {
			return errSilent
		}
		if cpPatchReleaseVersion == "" {
			return failf("--release-version is required")
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
				return failf("%v", err)
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
				return failf("%v", err)
			}
		} else {
			artifact := cpPatchArtifact
			if _, err := os.Stat(artifact); err != nil {
				return failf("Artifact not found: %s", artifact)
			}
			if platform == "IOS" && cpPatchArchitecture == "" {
				cpPatchArchitecture = "aarch64"
			}
			diffs = []api.FlutterArchDiff{{Arch: cpPatchArchitecture, Path: artifact, ArtifactSha256: cpPatchArtifactHash}}
		}

		for _, d := range diffs {
			if _, err := os.Stat(d.Path); err != nil {
				return failf("Artifact not found: %s", d.Path)
			}
		}

		if diffs[0].ArtifactSha256 == "" {
			// Without the patched-artifact hash the server can't send a
			// `hash` the updater accepts — devices will download the patch
			// then reject it ("Update rejected: hash mismatch").
			ui.Muted("No patched-artifact hash supplied — pass --artifact-sha256 (sha256 of the new libapp.so) or use auto-diff.")
		}

		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		client := api.New(cfg.APIURL, cfg.APIKey)

		ui.Info("Uploading patch...")
		resp, err := client.CodePushFlutterPatchMulti(
			diffs, cpPatchAppID, platform, cpPatchReleaseVersion, cpPatchChannel, cpPatchNotes,
		)
		if err != nil {
			return failf("Failed to create patch: %v", err)
		}

		// Auto-generated diffs are temp files — clean up after upload.
		if cpPatchArtifact == "" {
			for _, d := range diffs {
				os.Remove(d.Path) //nolint:errcheck
			}
		}

		if jsonOutput {
			ui.PrintJSON(resp)
			return nil
		}

		ui.Success("Patch #%d created (DRAFT, 0%% rollout)", resp.Update.PatchNumber)
		ui.Info("Promote it with: airbuild codepush flutter promote --patch %d --channel %s --rollout 25",
			resp.Update.PatchNumber, valueOr(cpPatchChannel, "production"))
		return nil
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
	RunE: func(cmd *cobra.Command, args []string) error {
		var ok bool
		cpPromoteAppID, ok = resolveAppID(cpPromoteAppID)
		if !ok {
			return errSilent
		}
		if cpPromoteUpdateID == "" && (cpPromoteReleaseVersion == "" || cpPromotePlatform == "" || cpPromotePatchNumber == 0) {
			return failf("Provide --update-id, or --release-version + --platform + --patch")
		}
		platform, err := normalizePlatformFlag(cpPromotePlatform)
		if err != nil {
			return failf("%v", err)
		}
		if err := validateRolloutPercent(cpPromoteRollout); err != nil {
			return failf("%v", err)
		}

		cfg, err := loadConfig()
		if err != nil {
			return err
		}
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
			return failf("Failed to promote patch: %v", err)
		}

		if jsonOutput {
			ui.PrintJSON(resp)
			return nil
		}
		ui.Success("Patch #%d promoted to %s at %d%% rollout", resp.Update.PatchNumber, resp.Update.Channel, resp.Update.RolloutPercent)
		return nil
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
	RunE: func(cmd *cobra.Command, args []string) error {
		var ok bool
		cpRollbackAppID, ok = resolveAppID(cpRollbackAppID)
		if !ok {
			return errSilent
		}
		if cpRollbackUpdateID == "" && (cpRollbackReleaseVersion == "" || cpRollbackPlatform == "" || cpRollbackPatchNumber == 0) {
			return failf("Provide --update-id, or --release-version + --platform + --patch")
		}
		platform, err := normalizePlatformFlag(cpRollbackPlatform)
		if err != nil {
			return failf("%v", err)
		}

		cfg, err := loadConfig()
		if err != nil {
			return err
		}
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
			return failf("Failed to rollback patch: %v", err)
		}

		if jsonOutput {
			ui.PrintJSON(resp)
			return nil
		}
		ui.Success("Patch #%d rolled back", resp.Update.PatchNumber)
		return nil
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
	RunE: func(cmd *cobra.Command, args []string) error {
		var ok bool
		cpStatusAppID, ok = resolveAppID(cpStatusAppID)
		if !ok {
			return errSilent
		}

		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		client := api.New(cfg.APIURL, cfg.APIKey)

		resp, err := client.CodePushFlutterStatus(cpStatusAppID)
		if err != nil {
			return failf("Failed to fetch status: %v", err)
		}

		if jsonOutput {
			ui.PrintJSON(resp)
			return nil
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
			return nil
		}

		for _, r := range resp.Releases {
			ui.Header("Release %s (%s, %s)", r.Version, r.Platform, r.Channel)
			if len(r.Updates) == 0 {
				ui.Muted("  No patches yet. Create one with: airbuild codepush flutter patch %s --release-version %s", strings.ToLower(r.Platform), r.Version)
				continue
			}
			rows := make([][]string, 0, len(r.Updates))
			for _, u := range r.Updates {
				status := u.Status
				if u.AutoPaused {
					status += " (auto-paused)"
				}
				rows = append(rows, []string{
					fmt.Sprintf("#%d", u.PatchNumber),
					status,
					fmt.Sprintf("%d%%", u.RolloutPercent),
					u.Channel,
					u.CreatedAt,
				})
			}
			ui.Table([]string{"Patch", "Status", "Rollout", "Channel", "Created"}, rows)
			fmt.Println()
		}
		return nil
	},
}

func init() {
	codepushFlutterStatusCmd.Flags().StringVar(&cpStatusAppID, "app", "", "App ID (defaults to .airbuild.json)")
	codepushFlutterCmd.AddCommand(codepushFlutterStatusCmd)
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
	// (native code, assets, or a different engine revision), and if the
	// dart-define constants differ from what the release was built with.
	if !skipBuild {
		warnDartDefineDrift(".", "ANDROID", releaseVersion)
	}
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
	cfg, err := loadConfig()
	if err != nil {
		return nil, err
	}
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
		warnDartDefineDrift(".", "IOS", releaseVersion)
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
	cfg, err := loadConfig()
	if err != nil {
		return "", "", err
	}
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
