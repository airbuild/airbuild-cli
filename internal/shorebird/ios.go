// iOS-specific helpers for the Flutter CodePush pipeline.
//
// Unlike Android — where the patchable artifact is a whole libapp.so —
// iOS patches operate on the Dart AOT snapshot regions inside the app's
// Mach-O binary (Frameworks/App.framework/App). Shorebird's iOS flow:
//
//  1. `flutter build ios --release` produces the .app (and link supplement
//     files under build/ios/shorebird/App.*).
//  2. gen_snapshot recompiles app.dill into an ELF AOT snapshot (out.aot).
//  3. `aot-tools link` minimizes the diff between the release snapshot and
//     the new snapshot, producing linked vmcode (out.vmcode) that can run
//     at full speed on-device.
//  4. `aot-tools dump_blobs` extracts the release's snapshot regions —
//     byte-identical to what the on-device updater extracts from the
//     installed app — which is the diff base.
//  5. Shorebird's `patch` binary creates the bidiff+zstd diff; the
//     artifact hash is sha256(out.vmcode).
//
// The release artifact uploaded to AirBuild is a zip containing `App`
// plus its link supplements (`App.ct.link`, `App.class_table.json`, …)
// laid out flat — the same side-by-side layout aot-tools expects.
package shorebird

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// iosSupplementNames are the link supplement files Shorebird's engine can
// emit next to the App binary. aot-tools discovers them by name next to
// the snapshot, and tolerates missing files — different Flutter/engine
// versions emit different subsets.
var iosSupplementNames = []string{
	"App.ct.link",
	"App.class_table.json",
	"App.ft.link",
	"App.field_table.json",
	"App.dt.link",
	"App.dispatch_table.json",
	"App.dd.link",
	"App.dd_callers.link",
	"App.dd_resolution.tsv",
}

// FindIosAppBinary locates the App Mach-O binary inside the built .app
// bundle or .xcarchive under projectDir. It checks the usual `flutter
// build ios` output (build/ios/iphoneos/*.app) first, then archives
// produced by `flutter build ipa` / Xcode.
func FindIosAppBinary(projectDir string) (string, error) {
	searchRoots := []struct {
		root    string
		pattern string // glob for the .app/.xcarchive container
	}{
		{filepath.Join(projectDir, "build", "ios", "iphoneos"), "*.app"},
		{filepath.Join(projectDir, "build", "ios", "archive"), "*.xcarchive"},
	}
	type hit struct {
		path string
		mod  time.Time
	}
	var hits []hit
	for _, r := range searchRoots {
		entries, err := filepath.Glob(filepath.Join(r.root, r.pattern))
		if err != nil {
			continue
		}
		for _, container := range entries {
			var appPath string
			if strings.HasSuffix(container, ".xcarchive") {
				matches, _ := filepath.Glob(filepath.Join(container, "Products", "Applications", "*.app", "Frameworks", "App.framework", "App"))
				if len(matches) == 0 {
					continue
				}
				appPath = matches[0]
			} else {
				candidate := filepath.Join(container, "Frameworks", "App.framework", "App")
				if _, err := os.Stat(candidate); err != nil {
					continue
				}
				appPath = candidate
			}
			if info, err := os.Stat(appPath); err == nil {
				hits = append(hits, hit{path: appPath, mod: info.ModTime()})
			}
		}
	}
	if len(hits) == 0 {
		return "", fmt.Errorf(
			"could not find the App binary under %s (expected build/ios/iphoneos/*.app/Frameworks/App.framework/App) — run `airbuild codepush flutter release ios` without --skip-build, or pass --artifact",
			projectDir,
		)
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].mod.After(hits[j].mod) })
	return hits[0].path, nil
}

// IosSupplementDir returns the directory where Shorebird's engine writes
// link supplement files during an iOS build (build/ios/shorebird).
func IosSupplementDir(projectDir string) string {
	return filepath.Join(projectDir, "build", "ios", "shorebird")
}

