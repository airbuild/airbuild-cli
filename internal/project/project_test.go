package project

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFromMissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".airbuild.json")

	_, err := LoadFrom(path)
	if err == nil {
		t.Fatal("expected an error for a missing config file, got nil")
	}
}

func TestLoadFromInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".airbuild.json")
	if err := os.WriteFile(path, []byte("{not valid json"), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	_, err := LoadFrom(path)
	if err == nil {
		t.Fatal("expected an error for invalid JSON, got nil")
	}
}

func TestLoadFromMissingAppID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".airbuild.json")
	if err := os.WriteFile(path, []byte(`{"builds":{}}`), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	_, err := LoadFrom(path)
	if err == nil {
		t.Fatal("expected an error when appId is missing, got nil")
	}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".airbuild.json")

	cfg := &ProjectConfig{
		AppID: "app_xxx",
		Builds: map[string]PlatformBuilds{
			"android": {Debug: "debug.apk", Release: "release.apk"},
			"ios":     {Debug: "debug.ipa", Release: "release.ipa"},
		},
	}
	if err := cfg.SaveTo(path); err != nil {
		t.Fatalf("SaveTo failed: %v", err)
	}

	loaded, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom failed: %v", err)
	}
	if loaded.AppID != "app_xxx" {
		t.Errorf("AppID = %q, want %q", loaded.AppID, "app_xxx")
	}
	if loaded.GetBuildPath("android", "release") != "release.apk" {
		t.Errorf("GetBuildPath(android, release) = %q, want %q", loaded.GetBuildPath("android", "release"), "release.apk")
	}
	if loaded.GetBuildPath("ios", "debug") != "debug.ipa" {
		t.Errorf("GetBuildPath(ios, debug) = %q, want %q", loaded.GetBuildPath("ios", "debug"), "debug.ipa")
	}
}

func TestLoadFromNormalizesPlatformKeyCase(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".airbuild.json")
	// "Android" with a capital A should still be found under "android".
	if err := os.WriteFile(path, []byte(`{"appId":"app_xxx","builds":{"Android":{"release":"app-release.apk"}}}`), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom failed: %v", err)
	}
	if !cfg.HasPlatform("android") {
		t.Error("expected HasPlatform(\"android\") to be true after case normalization")
	}
	if cfg.GetBuildPath("android", "release") != "app-release.apk" {
		t.Errorf("GetBuildPath(android, release) = %q, want %q", cfg.GetBuildPath("android", "release"), "app-release.apk")
	}
}

func TestLoadFromHandlesNullBuilds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".airbuild.json")
	if err := os.WriteFile(path, []byte(`{"appId":"app_xxx","builds":null}`), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom failed: %v", err)
	}
	if cfg.Builds == nil {
		t.Error("expected Builds to be initialized to an empty map, got nil")
	}
	if cfg.HasPlatform("android") {
		t.Error("expected HasPlatform(\"android\") to be false for an empty builds map")
	}
}

func TestGetBuildPathUnknownPlatformOrType(t *testing.T) {
	cfg := &ProjectConfig{
		AppID: "app_xxx",
		Builds: map[string]PlatformBuilds{
			"android": {Release: "release.apk"},
		},
	}
	if got := cfg.GetBuildPath("ios", "release"); got != "" {
		t.Errorf("GetBuildPath(ios, release) = %q, want empty string for unconfigured platform", got)
	}
	if got := cfg.GetBuildPath("android", "staging"); got != "" {
		t.Errorf("GetBuildPath(android, staging) = %q, want empty string for unknown build type", got)
	}
}

func TestConfiguredPlatforms(t *testing.T) {
	cfg := &ProjectConfig{
		AppID: "app_xxx",
		Builds: map[string]PlatformBuilds{
			"android": {Release: "release.apk"},
			"ios":     {Release: "release.ipa"},
		},
	}
	platforms := cfg.ConfiguredPlatforms()
	if len(platforms) != 2 {
		t.Fatalf("ConfiguredPlatforms() returned %d platforms, want 2: %v", len(platforms), platforms)
	}
}

func TestExists(t *testing.T) {
	dir := t.TempDir()
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	defer func() {
		_ = os.Chdir(origWd)
	}()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("failed to chdir: %v", err)
	}

	if Exists() {
		t.Error("Exists() = true before config file is created, want false")
	}

	cfg := &ProjectConfig{AppID: "app_xxx"}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	if !Exists() {
		t.Error("Exists() = false after config file is created, want true")
	}
}
