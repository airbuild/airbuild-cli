package config

import (
	"os"
	"path/filepath"
	"testing"
)

// withTempHome points $HOME (and, on Windows, USERPROFILE) at a temp
// directory for the duration of the test so config Load/Save don't touch
// the developer's real ~/.airbuild directory.
func withTempHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir) // os.UserHomeDir() on Windows
	return dir
}

func TestLoadReturnsDefaultsWhenNoConfigExists(t *testing.T) {
	withTempHome(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.APIURL != DefaultAPIURL {
		t.Errorf("APIURL = %q, want default %q", cfg.APIURL, DefaultAPIURL)
	}
	if cfg.IsLoggedIn() {
		t.Error("IsLoggedIn() = true for a fresh config, want false")
	}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	withTempHome(t)

	cfg := &Config{
		APIKey:  "airbuild_test_key",
		APIURL:  "https://staging.airbuild.dev",
		AppID:   "app_xxx",
		OrgID:   "org_xxx",
		OrgName: "Test Org",
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if loaded.APIKey != cfg.APIKey {
		t.Errorf("APIKey = %q, want %q", loaded.APIKey, cfg.APIKey)
	}
	if loaded.APIURL != cfg.APIURL {
		t.Errorf("APIURL = %q, want %q", loaded.APIURL, cfg.APIURL)
	}
	if !loaded.IsLoggedIn() {
		t.Error("IsLoggedIn() = false after saving a config with an APIKey, want true")
	}
}

func TestLoadFillsInDefaultAPIURLWhenMissing(t *testing.T) {
	home := withTempHome(t)

	dir := filepath.Join(home, ".airbuild")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	// A config saved before apiUrl existed, or with it explicitly cleared.
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"apiKey":"airbuild_xxx"}`), 0600); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.APIURL != DefaultAPIURL {
		t.Errorf("APIURL = %q, want default %q to be filled in", cfg.APIURL, DefaultAPIURL)
	}
}

func TestLoadInvalidJSON(t *testing.T) {
	home := withTempHome(t)

	dir := filepath.Join(home, ".airbuild")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{not valid json"), 0600); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	if _, err := Load(); err == nil {
		t.Fatal("expected an error for invalid JSON config, got nil")
	}
}
