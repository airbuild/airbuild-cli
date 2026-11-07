package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/airbuild/airbuild-cli/internal/api"
	"github.com/airbuild/airbuild-cli/internal/expo"
	"github.com/airbuild/airbuild-cli/internal/ui"
	"github.com/spf13/cobra"
)

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
	RunE: func(cmd *cobra.Command, args []string) error {
		var ok bool
		cpRnAppID, ok = resolveAppID(cpRnAppID)
		if !ok {
			return errSilent
		}
		if cpRnPlatform == "" || cpRnRuntimeVersion == "" {
			return failf("--platform and --runtime-version are required")
		}
		platform, err := normalizeFlutterPlatform(cpRnPlatform) // ANDROID/IOS normalization is framework-agnostic
		if err != nil {
			return failf("%v", err)
		}
		expoPlatform := strings.ToLower(cpRnPlatform)

		if !cpRnSkipExport {
			if !expo.IsInstalled() {
				return failf("npx not found on PATH. Install Node.js, or pass --skip-export if you already ran `expo export`.")
			}
			ui.Info("Running `npx expo export --output-dir %s --platform %s`...", cpRnOutputDir, expoPlatform)
			if err := expo.Run(".", "export", "--output-dir", cpRnOutputDir, "--platform", expoPlatform); err != nil {
				return failf("%v", err)
			}
		}

		meta, err := expo.ParseMetadata(cpRnOutputDir)
		if err != nil {
			return failf("%v", err)
		}
		entries, err := expo.BuildManifestEntries(cpRnOutputDir, meta, expoPlatform)
		if err != nil {
			return failf("%v", err)
		}

		apiEntries := make([]api.ReactNativeManifestEntry, 0, len(entries))
		for _, e := range entries {
			if _, err := os.Stat(e.FilePath); err != nil {
				return failf("File referenced in metadata.json not found: %s", e.FilePath)
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

		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		client := api.New(cfg.APIURL, cfg.APIKey)

		ui.Info("Uploading %d file(s) (bundle + assets)...", len(apiEntries))
		resp, err := client.CodePushReactNativePublish(apiEntries, cpRnAppID, platform, cpRnRuntimeVersion, cpRnChannel, cpRnNotes)
		if err != nil {
			return failf("Failed to publish update: %v", err)
		}

		if jsonOutput {
			ui.PrintJSON(resp)
			return nil
		}

		ui.Success("Update published (DRAFT, 0%% rollout)")
		ui.Table([]string{"Field", "Value"}, [][]string{
			{"Update ID", resp.Update.ID},
			{"Channel", resp.Update.Channel},
			{"Assets", fmt.Sprintf("%d", len(resp.Update.Assets))},
		})
		ui.Info("Promote it with: airbuild codepush react-native promote --update-id %s --channel %s --rollout 25",
			resp.Update.ID, valueOr(cpRnChannel, "production"))
		return nil
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
	RunE: func(cmd *cobra.Command, args []string) error {
		var ok bool
		cpRnPromoteAppID, ok = resolveAppID(cpRnPromoteAppID)
		if !ok {
			return errSilent
		}
		if cpRnPromoteUpdateID == "" && (cpRnPromotePlatform == "" || cpRnPromoteRuntimeVersion == "") {
			return failf("Provide --update-id, or --platform + --runtime-version")
		}
		platform, err := normalizePlatformFlag(cpRnPromotePlatform)
		if err != nil {
			return failf("%v", err)
		}
		if err := validateRolloutPercent(cpRnPromoteRollout); err != nil {
			return failf("%v", err)
		}

		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		client := api.New(cfg.APIURL, cfg.APIKey)

		resp, err := client.CodePushReactNativePromote(
			cpRnPromoteAppID, cpRnPromoteUpdateID, platform, cpRnPromoteRuntimeVersion,
			cpRnPromoteChannel, cpRnPromoteRollout,
		)
		if err != nil {
			return failf("Failed to promote update: %v", err)
		}

		if jsonOutput {
			ui.PrintJSON(resp)
			return nil
		}
		ui.Success("Update promoted to %s at %d%% rollout", resp.Update.Channel, resp.Update.RolloutPercent)
		return nil
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
	RunE: func(cmd *cobra.Command, args []string) error {
		var ok bool
		cpRnRollbackAppID, ok = resolveAppID(cpRnRollbackAppID)
		if !ok {
			return errSilent
		}
		if cpRnRollbackUpdateID == "" {
			return failf("--update-id is required")
		}

		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		client := api.New(cfg.APIURL, cfg.APIKey)

		resp, err := client.CodePushReactNativeRollback(cpRnRollbackAppID, cpRnRollbackUpdateID)
		if err != nil {
			return failf("Failed to rollback update: %v", err)
		}

		if jsonOutput {
			ui.PrintJSON(resp)
			return nil
		}
		ui.Success("Update %s rolled back", resp.Update.ID)
		return nil
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
	RunE: func(cmd *cobra.Command, args []string) error {
		var ok bool
		cpRnStatusAppID, ok = resolveAppID(cpRnStatusAppID)
		if !ok {
			return errSilent
		}

		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		client := api.New(cfg.APIURL, cfg.APIKey)

		resp, err := client.CodePushReactNativeStatus(cpRnStatusAppID)
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
			ui.Muted("No releases yet. Publish one with: airbuild codepush react-native publish --platform android --runtime-version 1.0.0")
			return nil
		}

		for _, r := range resp.Releases {
			ui.Header("Runtime %s (%s, %s)", r.Version, r.Platform, r.Channel)
			if len(r.Updates) == 0 {
				ui.Muted("  No updates yet. Publish one with: airbuild codepush react-native publish --platform %s --runtime-version %s", strings.ToLower(r.Platform), r.Version)
				continue
			}
			rows := make([][]string, 0, len(r.Updates))
			for _, u := range r.Updates {
				status := u.Status
				if u.AutoPaused {
					status += " (auto-paused)"
				}
				rows = append(rows, []string{
					u.ID,
					status,
					fmt.Sprintf("%d%%", u.RolloutPercent),
					u.Channel,
					fmt.Sprintf("%d", len(u.Assets)),
					u.CreatedAt,
				})
			}
			ui.Table([]string{"Update ID", "Status", "Rollout", "Channel", "Assets", "Created"}, rows)
			fmt.Println()
		}
		return nil
	},
}

func init() {
	codepushReactNativeStatusCmd.Flags().StringVar(&cpRnStatusAppID, "app", "", "App ID (defaults to .airbuild.json)")
	codepushReactNativeCmd.AddCommand(codepushReactNativeStatusCmd)
}
