package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/airbuild/airbuild-cli/internal/setup"
	"github.com/airbuild/airbuild-cli/internal/ui"
	"github.com/spf13/cobra"
)

// setupFramework identifies a framework for display and for the CLI
// subcommand name used in follow-up hints.
type setupFramework struct {
	Name    string // "React Native"
	Command string // "react-native"
}

var (
	flutterFramework     = setupFramework{Name: "Flutter", Command: "flutter"}
	reactNativeFramework = setupFramework{Name: "React Native", Command: "react-native"}
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

Exits with status 1 if any check fails.`,
	Run: func(cmd *cobra.Command, args []string) {
		runDoctor(flutterFramework, setup.FlutterChecklist())
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

Exits with status 1 if any check fails.`,
	Run: func(cmd *cobra.Command, args []string) {
		runDoctor(reactNativeFramework, setup.ReactNativeChecklist())
	},
}

// --- install ---

var codepushFlutterInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install missing Flutter CodePush dependencies",
	Long: `Install missing Flutter CodePush dependencies.

Currently installs:
  - Shorebird CLI (via 'dart pub global activate shorebird_cli')

Requires Dart to be installed first. Asks for confirmation unless --yes is set.`,
	Run: func(cmd *cobra.Command, args []string) {
		runFixes(cmd, flutterFramework, "install", setup.FlutterInstallActions(), setup.FlutterChecklist())
	},
}

var codepushReactNativeInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install missing React Native CodePush dependencies",
	Long: `Install missing React Native CodePush dependencies.

Currently installs:
  - expo-updates (via 'npx expo install' for Expo apps, 'npm install' for bare React Native)

Requires Node.js to be installed first. Asks for confirmation unless --yes is set.`,
	Run: func(cmd *cobra.Command, args []string) {
		runFixes(cmd, reactNativeFramework, "install", setup.ReactNativeInstallActions(), setup.ReactNativeChecklist())
	},
}

// --- init ---

var codepushFlutterInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize project for Flutter CodePush",
	Long: `Initialize the current project for Flutter CodePush.

Creates or updates shorebird.yaml so the Shorebird updater checks AirBuild:
sets base_url and your app's distribution_key, and adds app_id if missing.
Run 'airbuild init' and 'airbuild login' first.

Asks for confirmation unless --yes is set.`,
	Run: func(cmd *cobra.Command, args []string) {
		runFixes(cmd, flutterFramework, "init", setup.FlutterInitActions(), setup.FlutterChecklist())
	},
}

var codepushReactNativeInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize project for React Native CodePush",
	Long: `Initialize the current project for React Native CodePush.

Installs expo-updates if missing and sets expo.updates.url in app.json to
your app's AirBuild manifest URL. For app.config.js/ts, prints the snippet
to add. Run 'airbuild init' and 'airbuild login' first.

Asks for confirmation unless --yes is set.`,
	Run: func(cmd *cobra.Command, args []string) {
		runFixes(cmd, reactNativeFramework, "init", setup.ReactNativeInitActions(), setup.ReactNativeChecklist())
	},
}

func init() {
	for _, c := range []*cobra.Command{
		codepushFlutterInstallCmd, codepushFlutterInitCmd,
		codepushReactNativeInstallCmd, codepushReactNativeInitCmd,
	} {
		c.Flags().BoolP("yes", "y", false, "Apply changes without asking for confirmation")
	}

	codepushFlutterCmd.AddCommand(codepushFlutterDoctorCmd, codepushFlutterInstallCmd, codepushFlutterInitCmd)
	codepushReactNativeCmd.AddCommand(codepushReactNativeDoctorCmd, codepushReactNativeInstallCmd, codepushReactNativeInitCmd)
}

// --- shared runners ---

