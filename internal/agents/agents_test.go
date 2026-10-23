package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testServer = "https://airbuild.test"

func TestWriteAllCreatesFiles(t *testing.T) {
	dir := t.TempDir()

	written, err := WriteAll(dir, testServer)
	if err != nil {
		t.Fatalf("WriteAll failed: %v", err)
	}
	if len(written) != len(Files) {
		t.Fatalf("wrote %d files, want %d", len(written), len(Files))
	}

	for _, name := range Files {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("failed to read %s: %v", name, err)
		}
		s := string(data)
		if !strings.Contains(s, startMarker) || !strings.Contains(s, endMarker) {
			t.Errorf("%s missing markers", name)
		}
		if !strings.Contains(s, "AIRBUILD_API_KEY") || !strings.Contains(s, "--json") {
			t.Errorf("%s missing key content", name)
		}
		if !strings.Contains(s, "flutter pub add airbuild") || !strings.Contains(s, "dk_") {
			t.Errorf("%s missing in-app SDK content", name)
		}
		if !strings.Contains(s, testServer+"/i/<slug>") || !strings.Contains(s, testServer+"/api/openapi") {
			t.Errorf("%s missing configured server URLs", name)
		}
		if !strings.Contains(s, "baseUrl: '"+testServer+"'") {
			t.Errorf("%s missing SDK baseUrl wiring", name)
		}
	}
}

func TestWriteAllPreservesExistingContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(path, []byte("# My project\n\nCustom rules here.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := WriteAll(dir, testServer); err != nil {
		t.Fatalf("WriteAll failed: %v", err)
	}

	data, _ := os.ReadFile(path)
	s := string(data)
	if !strings.Contains(s, "Custom rules here.") {
		t.Error("existing content was lost")
	}
	if !strings.Contains(s, startMarker) {
		t.Error("block was not appended")
	}
}

func TestWriteAllIsIdempotent(t *testing.T) {
	dir := t.TempDir()

	if _, err := WriteAll(dir, testServer); err != nil {
		t.Fatalf("first WriteAll failed: %v", err)
	}
	if _, err := WriteAll(dir, testServer); err != nil {
		t.Fatalf("second WriteAll failed: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	s := string(data)
	if n := strings.Count(s, startMarker); n != 1 {
		t.Errorf("block appears %d times after re-run, want 1", n)
	}
}

func TestWriteAllReplacesBlockInPlace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")
	orig := "# Notes\n\n" + startMarker + "\nold airbuild content\n" + endMarker + "\n\nMore notes.\n"
	if err := os.WriteFile(path, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := WriteAll(dir, testServer); err != nil {
		t.Fatalf("WriteAll failed: %v", err)
	}

	data, _ := os.ReadFile(path)
	s := string(data)
	if strings.Contains(s, "old airbuild content") {
		t.Error("stale block was not replaced")
	}
	if !strings.Contains(s, "More notes.") {
		t.Error("content after the block was lost")
	}
}
