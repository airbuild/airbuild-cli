package setup

import (
	"fmt"
	"os"
	"strings"

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
		{Name: "shorebird", Run: checkShorebird, Fix: installShorebird,
			Modifies: "global Dart packages (dart pub global activate shorebird_cli)"},
	}
}

// FlutterInitActions returns the checks that `init` can fix.
func FlutterInitActions() Checklist {
	return Checklist{
		{Name: "shorebird.yaml", Run: checkShorebirdYAML, Fix: initShorebirdYAML, Modifies: "shorebird.yaml"},
	}
}

func flutterBaseURL() string {
	return apiURL() + "/api/codepush/flutter"
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
	data, err := os.ReadFile("shorebird.yaml")
	if err != nil {
		return Fail("shorebird.yaml", "not found in current directory",
			"Run `airbuild codepush flutter init` to create it")
	}
	values := yamlTopLevel(string(data))
	want := flutterBaseURL()
	if values["base_url"] != want {
		return Fail("shorebird.yaml", fmt.Sprintf("base_url is %q, expected %q", values["base_url"], want),
			"Run `airbuild codepush flutter init` to point it at AirBuild")
	}
	if values["distribution_key"] == "" {
		return Fail("shorebird.yaml", "distribution_key is missing",
			"Run `airbuild codepush flutter init` to add it")
	}
	if values["app_id"] == "" {
		return Warn("shorebird.yaml", "app_id is missing",
			"Run `airbuild codepush flutter init` to add it")
	}
	return Pass("shorebird.yaml", "configured for AirBuild")
}

// --- Flutter fix actions ---

func installShorebird() error {
	if err := RunCmd(".", "dart", "pub", "global", "activate", "shorebird_cli"); err != nil {
		return fmt.Errorf("failed to install shorebird_cli: %w", err)
	}
	return nil
}

// initShorebirdYAML creates or updates shorebird.yaml so the Shorebird
// updater checks AirBuild. Existing keys other than base_url and
// distribution_key are preserved; app_id is only added when missing.
func initShorebirdYAML() error {
	dk, err := distributionKey()
	if err != nil {
		return err
	}
	proj, err := project.Load()
	if err != nil {
		return fmt.Errorf("no linked app — run `airbuild init` first")
	}

	existing := ""
	if data, err := os.ReadFile("shorebird.yaml"); err == nil {
		existing = string(data)
	} else {
		existing = "# Shorebird configuration for AirBuild CodePush.\n" +
			"# base_url points the Shorebird updater at AirBuild instead of Shorebird's cloud.\n"
	}

	updated := existing
	if yamlTopLevel(updated)["app_id"] == "" {
		updated = yamlUpsert(updated, "app_id", proj.AppID)
	}
	updated = yamlUpsert(updated, "base_url", flutterBaseURL())
	updated = yamlUpsert(updated, "distribution_key", dk)
	if yamlTopLevel(updated)["channel"] == "" {
		updated = yamlUpsert(updated, "channel", "production")
	}
	// Matches the dashboard's integration snippet — enables auto-apply of
	// downloaded patches by the Shorebird updater.
	if yamlTopLevel(updated)["auto_update"] == "" {
		updated = yamlUpsert(updated, "auto_update", "true")
	}

	if err := os.WriteFile("shorebird.yaml", []byte(updated), 0644); err != nil {
		return fmt.Errorf("could not write shorebird.yaml: %w", err)
	}
	return nil
}

// yamlTopLevel extracts simple top-level `key: value` scalars from a YAML
// document. Nested structures and multi-line values are ignored, which is
// sufficient for shorebird.yaml's flat schema.
func yamlTopLevel(doc string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(doc, "\n") {
		if line == "" || line[0] == ' ' || line[0] == '\t' || line[0] == '#' {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if i := strings.Index(v, " #"); i >= 0 {
			v = v[:i]
		}
		out[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
	}
	return out
}

// yamlUpsert replaces a top-level `key: value` line, or appends it.
func yamlUpsert(doc, key, value string) string {
	lines := strings.Split(doc, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, key+":") {
			lines[i] = fmt.Sprintf("%s: %s", key, value)
			return strings.Join(lines, "\n")
		}
	}
	if doc != "" && !strings.HasSuffix(doc, "\n") {
		doc += "\n"
	}
	return doc + fmt.Sprintf("%s: %s\n", key, value)
}