func printResults(results []setup.CheckResult) {
	for _, r := range results {
		msg := fmt.Sprintf("%-20s %s", r.Name, r.Message)
		if r.Fix != "" && r.Status != setup.StatusPass {
			msg += fmt.Sprintf("\n    → %s", r.Fix)
		}
		switch r.Status {
		case setup.StatusPass:
			fmt.Printf("  %s %s\n", r.Status, msg)
		case setup.StatusWarn:
			ui.Warn("%s %s", r.Status, msg)
		case setup.StatusFail:
			ui.Error("%s %s", r.Status, msg)
		}
	}
}

func runDoctor(fw setupFramework, checks setup.Checklist) {
	ui.Header("%s CodePush — Environment Check", fw.Name)
	fmt.Println()

	results := checks.Evaluate()
	printResults(results)
	failures := setup.CountFailures(results)
	warnings := setup.CountWarnings(results)

	fmt.Println()
	if failures == 0 && warnings == 0 {
		ui.Success("All checks passed — %s CodePush is ready to use", fw.Name)
		return
	}
	ui.Info("%d issue(s) found, %d warning(s)", failures, warnings)
	ui.Muted("Run 'airbuild codepush %s install' and 'airbuild codepush %s init' to fix what can be fixed automatically", fw.Command, fw.Command)
	if failures > 0 {
		os.Exit(1)
	}
}

// runFixes powers both `install` and `init`: it evaluates the full checklist,
// selects the fixable actions whose check isn't passing, confirms with the
// user, applies them, then re-checks. Exits 1 if anything remains broken.
func runFixes(cmd *cobra.Command, fw setupFramework, verb string, actions, fullChecklist setup.Checklist) {
	yes, _ := cmd.Flags().GetBool("yes")
	title := map[string]string{"install": "Install Dependencies", "init": "Initialize Project"}[verb]
	ui.Header("%s CodePush — %s", fw.Name, title)
	fmt.Println()

	results := fullChecklist.Evaluate()
	printResults(results)

	status := map[string]setup.CheckStatus{}
	for _, r := range results {
		status[r.Name] = r.Status
	}
	var toFix []setup.SetupCheck
	for _, c := range actions {
		if c.Fix != nil && status[c.Name] != setup.StatusPass {
			toFix = append(toFix, c)
		}
	}

	fmt.Println()
	if len(toFix) == 0 {
		ui.Success("Nothing to %s — everything this command manages is already set up", verb)
		exitIfFailures(fw, actions, fullChecklist.Evaluate())
		return
	}

	fmt.Println("The following changes will be made:")
	for _, c := range toFix {
		fmt.Printf("  - %s (modifies %s)\n", c.Name, c.Modifies)
	}
	fmt.Println()
	if !yes && !confirm("Proceed?") {
		ui.Muted("Aborted — no changes made")
		os.Exit(1)
	}

	for _, c := range toFix {
		fmt.Println()
		ui.Info("Setting up %s...", c.Name)
		switch err := c.Fix(); {
		case errors.Is(err, setup.ErrManualAction):
			ui.Warn("%s: %v", c.Name, err)
		case err != nil:
			ui.Error("Failed to set up %s: %v", c.Name, err)
		default:
			ui.Success("%s done", c.Name)
		}
	}

	fmt.Println()
	exitIfFailures(fw, actions, fullChecklist.Evaluate())
}

// exitIfFailures reports overall readiness, and exits 1 if any check this
// command is responsible for (actions) is still not passing.
func exitIfFailures(fw setupFramework, actions setup.Checklist, results []setup.CheckResult) {
	remaining := setup.CountFailures(results) + setup.CountWarnings(results)
	if remaining == 0 {
		ui.Success("%s CodePush is ready", fw.Name)
		return
	}
	ui.Warn("%d issue(s) remain — run 'airbuild codepush %s doctor' for details", remaining, fw.Command)
	ui.Muted("Some items need manual installation (e.g. Dart/Flutter SDK, Node.js)")

	owned := map[string]bool{}
	for _, a := range actions {
		owned[a.Name] = true
	}
	for _, r := range results {
		if owned[r.Name] && r.Status != setup.StatusPass {
			os.Exit(1)
		}
	}
}
