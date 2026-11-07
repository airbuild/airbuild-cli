package expo

import (
	"os"
	"path/filepath"
	"testing"
)

func writeMeta(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "metadata.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestParseMetadata(t *testing.T) {
	dir := t.TempDir()
	writeMeta(t, dir, `{"version":0,"bundler":"metro","fileMetadata":{"android":{"bundle":"bundles/android-abc.js","assets":[{"path":"assets/h1","ext":"png"}]}}}`)
	meta, err := ParseMetadata(dir)
	if err != nil {
		t.Fatal(err)
	}
	a := meta.FileMetadata["android"]
	if a.Bundle != "bundles/android-abc.js" || len(a.Assets) != 1 || a.Assets[0].Ext != "png" {
		t.Errorf("unexpected metadata: %+v", meta)
	}
}

func TestParseMetadataErrors(t *testing.T) {
	if _, err := ParseMetadata(t.TempDir()); err == nil {
		t.Error("expected an error when metadata.json is missing")
	}
	dir := t.TempDir()
	writeMeta(t, dir, `{not json`)
	if _, err := ParseMetadata(dir); err == nil {
		t.Error("expected an error for invalid JSON")
	}
}

func TestBuildManifestEntries(t *testing.T) {
	meta := &ExportMetadata{FileMetadata: map[string]ExportPlatformMetadata{
		"ios": {
			Bundle: "bundles/ios-123.js",
			Assets: []ExportMetadataAsset{{Path: "assets/a", Ext: "png"}, {Path: "assets/b", Ext: "ttf"}, {Path: "assets/c", Ext: "weird"}},
		},
	}}
	entries, err := BuildManifestEntries("/out", meta, "IOS")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 4 {
		t.Fatalf("got %d entries, want 4 (bundle + 3 assets)", len(entries))
	}
	launch := entries[0]
	if !launch.IsLaunchAsset || launch.Key != "ios-123" || launch.FieldName != "file_0" ||
		launch.ContentType != "application/javascript" || launch.FilePath != filepath.Join("/out", "bundles/ios-123.js") {
		t.Errorf("bad launch entry: %+v", launch)
	}
	wantTypes := []string{"image/png", "font/ttf", "application/octet-stream"}
	for i, e := range entries[1:] {
		if e.IsLaunchAsset || e.ContentType != wantTypes[i] || e.FieldName != "file_"+string(rune('1'+i)) {
			t.Errorf("bad asset entry %d: %+v", i, e)
		}
	}
}

func TestBuildManifestEntriesMissingPlatform(t *testing.T) {
	meta := &ExportMetadata{FileMetadata: map[string]ExportPlatformMetadata{"android": {}}}
	if _, err := BuildManifestEntries("/out", meta, "ios"); err == nil {
		t.Error("expected an error for a platform missing from metadata.json")
	}
}

func TestContentTypeForExtension(t *testing.T) {
	cases := map[string]string{"PNG": "image/png", ".jpg": "image/jpeg", "woff2": "font/woff2", "": "application/octet-stream", "xyz": "application/octet-stream"}
	for in, want := range cases {
		if got := contentTypeForExtension(in); got != want {
			t.Errorf("contentTypeForExtension(%q) = %q, want %q", in, got, want)
		}
	}
}
