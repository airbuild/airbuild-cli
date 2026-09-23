package setup

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/airbuild/airbuild-cli/internal/config"
)

// ReactNativeChecklist returns the ordered checks for React Native CodePush.
func ReactNativeChecklist() Checklist {
	return Checklist{
		{Name: "node", Run: checkNode},
		{Name: "npx", Run: checkNpx},
		{Name: "expo-updates", Run: checkExpoUpdates},
		{Name: "expo-updates config", Run: checkExpoUpdatesConfig},
		{Name: ".airbuild.json", Run: checkAirbuildJSON},
		{Name: "api key", Run: checkAPIKey},
		{Name: "connectivity", Run: checkConnectivity},
	}
}

// ReactNativeInstallActions returns the checks that `install` can fix.
func ReactNativeInstallActions() Checklist {
	return Checklist{
		{Name: "expo-updates", Run: checkExpoUpdates, Fix: installExpoUpdates, FixOn: "install"},
	}
}

// ReactNativeInitActions returns the checks that `init` can fix.
func ReactNativeInitActions() Checklist {
	return Checklist{
		{Name: "expo-updates", Run: checkExpoUpdates, Fix: installExpoUpdates, FixOn: "init"},
		{Name: "expo-updates config", Run: checkExpoUpdatesConfig, Fix: initExpoUpdatesConfig, FixOn: "init"},
		{Name: ".airbuild.json", Run: checkAirbuildJSON},
	}
}

// --- React Native checks ---

func checkNode() CheckResult {
	path, ok := Which("node")
	if !ok {
		return Fail("node", "not found on PATH",
			"Install Node.js: https://nodejs.org/")
	}
	return Passf("node", "found at %s", path)
}

func checkNpx() CheckResult {
	path, ok := Which("npx")
	if !ok {
		return Fail("npx", "not found on PATH",
			"Install Node.js: https://nodejs.org/")
	}
	return Passf("npx", "found at %s", path)
}

func checkExpoUpdates() CheckResult {
	// Check package.json for expo-updates dependency
	pkgPath := FindFile("package.json")
	if pkgPath == "" {
		return Fail("expo-updates", "package.json not found — are you in a React Native project?",
			"Navigate to your React Native project directory")
	}
	if FileContains(pkgPath, `"expo-updates"`) {
		return Pass("expo-updates", "found in package.json")
	}
	return Fail("expo-updates", "not found in package.json dependencies",
		"Run `airbuild codepush react-native install` or `npx expo install expo-updates`")
}

func checkExpoUpdatesConfig() CheckResult {
	// Check app.json or app.config.js for updates.url
	if FileContains("app.json", `"updates"`) || FileContains("app.config.js", `updates`) {
		// Check if updates.url points to AirBuild
		if FileContains("app.json", "airbuild.dev") || FileContains("app.config.js", "airbuild.dev") {
			return Pass("expo-updates config", "configured for AirBuild")
		}
		return Warn("expo-updates config", "updates configured but may not point to AirBuild",
			"Run `airbuild codepush react-native init` to configure updates.url")
	}
	return Fail("expo-updates config", "updates not configured in app.json or app.config.js",
		"Run `airbuild codepush react-native init` to configure it")
}

// --- React Native fix actions ---

func installExpoUpdates() error {
	pkgPath := FindFile("package.json")
	if pkgPath == "" {
		return fmt.Errorf("package.json not found")
	}
	dir := "."
	if idx := strings.LastIndex(pkgPath, string(os.PathSeparator)); idx > 0 {
		dir = pkgPath[:idx]
	}

	// Detect if this is an Expo-managed project (has expo in deps) or bare RN
	isExpo := FileContains(pkgPath, `"expo"`)
	if isExpo {
		fmt.Println("Installing expo-updates via npx expo install...")
		return RunCmd(dir, "npx", "expo", "install", "expo-updates")
	}
	fmt.Println("Installing expo-updates via npm install...")
	return RunCmd(dir, "npm", "install", "expo-updates")
}

func initExpoUpdatesConfig() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	manifestURL := cfg.APIURL + "/api/codepush/react-native/manifest"

	// Try app.json first (Expo-managed)
	if FileExists("app.json") {
		return updateAppJSON(manifestURL)
	}
	// Try app.config.js (bare RN with Expo modules)
	if FileExists("app.config.js") {
		return updateAppConfigJS(manifestURL)
	}
	return fmt.Errorf(
		"no app.json or app.config.js found — create one and re-run `airbuild codepush react-native init`",
	)
}

// updateAppJSON adds the updates.url config to app.json.
func updateAppJSON(manifestURL string) error {
	data, err := os.ReadFile("app.json")
	if err != nil {
		return fmt.Errorf("could not read app.json: %w", err)
	}

	var appConfig map[string]interface{}
	if err := json.Unmarshal(data, &appConfig); err != nil {
		return fmt.Errorf("could not parse app.json: %w", err)
	}

	// Navigate to expo.updates
	expoSection, ok := appConfig["expo"].(map[string]interface{})
	if !ok {
		expoSection = make(map[string]interface{})
		appConfig["expo"] = expoSection
	}

	updates, ok := expoSection["updates"].(map[string]interface{})
	if !ok {
		updates = make(map[string]interface{})
		expoSection["updates"] = updates
	}

	// Check if already configured
	if existing, ok := updates["url"].(string); ok && strings.Contains(existing, "airbuild") {
		fmt.Println("expo-updates already configured for AirBuild")
		return nil
	}

	updates["url"] = manifestURL
	updates["checkAutomatically"] = "ON_LOAD"
	updates["fallbackToCacheTimeout"] = 0

	// Set runtimeVersion policy if not present
	if _, ok := expoSection["runtimeVersion"]; !ok {
		expoSection["runtimeVersion"] = map[string]interface{}{
			"policy": "appVersion",
		}
	}

	out, err := json.MarshalIndent(appConfig, "", "  ")
	if err != nil {
		return fmt.Errorf("could not marshal app.json: %w", err)
	}
	if err := os.WriteFile("app.json", append(out, '\n'), 0644); err != nil {
		return fmt.Errorf("could not write app.json: %w", err)
	}
	fmt.Println("Configured expo-updates in app.json")
	return nil
}

// updateAppConfigJS adds the updates.url config to app.config.js.
// This is harder to do programmatically — we append a note instead.
func updateAppConfigJS(manifestURL string) error {
	data, err := os.ReadFile("app.config.js")
	if err != nil {
		return err
	}
	if strings.Contains(string(data), "airbuild.dev") {
		fmt.Println("expo-updates already configured for AirBuild in app.config.js")
		return nil
	}
	// For JS config files, we can't reliably modify them programmatically.
	// Show the user what to add.
	fmt.Println("app.config.js detected — add the following to the 'expo' section:")
	fmt.Println()
	fmt.Println("  updates: {")
	fmt.Printf("    url: '%s',\n", manifestURL)
	fmt.Println("    checkAutomatically: 'ON_LOAD',")
	fmt.Println("    fallbackToCacheTimeout: 0,")
	fmt.Println("  },")
	fmt.Println("  runtimeVersion: { policy: 'appVersion' },")
	fmt.Println()
	fmt.Println("Then re-run `airbuild codepush react-native doctor` to verify.")
	return nil
}
