package setup

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// ReactNativeChecklist returns the ordered checks for React Native CodePush.
func ReactNativeChecklist() Checklist {
	return Checklist{
		{Name: "node", Run: checkNode},
		{Name: "npx", Run: checkNpx},
		{Name: "expo-updates", Run: checkExpoUpdates},
		{Name: "expo-updates config", Run: checkExpoUpdatesConfig},
		{Name: "expo-updates manifest", Run: checkExpoUpdatesManifest},
		{Name: "expo cliFile", Run: checkExpoCliFile},
		{Name: "ios bundleIdentifier", Run: checkIOSBundleIdentifier},
		{Name: "telemetry module", Run: checkTelemetry},
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
		{Name: "telemetry module", Run: checkTelemetry, Fix: writeTelemetry,
			Modifies: "airbuild-telemetry.js"},
	}
}

// ReactNativeInitActions returns the checks that `init` can fix.
func ReactNativeInitActions() Checklist {
	return Checklist{
		{Name: "expo-updates", Run: checkExpoUpdates, Fix: installExpoUpdates,
			Modifies: "package.json, lockfile, node_modules"},
		{Name: "expo-updates config", Run: checkExpoUpdatesConfig, Fix: initExpoUpdatesConfig,
			Modifies: "app.json"},
		{Name: "expo-updates manifest", Run: checkExpoUpdatesManifest, Fix: initExpoUpdatesManifest,
			Modifies: "android/app/src/main/AndroidManifest.xml"},
		{Name: "expo cliFile", Run: checkExpoCliFile, Fix: fixExpoCliFile,
			Modifies: "android/app/build.gradle"},
		{Name: "ios bundleIdentifier", Run: checkIOSBundleIdentifier, Fix: fixIOSBundleIdentifier,
			Modifies: "app.json"},
		{Name: "telemetry module", Run: checkTelemetry, Fix: writeTelemetry,
			Modifies: "airbuild-telemetry.js"},
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

const (
	rnAndroidManifestPath = "android/app/src/main/AndroidManifest.xml"
	rnAppBuildGradlePath  = "android/app/build.gradle"
)

// checkExpoUpdatesManifest verifies the expo-updates meta-data block exists in
// AndroidManifest.xml. app.json's expo.updates section only feeds `expo
// export` — the *runtime* reads EXPO_UPDATE_URL from the manifest meta-data,
// so bare-RN projects are silently non-updating without it.
func checkExpoUpdatesManifest() CheckResult {
	if !FileExists(rnAndroidManifestPath) {
		return Pass("expo-updates manifest", "no AndroidManifest.xml (Expo-managed or iOS-only)")
	}
	if !FileContains(rnAndroidManifestPath, "expo.modules.updates.EXPO_UPDATE_URL") ||
		!FileContains(rnAndroidManifestPath, "expo.modules.updates.ENABLED") {
		return Warn("expo-updates manifest",
			"expo.modules.updates meta-data missing — bare-RN apps need it or the runtime never checks for updates",
			"Run `airbuild codepush react-native init` to write it")
	}
	if FileContains(rnAndroidManifestPath, rnManifestBase()) {
		return Pass("expo-updates manifest", "AndroidManifest.xml points to AirBuild")
	}
	return Warn("expo-updates manifest",
		"meta-data present but EXPO_UPDATE_URL does not point to this AirBuild server",
		"Run `airbuild codepush react-native init` to rewrite it")
}

var rnVersionNameRe = regexp.MustCompile(`versionName\s+["']([^"']+)["']`)
var rnUpdatesMetaRe = regexp.MustCompile(`(?m)^[^\n]*android:name="expo\.modules\.updates\.[^\n]*\n?`)

func rnVersionName() string {
	data, err := os.ReadFile(rnAppBuildGradlePath)
	if err == nil {
		if m := rnVersionNameRe.FindSubmatch(data); m != nil {
			return string(m[1])
		}
	}
	return "1.0.0"
}

// initExpoUpdatesManifest writes (or refreshes) the expo.modules.updates
// meta-data block inside <application> — URL with the distribution key,
// runtime version from build.gradle's versionName, ON_LOAD checking, and the
// production channel header.
func initExpoUpdatesManifest() error {
	if !FileExists(rnAndroidManifestPath) {
		return nil // Expo-managed or iOS-only — nothing to do.
	}
	dk, err := distributionKey()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(rnAndroidManifestPath)
	if err != nil {
		return fmt.Errorf("could not read %s: %w", rnAndroidManifestPath, err)
	}
	s := rnUpdatesMetaRe.ReplaceAllString(string(data), "")
	q := func(v string) string { return strings.ReplaceAll(v, "\"", "&quot;") }
	block := fmt.Sprintf("      <meta-data android:name=\"expo.modules.updates.ENABLED\" android:value=\"true\" />\n"+
		"      <meta-data android:name=\"expo.modules.updates.EXPO_UPDATE_URL\" android:value=\"%s\" />\n"+
		"      <meta-data android:name=\"expo.modules.updates.EXPO_RUNTIME_VERSION\" android:value=\"%s\" />\n"+
		"      <meta-data android:name=\"expo.modules.updates.EXPO_UPDATES_CHECK_ON_LAUNCH\" android:value=\"ALWAYS\" />\n"+
		"      <meta-data android:name=\"expo.modules.updates.EXPO_UPDATES_LAUNCH_WAIT_MS\" android:value=\"0\" />\n"+
		"      <meta-data android:name=\"expo.modules.updates.EXPO_REQUEST_HEADERS\" android:value=\"{%s}\" />\n",
		q(rnManifestBase()+"?key="+dk), q(rnVersionName()), q(`"expo-channel-name":"production"`))
	idx := strings.Index(s, "<activity")
	if idx < 0 {
		idx = strings.Index(s, "</application>")
	}
	if idx < 0 {
		return fmt.Errorf("could not locate <application> body in %s", rnAndroidManifestPath)
	}
	return os.WriteFile(rnAndroidManifestPath, []byte(s[:idx]+block+s[idx:]), 0644)
}

var expoCliResolveRe = regexp.MustCompile(`require\.resolve\(['"]@expo/cli['"]\)`)
var expoCliFileLineRe = regexp.MustCompile(`(?m)^\s*cliFile\s*=\s*new File\(\[.*require\.resolve\(['"]@expo/cli['"]\).*$`)

// checkExpoCliFile detects the install-expo-modules generated line
//   cliFile = new File(["node","--print","require.resolve('@expo/cli')"].execute(null, rootDir).text.trim())
// which fails silently on Node versions whose exports map hides @expo/cli —
// producing `new File("")`, i.e. the project directory, so Gradle fails with
// "cliFile is not a file" plus a cascade of implicit-dependency errors.
func checkExpoCliFile() CheckResult {
	if !FileExists(rnAppBuildGradlePath) {
		return Pass("expo cliFile", "no android project — skipping")
	}
	data, err := os.ReadFile(rnAppBuildGradlePath)
	if err != nil || !expoCliResolveRe.Match(data) {
		return Pass("expo cliFile", "cliFile does not use require.resolve('@expo/cli')")
	}
	out, err := exec.Command("node", "--print", "require.resolve('@expo/cli')").Output()
	if err == nil && strings.TrimSpace(string(out)) != "" {
		return Pass("expo cliFile", "require.resolve('@expo/cli') resolves on this Node")
	}
	return Warn("expo cliFile",
		"generated cliFile uses require.resolve('@expo/cli') which fails on this Node — Gradle would set cliFile to a directory",
		"Run `airbuild codepush react-native init` to point cliFile at node_modules/expo/bin/cli")
}

// fixExpoCliFile replaces the fragile generated resolution with the concrete
// CLI entry — node_modules/expo/bin/cli is what `npx expo` links to and it
// supports the `export:embed` bundle command.
func fixExpoCliFile() error {
	data, err := os.ReadFile(rnAppBuildGradlePath)
	if err != nil {
		return fmt.Errorf("could not read %s: %w", rnAppBuildGradlePath, err)
	}
	if !expoCliFileLineRe.Match(data) {
		return ErrManualAction
	}
	s := expoCliFileLineRe.ReplaceAllStringFunc(string(data), func(string) string {
		return `    cliFile = file("${rootDir}/../node_modules/expo/bin/cli")`
	})
	return os.WriteFile(rnAppBuildGradlePath, []byte(s), 0644)
}

func checkIOSBundleIdentifier() CheckResult {
	if !FileExists("app.json") {
		return Warn("ios bundleIdentifier", "no app.json — iOS bundle ID unchecked",
			"iOS builds need expo.ios.bundleIdentifier in app.json")
	}
	data, err := os.ReadFile("app.json")
	if err != nil {
		return Warn("ios bundleIdentifier", err.Error(), "Fix the syntax error in app.json")
	}
	var cfg struct {
		Expo struct {
			IOS struct {
				BundleIdentifier string `json:"bundleIdentifier"`
			} `json:"ios"`
		} `json:"expo"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Warn("ios bundleIdentifier", err.Error(), "Fix the syntax error in app.json")
	}
	if cfg.Expo.IOS.BundleIdentifier != "" {
		return Passf("ios bundleIdentifier", cfg.Expo.IOS.BundleIdentifier)
	}
	return Warn("ios bundleIdentifier", "not set — required before building for iOS",
		"Run `airbuild codepush react-native init` to derive one automatically")
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

// fixIOSBundleIdentifier derives and writes expo.ios.bundleIdentifier when
// missing. No-op for app.config.js/ts projects (can't be edited reliably).
func fixIOSBundleIdentifier() error {
	if !FileExists("app.json") {
		return ErrManualAction
	}
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
		return fmt.Errorf("app.json has no expo section")
	}
	ios, ok := expo["ios"].(map[string]interface{})
	if !ok {
		ios = map[string]interface{}{}
		expo["ios"] = ios
	}
	if _, ok := ios["bundleIdentifier"]; ok {
		return nil
	}
	bid := deriveBundleIdentifier(expo)
	if bid == "" {
		return fmt.Errorf("could not derive a bundle identifier — set expo.ios.bundleIdentifier manually")
	}
	ios["bundleIdentifier"] = bid
	out, err := json.MarshalIndent(appConfig, "", "  ")
	if err != nil {
		return fmt.Errorf("could not encode app.json: %w", err)
	}
	return os.WriteFile("app.json", append(out, '\n'), 0644)
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

	// iOS builds fail without a bundleIdentifier. Derive one from
	// android.package or the app slug — iOS bundle IDs allow only
	// [A-Za-z0-9.-], so underscores/hyphens-in-segment issues like
	// "com.anonymous.codepush_rn" must be sanitized.
	ios, ok := expo["ios"].(map[string]interface{})
	if !ok {
		ios = map[string]interface{}{}
		expo["ios"] = ios
	}
	if _, ok := ios["bundleIdentifier"]; !ok {
		if bid := deriveBundleIdentifier(expo); bid != "" {
			ios["bundleIdentifier"] = bid
		}
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

// deriveBundleIdentifier builds an iOS-safe bundle ID from android.package
// (preferred) or the app slug. iOS IDs allow only [A-Za-z0-9.-]; segments
// must not be empty.
func deriveBundleIdentifier(expo map[string]interface{}) string {
	src := ""
	if android, ok := expo["android"].(map[string]interface{}); ok {
		if pkg, _ := android["package"].(string); pkg != "" {
			src = pkg
		}
	}
	if src == "" {
		slug, _ := expo["slug"].(string)
		name, _ := expo["name"].(string)
		switch {
		case slug != "":
			src = "com.anonymous." + slug
		case name != "":
			src = "com.anonymous." + name
		default:
			return ""
		}
	}
	var b strings.Builder
	for _, r := range src {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-':
			b.WriteRune(r)
		}
	}
	id := b.String()
	// Drop empty segments produced by removed characters (e.g. "a_.b").
	parts := strings.Split(id, ".")
	kept := parts[:0]
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	if len(kept) < 2 {
		return ""
	}
	return strings.Join(kept, ".")
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
	fmt.Println("  ios: { bundleIdentifier: 'com.example.myapp' }, // required for iOS builds")
	fmt.Println()
	fmt.Println("Then run `airbuild codepush react-native doctor` to verify.")
}
