package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/airbuild/cli/internal/api"
	"github.com/airbuild/cli/internal/project"
	"github.com/airbuild/cli/internal/ui"
	"github.com/spf13/cobra"
)

var initAppID string

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize AirBuild in your project",
	Long: `Create a .airbuild.json config file for your project.

This command sets up project-level configuration so you can use
` + "`airbuild push`" + ` to upload builds without specifying --app-id and --file
each time.

You can either link an existing app or create a new one. Build output
paths are auto-detected based on your framework (Flutter, React Native,
Android native, iOS native).

Examples:
  airbuild init                    # Interactive setup
  airbuild init --app-id clxxxx    # Link an existing app`,
	Run: func(cmd *cobra.Command, args []string) {
		cfg := mustLoadConfig()
		client := api.New(cfg.APIURL, cfg.APIKey)

		// Check if config already exists
		if project.Exists() {
			ui.Warn(".airbuild.json already exists in this directory.")
			if !confirm("Overwrite it?") {
				ui.Info("Aborted.")
				return
			}
		}

		var appID string
		var app api.App

		if initAppID != "" {
			// Link existing app by ID
			appID = initAppID
			ui.Info("Looking up app %s...", appID)
			apps, err := client.ListApps()
			if err != nil {
				ui.Error("Failed to list apps: %v", err)
				return
			}
			found := false
			for _, a := range apps.Apps {
				if a.ID == appID {
					app = a
					found = true
					break
				}
			}
			if !found {
				ui.Error("App not found: %s (make sure it belongs to your organization)", appID)
				return
			}
			ui.Success("Found app: %s", app.Name)
		} else {
			// Ask: new or existing?
			ui.Info("Do you want to link an existing app or create a new one?")
			fmt.Println("  1. Link an existing app")
			fmt.Println("  2. Create a new app")
			choice := prompt("Enter choice (1 or 2)", "1")

			if choice == "1" {
				// List apps and let user pick
				ui.Info("Fetching your apps...")
				apps, err := client.ListApps()
				if err != nil {
					ui.Error("Failed to list apps: %v", err)
					return
				}
				if len(apps.Apps) == 0 {
					ui.Error("No apps found in your organization. Create one first or choose option 2.")
					return
				}
				fmt.Println()
				for i, a := range apps.Apps {
					platforms := strings.Join(a.Platforms, ", ")
					fmt.Printf("  %d. %s (%s) [%s]\n", i+1, a.Name, a.ID, platforms)
				}
				fmt.Println()
				idx := promptInt("Select app number", 1)
				if idx < 1 || idx > len(apps.Apps) {
					ui.Error("Invalid selection")
					return
				}
				app = apps.Apps[idx-1]
				appID = app.ID
				ui.Success("Selected: %s", app.Name)
			} else if choice == "2" {
				// Create new app
				name := prompt("App name", defaultAppName())
				if name == "" {
					ui.Error("App name is required")
					return
				}

				ui.Info("Which platforms does this app support?")
				fmt.Println("  1. Android only")
				fmt.Println("  2. iOS only")
				fmt.Println("  3. Both Android and iOS")
				platChoice := prompt("Enter choice (1, 2, or 3)", "3")

				var platforms []string
				switch platChoice {
				case "1":
					platforms = []string{"ANDROID"}
				case "2":
					platforms = []string{"IOS"}
				default:
					platforms = []string{"IOS", "ANDROID"}
				}

				ui.Info("Creating app '%s'...", name)
				resp, err := client.CreateApp(name, platforms)
				if err != nil {
					ui.Error("Failed to create app: %v", err)
					return
				}
				app = resp.App
				appID = app.ID
				ui.Success("Created app: %s (%s)", app.Name, appID)
			} else {
				ui.Error("Invalid choice")
				return
			}
		}

		// Detect framework and suggest build paths
		framework := detectFramework()
		if framework != "" {
			ui.Info("Detected framework: %s", framework)
		}

		// Build the project config
		projCfg := &project.ProjectConfig{
			AppID:  appID,
			Builds: make(map[string]project.PlatformBuilds),
		}

		// Configure Android builds
		if containsPlatform(app.Platforms, "ANDROID") {
			ui.Info("")
			ui.Header("Android Build Paths")
			if confirm("Configure Android build paths?") {
				debugDefault, releaseDefault := defaultBuildPaths(framework, "android")
				debugPath := prompt("Debug APK path", debugDefault)
				releasePath := prompt("Release APK path", releaseDefault)
				projCfg.Builds["android"] = project.PlatformBuilds{
					Debug:   debugPath,
					Release: releasePath,
				}
			}
		}

		// Configure iOS builds
		if containsPlatform(app.Platforms, "IOS") {
			ui.Info("")
			ui.Header("iOS Build Paths")
			if confirm("Configure iOS build paths?") {
				debugDefault, releaseDefault := defaultBuildPaths(framework, "ios")
				debugPath := prompt("Debug IPA path", debugDefault)
				releasePath := prompt("Release IPA path", releaseDefault)
				projCfg.Builds["ios"] = project.PlatformBuilds{
					Debug:   debugPath,
					Release: releasePath,
				}
			}
		}

		// Save config
		if err := projCfg.Save(); err != nil {
			ui.Error("Failed to save config: %v", err)
			return
		}

		ui.Info("")
		ui.Success("Created %s", project.ConfigFile)
		ui.Info("")
		ui.Info("You can now upload builds with:")
		ui.Muted("  airbuild push")
		ui.Muted("  airbuild push --platform android --release")
		ui.Muted("  airbuild push --all")
	},
}

