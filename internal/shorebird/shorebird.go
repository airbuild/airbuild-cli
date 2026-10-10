// Package shorebird wraps the (locally installed) Shorebird CLI to build
// Flutter releases/patches, and locates the resulting build artifacts so
// they can be uploaded to AirBuild's own control-plane server.
//
// # Auto-diff flow (Option B)
//
// When the user runs `airbuild codepush flutter patch android` without
// --artifact, the CLI:
//  1. Runs `shorebird patch android --dry-run` to build the patch's libapp.so
//     (without uploading to Shorebird's cloud).
//  2. Locates the freshly built libapp.so via FindAndroidLibapp.
//  3. Downloads the release's original libapp.so from AirBuild.
//  4. Uses Shorebird's own cached `patch` binary (~/.shorebird/bin/cache/
//     artifacts/patch/patch) to create the binary diff (bidiff + zstd).
//  5. Uploads the diff to AirBuild.
//
// This avoids requiring the user to manually locate the diff artifact.
// The `patch` binary is the same one Shorebird uses internally, so the
// output format is guaranteed compatible with the Shorebird updater
// runtime embedded in the user's app.
package shorebird

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// InstallDir returns the directory the official Shorebird installer uses:
// $XDG_CONFIG_HOME/shorebird when set, otherwise ~/.shorebird.
func InstallDir() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "shorebird"), nil
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("could not determine home directory: %w", err)
	}
	return filepath.Join(homeDir, ".shorebird"), nil
}

// binaryName matches Shorebird's own naming: a shell script on Unix, a
// batch file on Windows.
func binaryName() string {
	if runtime.GOOS == "windows" {
		return "shorebird.bat"
	}
	return "shorebird"
}

// DefaultBinary returns the shorebird binary inside InstallDir if it
// exists there, regardless of PATH. Returns "" when Shorebird isn't
// installed at the default location.
func DefaultBinary() string {
	dir, err := InstallDir()
	if err != nil {
		return ""
	}
	candidate := filepath.Join(dir, "bin", binaryName())
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	return ""
}

// BinaryPath resolves the shorebird executable: first on PATH, then the
// default install location. The PATH fallback lets airbuild drive the
// CLI right after `codepush flutter install`, before the user restarts
// their shell to pick up the updated PATH.
func BinaryPath() (string, error) {
	if p, err := exec.LookPath("shorebird"); err == nil {
		return p, nil
	}
	if p := DefaultBinary(); p != "" {
		return p, nil
	}
	return "", fmt.Errorf("shorebird CLI not found — run `airbuild codepush flutter install`")
}

// IsInstalled reports whether the `shorebird` binary is on PATH or in the
// default install location.
func IsInstalled() bool {
	_, err := BinaryPath()
	return err == nil
}

// --- Bundled Flutter toolchain ---
//
// Shorebird's releases are plain `flutter build` invocations run with
// Shorebird's own Flutter SDK — a fork whose engine has the updater
// runtime compiled in. Everything server-side (release metadata, artifact
// hosting, rollout decisions) is AirBuild's job, so airbuild drives the
// bundled SDK directly instead of `shorebird release`/`shorebird patch`,
// which require a Shorebird cloud account and backend. This keeps the
// whole flow self-hosted: no Shorebird login, no data leaving AirBuild.

// FlutterDir returns the Shorebird-managed Flutter SDK directory to build
// with: <install>/bin/cache/flutter/<revision>. When several revisions are
// cached it prefers the one named by SHOREBIRD_FLUTTER_REVISION or the
// project's shorebird.yaml flutter_revision, then falls back to the most
// recently modified entry.
func FlutterDir() (string, error) {
	installDir, err := InstallDir()
	if err != nil {
		return "", err
	}
	cacheDir := filepath.Join(installDir, "bin", "cache", "flutter")
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		return "", fmt.Errorf("no Shorebird Flutter SDK found at %s — run `airbuild codepush flutter install`", cacheDir)
	}
	var dirs []os.DirEntry
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e)
		}
	}
	if len(dirs) == 0 {
		return "", fmt.Errorf("no Shorebird Flutter SDK found at %s — run `airbuild codepush flutter install`", cacheDir)
	}
	if want := preferredFlutterRevision(); want != "" {
		for _, d := range dirs {
			if d.Name() == want {
				return filepath.Join(cacheDir, want), nil
			}
		}
	}
	// Most recently modified revision dir wins.
	sort.Slice(dirs, func(i, j int) bool {
		fi, _ := dirs[i].Info()
		fj, _ := dirs[j].Info()
		return fi.ModTime().After(fj.ModTime())
	})
	return filepath.Join(cacheDir, dirs[0].Name()), nil
}

