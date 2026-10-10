package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/airbuild/airbuild-cli/internal/project"
	"github.com/airbuild/airbuild-cli/internal/shorebird"
)

// FlutterChecklist returns the ordered checks for Flutter CodePush.
func FlutterChecklist() Checklist {
	return Checklist{
		{Name: "dart", Run: checkDart},
		{Name: "flutter", Run: checkFlutter},
		{Name: "shorebird", Run: checkShorebird},
		{Name: "shorebird.yaml", Run: checkShorebirdYAML},
		{Name: "pubspec.yaml", Run: checkPubspecAssets},
		{Name: "android permissions", Run: checkInternetPermission},
		{Name: ".airbuild.json", Run: checkAirbuildJSON},
		{Name: "api key", Run: checkAPIKey},
		{Name: "connectivity", Run: checkConnectivity},
	}
}

// FlutterInstallActions returns the checks that `install` can fix.
func FlutterInstallActions() Checklist {
	return Checklist{
		{Name: "shorebird", Run: checkShorebird, Fix: installShorebird,
			Modifies: "~/.shorebird and PATH in shell config"},
	}
}

// FlutterInitActions returns the checks that `init` can fix.
func FlutterInitActions() Checklist {
	return Checklist{
		{Name: "shorebird.yaml", Run: checkShorebirdYAML, Fix: initShorebirdYAML, Modifies: "shorebird.yaml"},
		{Name: "pubspec.yaml", Run: checkPubspecAssets, Fix: fixPubspecAssets, Modifies: "pubspec.yaml"},
		{Name: "android permissions", Run: checkInternetPermission, Fix: fixInternetPermission,
			Modifies: "android/app/src/main/AndroidManifest.xml"},
	}
}

// flutterBaseURL returns the base_url written to shorebird.yaml. The
// Shorebird updater appends /api/v1/patches/check (and /api/v1/patches/events)
// to it, so the canonical value is the bare API origin — matching how
// Shorebird's own api.shorebird.dev is used.
func flutterBaseURL() string {
	return apiURL()
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
	if path, ok := Which("shorebird"); ok {
		return Passf("shorebird", "found at %s", path)
	}
	// Shorebird can be installed but not yet on PATH (e.g. the user hasn't
	// restarted their shell after installing). AirBuild resolves the
	// default install location automatically, so this is still a pass —
	// only direct `shorebird` invocations need the PATH entry.
	if p := shorebird.DefaultBinary(); p != "" {
		return Passf("shorebird", "installed at %s (not on PATH — restart your terminal to run it directly)", p)
	}
	return Fail("shorebird", "not found on PATH",
		"Run `airbuild codepush flutter install` or see https://docs.shorebird.dev")
}

