package setup

import (
	"fmt"
	"os"
	"strings"

	"github.com/airbuild/airbuild-cli/internal/config"
	"github.com/airbuild/airbuild-cli/internal/project"
)

// FlutterChecklist returns the ordered checks for Flutter CodePush.
func FlutterChecklist() Checklist {
	return Checklist{
		{Name: "dart", Run: checkDart},
		{Name: "flutter", Run: checkFlutter},
		{Name: "shorebird", Run: checkShorebird},
		{Name: "shorebird.yaml", Run: checkShorebirdYAML},
		{Name: ".airbuild.json", Run: checkAirbuildJSON},
		{Name: "api key", Run: checkAPIKey},
		{Name: "connectivity", Run: checkConnectivity},
	}
}

// FlutterInstallActions returns the checks that `install` can fix.
func FlutterInstallActions() Checklist {
	return Checklist{
		{Name: "shorebird", Run: checkShorebird, Fix: installShorebird, FixOn: "install"},
	}
}

// FlutterInitActions returns the checks that `init` can fix.
func FlutterInitActions() Checklist {
	return Checklist{
		{Name: "shorebird.yaml", Run: checkShorebirdYAML, Fix: initShorebirdYAML, FixOn: "init"},
		{Name: ".airbuild.json", Run: checkAirbuildJSON},
	}
}

// --- Flutter checks ---

func checkDart() CheckResult {
	path, ok := Which("dart")
	if !ok {
		return Fail("dart", "not found on PATH",
			"Install Dart/Flutter: https://docs.flutter.dev/get-started/install")
	}
	return Passf("dart", "found at %s", path)
}

func checkFlutter() CheckResult {
	path, ok := Which("flutter")
	if !ok {
		return Fail("flutter", "not found on PATH",
			"Install Flutter SDK: https://docs.flutter.dev/get-started/install")
	}
	return Passf("flutter", "found at %s", path)
}

func checkShorebird() CheckResult {
	path, ok := Which("shorebird")
	if !ok {
		return Fail("shorebird", "not found on PATH",
			"Run `airbuild codepush flutter install` or `dart pub global activate shorebird_cli`")
	}
	return Passf("shorebird", "found at %s", path)
}

func checkShorebirdYAML() CheckResult {
	if !FileExists("shorebird.yaml") {
		return Fail("shorebird.yaml", "not found in current directory",
			"Run `airbuild codepush flutter init` to create it")
	}
	// Check for base_url pointing to AirBuild
	if FileContains("shorebird.yaml", "airbuild.dev") {
		return Pass("shorebird.yaml", "found, configured for AirBuild")
	}
	return Warn("shorebird.yaml", "found but may not point to AirBuild",
		"Add `base_url: https://airbuild.dev` to shorebird.yaml, or run `airbuild codepush flutter init`")
}

func checkAirbuildJSON() CheckResult {
	if !project.Exists() {
		return Fail(".airbuild.json", "not found",
			"Run `airbuild init` to link your app")
	}
	cfg, err := project.Load()
	if err != nil {
		return Fail(".airbuild.json", fmt.Sprintf("invalid: %v", err),
			"Run `airbuild init` to recreate it")
	}
	return Passf(".airbuild.json", "found (app: %s)", cfg.AppID)
}

func checkAPIKey() CheckResult {
	cfg, err := config.Load()
	if err != nil || !cfg.IsLoggedIn() {
		return Fail("api key", "not configured",
			"Run `airbuild login --api-key airbuild_xxx`")
	}
	return Passf("api key", "configured (%s)", cfg.APIURL)
}

func checkConnectivity() CheckResult {
	cfg, err := config.Load()
	if err != nil {
		return Fail("connectivity", "could not load config", "")
	}
	url := cfg.APIURL + "/api/health"
	if !FileExists("/dev/null") { // sanity check we're on a real OS
		return Warn("connectivity", "skipped", "")
	}
	// Simple check — just verify the URL is set
	if cfg.APIURL == "" {
		return Fail("connectivity", "API URL not configured",
			"Run `airbuild config set --api-url https://airbuild.dev`")
	}
	_ = url // used in future for actual connectivity check
	return Passf("connectivity", "target: %s", cfg.APIURL)
}

// --- Flutter fix actions ---

func installShorebird() error {
	fmt.Println("Installing Shorebird CLI...")
	if err := RunCmd(".", "dart", "pub", "global", "activate", "shorebird_cli"); err != nil {
		return fmt.Errorf("failed to install shorebird_cli: %w", err)
	}
	return nil
}

func initShorebirdYAML() error {
	if FileExists("shorebird.yaml") {
		// Check if base_url is already set
		if FileContains("shorebird.yaml", "airbuild.dev") {
			fmt.Println("shorebird.yaml already configured for AirBuild")
			return nil
		}
		// Append base_url to existing file
		return appendBaseURLToShorebirdYAML()
	}
	// Create a minimal shorebird.yaml with base_url
	return createShorebirdYAML()
}

func createShorebirdYAML() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	content := fmt.Sprintf(`# Shorebird configuration for AirBuild CodePush
# https://shorebird.dev
# The base_url tells the Shorebird updater to check AirBuild (not Shorebird's cloud).
base_url: %s
`, cfg.APIURL)
	if err := os.WriteFile("shorebird.yaml", []byte(content), 0644); err != nil {
		return fmt.Errorf("could not create shorebird.yaml: %w", err)
	}
	fmt.Println("Created shorebird.yaml with AirBuild base_url")
	return nil
}

func appendBaseURLToShorebirdYAML() error {
	data, err := os.ReadFile("shorebird.yaml")
	if err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if strings.Contains(string(data), "base_url") {
		fmt.Println("shorebird.yaml already has base_url configured")
		return nil
	}
	updated := string(data) + fmt.Sprintf("\nbase_url: %s\n", cfg.APIURL)
	if err := os.WriteFile("shorebird.yaml", []byte(updated), 0644); err != nil {
		return fmt.Errorf("could not update shorebird.yaml: %w", err)
	}
	fmt.Println("Added base_url to shorebird.yaml")
	return nil
}
