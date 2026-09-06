// Package shorebird wraps the (locally installed) Shorebird CLI to build
// Flutter releases/patches, and locates the resulting build artifacts so
// they can be uploaded to AirBuild's own control-plane server.
//
// IMPORTANT — artifact location is best-effort:
// Shorebird's `release`/`patch` commands build the app and (when talking to
// Shorebird's own cloud) upload artifacts directly — there is no officially
// documented, version-stable local path to "the artifact Shorebird just
// built" once the command finishes. For Android, the compiled
// `libapp.so` ends up in the normal Flutter/Gradle native-libs merge
// output, which we scan for below. This is the same file regardless of
// whether Shorebird or plain `flutter build` produced it, but the exact
// path can shift across Android Gradle Plugin / Flutter versions.
//
// For reliability in CI or across Flutter versions, prefer passing
// --artifact explicitly to `airbuild codepush flutter release/patch`
// rather than relying on auto-detection.
package shorebird

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
