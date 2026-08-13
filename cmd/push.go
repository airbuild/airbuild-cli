package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/airbuild/cli/internal/api"
	"github.com/airbuild/cli/internal/project"
	"github.com/airbuild/cli/internal/ui"
	"github.com/spf13/cobra"
)

var pushPlatform string
var pushRelease bool
var pushDebug bool
var pushAll bool
var pushExpiry int
var pushJSON bool
var pushReleaseNotes string

var pushCmd = &cobra.Command{
	Use:   "push",
	Short: "Upload a build using project config",
	Long: `Upload a build to AirBuild using the .airbuild.json config file.

Reads build output paths from the config created by ` + "`airbuild init`" + `.
The platform and build type (debug/release) determine which file to upload.

Examples:
  airbuild push                              # Push default (release, auto platform)
  airbuild push --platform android           # Push Android release
  airbuild push --platform ios --debug       # Push iOS debug
  airbuild push --all                        # Push both platforms
  airbuild push --release --expiry 30        # Push with 30-day link expiry
  airbuild push --json                       # JSON output for CI/CD`,
	Run: func(cmd *cobra.Command, args []string) {
		// Load project config
		projCfg, err := project.Load()
		if err != nil {
			ui.Error("%v", err)
			ui.Info("Run `airbuild init` to set up your project first.")
			return
		}

		cfg := mustLoadConfig()
		client := api.New(cfg.APIURL, cfg.APIKey)

		// Determine build type
		buildType := "release"
		if pushDebug {
			buildType = "debug"
		}
		if pushRelease && pushDebug {
			ui.Error("Cannot specify both --release and --debug")
			return
		}

		// Determine which platforms to push
		configuredPlatforms := projCfg.ConfiguredPlatforms()
		var platformsToPush []string

		if pushAll {
			platformsToPush = configuredPlatforms
		} else if pushPlatform != "" {
			p := strings.ToLower(pushPlatform)
			if !projCfg.HasPlatform(p) {
				ui.Error("Platform '%s' is not configured in .airbuild.json", p)
				if len(configuredPlatforms) > 0 {
					ui.Info("Configured platforms: %s", strings.Join(configuredPlatforms, ", "))
				} else {
					ui.Info("No platforms found in config. Check your .airbuild.json — the 'builds' key may be missing or empty.")
				}
				return
			}
			platformsToPush = []string{p}
		} else {
			// Auto: if only one platform configured, use it
			if len(configuredPlatforms) == 1 {
				platformsToPush = configuredPlatforms
			} else if len(configuredPlatforms) == 0 {
				ui.Error("No platforms configured in .airbuild.json")
				ui.Info("Run `airbuild init` to configure build paths.")
				return
			} else {
				ui.Error("Multiple platforms configured. Specify --platform or --all.")
				ui.Info("Configured platforms: %s", strings.Join(configuredPlatforms, ", "))
				return
			}
		}

		if len(platformsToPush) == 0 {
			ui.Error("No platforms to push")
			return
		}

		// Upload each platform
		results := make([]pushResult, 0, len(platformsToPush))
		for _, platform := range platformsToPush {
			result := pushSingle(client, projCfg, platform, buildType, cfg.APIURL)
			results = append(results, result)
		}

		// Output results
		if pushJSON {
			outputJSON(results)
		} else {
			outputHuman(results, cfg.APIURL)
		}

		// Exit with error if any upload failed
		for _, r := range results {
			if r.Error != "" {
				os.Exit(1)
			}
		}
	},
}

type pushResult struct {
	Platform   string `json:"platform"`
	BuildType  string `json:"buildType"`
	FilePath   string `json:"filePath"`
	Success    bool   `json:"success"`
	Error      string `json:"error,omitempty"`
	BuildID    string `json:"buildId,omitempty"`
	Version    string `json:"version,omitempty"`
	Slug       string `json:"slug,omitempty"`
	InstallURL string `json:"installUrl,omitempty"`
	Expiry     string `json:"expiry,omitempty"`
}