func checkShorebirdYAML() CheckResult {
	data, err := os.ReadFile("shorebird.yaml")
	if err != nil {
		return Fail("shorebird.yaml", "not found in current directory",
			"Run `airbuild codepush flutter init` to create it")
	}
	values := yamlTopLevel(string(data))
	// Canonical base_url is the bare API origin (the updater appends
	// /api/v1/patches/check). The legacy /api/codepush/flutter suffix still
	// works — the server mounts compat routes for it — so accept either.
	want := flutterBaseURL()
	legacy := want + "/api/codepush/flutter"
	if values["base_url"] != want && values["base_url"] != legacy {
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

// checkPubspecAssets verifies shorebird.yaml is bundled as a Flutter
// asset. The updater embedded in the Shorebird engine reads its config
// (app_id, base_url, distribution_key) from that bundled asset at
// runtime — a project that builds without it can never check for patches.
func checkPubspecAssets() CheckResult {
	data, err := os.ReadFile("pubspec.yaml")
	if err != nil {
		return Fail("pubspec.yaml", "not found in current directory",
			"Run this command in your Flutter project root")
	}
	inSection, _ := flutterAssetEntry(string(data), "shorebird.yaml")
	if inSection {
		return Pass("pubspec.yaml", "bundles shorebird.yaml")
	}
	return Fail("pubspec.yaml", "shorebird.yaml is not listed under flutter: assets:",
		"Run `airbuild codepush flutter init` to add it — required for the updater to read its config")
}

// flutterAssetEntry reports whether `asset` is already listed under
// flutter: assets: in a pubspec.yaml document, and returns the byte range
// of the flutter: section for use by fixPubspecAssets.
func flutterAssetEntry(doc, asset string) (found bool, section assetSection) {
	lines := strings.Split(doc, "\n")
	start := -1
	for i, l := range lines {
		if strings.HasPrefix(l, "flutter:") {
			start = i
			break
		}
	}
	if start == -1 {
		return false, assetSection{start: -1, end: len(lines)}
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		l := lines[i]
		trimmed := strings.TrimSpace(l)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if l[0] != ' ' && l[0] != '\t' {
			end = i
			break
		}
	}
	for i := start + 1; i < end; i++ {
		if trimmed := strings.TrimSpace(lines[i]); trimmed == "- "+asset || trimmed == "-"+asset {
			return true, assetSection{start: start, end: end}
		}
	}
	return false, assetSection{start: start, end: end}
}

type assetSection struct {
	start int // index of the top-level `flutter:` line, or -1 if absent
	end   int // index of the first line after the flutter: section
}

// fixPubspecAssets adds `- shorebird.yaml` under flutter: assets: in
// pubspec.yaml, creating the keys if needed. Pure line editing — pubspec
// formatting and other keys are preserved.
func fixPubspecAssets() error {
	const asset = "shorebird.yaml"
	data, err := os.ReadFile("pubspec.yaml")
	if err != nil {
		return fmt.Errorf("pubspec.yaml not found — run this in your Flutter project root")
	}
	found, section := flutterAssetEntry(string(data), asset)
	if found {
		return nil
	}
	lines := strings.Split(string(data), "\n")

	if section.start == -1 {
		// No flutter: section at all — append one.
		out := string(data)
		if !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		out += "\nflutter:\n  assets:\n    - " + asset + "\n"
		return os.WriteFile("pubspec.yaml", []byte(out), 0644)
	}

	// Find `assets:` inside the flutter section and its indentation.
	for i := section.start + 1; i < section.end; i++ {
		l := lines[i]
		trimmed := strings.TrimSpace(l)
		if trimmed == "assets:" || strings.HasPrefix(trimmed, "assets:") {
			// Empty inline value (assets: or assets: []) — nothing after ':'
			if rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "assets:")); rest != "" && rest != "[]" {
				fmt.Println("Add shorebird.yaml to your pubspec.yaml manually:")
				fmt.Println("\n  flutter:\n    assets:\n      - " + asset)
				return ErrManualAction
			}
			itemIndent := l[:len(l)-len(strings.TrimLeft(l, " \t"))] + "  "
			lines = append(lines[:i+1], append([]string{itemIndent + "- " + asset}, lines[i+1:]...)...)
			return os.WriteFile("pubspec.yaml", []byte(strings.Join(lines, "\n")), 0644)
		}
	}

	// flutter: exists but has no assets: — add it as the first child.
	childIndent := "  "
	for i := section.start + 1; i < section.end; i++ {
		if l := lines[i]; strings.TrimSpace(l) != "" && (l[0] == ' ' || l[0] == '\t') {
			childIndent = l[:len(l)-len(strings.TrimLeft(l, " \t"))]
			break
		}
	}
	insert := []string{childIndent + "assets:", childIndent + "  - " + asset}
	lines = append(lines[:section.start+1], append(insert, lines[section.start+1:]...)...)
	return os.WriteFile("pubspec.yaml", []byte(strings.Join(lines, "\n")), 0644)
}

// androidManifestPath is the release-variant manifest the merged APK
// permissions come from. Flutter templates declare INTERNET only in
// android/app/src/debug — release builds silently ship without it.
const androidManifestPath = "android/app/src/main/AndroidManifest.xml"

// checkInternetPermission verifies android.permission.INTERNET is declared
// in the main manifest. Without it the embedded updater cannot reach the
// network at all in release builds ("server could not be reached" on the
// device), even though everything works in debug.
func checkInternetPermission() CheckResult {
	data, err := os.ReadFile(androidManifestPath)
	if err != nil {
		return Warn("android permissions", androidManifestPath+" not found",
			"Declare <uses-permission android:name=\"android.permission.INTERNET\"/> in your main AndroidManifest.xml")
	}
	if strings.Contains(string(data), "android.permission.INTERNET") {
		return Pass("android permissions", "INTERNET permission declared")
	}
	return Fail("android permissions", "android.permission.INTERNET is not declared in "+androidManifestPath,
		"Run `airbuild codepush flutter init` to add it — required for the updater to reach the network in release builds")
}

