package cmd

import (
	"fmt"
	"os"

	"github.com/airbuild/airbuild-cli/internal/setup"
	"github.com/airbuild/airbuild-cli/internal/ui"
	"github.com/spf13/cobra"
)

// --- doctor ---

var codepushFlutterDoctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check Flutter CodePush environment",
	Long: `Check that all Flutter CodePush dependencies and configuration are in place.

Checks:
  - Dart SDK on PATH
  - Flutter SDK on PATH
  - Shorebird CLI installed
  - shorebird.yaml configured for AirBuild
  - .airbuild.json project config
  - API key configured
  - AirBuild API reachable

Run 'airbuild codepush flutter install' to fix issues automatically.`,
	Run: func(cmd *cobra.Command, args []string) {
		runDoctor("Flutter", setup.FlutterChecklist())
	},
}

var codepushReactNativeDoctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check React Native CodePush environment",
	Long: `Check that all React Native CodePush dependencies and configuration are in place.

Checks:
  - Node.js and npx on PATH
  - expo-updates installed in project
  - expo-updates configured for AirBuild
  - .airbuild.json project config
  - API key configured
  - AirBuild API reachable

Run 'airbuild codepush react-native install' to fix issues automatically.`,
	Run: func(cmd *cobra.Command, args []string) {
		runDoctor("React Native", setup.ReactNativeChecklist())
	},
}

// --- install ---

var codepushFlutterInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install missing Flutter CodePush dependencies",
	Long: `Install missing Flutter CodePush dependencies.

Currently installs:
  - Shorebird CLI (via 'dart pub global activate shorebird_cli')

Requires Dart to be installed first (system-level, cannot be automated).`,
	Run: func(cmd *cobra.Command, args []string) {
		runInstall("Flutter", setup.FlutterInstallActions(), setup.FlutterChecklist())
	},
}

var codepushReactNativeInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install missing React Native CodePush dependencies",
	Long: `Install missing React Native CodePush dependencies.

Currently installs:
  - expo-updates (via 'npx expo install' or 'npm install')

Requires Node.js to be installed first (system-level, cannot be automated).`,
	Run: func(cmd *cobra.Command, args []string) {
		runInstall("React Native", setup.ReactNativeInstallActions(), setup.ReactNativeChecklist())
	},
}

// --- init ---

var codepushFlutterInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize project for Flutter CodePush",
	Long: `Initialize the current project for Flutter CodePush.

Creates shorebird.yaml with the AirBuild base_url if missing, and verifies
.airbuild.json exists. Run 'airbuild init' first if you haven't already.`,
	Run: func(cmd *cobra.Command, args []string) {
		runInit("Flutter", setup.FlutterInitActions(), setup.FlutterChecklist())
	},
}

var codepushReactNativeInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize project for React Native CodePush",
	Long: `Initialize the current project for React Native CodePush.

Installs expo-updates if missing, configures app.json/app.config.js with
the AirBuild manifest URL, and verifies .airbuild.json exists.`,
	Run: func(cmd *cobra.Command, args []string) {
		runInit("React Native", setup.ReactNativeInitActions(), setup.ReactNativeChecklist())
	},
}

func init() {
	// Flutter subcommands
	codepushFlutterCmd.AddCommand(codepushFlutterDoctorCmd)
	codepushFlutterCmd.AddCommand(codepushFlutterInstallCmd)
	codepushFlutterCmd.AddCommand(codepushFlutterInitCmd)

	// React Native subcommands
	codepushReactNativeCmd.AddCommand(codepushReactNativeDoctorCmd)
	codepushReactNativeCmd.AddCommand(codepushReactNativeInstallCmd)
	codepushReactNativeCmd.AddCommand(codepushReactNativeInitCmd)
}

// --- shared runners ---