func pushSingle(client *api.Client, projCfg *project.ProjectConfig, platform, buildType, baseURL string) pushResult {
	result := pushResult{
		Platform:  platform,
		BuildType: buildType,
	}

	// Get the file path from config
	filePath := projCfg.GetBuildPath(platform, buildType)
	if filePath == "" {
		result.Error = fmt.Sprintf("No %s build path configured for %s in .airbuild.json", buildType, platform)
		return result
	}
	result.FilePath = filePath

	// Check file exists
	info, err := os.Stat(filePath)
	if err != nil {
		result.Error = fmt.Sprintf("File not found: %s — did you run the build first?", filePath)
		return result
	}

	// Validate extension
	ext := strings.ToLower(filepath.Ext(filePath))
	if platform == "ios" && ext != ".ipa" {
		result.Error = fmt.Sprintf("iOS builds must be .ipa files, got: %s", ext)
		return result
	}
	if platform == "android" && ext != ".apk" {
		result.Error = fmt.Sprintf("Android builds must be .apk files, got: %s", ext)
		return result
	}

	// Map platform to API format
	apiPlatform := "IOS"
	if platform == "android" {
		apiPlatform = "ANDROID"
	}

	sizeStr := formatFileSize(info.Size())
	ui.Info("Uploading %s (%s, %s)...", filepath.Base(filePath), platform, sizeStr)

	start := time.Now()
	resp, err := client.Upload(filePath, projCfg.AppID, apiPlatform, pushReleaseNotes)
	if err != nil {
		result.Error = fmt.Sprintf("Upload failed: %v", err)
		return result
	}
	elapsed := time.Since(start).Round(time.Millisecond)

	result.Success = true
	result.BuildID = resp.Build.ID
	result.Version = resp.Build.Version
	result.Slug = resp.InstallLink.Slug

	// Build install URL
	if resp.InstallLink.Slug != "" {
		result.InstallURL = fmt.Sprintf("%s/i/%s", baseURL, resp.InstallLink.Slug)
	}

	// Handle expiry info
	if pushExpiry > 0 {
		result.Expiry = fmt.Sprintf("%d days", pushExpiry)
	}

	ui.Success("Uploaded %s %s in %s", platform, buildType, elapsed)
	return result
}

func outputHuman(results []pushResult, baseURL string) {
	for _, r := range results {
		fmt.Println()
		if r.Error != "" {
			ui.Error("%s: %s", r.Platform, r.Error)
			continue
		}
		ui.Table(
			[]string{"Field", "Value"},
			[][]string{
				{"Platform", r.Platform},
				{"Build Type", r.BuildType},
				{"Build ID", r.BuildID},
				{"Version", r.Version},
				{"Install URL", r.InstallURL},
			},
		)
		if r.Expiry != "" {
			ui.Info("Link expires in: %s", r.Expiry)
		}
	}
}

func outputJSON(results []pushResult) {
	data, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		ui.Error("Could not marshal JSON output: %v", err)
		return
	}
	fmt.Println(string(data))
}

func init() {
	pushCmd.Flags().StringVar(&pushPlatform, "platform", "", "Platform to push: android or ios")
	pushCmd.Flags().BoolVar(&pushRelease, "release", false, "Push release build (default)")
	pushCmd.Flags().BoolVar(&pushDebug, "debug", false, "Push debug build")
	pushCmd.Flags().BoolVar(&pushAll, "all", false, "Push all configured platforms")
	pushCmd.Flags().IntVar(&pushExpiry, "expiry", 0, "Install link expiry in days (0 = plan default)")
	pushCmd.Flags().BoolVar(&pushJSON, "json", false, "Output results as JSON (for CI/CD)")
	pushCmd.Flags().StringVar(&pushReleaseNotes, "release-notes", "", "Release notes for this build")
	rootCmd.AddCommand(pushCmd)
}