func init() {
	initCmd.Flags().StringVar(&initAppID, "app-id", "", "Link an existing app by ID")
	rootCmd.AddCommand(initCmd)
}

// --- Helpers ---

// reader is a shared bufio reader for stdin prompts.
var reader = bufio.NewReader(os.Stdin)

// prompt reads a line from stdin with a label and default value.
func prompt(label, defaultVal string) string {
	if defaultVal != "" {
		fmt.Printf("%s [%s]: ", label, defaultVal)
	} else {
		fmt.Printf("%s: ", label)
	}
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return defaultVal
	}
	return line
}

// promptInt reads an integer from stdin with a label and default value.
func promptInt(label string, defaultVal int) int {
	s := prompt(fmt.Sprintf("%s (%d)", label, defaultVal), fmt.Sprintf("%d", defaultVal))
	var n int
	fmt.Sscanf(s, "%d", &n)
	if n == 0 {
		return defaultVal
	}
	return n
}

// confirm asks a yes/no question.
func confirm(label string) bool {
	fmt.Printf("%s [Y/n]: ", label)
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(strings.ToLower(line))
	return line == "" || line == "y" || line == "yes"
}

// detectFramework checks for framework-specific files in the current directory.
func detectFramework() string {
	if fileExists("pubspec.yaml") {
		return "flutter"
	}
	if fileExists("react-native.config.js") || fileExists("app.json") && fileExists("package.json") && fileExists("metro.config.js") {
		return "react-native"
	}
	if fileExists("app.json") && fileExists("metro.config.js") {
		return "react-native"
	}
	if fileExists("build.gradle") || fileExists("build.gradle.kts") {
		return "android-native"
	}
	if fileExists("Package.swift") {
		return "ios-native"
	}
	return ""
}

// defaultBuildPaths returns suggested build output paths for a framework/platform.
func defaultBuildPaths(framework, platform string) (debug, release string) {
	switch framework {
	case "flutter":
		if platform == "android" {
			return "build/app/outputs/flutter-apk/app-debug.apk",
				"build/app/outputs/flutter-apk/app-release.apk"
		}
		return "build/ios/ipa/app-debug.ipa",
			"build/ios/ipa/app-release.ipa"
	case "react-native":
		if platform == "android" {
			return "android/app/build/outputs/apk/debug/app-debug.apk",
				"android/app/build/outputs/apk/release/app-release.apk"
		}
		return "ios/build/Debug-iphonesimulator/app-debug.ipa",
			"ios/build/Release-iphoneos/app-release.ipa"
	case "android-native":
		return "app/build/outputs/apk/debug/app-debug.apk",
			"app/build/outputs/apk/release/app-release.apk"
	case "ios-native":
		return "", "build/Release-iphoneos/app-release.ipa"
	}
	if platform == "android" {
		return "app-debug.apk", "app-release.apk"
	}
	return "app-debug.ipa", "app-release.ipa"
}

// defaultAppName derives a default app name from the current directory.
func defaultAppName() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	return filepath.Base(dir)
}

// fileExists checks if a file exists in the current directory.
func fileExists(name string) bool {
	_, err := os.Stat(name)
	return err == nil
}

// containsPlatform checks if a platform string is in a list.
func containsPlatform(platforms []string, target string) bool {
	for _, p := range platforms {
		if p == target {
			return true
		}
	}
	return false
}
