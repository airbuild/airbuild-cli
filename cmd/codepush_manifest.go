package cmd

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/airbuild/airbuild-cli/internal/ui"
)

// --- Build manifests: native/asset change detection ---
//
// A Dart patch can only replace the compiled Dart snapshot (libapp.so /
// App binary). Native code, assets, manifests, and engine changes are NOT
// patchable — shipping a patch whose other files changed produces a silent
// no-op or a subtle mismatch. At release time we record a manifest of the
// packaged build's contents; at patch time we compare and warn.

type buildManifest map[string]string

// archive-relative path → sha256

func manifestPath(projectDir, platform, version string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			return r
		}
		return '_'
	}, version)
	return filepath.Join(projectDir, ".airbuild", "manifests", strings.ToLower(platform)+"-"+safe+".json")
}

// classifyManifestEntry buckets a packaged file by what an OTA patch can
// and cannot change. "" means "ignore" (packaging/signing churn that
// legitimately differs between builds).
func classifyManifestEntry(platform, name string) string {
	switch platform {
	case "ANDROID":
		switch {
		case strings.HasPrefix(name, "META-INF/"),
			strings.HasPrefix(name, "assets/dexopt/"),
			strings.HasPrefix(name, "BUNDLE-METADATA/"):
			return "" // signing output, dexopt profiles, bundle metadata — build churn
		case strings.HasSuffix(name, "/libapp.so"), name == "libapp.so":
			return "dart"
		case strings.HasSuffix(name, "/libflutter.so"), strings.HasSuffix(name, "/libflutter_package.so"):
			return "engine"
		case strings.HasPrefix(name, "assets/flutter_assets/"):
			return "asset"
		case strings.HasSuffix(name, ".dex"),
			name == "AndroidManifest.xml", name == "resources.arsc",
			strings.HasPrefix(name, "res/"), strings.HasPrefix(name, "kotlin/"),
			strings.HasPrefix(name, "lib/"):
			return "native"
		default:
			return "asset"
		}
	case "IOS":
		switch {
		case strings.HasPrefix(name, "_CodeSignature/"),
			name == "embedded.mobileprovision", name == "PkgInfo",
			name == ".DS_Store", strings.HasSuffix(name, "/.DS_Store"):
			return ""
		case strings.HasSuffix(name, "App.framework/App"):
			return "dart"
		case strings.Contains(name, "App.framework/flutter_assets/"):
			return "asset"
		case strings.Contains(name, "Flutter.framework/"):
			return "engine"
		default:
			return "native"
		}
	}
	return "native"
}

// zipManifest hashes every entry of a zip archive (APK/AAB).
func zipManifest(path string) (buildManifest, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer zr.Close() //nolint:errcheck
	m := buildManifest{}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		h := sha256.New()
		if _, err := io.Copy(h, rc); err != nil {
			rc.Close() //nolint:errcheck
			return nil, err
		}
		rc.Close() //nolint:errcheck
		m[f.Name] = hex.EncodeToString(h.Sum(nil))
	}
	return m, nil
}

// dirManifest hashes every file under root, keyed by path relative to root.
func dirManifest(root string) (buildManifest, error) {
	m := buildManifest{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr
		}
		h, err := sha256File(path)
		if err != nil {
			return nil // skip unreadable files
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		m[filepath.ToSlash(rel)] = h
		return nil
	})
	return m, err
}

// writeBuildManifest persists the release build's manifest for the patch
// command to compare against. Best-effort — silently skipped when the
// packaged artifact can't be found (e.g. --artifact/--skip-build flows).
func writeBuildManifest(projectDir, platform, version string) {
	var m buildManifest
	var err error
	if platform == "ANDROID" {
		for _, candidate := range []string{
			filepath.Join(projectDir, "build", "app", "outputs", "flutter-apk", "app-release.apk"),
			filepath.Join(projectDir, "build", "app", "outputs", "bundle", "release", "app-release.aab"),
		} {
			if m, err = zipManifest(candidate); err == nil {
				break
			}
		}
	} else {
		// iOS: hash the built .app directory.
		for _, candidate := range []string{
			filepath.Join(projectDir, "build", "ios", "iphoneos", "Runner.app"),
			filepath.Join(projectDir, "build", "ios", "ipa", "*.app"),
		} {
			matches, _ := filepath.Glob(candidate)
			for _, dir := range matches {
				if m, err = dirManifest(dir); err == nil && len(m) > 0 {
					break
				}
			}
			if len(m) > 0 {
				break
			}
		}
	}
	if len(m) == 0 {
		return
	}
	path := manifestPath(projectDir, platform, version)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o644)
}

// loadBuildManifest reads the manifest recorded at release time. Returns
// nil when absent (foreign machine, --skip-build release, etc).
func loadBuildManifest(projectDir, platform, version string) buildManifest {
	data, err := os.ReadFile(manifestPath(projectDir, platform, version))
	if err != nil {
		return nil
	}
	var m buildManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil
	}
	return m
}