// preferredFlutterRevision reads SHOREBIRD_FLUTTER_REVISION, then
// shorebird.yaml's flutter_revision in the current directory.
func preferredFlutterRevision() string {
	if rev := strings.TrimSpace(os.Getenv("SHOREBIRD_FLUTTER_REVISION")); rev != "" {
		return rev
	}
	if data, err := os.ReadFile("shorebird.yaml"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "flutter_revision:") {
				return strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "flutter_revision:")), `"'`)
			}
		}
	}
	return ""
}

// BundledFlutter returns the path to Shorebird's bundled `flutter` binary.
func BundledFlutter() (string, error) {
	dir, err := FlutterDir()
	if err != nil {
		return "", err
	}
	bin := filepath.Join(dir, "bin", "flutter")
	if runtime.GOOS == "windows" {
		bin += ".bat"
	}
	if _, err := os.Stat(bin); err != nil {
		return "", fmt.Errorf("bundled Flutter binary not found at %s — run `airbuild codepush flutter install`", bin)
	}
	return bin, nil
}

// EngineRevision reads the Shorebird engine revision from the bundled
// Flutter SDK (bin/internal/engine.version) — the revision Shorebird uses
// to name its public artifact downloads (patch binary, aot-tools).
func EngineRevision() (string, error) {
	dir, err := FlutterDir()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Join(dir, "bin", "internal", "engine.version"))
	if err != nil {
		return "", fmt.Errorf("could not read engine.version: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

// RunFlutter runs Shorebird's bundled `flutter <args...>` in dir with
// output streamed through, then best-effort restores the project's
// package_config.json to the system Flutter (mirroring what the Shorebird
// CLI does after builds so the IDE isn't left pointing at the forked SDK).
func RunFlutter(dir string, env map[string]string, args ...string) error {
	bin, err := BundledFlutter()
	if err != nil {
		return err
	}
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if len(env) > 0 {
		cmd.Env = os.Environ()
		for k, v := range env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	runErr := cmd.Run()

	// Best-effort: reset .dart_tool/package_config.json to the system
	// Flutter, like the Shorebird CLI does after every build.
	if sysFlutter, err := exec.LookPath("flutter"); err == nil {
		reset := exec.Command(sysFlutter, "--no-version-check", "pub", "get", "--offline")
		reset.Dir = dir
		reset.Stdout = io.Discard
		reset.Stderr = io.Discard
		_ = reset.Run()
	}

	if runErr != nil {
		return fmt.Errorf("flutter %v failed: %w", args, runErr)
	}
	return nil
}

// patchArtifactName maps the host platform to Shorebird's public artifact
// zip name (e.g. patch-darwin-arm64.zip).
func patchArtifactName() (string, error) {
	switch runtime.GOOS {
	case "darwin":
		if runtime.GOARCH == "arm64" {
			return "patch-darwin-arm64.zip", nil
		}
		return "patch-darwin-x64.zip", nil
	case "linux":
		return "patch-linux-x64.zip", nil
	case "windows":
		return "patch-windows-x64.zip", nil
	}
	return "", fmt.Errorf("unsupported platform %s/%s", runtime.GOOS, runtime.GOARCH)
}

// EnsurePatchBinary locates Shorebird's cached `patch` binary, downloading
// it from Shorebird's public artifact bucket if it isn't cached yet. The
// download needs no authentication — the bucket is public; only the
// Shorebird cloud API requires an account (which airbuild never calls).
func EnsurePatchBinary() (string, error) {
	if p, err := FindPatchBinary(); err == nil {
		return p, nil
	}
	engineRev, err := EngineRevision()
	if err != nil {
		return "", err
	}
	artifact, err := patchArtifactName()
	if err != nil {
		return "", err
	}
	installDir, err := InstallDir()
	if err != nil {
		return "", err
	}
	outDir := filepath.Join(installDir, "bin", "cache", "artifacts", "patch")
	url := fmt.Sprintf("https://storage.googleapis.com/download.shorebird.dev/shorebird/%s/%s", engineRev, artifact)
	if err := downloadAndUnzip(url, outDir); err != nil {
		return "", fmt.Errorf("failed to download patch tool: %w", err)
	}
	return FindPatchBinary()
}

// downloadAndUnzip fetches url and extracts the zip into outDir.
func downloadAndUnzip(url, outDir string) error {
	resp, err := http.Get(url) //nolint:gosec // fixed shorebird.dev host
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	tmp, err := os.CreateTemp("", "airbuild-artifact-*.zip")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck
	defer tmp.Close()           //nolint:errcheck
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		return err
	}
	zr, err := zip.OpenReader(tmp.Name())
	if err != nil {
		return fmt.Errorf("invalid zip: %w", err)
	}
	defer zr.Close() //nolint:errcheck
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	for _, f := range zr.File {
		dest := filepath.Join(outDir, f.Name)
		// Guard against zip-slip: only extract names that stay under outDir.
		if !strings.HasPrefix(dest, filepath.Clean(outDir)+string(os.PathSeparator)) && filepath.Clean(dest) != filepath.Clean(outDir) {
			return fmt.Errorf("unsafe path in zip: %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return err
			}
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		w, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, f.Mode()|0o111)
		if err != nil {
			rc.Close()
			return err
		}
		if _, err := io.Copy(w, rc); err != nil {
			rc.Close()
			w.Close()
			return err
		}
		rc.Close()
		w.Close()
	}
	return nil
}

// Run executes `shorebird <args...>` in dir, streaming output to the
// current process's stdout/stderr so the user sees Shorebird's own
// progress output (build logs, prompts, etc).
func Run(dir string, args ...string) error {
	bin, err := BinaryPath()
	if err != nil {
		return err
	}
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("shorebird %v failed: %w", args, err)
	}
	return nil
}

// FindAndroidLibapp scans the standard Flutter/Gradle build output for the
// compiled libapp.so, returning the first match for the given architecture
// (e.g. "arm64-v8a"), or the first one found if architecture is empty.
// Returns an error if none is found — the caller should fall back to
// requiring an explicit --artifact path.
func FindAndroidLibapp(projectDir, architecture string) (string, error) {
	candidateRoots := []string{
		filepath.Join(projectDir, "build", "app", "intermediates", "merged_native_libs"),
		filepath.Join(projectDir, "build", "app", "intermediates", "stripped_native_libs"),
	}

	var found []string
	for _, root := range candidateRoots {
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil //nolint:nilerr — best-effort scan, skip unreadable entries
			}
			if filepath.Base(path) == "libapp.so" {
				if architecture == "" || filepath.Base(filepath.Dir(path)) == architecture {
					found = append(found, path)
				}
			}
			return nil
		})
		if len(found) > 0 {
			break
		}
	}

	if len(found) == 0 {
		return "", fmt.Errorf(
			"could not auto-locate libapp.so under %s (Flutter/AGP version differences can move this path) — pass --artifact <path> explicitly",
			projectDir,
		)
	}
	return found[0], nil
}

