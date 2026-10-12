package shorebird

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFindIosAppBinaryFromAppBundle(t *testing.T) {
	dir := t.TempDir()
	appDir := filepath.Join(dir, "build", "ios", "iphoneos", "Runner.app", "Frameworks", "App.framework")
	if err := os.MkdirAll(appDir, 0755); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(appDir, "App")
	if err := os.WriteFile(want, []byte("macho"), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := FindIosAppBinary(dir)
	if err != nil {
		t.Fatalf("FindIosAppBinary: %v", err)
	}
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFindIosAppBinaryFromXcarchive(t *testing.T) {
	dir := t.TempDir()
	appDir := filepath.Join(dir, "build", "ios", "archive", "Runner.xcarchive",
		"Products", "Applications", "Runner.app", "Frameworks", "App.framework")
	if err := os.MkdirAll(appDir, 0755); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(appDir, "App")
	if err := os.WriteFile(want, []byte("macho"), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := FindIosAppBinary(dir)
	if err != nil {
		t.Fatalf("FindIosAppBinary: %v", err)
	}
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFindIosAppBinaryPrefersNewest(t *testing.T) {
	dir := t.TempDir()
	write := func(rel string) string {
		p := filepath.Join(dir, rel, "Frameworks", "App.framework", "App")
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(rel), 0644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	old := write(filepath.Join("build", "ios", "iphoneos", "Old.app"))
	time.Sleep(10 * time.Millisecond)
	newest := write(filepath.Join("build", "ios", "iphoneos", "New.app"))

	got, err := FindIosAppBinary(dir)
	if err != nil {
		t.Fatalf("FindIosAppBinary: %v", err)
	}
	if got != newest {
		t.Errorf("got %q, want newest %q (not %q)", got, newest, old)
	}
}

func TestFindIosAppBinaryNotFound(t *testing.T) {
	if _, err := FindIosAppBinary(t.TempDir()); err == nil {
		t.Fatal("expected an error when no App binary exists, got nil")
	}
}

func TestCollectIosSupplements(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"App.ct.link", "App.class_table.json"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	// Unknown files must not be collected.
	if err := os.WriteFile(filepath.Join(dir, "unrelated.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	got := CollectIosSupplements(dir)
	if len(got) != 2 {
		t.Fatalf("got %d supplements, want 2: %v", len(got), got)
	}
}

func TestCopyIosSupplementsRenamesBase(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "App.ct.link"), []byte("ct"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "App.dd.link"), []byte("dd"), 0644); err != nil {
		t.Fatal(err)
	}

	n, err := CopyIosSupplements(src, dst, "out")
	if err != nil {
		t.Fatalf("CopyIosSupplements: %v", err)
	}
	if n != 2 {
		t.Fatalf("copied %d, want 2", n)
	}
	for _, want := range []string{"out.ct.link", "out.dd.link"} {
		if _, err := os.Stat(filepath.Join(dst, want)); err != nil {
			t.Errorf("expected %s in dst: %v", want, err)
		}
	}
}

func TestCopyIosSupplementsToleratesMissing(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	n, err := CopyIosSupplements(src, dst, "out")
	if err != nil {
		t.Fatalf("expected no error with empty src dir: %v", err)
	}
	if n != 0 {
		t.Fatalf("copied %d, want 0", n)
	}
}

func TestIosReleaseArtifactRoundTrip(t *testing.T) {
	dir := t.TempDir()
	appPath := filepath.Join(dir, "App")
	supPath := filepath.Join(dir, "App.ct.link")
	if err := os.WriteFile(appPath, []byte("macho-bytes"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(supPath, []byte("ct-data"), 0644); err != nil {
		t.Fatal(err)
	}

	zipPath := filepath.Join(dir, "release.zip")
	if err := CreateIosReleaseArtifact(appPath, []string{supPath}, zipPath); err != nil {
		t.Fatalf("CreateIosReleaseArtifact: %v", err)
	}

	// Zip must contain the flat layout aot-tools expects.
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	zr.Close()
	for _, want := range []string{"App", "App.ct.link"} {
		if !names[want] {
			t.Errorf("zip missing entry %s (has %v)", want, names)
		}
	}

	extractDir := filepath.Join(dir, "extract")
	if err := os.MkdirAll(extractDir, 0755); err != nil {
		t.Fatal(err)
	}
	gotApp, err := ExtractIosReleaseArtifact(zipPath, extractDir)
	if err != nil {
		t.Fatalf("ExtractIosReleaseArtifact: %v", err)
	}
	if gotApp != filepath.Join(extractDir, "App") {
		t.Errorf("got App path %q", gotApp)
	}
	data, _ := os.ReadFile(gotApp)
	if string(data) != "macho-bytes" {
		t.Errorf("App content = %q", string(data))
	}
	if _, err := os.Stat(filepath.Join(extractDir, "App.ct.link")); err != nil {
		t.Errorf("supplement not extracted beside App: %v", err)
	}
}

func TestExtractIosReleaseArtifactMissingApp(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "empty.zip")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, _ := zw.Create("not-app.txt")
	w.Write([]byte("x")) //nolint:errcheck
	zw.Close()
	f.Close() //nolint:errcheck

	if _, err := ExtractIosReleaseArtifact(zipPath, dir); err == nil {
		t.Fatal("expected an error when the zip has no App entry, got nil")
	}
}

func TestFindRecentAppDill(t *testing.T) {
	dir := t.TempDir()
	dillDir := filepath.Join(dir, ".dart_tool", "flutter_build", "abc123")
	if err := os.MkdirAll(dillDir, 0755); err != nil {
		t.Fatal(err)
	}
	dill := filepath.Join(dillDir, "app.dill")
	if err := os.WriteFile(dill, []byte("kernel"), 0644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)

	got, err := FindRecentAppDill(dir, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatalf("FindRecentAppDill: %v", err)
	}
	if got != dill {
		t.Errorf("got %q, want %q", got, dill)
	}
}

func TestFindRecentAppDillRespectsSince(t *testing.T) {
	dir := t.TempDir()
	dillDir := filepath.Join(dir, ".dart_tool", "flutter_build", "abc123")
	if err := os.MkdirAll(dillDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dillDir, "app.dill"), []byte("stale"), 0644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)

	if _, err := FindRecentAppDill(dir, time.Now()); err == nil {
		t.Fatal("expected an error for a stale app.dill, got nil")
	}
}

// When the build ran but kernel_snapshot was skipped (identical Dart
// sources → content-hash cache hit), app.dill's mtime predates the build.
// A fresh sibling output still marks the dir as consumed by this build.
func TestFindRecentAppDillSkippedKernelSnapshot(t *testing.T) {
	dir := t.TempDir()
	dillDir := filepath.Join(dir, ".dart_tool", "flutter_build", "abc123")
	if err := os.MkdirAll(dillDir, 0755); err != nil {
		t.Fatal(err)
	}
	dill := filepath.Join(dillDir, "app.dill")
	if err := os.WriteFile(dill, []byte("reused-kernel"), 0644); err != nil {
		t.Fatal(err)
	}
	buildStart := time.Now()
	time.Sleep(10 * time.Millisecond)
	// Simulate the build consuming the dir: a stamp file updates.
	if err := os.WriteFile(filepath.Join(dillDir, "aot_assembly_release.stamp"), []byte("stamp"), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := FindRecentAppDill(dir, buildStart)
	if err != nil {
		t.Fatalf("FindRecentAppDill: %v", err)
	}
	if got != dill {
		t.Errorf("got %q, want %q", got, dill)
	}
}