// CollectIosSupplements returns the paths of known link supplement files
// present in dir.
func CollectIosSupplements(dir string) []string {
	var out []string
	for _, name := range iosSupplementNames {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// CopyIosSupplements copies App.* supplement files from srcDir into
// dstDir, renaming the `App` basename to newBaseName (e.g. "out") so they
// sit beside the corresponding snapshot — aot-tools looks up supplements
// by the snapshot's basename. Missing files are skipped; returns the
// number copied.
func CopyIosSupplements(srcDir, dstDir, newBaseName string) (int, error) {
	copied := 0
	for _, name := range iosSupplementNames {
		src := filepath.Join(srcDir, name)
		data, err := os.ReadFile(src)
		if err != nil {
			continue // tolerate missing — engine versions emit different subsets
		}
		dst := filepath.Join(dstDir, newBaseName+strings.TrimPrefix(name, "App"))
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return copied, fmt.Errorf("failed to copy supplement %s: %w", name, err)
		}
		copied++
	}
	return copied, nil
}

// CreateIosReleaseArtifact writes a flat zip containing the App binary
// plus its link supplements — the release artifact AirBuild stores and
// later serves back for patch diffing. The side-by-side layout is what
// aot-tools expects (supplements discovered next to the snapshot).
func CreateIosReleaseArtifact(appPath string, supplements []string, destZip string) error {
	out, err := os.Create(destZip)
	if err != nil {
		return err
	}
	defer out.Close() //nolint:errcheck
	w := zip.NewWriter(out)
	addFile := func(path, name string) error {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fw, err := w.Create(name)
		if err != nil {
			return err
		}
		_, err = fw.Write(data)
		return err
	}
	if err := addFile(appPath, "App"); err != nil {
		return fmt.Errorf("failed to add App binary: %w", err)
	}
	for _, s := range supplements {
		if err := addFile(s, filepath.Base(s)); err != nil {
			return fmt.Errorf("failed to add supplement %s: %w", s, err)
		}
	}
	if err := w.Close(); err != nil {
		return err
	}
	return nil
}

// ExtractIosReleaseArtifact unzips a release artifact created by
// CreateIosReleaseArtifact into destDir and returns the path to the
// extracted App binary (supplements land beside it). Returns
// os.ErrNotExist when zipPath doesn't exist.
func ExtractIosReleaseArtifact(zipPath, destDir string) (string, error) {
	if _, err := os.Stat(zipPath); err != nil {
		return "", err
	}
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", fmt.Errorf("release artifact %s is not a valid zip: %w", zipPath, err)
	}
	defer zr.Close() //nolint:errcheck
	for _, f := range zr.File {
		name := filepath.Base(f.Name) // flat layout — strip any dirs, prevents zip-slip
		if name == "" || name == "." {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", err
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(destDir, name), data, 0o644); err != nil {
			return "", err
		}
	}
	appPath := filepath.Join(destDir, "App")
	if _, err := os.Stat(appPath); err != nil {
		return "", fmt.Errorf("release artifact %s contains no App binary", zipPath)
	}
	return appPath, nil
}

// FindRecentAppDill locates the kernel snapshot (app.dill) produced under
// .dart_tool by an iOS build. Flutter's content-hash filestore can skip
// kernel_snapshot entirely when the Dart sources are unchanged — in that
// case the build still *consumes* an older app.dill whose own mtime stays
// behind. So freshness is checked against the flutter_build directory:
// any sibling stamp/output file newer than `since` proves this build ran
// there, making that dir's app.dill the kernel this build used. Falls back
// to the dill's own mtime when no fresh stamp dir exists.
func FindRecentAppDill(projectDir string, since time.Time) (string, error) {
	dartTool := filepath.Join(projectDir, ".dart_tool")
	flutterBuild := filepath.Join(dartTool, "flutter_build")
	if _, err := os.Stat(flutterBuild); err != nil {
		return "", fmt.Errorf("no .dart_tool/flutter_build directory under %s — run a flutter build first", projectDir)
	}

	// Pass 1: a flutter_build dir touched by this build — its app.dill is
	// valid even when the dill file itself predates the build (skipped
	// kernel_snapshot on identical input).
	if !since.IsZero() {
		entries, err := os.ReadDir(flutterBuild)
		if err == nil {
			type hit struct {
				path string
				mod  time.Time
			}
			var hits []hit
			for _, e := range entries {
				if !e.IsDir() {
					continue
				}
				dill := filepath.Join(flutterBuild, e.Name(), "app.dill")
				if _, err := os.Stat(dill); err != nil {
					continue
				}
				var dirUsed time.Time
				_ = filepath.WalkDir(filepath.Join(flutterBuild, e.Name()), func(p string, d os.DirEntry, err error) error {
					if err != nil || d.IsDir() {
						return nil
					}
					info, err := d.Info()
					if err == nil && info.ModTime().After(dirUsed) {
						dirUsed = info.ModTime()
					}
					return nil
				})
				if dirUsed.After(since) {
					info, _ := os.Stat(dill)
					hits = append(hits, hit{path: dill, mod: info.ModTime()})
				}
			}
			if len(hits) > 0 {
				sort.Slice(hits, func(i, j int) bool { return hits[i].mod.After(hits[j].mod) })
				return hits[0].path, nil
			}
		}
	}

	// Pass 2 (fallback): most recently modified app.dill — matches
	// Shorebird's _findAppDill when `since` is zero or no fresh dir exists.
	var best string
	var bestMod time.Time
	_ = filepath.WalkDir(flutterBuild, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != "app.dill" {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if !since.IsZero() && !info.ModTime().After(since) {
			return nil
		}
		if info.ModTime().After(bestMod) {
			best, bestMod = path, info.ModTime()
		}
		return nil
	})
	if best == "" {
		return "", fmt.Errorf(
			"could not find app.dill under %s touched by a build after %s — the iOS patch needs the kernel snapshot from a fresh `flutter build ios`",
			flutterBuild, since.Format(time.RFC3339),
		)
	}
	return best, nil
}

