package setup

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
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
		{Name: "expo-updates", Run: checkExpoUpdates, Fix: installExpoUpdates,
			Modifies: "package.json, lockfile, node_modules"},
	}
}

// ReactNativeInitActions returns the checks that `init` can fix.
func ReactNativeInitActions() Checklist {
	return Checklist{
		{Name: "expo-updates", Run: checkExpoUpdates, Fix: installExpoUpdates,
			Modifies: "package.json, lockfile, node_modules"},
		{Name: "expo-updates config", Run: checkExpoUpdatesConfig, Fix: initExpoUpdatesConfig,
			Modifies: "app.json"},
	}
}

var appConfigScripts = []string{"app.config.js", "app.config.ts"}

func rnManifestBase() string {
	return apiURL() + "/api/codepush/react-native/manifest"
}

// --- React Native checks ---

func checkNode() CheckResult {
	path, ok := Which("node")
	if !ok {
		return Fail("node", "not found on PATH", "Install Node.js: https://nodejs.org/")
	}
	return Passf("node", "found at %s", path)
}

func checkNpx() CheckResult {
	path, ok := Which("npx")
	if !ok {
		return Fail("npx", "not found on PATH", "Install Node.js: https://nodejs.org/")
	}
	return Passf("npx", "found at %s", path)
}

// packageDeps returns the merged dependencies + devDependencies of package.json.
func packageDeps(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var pkg struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil, fmt.Errorf("could not parse %s: %w", path, err)
	}
	deps := map[string]string{}
	for k, v := range pkg.DevDependencies {
		deps[k] = v
	}
	for k, v := range pkg.Dependencies {
		deps[k] = v
	}
	return deps, nil
}

func checkExpoUpdates() CheckResult {
	if !FileExists("package.json") {
		return Fail("expo-updates", "package.json not found — are you in a React Native project?",
			"Run this command from your React Native project root")
	}
	deps, err := packageDeps("package.json")
	if err != nil {
		return Fail("expo-updates", err.Error(), "Fix the syntax error in package.json")
	}
	if v, ok := deps["expo-updates"]; ok {
		return Passf("expo-updates", "found in package.json (%s)", v)
	}
	return Fail("expo-updates", "not found in package.json dependencies",
		"Run `airbuild codepush react-native install`")
}

func checkExpoUpdatesConfig() CheckResult {
	base := rnManifestBase()
	if FileExists("app.json") {
		url, err := appJSONUpdatesURL()
		if err != nil {
			return Fail("expo-updates config", err.Error(), "Fix the syntax error in app.json")
		}
		switch {
		case url == "":
			// Fall through to app.config.* below — app.json may be a stub.
		case strings.HasPrefix(url, base) && strings.Contains(url, "key="):
			return Pass("expo-updates config", "app.json points to AirBuild")
		case strings.HasPrefix(url, base):
			return Fail("expo-updates config", "updates.url is missing the ?key= distribution key",
				"Run `airbuild codepush react-native init`")
		default:
			return Fail("expo-updates config", fmt.Sprintf("updates.url is %q, expected %s?key=…", url, base),
				"Run `airbuild codepush react-native init` to point it at AirBuild")
		}
	}
	for _, f := range appConfigScripts {
		if FileContains(f, base) {
			return Passf("expo-updates config", "%s points to AirBuild", f)
		}
		if FileExists(f) {
			return Warn("expo-updates config", fmt.Sprintf("%s does not reference %s", f, base),
				"Run `airbuild codepush react-native init` for the snippet to add")
		}
	}
	return Fail("expo-updates config", "updates.url not configured in app.json",
		"Run `airbuild codepush react-native init` to configure it")
}

