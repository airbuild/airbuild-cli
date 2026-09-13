package cmd

import (
	"os"
	"testing"

	"github.com/airbuild/cli/internal/project"
)

// chdir changes the working directory for the duration of the test and
// restores it afterwards.
func chdir(t *testing.T, dir string) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("failed to chdir to %s: %v", dir, err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(orig)
	})
}

func TestNormalizeFlutterPlatform(t *testing.T) {
	cases := []struct {
		input   string
		want    string
		wantErr bool
	}{
		{"android", "ANDROID", false},
		{"ANDROID", "ANDROID", false},
		{"Android", "ANDROID", false},
		{"ios", "IOS", false},
		{"IOS", "IOS", false},
		{"windows", "", true},
		{"", "", true},
	}
	for _, c := range cases {
		got, err := normalizeFlutterPlatform(c.input)
		if c.wantErr {
			if err == nil {
				t.Errorf("normalizeFlutterPlatform(%q) = %q, nil; want an error", c.input, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("normalizeFlutterPlatform(%q) returned unexpected error: %v", c.input, err)
			continue
		}
		if got != c.want {
			t.Errorf("normalizeFlutterPlatform(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestNormalizePlatformFlag(t *testing.T) {
	// Empty input is passed through untouched (caller is using --update-id
	// instead of --platform).
	got, err := normalizePlatformFlag("")
	if err != nil {
		t.Fatalf("normalizePlatformFlag(\"\") returned unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("normalizePlatformFlag(\"\") = %q, want empty string", got)
	}

	got, err = normalizePlatformFlag("android")
	if err != nil {
		t.Fatalf("normalizePlatformFlag(\"android\") returned unexpected error: %v", err)
	}
	if got != "ANDROID" {
		t.Errorf("normalizePlatformFlag(\"android\") = %q, want \"ANDROID\"", got)
	}

	if _, err := normalizePlatformFlag("andriod"); err == nil {
		t.Error("normalizePlatformFlag(\"andriod\") = nil error, want an error for the typo")
	}
}

func TestValidateRolloutPercent(t *testing.T) {
	valid := []int{0, 1, 50, 99, 100}
	for _, pct := range valid {
		if err := validateRolloutPercent(pct); err != nil {
			t.Errorf("validateRolloutPercent(%d) returned unexpected error: %v", pct, err)
		}
	}

	invalid := []int{-1, -100, 101, 1000}
	for _, pct := range invalid {
		if err := validateRolloutPercent(pct); err == nil {
			t.Errorf("validateRolloutPercent(%d) = nil, want an error", pct)
		}
	}
}

func TestValueOr(t *testing.T) {
	if got := valueOr("", "fallback"); got != "fallback" {
		t.Errorf("valueOr(\"\", \"fallback\") = %q, want \"fallback\"", got)
	}
	if got := valueOr("explicit", "fallback"); got != "explicit" {
		t.Errorf("valueOr(\"explicit\", \"fallback\") = %q, want \"explicit\"", got)
	}
}

func TestResolveAppIDExplicitFlagTakesPrecedence(t *testing.T) {
	// No .airbuild.json needed — an explicit flag value should short-circuit
	// before any filesystem access.
	got, ok := resolveAppID("app_explicit")
	if !ok {
		t.Fatal("resolveAppID with an explicit value returned ok=false")
	}
	if got != "app_explicit" {
		t.Errorf("resolveAppID(\"app_explicit\") = %q, want \"app_explicit\"", got)
	}
}

func TestResolveAppIDFallsBackToProjectConfig(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)

	cfg := &project.ProjectConfig{AppID: "app_from_config"}
	if err := cfg.Save(); err != nil {
		t.Fatalf("failed to save .airbuild.json: %v", err)
	}

	got, ok := resolveAppID("")
	if !ok {
		t.Fatal("resolveAppID fell back to .airbuild.json but returned ok=false")
	}
	if got != "app_from_config" {
		t.Errorf("resolveAppID(\"\") = %q, want %q", got, "app_from_config")
	}
}

func TestResolveAppIDFailsWithoutFlagOrProjectConfig(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	// No .airbuild.json in this empty directory.

	_, ok := resolveAppID("")
	if ok {
		t.Fatal("resolveAppID with no --app and no .airbuild.json returned ok=true, want false")
	}
}

func TestResolveAppIDUsesConfigFileNameConstant(t *testing.T) {
	// Sanity check that project.ConfigFile matches what `airbuild init`
	// writes, so resolveAppID's fallback lines up with the rest of the CLI.
	if project.ConfigFile != ".airbuild.json" {
		t.Errorf("project.ConfigFile = %q, want \".airbuild.json\"", project.ConfigFile)
	}
}