// FindPatchBinary locates Shorebird's cached `patch` binary, which is the
// tool that creates bidiff+zstd binary diffs between two libapp.so files.
// Shorebird caches it at ~/.shorebird/bin/cache/artifacts/patch/patch
// (the binary name is "patch" on all platforms, including Windows where
// it has no .exe extension).
//
// Returns the path if found, or an error if not. The caller should fall
// back to requiring --artifact explicitly when this fails.
func FindPatchBinary() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("could not determine home directory: %w", err)
	}

	// Shorebird stores its cache under ~/.shorebird/bin/cache/
	cacheBase := filepath.Join(homeDir, ".shorebird", "bin", "cache", "artifacts", "patch")

	// The binary is named "patch" on all platforms (no .exe extension,
	// matching Shorebird's own cross-platform naming).
	binaryName := "patch"
	candidatePath := filepath.Join(cacheBase, binaryName)

	if _, err := os.Stat(candidatePath); err == nil {
		return candidatePath, nil
	}

	// Fallback: scan the patch cache directory for any executable.
	// Shorebird may version the binary or use a different name in
	// future releases.
	entries, err := os.ReadDir(cacheBase)
	if err != nil {
		return "", fmt.Errorf(
			"shorebird patch binary not found at %s — ensure shorebird CLI is installed and has been run at least once (run `shorebird patch android --dry-run` manually to populate the cache), or pass --artifact explicitly",
			candidatePath,
		)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		full := filepath.Join(cacheBase, entry.Name())
		// On Unix, check if the file is executable.
		if runtime.GOOS != "windows" {
			if info, err := entry.Info(); err == nil && info.Mode()&0o111 != 0 {
				return full, nil
			}
		} else {
			return full, nil
		}
	}

	return "", fmt.Errorf(
		"shorebird patch binary not found at %s — ensure shorebird CLI is installed and has been run at least once, or pass --artifact explicitly",
		candidatePath,
	)
}

// CreateDiff runs Shorebird's `patch` binary to create a binary diff
// (bidiff + zstd) between the release libapp.so and the patch libapp.so.
// The output diff file is written to diffPath. This produces the exact
// format expected by the Shorebird updater runtime on devices.
//
// The patch binary is invoked as: patch <releaseArtifact> <patchArtifact> <diffPath>
// matching Shorebird's own ArtifactManager.createDiff() call.
func CreateDiff(patchBinary, releaseArtifactPath, patchArtifactPath, diffPath string) error {
	if _, err := os.Stat(releaseArtifactPath); err != nil {
		return fmt.Errorf("release artifact not found: %w", err)
	}
	if _, err := os.Stat(patchArtifactPath); err != nil {
		return fmt.Errorf("patch artifact not found: %w", err)
	}

	cmd := exec.Command(patchBinary, releaseArtifactPath, patchArtifactPath, diffPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to create diff: %w", err)
	}

	if _, err := os.Stat(diffPath); err != nil {
		return fmt.Errorf("diff was not created at %s: %w", diffPath, err)
	}
	return nil
}