func runDoctor(framework string, checks setup.Checklist) {
	ui.Header(fmt.Sprintf("%s CodePush — Environment Check", framework))
	fmt.Println()

	results := checks.Evaluate()
	failures := setup.CountFailures(results)
	warnings := setup.CountWarnings(results)

	for _, r := range results {
		sym := r.Status.String()
		msg := r.Message
		if r.Fix != "" {
			msg += fmt.Sprintf("\n    → %s", r.Fix)
		}
		switch r.Status {
		case setup.StatusPass:
			fmt.Printf("  %s %s\n", sym, msg)
		case setup.StatusWarn:
			ui.Warn("%s %s", sym, msg)
		case setup.StatusFail:
			ui.Error("%s %s", sym, msg)
		}
	}

	fmt.Println()
	if failures == 0 && warnings == 0 {
		ui.Success("All checks passed — %s CodePush is ready to use", framework)
	} else {
		ui.Info("%d issue(s) found, %d warning(s)", failures, warnings)
		if failures > 0 {
			ui.Muted("Run 'airbuild codepush %s install' to fix automatically", framework)
		}
	}
}

func runInstall(framework string, actions setup.Checklist, fullChecklist setup.Checklist) {
	ui.Header(fmt.Sprintf("%s CodePush — Install Dependencies", framework))
	fmt.Println()

	// First run doctor to see what's missing
	results := fullChecklist.Evaluate()

	// Find checks that need fixing
	var toFix []setup.SetupCheck
	for _, c := range actions {
		// Find the check result for this action
		for _, r := range results {
			if r.Name == c.Name && r.Status != setup.StatusPass {
				toFix = append(toFix, c)
				break
			}
		}
	}

	if len(toFix) == 0 {
		ui.Success("All dependencies already installed — nothing to do")
		return
	}

	fmt.Printf("Will install %d missing dep(s):\n", len(toFix))
	for _, c := range toFix {
		fmt.Printf("  - %s\n", c.Name)
	}
	fmt.Println()
	ui.Muted("Installing...")

	installed := 0
	for _, c := range toFix {
		fmt.Println()
		ui.Info("Installing %s...", c.Name)
		if err := c.Fix(); err != nil {
			ui.Error("Failed to install %s: %v", c.Name, err)
			continue
		}
		installed++
		ui.Success("Installed %s", c.Name)
	}

	fmt.Println()
	ui.Info("Installed %d/%d dependencies. Run 'airbuild codepush %s doctor' to verify.", installed, len(toFix), framework)
}

func runInit(framework string, actions setup.Checklist, fullChecklist setup.Checklist) {
	ui.Header(fmt.Sprintf("%s CodePush — Initialize Project", framework))
	fmt.Println()

	// Run doctor first
	results := fullChecklist.Evaluate()
	failures := setup.CountFailures(results)

	// Show doctor results
	for _, r := range results {
		sym := r.Status.String()
		msg := r.Message
		if r.Fix != "" && r.Status != setup.StatusPass {
			msg += fmt.Sprintf("\n    → %s", r.Fix)
		}
		switch r.Status {
		case setup.StatusPass:
			fmt.Printf("  %s %s\n", sym, msg)
		case setup.StatusWarn:
			ui.Warn("%s %s", sym, msg)
		case setup.StatusFail:
			ui.Error("%s %s", sym, msg)
		}
	}

	if failures > 0 {
		fmt.Println()
		ui.Warn("%d check(s) failed — fixing what can be fixed...", failures)
	}

	// Run init actions
	var fixed []string
	for _, c := range actions {
		for _, r := range results {
			if r.Name == c.Name && r.Status != setup.StatusPass && c.Fix != nil {
				fmt.Println()
				ui.Info("Fixing %s...", c.Name)
				if err := c.Fix(); err != nil {
					ui.Error("Failed to fix %s: %v", c.Name, err)
				} else {
					fixed = append(fixed, c.Name)
					ui.Success("Fixed %s", c.Name)
				}
				break
			}
		}
	}

	// Re-check
	fmt.Println()
	results = fullChecklist.Evaluate()
	remaining := setup.CountFailures(results)

	fmt.Println()
	if remaining == 0 {
		ui.Success("%s CodePush is ready — run 'airbuild codepush %s status' to see your updates", framework, framework)
	} else {
		ui.Warn("%d issue(s) remain — run 'airbuild codepush %s doctor' for details", remaining, framework)
		ui.Muted("Some issues require manual installation (e.g. Dart/Flutter SDK, Node.js)")
	}

	os.Exit(0)
}
