package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// chdir switches into a temp dir for the duration of the test and isolates
// HOME/USERPROFILE so config.Load() can't see the developer's real
// ~/.airbuild/config.json (which would make apiURL() machine-dependent).
func chdir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
	return dir
}

func writeFile(t *testing.T, name, content string) {
	t.Helper()
	if err := os.WriteFile(name, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// --- yamlTopLevel / yamlUpsert ---

func TestYamlTopLevelParsesFlatKeys(t *testing.T) {
	doc := "# comment\napp_id: abc123\nbase_url: https://airbuild.dev/api/codepush/flutter # inline note\nnested:\n  inner: ignored\nquoted: 'yes'\n"
	got := yamlTopLevel(doc)
	if got["app_id"] != "abc123" {
		t.Errorf("app_id = %q", got["app_id"])
	}
	if got["base_url"] != "https://airbuild.dev/api/codepush/flutter" {
		t.Errorf("base_url = %q", got["base_url"])
	}
	if _, ok := got["inner"]; ok {
		t.Error("indented key should be ignored")
	}
	if got["quoted"] != "yes" {
		t.Errorf("quoted value not unquoted: %q", got["quoted"])
	}
	if _, ok := got["nested"]; !ok {
		t.Error("nested parent key should appear with empty value")
	}
}

func TestYamlUpsertReplacesExisting(t *testing.T) {
	doc := "app_id: abc\nbase_url: https://old.example.com\n"
	out := yamlUpsert(doc, "base_url", "https://new.example.com")
	if !strings.Contains(out, "base_url: https://new.example.com") {
		t.Fatalf("value not replaced: %q", out)
	}
	if strings.Contains(out, "old.example.com") {
		t.Fatalf("old value leaked: %q", out)
	}
	if !strings.HasPrefix(out, "app_id: abc\n") {
		t.Fatalf("other keys disturbed: %q", out)
	}
}

func TestYamlUpsertAppendsMissing(t *testing.T) {
	out := yamlUpsert("app_id: abc\n", "channel", "production")
	if out != "app_id: abc\nchannel: production\n" {
		t.Fatalf("unexpected output: %q", out)
	}
	// no trailing newline in input
	out = yamlUpsert("app_id: abc", "channel", "production")
	if out != "app_id: abc\nchannel: production\n" {
		t.Fatalf("unexpected output: %q", out)
	}
}

// --- packageDeps ---

func TestPackageDepsMergesDepsAndDevDeps(t *testing.T) {
	dir := chdir(t)
	writeFile(t, "package.json", `{
		"dependencies": {"expo": "^52.0.0", "react-native": "0.76.0"},
		"devDependencies": {"expo-updates": "~0.26.0"}
	}`)
	deps, err := packageDeps(filepath.Join(dir, "package.json"))
	if err != nil {
		t.Fatalf("packageDeps: %v", err)
	}
	if _, ok := deps["expo"]; !ok {
		t.Error("missing expo dependency")
	}
	if _, ok := deps["expo-updates"]; !ok {
		t.Error("missing expo-updates devDependency")
	}
}

func TestPackageDepsMalformed(t *testing.T) {
	dir := chdir(t)
	writeFile(t, "package.json", `{not json`)
	if _, err := packageDeps(filepath.Join(dir, "package.json")); err == nil {
		t.Fatal("expected error for malformed package.json")
	}
}

// --- checkExpoUpdates ---

func TestCheckExpoUpdatesMissingPackageJSON(t *testing.T) {
	chdir(t)
	r := checkExpoUpdates()
	if r.Status != StatusFail {
		t.Fatalf("expected fail, got %v", r.Status)
	}
}

func TestCheckExpoUpdatesPresent(t *testing.T) {
	chdir(t)
	writeFile(t, "package.json", `{"dependencies": {"expo-updates": "~0.26.0"}}`)
	if r := checkExpoUpdates(); r.Status != StatusPass {
		t.Fatalf("expected pass, got %v (%s)", r.Status, r.Message)
	}
}

func TestCheckExpoUpdatesAbsent(t *testing.T) {
	chdir(t)
	// Bare RN app with expo-modules-core but NOT expo-updates: the old
	// substring check would false-positive on "expo" here.
	writeFile(t, "package.json", `{"dependencies": {"react-native": "0.76.0", "expo-modules-core": "^2.0.0"}}`)
	if r := checkExpoUpdates(); r.Status != StatusFail {
		t.Fatalf("expected fail, got %v (%s)", r.Status, r.Message)
	}
}

// --- updateAppJSON ---

func TestUpdateAppJSONPreservesExistingKeys(t *testing.T) {
	chdir(t)
	writeFile(t, "app.json", "{\n  \"expo\": {\n    \"name\": \"MyApp\",\n    \"slug\": \"myapp\"\n  }\n}\n")

	if err := updateAppJSON("https://airbuild.dev/api/codepush/react-native/manifest?key=dk_1"); err != nil {
		t.Fatalf("updateAppJSON: %v", err)
	}

	data, err := os.ReadFile("app.json")
	if err != nil {
		t.Fatalf("read app.json: %v", err)
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("app.json is not valid JSON: %v", err)
	}
	expo := cfg["expo"].(map[string]interface{})
	if expo["name"] != "MyApp" || expo["slug"] != "myapp" {
		t.Error("existing expo keys were lost")
	}
	updates := expo["updates"].(map[string]interface{})
	if updates["url"] != "https://airbuild.dev/api/codepush/react-native/manifest?key=dk_1" {
		t.Errorf("updates.url = %v", updates["url"])
	}
	headers := updates["requestHeaders"].(map[string]interface{})
	if headers["expo-channel-name"] != "production" {
		t.Errorf("expo-channel-name = %v", headers["expo-channel-name"])
	}
	rv := expo["runtimeVersion"].(map[string]interface{})
	if rv["policy"] != "appVersion" {
		t.Errorf("runtimeVersion = %v", rv)
	}
}

func TestUpdateAppJSONKeepsUserValues(t *testing.T) {
	chdir(t)
	writeFile(t, "app.json", `{"expo":{"updates":{"checkAutomatically":"ON_ERROR_RECOVERY"},"runtimeVersion":"1.2.3"}}`)

	if err := updateAppJSON("https://airbuild.dev/api/codepush/react-native/manifest?key=dk_9"); err != nil {
		t.Fatalf("updateAppJSON: %v", err)
	}
	data, _ := os.ReadFile("app.json")
	var cfg map[string]interface{}
	_ = json.Unmarshal(data, &cfg)
	expo := cfg["expo"].(map[string]interface{})
	updates := expo["updates"].(map[string]interface{})
	if updates["checkAutomatically"] != "ON_ERROR_RECOVERY" {
		t.Error("user's checkAutomatically was overwritten")
	}
	if expo["runtimeVersion"] != "1.2.3" {
		t.Error("user's runtimeVersion was overwritten")
	}
}

// --- checkExpoUpdatesConfig ---

func TestCheckExpoUpdatesConfigPassesWithAirbuildURL(t *testing.T) {
	chdir(t)
	writeFile(t, "app.json", `{"expo":{"updates":{"url":"https://airbuild.dev/api/codepush/react-native/manifest?key=dk_1"}}}`)
	if r := checkExpoUpdatesConfig(); r.Status != StatusPass {
		t.Fatalf("expected pass, got %v (%s)", r.Status, r.Message)
	}
}

func TestCheckExpoUpdatesConfigFailsOnMissingKey(t *testing.T) {
	chdir(t)
	writeFile(t, "app.json", `{"expo":{"updates":{"url":"https://airbuild.dev/api/codepush/react-native/manifest"}}}`)
	if r := checkExpoUpdatesConfig(); r.Status != StatusFail {
		t.Fatalf("expected fail for missing ?key=, got %v", r.Status)
	}
}

func TestCheckExpoUpdatesConfigFailsOnForeignURL(t *testing.T) {
	chdir(t)
	writeFile(t, "app.json", `{"expo":{"updates":{"url":"https://u.expo.dev/abc"}}}`)
	if r := checkExpoUpdatesConfig(); r.Status != StatusFail {
		t.Fatalf("expected fail for non-AirBuild URL, got %v", r.Status)
	}
}

func TestCheckExpoUpdatesConfigAppConfigJS(t *testing.T) {
	chdir(t)
	writeFile(t, "app.config.js", `module.exports = { expo: { updates: { url: 'https://airbuild.dev/api/codepush/react-native/manifest?key=dk_1' } } };`)
	if r := checkExpoUpdatesConfig(); r.Status != StatusPass {
		t.Fatalf("expected pass via app.config.js, got %v (%s)", r.Status, r.Message)
	}
}

func TestCheckExpoUpdatesConfigAppConfigJSUnconfigured(t *testing.T) {
	chdir(t)
	writeFile(t, "app.config.js", `module.exports = { expo: { name: 'x' } };`)
	if r := checkExpoUpdatesConfig(); r.Status != StatusWarn {
		t.Fatalf("expected warn for unconfigured app.config.js, got %v", r.Status)
	}
}

func TestCheckExpoUpdatesConfigNothingPresent(t *testing.T) {
	chdir(t)
	if r := checkExpoUpdatesConfig(); r.Status != StatusFail {
		t.Fatalf("expected fail, got %v", r.Status)
	}
}

// --- checkShorebirdYAML ---

func TestCheckShorebirdYAMLMissing(t *testing.T) {
	chdir(t)
	if r := checkShorebirdYAML(); r.Status != StatusFail {
		t.Fatalf("expected fail, got %v", r.Status)
	}
}

func TestCheckShorebirdYAMLComplete(t *testing.T) {
	chdir(t)
	writeFile(t, "shorebird.yaml", "app_id: abc\nbase_url: https://airbuild.dev/api/codepush/flutter\ndistribution_key: dk_1\nchannel: production\nauto_update: true\n")
	if r := checkShorebirdYAML(); r.Status != StatusPass {
		t.Fatalf("expected pass, got %v (%s)", r.Status, r.Message)
	}
}

func TestCheckShorebirdYAMLWrongBaseURL(t *testing.T) {
	chdir(t)
	writeFile(t, "shorebird.yaml", "app_id: abc\nbase_url: https://api.shorebird.dev\ndistribution_key: dk_1\n")
	if r := checkShorebirdYAML(); r.Status != StatusFail {
		t.Fatalf("expected fail for wrong base_url, got %v", r.Status)
	}
}

func TestCheckShorebirdYAMLMissingKey(t *testing.T) {
	chdir(t)
	writeFile(t, "shorebird.yaml", "app_id: abc\nbase_url: https://airbuild.dev/api/codepush/flutter\n")
	if r := checkShorebirdYAML(); r.Status != StatusFail {
		t.Fatalf("expected fail for missing distribution_key, got %v", r.Status)
	}
}

// --- checkAPIKey ---

func TestCheckAPIKeyFailsWhenNotLoggedIn(t *testing.T) {
	chdir(t) // empty HOME → no ~/.airbuild/config.json
	if r := checkAPIKey(); r.Status != StatusFail {
		t.Fatalf("expected fail, got %v", r.Status)
	}
}