// zstdMagicBytes are the 4 magic bytes (little-endian 0xFD2FB528) that
// every zstd-compressed frame starts with. Shorebird's `patch` binary
// produces bidiff diffs compressed with zstd, so a well-formed diff.patch
// must start with these bytes. Checking for them lets us detect a
// misidentified file (e.g. an unrelated diff.patch from another tool)
// before uploading it as a CodePush patch.
var zstdMagicBytes = []byte{0x28, 0xB5, 0x2F, 0xFD}

// FindRecentDiffPatchResult is the result of scanning for a recently
// created diff.patch file.
type FindRecentDiffPatchResult struct {
	// Path is the most likely diff.patch file (most recently modified).
	Path string
	// AmbiguousMatches lists any other candidates found alongside Path.
	// A non-empty list means the scan can't be fully certain it picked
	// the right file (e.g. a concurrent build also created a diff.patch).
	AmbiguousMatches []string
}

// FindRecentDiffPatch scans the OS temp directory for files named
// "diff.patch" that were modified after the given timestamp. This is used
// for iOS Phase 1 auto-diff: `shorebird patch ios --dry-run` creates the
// final diff in a random temp directory (Directory.systemTemp.createTemp()),
// and this function locates it by scanning for recently-created diff.patch
// files.
//
// Returns the most recently modified matching file, plus any other matches
// found (which the caller should surface as a warning — see
// FindRecentDiffPatchResult.AmbiguousMatches). Returns an error if none is
// found. The caller should record the timestamp before running the
// shorebird command and pass it here as `since`.
//
// This is inherently a best-effort scan — if the OS cleans temp files
// aggressively, or if another process creates a diff.patch concurrently,
// the result may be wrong. Callers should also run ValidateDiffFile on the
// result before uploading.
func FindRecentDiffPatch(since time.Time) (FindRecentDiffPatchResult, error) {
	tempDir := os.TempDir()
	var matches []string

	// Walk the temp directory. Shorebird creates a random temp dir (one
	// level under os.TempDir()) containing diff.patch, so we only need to
	// scan two levels deep.
	_ = filepath.WalkDir(tempDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if filepath.Base(path) != "diff.patch" {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.ModTime().After(since) {
			matches = append(matches, path)
		}
		return nil
	})

	if len(matches) == 0 {
		return FindRecentDiffPatchResult{}, fmt.Errorf(
			"no diff.patch found in %s modified after %s — shorebird patch may have failed, or the temp dir was cleaned. Pass --artifact <path> explicitly",
			tempDir,
			since.Format(time.RFC3339),
		)
	}

	// Sort by modification time, most recent first.
	sort.Slice(matches, func(i, j int) bool {
		fi, _ := os.Stat(matches[i])
		fj, _ := os.Stat(matches[j])
		return fi.ModTime().After(fj.ModTime())
	})

	return FindRecentDiffPatchResult{
		Path:             matches[0],
		AmbiguousMatches: matches[1:],
	}, nil
}

// ValidateDiffFile does a best-effort sanity check on a diff file before
// it's uploaded as a CodePush patch: it must exist, be non-empty, and
// start with the zstd magic bytes that Shorebird's `patch` binary always
// produces. This can't guarantee the diff is byte-for-byte correct (that
// requires the Shorebird updater on a real device), but it catches the
// common failure modes of the iOS temp-dir scan: an empty/truncated file,
// or a diff.patch belonging to an unrelated tool/process.
func ValidateDiffFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("diff file not found: %w", err)
	}
	if info.Size() == 0 {
		return fmt.Errorf("diff file %s is empty — shorebird patch may have failed partway through", path)
	}

	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("could not open diff file %s: %w", path, err)
	}
	defer f.Close() //nolint:errcheck

	header := make([]byte, len(zstdMagicBytes))
	if _, err := f.Read(header); err != nil {
		return fmt.Errorf("could not read diff file %s: %w", path, err)
	}
	for i, b := range zstdMagicBytes {
		if header[i] != b {
			return fmt.Errorf(
				"diff file %s does not look like a valid Shorebird patch (missing zstd magic bytes) — it may belong to an unrelated tool. Pass --artifact <path> explicitly with a known-good diff",
				path,
			)
		}
	}
	return nil
}
