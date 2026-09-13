package shorebird

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFindAndroidLibappFindsMatch(t *testing.T) {
	dir := t.TempDir()
	libDir := filepath.Join(dir, "build", "app", "intermediates", "merged_native_libs", "release", "out", "lib", "arm64-v8a")
	if err := os.MkdirAll(libDir, 0755); err != nil {
		t.Fatalf("failed to create test dirs: %v", err)
	}
	libPath := filepath.Join(libDir, "libapp.so")
	if err := os.WriteFile(libPath, []byte("fake native lib"), 0644); err != nil {
		t.Fatalf("failed to write fake libapp.so: %v", err)
	}

	found, err := FindAndroidLibapp(dir, "")
	if err != nil {
		t.Fatalf("FindAndroidLibapp failed: %v", err)
	}
	if found != libPath {
		t.Errorf("found = %q, want %q", found, libPath)
	}
}

func TestFindAndroidLibappFiltersByArchitecture(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "build", "app", "intermediates", "merged_native_libs", "release", "out", "lib")
	for _, arch := range []string{"arm64-v8a", "armeabi-v7a"} {
		archDir := filepath.Join(base, arch)
		if err := os.MkdirAll(archDir, 0755); err != nil {
			t.Fatalf("failed to create test dirs: %v", err)
		}
		if err := os.WriteFile(filepath.Join(archDir, "libapp.so"), []byte("fake"), 0644); err != nil {
			t.Fatalf("failed to write fake libapp.so: %v", err)
		}
	}

	found, err := FindAndroidLibapp(dir, "armeabi-v7a")
	if err != nil {
		t.Fatalf("FindAndroidLibapp failed: %v", err)
	}
	if filepath.Base(filepath.Dir(found)) != "armeabi-v7a" {
		t.Errorf("found %q, want a file under armeabi-v7a", found)
	}
}

func TestFindAndroidLibappNotFound(t *testing.T) {
	dir := t.TempDir() // empty — no build output at all

	_, err := FindAndroidLibapp(dir, "")
	if err == nil {
		t.Fatal("expected an error when no libapp.so exists, got nil")
	}
}

func TestFindPatchBinaryFound(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	cacheDir := filepath.Join(home, ".shorebird", "bin", "cache", "artifacts", "patch")
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		t.Fatalf("failed to create cache dir: %v", err)
	}
	binPath := filepath.Join(cacheDir, "patch")
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatalf("failed to write fake patch binary: %v", err)
	}

	found, err := FindPatchBinary()
	if err != nil {
		t.Fatalf("FindPatchBinary failed: %v", err)
	}
	if found != binPath {
		t.Errorf("found = %q, want %q", found, binPath)
	}
}

func TestFindPatchBinaryNotFound(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	// No .shorebird directory created at all.

	_, err := FindPatchBinary()
	if err == nil {
		t.Fatal("expected an error when the shorebird cache doesn't exist, got nil")
	}
}

func TestCreateDiffMissingReleaseArtifact(t *testing.T) {
	dir := t.TempDir()
	patchArtifact := filepath.Join(dir, "patch-libapp.so")
	if err := os.WriteFile(patchArtifact, []byte("patch"), 0644); err != nil {
		t.Fatalf("failed to write patch artifact: %v", err)
	}

	err := CreateDiff("patch", filepath.Join(dir, "missing-release.so"), patchArtifact, filepath.Join(dir, "diff.patch"))
	if err == nil {
		t.Fatal("expected an error when the release artifact is missing, got nil")
	}
}

func TestCreateDiffMissingPatchArtifact(t *testing.T) {
	dir := t.TempDir()
	releaseArtifact := filepath.Join(dir, "release-libapp.so")
	if err := os.WriteFile(releaseArtifact, []byte("release"), 0644); err != nil {
		t.Fatalf("failed to write release artifact: %v", err)
	}

	err := CreateDiff("patch", releaseArtifact, filepath.Join(dir, "missing-patch.so"), filepath.Join(dir, "diff.patch"))
	if err == nil {
		t.Fatal("expected an error when the patch artifact is missing, got nil")
	}
}