func appJSONUpdatesURL() (string, error) {
	data, err := os.ReadFile("app.json")
	if err != nil {
		return "", err
	}
	var cfg struct {
		Expo struct {
			Updates struct {
				URL string `json:"url"`
			} `json:"updates"`
		} `json:"expo"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return "", fmt.Errorf("could not parse app.json: %w", err)
	}
	return cfg.Expo.Updates.URL, nil
}

// --- React Native fix actions ---

func installExpoUpdates() error {
	if !FileExists("package.json") {
		return fmt.Errorf("package.json not found — run this from your project root")
	}
	deps, err := packageDeps("package.json")
	if err != nil {
		return err
	}
	// `npx expo install` picks an SDK-compatible version, but only works when
	// the `expo` package itself is a dependency. Bare RN apps use npm.
	if _, isExpo := deps["expo"]; isExpo {
		return RunCmd(".", "npx", "expo", "install", "expo-updates")
	}
	return RunCmd(".", "npm", "install", "expo-updates")
}

func initExpoUpdatesConfig() error {
	dk, err := distributionKey()
	if err != nil {
		return err
	}
	manifestURL := rnManifestBase() + "?key=" + dk

	if FileExists("app.json") {
		if url, _ := appJSONUpdatesURL(); url != "" || !hasAppConfigScript() {
			return updateAppJSON(manifestURL)
		}
	}
	if hasAppConfigScript() {
		printAppConfigSnippet(manifestURL)
		return ErrManualAction
	}
	return fmt.Errorf("no app.json or app.config.js/ts found — create one and re-run `airbuild codepush react-native init`")
}

func hasAppConfigScript() bool {
	for _, f := range appConfigScripts {
		if FileExists(f) {
			return true
		}
	}
	return false
}

// updateAppJSON writes expo.updates.url (plus sensible defaults) to app.json.
func updateAppJSON(manifestURL string) error {
	data, err := os.ReadFile("app.json")
	if err != nil {
		return fmt.Errorf("could not read app.json: %w", err)
	}
	var appConfig map[string]interface{}
	if err := json.Unmarshal(data, &appConfig); err != nil {
		return fmt.Errorf("could not parse app.json: %w", err)
	}

	expo, ok := appConfig["expo"].(map[string]interface{})
	if !ok {
		expo = map[string]interface{}{}
		appConfig["expo"] = expo
	}
	updates, ok := expo["updates"].(map[string]interface{})
	if !ok {
		updates = map[string]interface{}{}
		expo["updates"] = updates
	}

	updates["url"] = manifestURL
	if _, ok := updates["checkAutomatically"]; !ok {
		updates["checkAutomatically"] = "ON_LOAD"
	}
	if _, ok := updates["fallbackToCacheTimeout"]; !ok {
		updates["fallbackToCacheTimeout"] = 0
	}
	headers, ok := updates["requestHeaders"].(map[string]interface{})
	if !ok {
		headers = map[string]interface{}{}
		updates["requestHeaders"] = headers
	}
	if _, ok := headers["expo-channel-name"]; !ok {
		headers["expo-channel-name"] = "production"
	}
	if _, ok := expo["runtimeVersion"]; !ok {
		expo["runtimeVersion"] = map[string]interface{}{"policy": "appVersion"}
	}

	out, err := json.MarshalIndent(appConfig, "", "  ")
	if err != nil {
		return fmt.Errorf("could not encode app.json: %w", err)
	}
	if err := os.WriteFile("app.json", append(out, '\n'), 0644); err != nil {
		return fmt.Errorf("could not write app.json: %w", err)
	}
	return nil
}

// printAppConfigSnippet shows what to add to app.config.js/ts, which can't be
// edited reliably without executing it.
func printAppConfigSnippet(manifestURL string) {
	fmt.Println("app.config.js/ts detected — add the following to the `expo` section:")
	fmt.Println()
	fmt.Println("  updates: {")
	fmt.Printf("    url: '%s',\n", manifestURL)
	fmt.Println("    requestHeaders: { 'expo-channel-name': 'production' },")
	fmt.Println("    checkAutomatically: 'ON_LOAD',")
	fmt.Println("    fallbackToCacheTimeout: 0,")
	fmt.Println("  },")
	fmt.Println("  runtimeVersion: { policy: 'appVersion' },")
	fmt.Println()
	fmt.Println("Then run `airbuild codepush react-native doctor` to verify.")
}
