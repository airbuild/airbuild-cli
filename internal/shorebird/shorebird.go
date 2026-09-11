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
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"time"
)

// IsInstalled reports whether the `shorebird` binary is on PATH.
func IsInstalled() bool {
	_, err := exec.LookPath("shorebird")
	return err == nil
}

// Run executes `shorebird <args...>` in dir, streaming output to the
// current process's stdout/stderr so the user sees Shorebird's own
// progress output (build logs, prompts, etc).
func Run(dir string, args ...string) error {
	cmd := exec.Command("shorebird", args...)
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

// FindRecentDiffPatch scans the OS temp directory for files named
// "diff.patch" that were modified after the given timestamp. This is used
// for iOS Phase 1 auto-diff: `shorebird patch ios --dry-run` creates the
// final diff in a random temp directory (Directory.systemTemp.createTemp()),
// and this function locates it by scanning for recently-created diff.patch
// files.
//
// Returns the path to the most recently modified matching file, or an error
// if none is found. The caller should record the timestamp before running
// the shorebird command and pass it here as `since`.
//
// This is inherently a best-effort scan — if the OS cleans temp files
// aggressively, or if another process creates a diff.patch concurrently,
// the result may be wrong. The caller should validate the file before
// uploading.
func FindRecentDiffPatch(since time.Time) (string, error) {
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
		return "", fmt.Errorf(
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

	return matches[0], nil
}
