package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/airbuild/airbuild-cli/internal/agents"
	"github.com/airbuild/airbuild-cli/internal/api"
	"github.com/airbuild/airbuild-cli/internal/project"
	"github.com/airbuild/airbuild-cli/internal/ui"
	"github.com/spf13/cobra"
)

var initAppID string
var initYes bool
var initAgents bool

// assumeYes makes every prompt()/confirm() return its default without
// reading stdin — set by --yes on interactive commands (init, upgrade).
var assumeYes bool

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
  airbuild init --app-id clxxxx    # Link an existing app
  airbuild init --yes --agents     # Non-interactive, defaults + AI assistant files`,
	Run: func(cmd *cobra.Command, args []string) {
		assumeYes = initYes

		cfg := mustLoadConfig()
		client := api.New(cfg.APIURL, cfg.APIKey)

		// Check if config already exists
		if project.Exists() {
			ui.Warn(".airbuild.json already exists in this directory.")
			// --yes defaults this to "no" — init never clobbers an
			// existing config non-interactively.
			if !confirm("Overwrite it?", false) {
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
				fail("Failed to list apps: %v", err)
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
				fail("App not found: %s (make sure it belongs to your organization)", appID)
			}
			ui.Success("Found app: %s", app.Name)
		} else {
			// Ask: new or existing?
			ui.Info("Do you want to link an existing app or create a new one?")
			ui.Println("  1. Link an existing app")
			ui.Println("  2. Create a new app")
			choice := prompt("Enter choice (1 or 2)", "1")

			if choice == "1" {
				// List apps and let user pick
				ui.Info("Fetching your apps...")
				apps, err := client.ListApps()
				if err != nil {
					fail("Failed to list apps: %v", err)
				}
				if len(apps.Apps) == 0 {
					fail("No apps found in your organization. Create one first or choose option 2.")
				}
				ui.Println("")
				for i, a := range apps.Apps {
					platforms := strings.Join(a.Platforms, ", ")
					ui.Printf("  %d. %s (%s) [%s]\n", i+1, a.Name, a.ID, platforms)
				}
				ui.Println("")
				idx := promptInt("Select app number", 1)
				if idx < 1 || idx > len(apps.Apps) {
					fail("Invalid selection")
				}
				app = apps.Apps[idx-1]
				appID = app.ID
				ui.Success("Selected: %s", app.Name)
			} else if choice == "2" {
				// Create new app
				name := prompt("App name", defaultAppName())
				if name == "" {
					fail("App name is required")
				}

				ui.Info("Which platforms does this app support?")
				ui.Println("  1. Android only")
				ui.Println("  2. iOS only")
				ui.Println("  3. Both Android and iOS")
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
					fail("Failed to create app: %v", err)
				}
				app = resp.App
				appID = app.ID
				ui.Success("Created app: %s (%s)", app.Name, appID)
			} else {
				fail("Invalid choice")
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
			if confirm("Configure Android build paths?", true) {
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
			if confirm("Configure iOS build paths?", true) {
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
			fail("Failed to save config: %v", err)
		}

		ui.Info("")
		ui.Success("Created %s", project.ConfigFile)

		// AI assistant files are opt-in: --agents writes them
		// non-interactively, interactive users are asked. --yes alone
		// does not create files the user didn't request.
		if initAgents || (!initYes && confirm("Write AI assistant files (AGENTS.md, CLAUDE.md)?", true)) {
			written, err := agents.WriteAll(".")
			if err != nil {
				ui.Warn("Could not write agent files: %v", err)
			} else {
				ui.Success("Wrote AI assistant docs: %s", strings.Join(written, ", "))
			}
		}

		ui.Info("")
		ui.Info("You can now upload builds with:")
		ui.Muted("  airbuild push")
		ui.Muted("  airbuild push --platform android --release")
		ui.Muted("  airbuild push --all")
	},
}

func init() {
	initCmd.Flags().StringVar(&initAppID, "app-id", "", "Link an existing app by ID")
	initCmd.Flags().BoolVar(&initYes, "yes", false, "Non-interactive: answer yes to prompts and accept all defaults")
	initCmd.Flags().BoolVar(&initAgents, "agents", false, "Write AI assistant files (AGENTS.md, CLAUDE.md) without asking")
	rootCmd.AddCommand(initCmd)
}

// --- Helpers ---

// reader is a shared bufio reader for stdin prompts.
var reader = bufio.NewReader(os.Stdin)

// prompt reads a line from stdin with a label and default value. With
// assumeYes set (--yes), it returns the default without reading stdin.
func prompt(label, defaultVal string) string {
	if assumeYes {
		ui.Printf("%s: %s (auto)\n", label, defaultVal)
		return defaultVal
	}
	if defaultVal != "" {
		ui.Printf("%s [%s]: ", label, defaultVal)
	} else {
		ui.Printf("%s: ", label)
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

// confirm asks a yes/no question. With assumeYes set (--yes), it answers
// with the given default without reading stdin — callers pass false for
// destructive actions so --yes stays safe.
func confirm(label string, defaultVal bool) bool {
	hint := "[y/N]"
	if defaultVal {
		hint = "[Y/n]"
	}
	if assumeYes {
		ui.Printf("%s %s: %v (auto)\n", label, hint, defaultVal)
		return defaultVal
	}
	ui.Printf("%s %s: ", label, hint)
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(strings.ToLower(line))
	if line == "" {
		return defaultVal
	}
	return line == "y" || line == "yes"
}

// detectFramework checks for framework-specific files in the current directory.
func detectFramework() string {
	if fileExists("pubspec.yaml") {
		return "flutter"
	}
	if fileExists("react-native.config.js") ||
		fileExists("app.json") && fileExists("package.json") && fileExists("metro.config.js") ||
		hasPackageDependency("react-native") || hasPackageDependency("expo") {
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

// hasPackageDependency reports whether package.json lists dep in
// dependencies or devDependencies. Used to detect RN/Expo projects that
// don't have a metro.config.js (standard Expo projects don't).
func hasPackageDependency(dep string) bool {
	data, err := os.ReadFile("package.json")
	if err != nil {
		return false
	}
	var pkg struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return false
	}
	_, ok := pkg.Dependencies[dep]
	if !ok {
		_, ok = pkg.DevDependencies[dep]
	}
	return ok
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
