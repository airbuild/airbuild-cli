package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/airbuild/cli/internal/api"
	"github.com/airbuild/cli/internal/ui"
	"github.com/spf13/cobra"
)

var uploadAppID string
var uploadPlatform string
var uploadReleaseNotes string

var uploadCmd = &cobra.Command{
	Use:   "upload <file>",
	Short: "Upload an IPA/APK build to AirBuild",
	Long: `Upload a build file (.ipa or .apk) to a specific app on AirBuild.

The platform is auto-detected from the file extension (.ipa → iOS, .apk → Android).
You can override with --platform if needed.

Examples:
  airbuild upload ./build/app-release.apk --app-id app_xxx
  airbuild upload ./MyApp.ipa --app-id app_xxx --release-notes "Bug fixes"`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		filePath := args[0]

		// Validate file exists
		info, err := os.Stat(filePath)
		if err != nil {
			ui.Error("File not found: %s", filePath)
			return
		}

		// Validate file extension
		ext := strings.ToLower(filepath.Ext(filePath))
		if ext != ".ipa" && ext != ".apk" {
			ui.Error("File must be .ipa or .apk, got: %s", ext)
			return
		}

		// Auto-detect platform if not specified
		platform := uploadPlatform
		if platform == "" {
			if ext == ".ipa" {
				platform = "IOS"
			} else {
				platform = "ANDROID"
			}
		} else {
			platform = strings.ToUpper(platform)
			if platform != "IOS" && platform != "ANDROID" {
				ui.Error("Invalid platform: %s (must be IOS or ANDROID)", platform)
				return
			}
		}

		if uploadAppID == "" {
			ui.Error("--app-id is required. Run `airbuild apps list` to see your apps.")
			return
		}

		cfg := mustLoadConfig()
		client := api.New(cfg.APIURL, cfg.APIKey)

		// Format file size
		sizeStr := formatFileSize(info.Size())
		ui.Info("Uploading %s (%s, %s)...", filepath.Base(filePath), platform, sizeStr)

		start := time.Now()
		resp, err := client.Upload(filePath, uploadAppID, platform, uploadReleaseNotes)
		if err != nil {
			ui.Error("Upload failed: %v", err)
			return
		}
		elapsed := time.Since(start).Round(time.Millisecond)

		ui.Success("Upload complete in %s", elapsed)
		fmt.Println()
		ui.Table(
			[]string{"Field", "Value"},
			[][]string{
				{"Build ID", resp.Build.ID},
				{"Version", resp.Build.Version},
				{"Build Number", resp.Build.BuildNumber},
				{"Platform", resp.Build.Platform},
				{"Status", resp.Build.Status},
				{"Install Slug", resp.InstallLink.Slug},
			},
		)
		fmt.Println()
		ui.Info("Install URL: %s", resp.InstallURL)
	},
}

func init() {
	uploadCmd.Flags().StringVar(&uploadAppID, "app-id", "", "App ID to upload to (required)")
	uploadCmd.Flags().StringVar(&uploadPlatform, "platform", "", "Platform: IOS or ANDROID (auto-detected from extension)")
	uploadCmd.Flags().StringVar(&uploadReleaseNotes, "release-notes", "", "Release notes for this build")
	rootCmd.AddCommand(uploadCmd)
}

// formatFileSize formats a byte count as a human-readable string.
func formatFileSize(size int64) string {
	const (
		KB = 1024
		MB = 1024 * KB
		GB = 1024 * MB
	)
	switch {
	case size >= GB:
		return fmt.Sprintf("%.2f GB", float64(size)/float64(GB))
	case size >= MB:
		return fmt.Sprintf("%.2f MB", float64(size)/float64(MB))
	case size >= KB:
		return fmt.Sprintf("%.2f KB", float64(size)/float64(KB))
	default:
		return fmt.Sprintf("%d B", size)
	}
}