// warnNonDartChanges compares the release build's manifest against the
// patch build's and warns about changes an OTA patch cannot deliver:
// native code, bundled assets, and (critically) engine changes.
func warnNonDartChanges(projectDir, platform, releaseVersion string) {
	old := loadBuildManifest(projectDir, platform, releaseVersion)
	if len(old) == 0 {
		return // nothing to compare — stay quiet
	}
	var fresh buildManifest
	var err error
	if platform == "ANDROID" {
		fresh, err = zipManifest(filepath.Join(projectDir, "build", "app", "outputs", "flutter-apk", "app-release.apk"))
		if err != nil {
			fresh, err = zipManifest(filepath.Join(projectDir, "build", "app", "outputs", "bundle", "release", "app-release.aab"))
		}
	} else {
		fresh, err = dirManifest(filepath.Join(projectDir, "build", "ios", "iphoneos", "Runner.app"))
	}
	if err != nil || len(fresh) == 0 {
		return
	}

	var engineChanged, nativeChanged, assetChanged []string
	names := map[string]bool{}
	for n := range old {
		names[n] = true
	}
	for n := range fresh {
		names[n] = true
	}
	for name := range names {
		if old[name] == fresh[name] {
			continue
		}
		switch classifyManifestEntry(platform, name) {
		case "":
			continue // packaging churn — not a real change
		case "dart":
			continue // the point of the patch
		case "engine":
			engineChanged = append(engineChanged, name)
		case "native":
			nativeChanged = append(nativeChanged, name)
		case "asset":
			assetChanged = append(assetChanged, name)
		}
	}

	show := func(label string, files []string) {
		sort.Strings(files)
		ui.Error("%s", label)
		const max = 8
		for i, f := range files {
			if i >= max {
				ui.Muted("    ... and %d more", len(files)-max)
				break
			}
			ui.Muted("    %s", f)
		}
	}
	if len(engineChanged) > 0 {
		show("⚠ Flutter engine changed since this release — patches built on a different engine revision will NOT apply. Re-register the release with this engine, or rebuild with the release's flutter-revision.", engineChanged)
	}
	if len(nativeChanged) > 0 {
		show("⚠ Native files changed since the release — OTA patches only ship Dart code; these changes will NOT reach devices:", nativeChanged)
	}
	if len(assetChanged) > 0 {
		show("⚠ Asset files changed since the release — assets are not patchable; they will NOT reach devices:", assetChanged)
	}
}

// --- Dart-define drift detection ---
//
// --dart-define constants are compiled into libapp.so, so they're part of
// what a patch ships. A patch built with different defines than the
// release silently changes (or reverts to fromEnvironment defaults) those
// constants on every patched device. We record the release's define set —
// value hashes only, never the values themselves, since defines may carry
// secrets — and warn when the patch's set differs.

// normalizedDartDefines merges --dart-define KEY=VALUE entries with the
// contents of --dart-define-from-file files into one map. Later entries
// override earlier ones, matching flutter's last-wins semantics.
func normalizedDartDefines() map[string]string {
	m := map[string]string{}
	for _, d := range cpDartDefines {
		if k, v, ok := strings.Cut(d, "="); ok {
			m[k] = v
		}
		// Malformed entries are left for flutter itself to report.
	}
	for _, f := range cpDartDefineFiles {
		mergeDefineFile(f, m)
	}
	return m
}

// mergeDefineFile reads a --dart-define-from-file file — JSON object (the
// format flutter requires since 3.22), with a dotenv-style KEY=VALUE
// fallback for newer Flutter versions that also accept .env files.
func mergeDefineFile(path string, m map[string]string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return // let flutter itself report an unreadable file
	}
	var fm map[string]string
	if err := json.Unmarshal(data, &fm); err == nil {
		for k, v := range fm {
			m[k] = v
		}
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			m[strings.TrimSpace(k)] = strings.Trim(v, `"' `+"\t")
		}
	}
}

// hashDefines maps define names to sha256(value) so the manifest never
// persists secret values.
func hashDefines(m map[string]string) map[string]string {
	h := make(map[string]string, len(m))
	for k, v := range m {
		sum := sha256.Sum256([]byte(v))
		h[k] = hex.EncodeToString(sum[:])
	}
	return h
}

func definesManifestPath(projectDir, platform, version string) string {
	return strings.TrimSuffix(manifestPath(projectDir, platform, version), ".json") + ".defines.json"
}

func loadDefinesManifest(projectDir, platform, version string) map[string]string {
	data, err := os.ReadFile(definesManifestPath(projectDir, platform, version))
	if err != nil {
		return nil
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return nil
	}
	return m
}

// writeDefinesManifest persists the release build's dart-define set
// (hashed values) for warnDartDefineDrift to compare against at patch time.
func writeDefinesManifest(projectDir, platform, version string) {
	m := hashDefines(normalizedDartDefines())
	path := definesManifestPath(projectDir, platform, version)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o644)
}

// warnDartDefineDrift compares the release's dart-define set against the
// current flags and reports adds/removes/changes by key name.
func warnDartDefineDrift(projectDir, platform, releaseVersion string) {
	old := loadDefinesManifest(projectDir, platform, releaseVersion)
	if old == nil {
		return // release registered elsewhere, or before defines tracking
	}
	fresh := hashDefines(normalizedDartDefines())

	var changed []string
	for k, v := range fresh {
		if ov, ok := old[k]; !ok {
			changed = append(changed, "+"+k)
		} else if ov != v {
			changed = append(changed, "~"+k)
		}
	}
	for k := range old {
		if _, ok := fresh[k]; !ok {
			changed = append(changed, "-"+k)
		}
	}
	if len(changed) == 0 {
		return
	}
	sort.Strings(changed)
	ui.Error("⚠ dart-define values differ from the release build — patched devices get these constants changed (+added / ~modified / -reverts to default):")
	const max = 8
	for i, f := range changed {
		if i >= max {
			ui.Muted("    ... and %d more", len(changed)-max)
			break
		}
		ui.Muted("    %s", f)
	}
}