// fixInternetPermission inserts the INTERNET uses-permission as the first
// child of the <manifest> element in the main AndroidManifest.
func fixInternetPermission() error {
	data, err := os.ReadFile(androidManifestPath)
	if err != nil {
		return fmt.Errorf("%s not found — declare android.permission.INTERNET in your main manifest manually", androidManifestPath)
	}
	doc := string(data)
	if strings.Contains(doc, "android.permission.INTERNET") {
		return nil
	}
	tagStart := strings.Index(doc, "<manifest")
	if tagStart == -1 {
		return fmt.Errorf("no <manifest> element in %s", androidManifestPath)
	}
	tagEnd := strings.Index(doc[tagStart:], ">")
	if tagEnd == -1 {
		return fmt.Errorf("malformed <manifest> element in %s", androidManifestPath)
	}
	insertAt := tagStart + tagEnd + 1
	out := doc[:insertAt] + "\n    <uses-permission android:name=\"android.permission.INTERNET\" />" + doc[insertAt:]
	return os.WriteFile(androidManifestPath, []byte(out), 0644)
}

// --- Flutter fix actions ---

// installShorebird mirrors Shorebird's official install script: it clones
// github.com/shorebirdtech/shorebird (branch stable) into the standard
// install dir (~/.shorebird, or $XDG_CONFIG_HOME/shorebird), bootstraps
// the bundled toolchain by running `shorebird --version`, then adds
// <dir>/bin to PATH via the user's shell rc files. shorebird_cli is not
// published on pub.dev — `dart pub global activate` cannot install it.
func installShorebird() error {
	if runtime.GOOS == "windows" {
		fmt.Println("Install Shorebird via PowerShell:")
		fmt.Println("  Invoke-WebRequest -UseBasicParsing 'https://raw.githubusercontent.com/shorebirdtech/install/main/install.ps1' | Invoke-Expression")
		fmt.Println("See https://docs.shorebird.dev for details.")
		return ErrManualAction
	}
	if _, ok := Which("git"); !ok {
		return fmt.Errorf("git not found on PATH — install git, or install Shorebird manually: https://docs.shorebird.dev")
	}

	dir, err := shorebird.InstallDir()
	if err != nil {
		return err
	}
	bin := filepath.Join(dir, "bin", "shorebird")

	if _, err := os.Stat(bin); err != nil {
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("could not clear partial install at %s: %w", dir, err)
		}
		if err := RunCmd(".", "git", "clone", "https://github.com/shorebirdtech/shorebird.git", "-b", "stable", dir); err != nil {
			return fmt.Errorf("failed to clone shorebird: %w", err)
		}
	}

	// Bootstrap: the first run downloads Shorebird's bundled Flutter into
	// bin/cache and verifies the checkout.
	if err := RunCmd(dir, bin, "--version"); err != nil {
		return fmt.Errorf("shorebird bootstrap failed: %w", err)
	}

	return addShorebirdToPath(filepath.Dir(bin))
}

// addShorebirdToPath appends `export PATH="<binDir>:$PATH"` to the user's
// shell rc files (~/.zshrc, ~/.bashrc), like the official installer. Files
// that don't exist are skipped; a file already mentioning binDir is left
// alone so re-runs stay idempotent.
func addShorebirdToPath(binDir string) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("could not determine home directory: %w", err)
	}
	export := fmt.Sprintf(`export PATH="%s:$PATH"`, binDir)
	updated := false
	for _, rc := range []string{".zshrc", ".bashrc"} {
		path := filepath.Join(homeDir, rc)
		data, err := os.ReadFile(path)
		if err != nil {
			continue // rc file doesn't exist — skip, like the official script
		}
		if strings.Contains(string(data), binDir) {
			continue
		}
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			return fmt.Errorf("could not update %s: %w", path, err)
		}
		if _, err := f.WriteString("\n# Added by airbuild codepush flutter install (Shorebird)\n" + export + "\n"); err != nil {
			f.Close()
			return fmt.Errorf("could not update %s: %w", path, err)
		}
		f.Close()
		fmt.Printf("Updated %s\n", path)
		updated = true
	}
	if !updated {
		fmt.Printf("Add Shorebird to your PATH manually:\n  %s\n", export)
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