func TestFindRecentDiffPatchFindsFile(t *testing.T) {
	since := time.Now()
	time.Sleep(10 * time.Millisecond) // ensure the new file's mtime is strictly after `since`

	dir := t.TempDir() // created under os.TempDir(), so the scan will find it
	diffPath := filepath.Join(dir, "diff.patch")
	if err := os.WriteFile(diffPath, append(zstdMagicBytes, []byte("fake diff body")...), 0644); err != nil {
		t.Fatalf("failed to write fake diff.patch: %v", err)
	}

	result, err := FindRecentDiffPatch(since)
	if err != nil {
		t.Fatalf("FindRecentDiffPatch failed: %v", err)
	}
	if result.Path != diffPath {
		t.Errorf("Path = %q, want %q", result.Path, diffPath)
	}
}

func TestFindRecentDiffPatchIgnoresOlderFiles(t *testing.T) {
	dir := t.TempDir()
	diffPath := filepath.Join(dir, "diff.patch")
	if err := os.WriteFile(diffPath, []byte("stale diff from a previous run"), 0644); err != nil {
		t.Fatalf("failed to write fake diff.patch: %v", err)
	}

	// `since` is set after the file was created, so it should be excluded.
	time.Sleep(10 * time.Millisecond)
	since := time.Now()

	_, err := FindRecentDiffPatch(since)
	if err == nil {
		t.Fatal("expected an error when only a stale diff.patch exists, got nil")
	}
}

func TestFindRecentDiffPatchReportsAmbiguousMatches(t *testing.T) {
	since := time.Now()
	time.Sleep(10 * time.Millisecond)

	dir1 := t.TempDir()
	dir2 := t.TempDir()
	path1 := filepath.Join(dir1, "diff.patch")
	path2 := filepath.Join(dir2, "diff.patch")
	if err := os.WriteFile(path1, []byte("candidate 1"), 0644); err != nil {
		t.Fatalf("failed to write %s: %v", path1, err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(path2, []byte("candidate 2"), 0644); err != nil {
		t.Fatalf("failed to write %s: %v", path2, err)
	}

	result, err := FindRecentDiffPatch(since)
	if err != nil {
		t.Fatalf("FindRecentDiffPatch failed: %v", err)
	}
	// The most recently modified file (path2) should be picked, with path1
	// surfaced as an ambiguous match.
	if result.Path != path2 {
		t.Errorf("Path = %q, want the most recently modified file %q", result.Path, path2)
	}
	if len(result.AmbiguousMatches) != 1 || result.AmbiguousMatches[0] != path1 {
		t.Errorf("AmbiguousMatches = %v, want [%q]", result.AmbiguousMatches, path1)
	}
}

func TestValidateDiffFileValid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "diff.patch")
	content := append(append([]byte{}, zstdMagicBytes...), []byte("compressed bidiff payload")...)
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	if err := ValidateDiffFile(path); err != nil {
		t.Errorf("ValidateDiffFile failed for a well-formed diff: %v", err)
	}
}

func TestValidateDiffFileEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "diff.patch")
	if err := os.WriteFile(path, []byte{}, 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	if err := ValidateDiffFile(path); err == nil {
		t.Fatal("expected an error for an empty diff file, got nil")
	}
}

func TestValidateDiffFileWrongMagicBytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "diff.patch")
	if err := os.WriteFile(path, []byte("not a zstd frame at all"), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	if err := ValidateDiffFile(path); err == nil {
		t.Fatal("expected an error for a file without zstd magic bytes, got nil")
	}
}

func TestValidateDiffFileMissing(t *testing.T) {
	dir := t.TempDir()
	if err := ValidateDiffFile(filepath.Join(dir, "does-not-exist.patch")); err == nil {
		t.Fatal("expected an error for a missing file, got nil")
	}
}