// IosEngineTool returns the path to a bundled-Flutter iOS engine artifact
// (gen_snapshot_arm64, analyze_snapshot_arm64) under the ios-release
// engine cache. These are fetched by `flutter precache`/build — if
// missing, the error tells the user how to populate them.
func IosEngineTool(name string) (string, error) {
	flutterDir, err := FlutterDir()
	if err != nil {
		return "", err
	}
	p := filepath.Join(flutterDir, "bin", "cache", "artifacts", "engine", "ios-release", name)
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf(
			"iOS engine tool %s not found at %s — run `%s precache --ios` (or an iOS build) with Shorebird's bundled Flutter first",
			name, p, filepath.Join(flutterDir, "bin", "flutter"),
		)
	}
	return p, nil
}

// DartBinary returns the `dart` binary inside Shorebird's bundled Flutter
// — used to run the aot-tools.dill kernel.
func DartBinary() (string, error) {
	flutterDir, err := FlutterDir()
	if err != nil {
		return "", err
	}
	bin := filepath.Join(flutterDir, "bin", "dart")
	if _, err := os.Stat(bin); err != nil {
		return "", fmt.Errorf("dart binary not found at %s — Shorebird Flutter SDK looks corrupt; reinstall via `airbuild codepush flutter install`", bin)
	}
	return bin, nil
}

// EnsureAotTools returns Shorebird's aot-tools.dill, downloading it from
// the public artifact bucket on first use (same scheme as the patch
// binary — no auth required). aot-tools powers the iOS link + dump_blobs
// steps of patch generation.
func EnsureAotTools() (string, error) {
	engineRev, err := EngineRevision()
	if err != nil {
		return "", err
	}
	installDir, err := InstallDir()
	if err != nil {
		return "", err
	}
	outDir := filepath.Join(installDir, "bin", "cache", "artifacts", "aot-tools", engineRev)
	dillPath := filepath.Join(outDir, "aot-tools.dill")
	if _, err := os.Stat(dillPath); err == nil {
		return dillPath, nil
	}
	url := fmt.Sprintf("https://storage.googleapis.com/download.shorebird.dev/shorebird/%s/aot-tools.dill", engineRev)
	resp, err := http.Get(url) //nolint:gosec // fixed shorebird.dev host
	if err != nil {
		return "", fmt.Errorf("failed to download aot-tools: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to download aot-tools: GET %s: %s", url, resp.Status)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(outDir, "aot-tools-*.dill.tmp")
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()           //nolint:errcheck
		os.Remove(tmp.Name()) //nolint:errcheck
		return "", fmt.Errorf("failed to write aot-tools: %w", err)
	}
	tmp.Close() //nolint:errcheck
	if err := os.Rename(tmp.Name(), dillPath); err != nil {
		return "", err
	}
	return dillPath, nil
}

// RunAotTools executes an aot-tools subcommand via the bundled Dart SDK
// (`dart run aot-tools.dill <args>`), streaming output. Mirrors
// Shorebird's AotTools._exec for the .dill artifact variant.
func RunAotTools(dillPath, workDir string, args ...string) error {
	dart, err := DartBinary()
	if err != nil {
		return err
	}
	cmd := exec.Command(dart, append([]string{"run", dillPath}, args...)...)
	cmd.Dir = workDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("aot-tools %v failed: %w", args, err)
	}
	return nil
}

// AotToolsSupports reports whether aot-tools advertises a subcommand/flag
// in its --help output (e.g. "dump_blobs"). Mirrors Shorebird's
// isGeneratePatchDiffBaseSupported feature-probe.
func AotToolsSupports(dillPath, workDir, feature string) (bool, error) {
	dart, err := DartBinary()
	if err != nil {
		return false, err
	}
	cmd := exec.Command(dart, "run", dillPath, "--help")
	cmd.Dir = workDir
	out, err := cmd.Output()
	if err != nil {
		// --help may exit non-zero on some versions; still inspect stdout.
		var ee *exec.ExitError
		if !errors.As(err, &ee) || len(out) == 0 {
			return false, fmt.Errorf("aot-tools --help failed: %w", err)
		}
	}
	return strings.Contains(string(out), feature), nil
}

// RunGenSnapshot invokes the iOS gen_snapshot to compile app.dill into an
// ELF AOT snapshot — Shorebird's buildElfAotSnapshot equivalent:
//
//	gen_snapshot_arm64 --deterministic --snapshot-kind=app-aot-elf \
//	    --elf=<outFile> <appDill>
func RunGenSnapshot(genSnapshotPath, appDillPath, outFilePath string, extraArgs ...string) error {
	args := []string{
		"--deterministic",
		"--snapshot-kind=app-aot-elf",
		"--elf=" + outFilePath,
	}
	args = append(args, extraArgs...)
	args = append(args, appDillPath)
	cmd := exec.Command(genSnapshotPath, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("gen_snapshot failed: %w", err)
	}
	if _, err := os.Stat(outFilePath); err != nil {
		return fmt.Errorf("gen_snapshot did not produce %s", outFilePath)
	}
	return nil
}
