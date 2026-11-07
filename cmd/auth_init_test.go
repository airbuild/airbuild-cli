package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/airbuild/airbuild-cli/internal/config"
)

func TestFailfReturnsSilentExitError(t *testing.T) {
	err := failf("boom %d", 1)
	var ee exitError
	if !errors.As(err, &ee) || ee.code != 1 || !errors.Is(err, errSilent) {
		t.Errorf("failf should return the silent exit error, got %#v", err)
	}
}

func TestReadKeyFromStdin(t *testing.T) {
	cases := map[string]string{
		"airbuild_abc\n":        "airbuild_abc",
		"  airbuild_abc  \r\n":  "airbuild_abc",
		"airbuild_abc":          "airbuild_abc",
		"airbuild_abc\nignored": "airbuild_abc",
		"":                      "",
	}
	for in, want := range cases {
		got, err := readKeyFromStdin(strings.NewReader(in))
		if err != nil || got != want {
			t.Errorf("readKeyFromStdin(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func withHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(config.EnvAPIKey, "")
	t.Setenv(config.EnvAPIURL, "")
	return home
}

func TestLogoutClearsKeyButKeepsURL(t *testing.T) {
	withHome(t)
	cfg := &config.Config{APIKey: "airbuild_secret", APIURL: "https://self.hosted", OrgID: "o1", OrgName: "Org"}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	if err := logoutCmd.RunE(logoutCmd, nil); err != nil {
		t.Fatal(err)
	}
	got, err := config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	if got.APIKey != "" || got.OrgID != "" || got.OrgName != "" {
		t.Errorf("credentials not cleared: %+v", got)
	}
	if got.APIURL != "https://self.hosted" {
		t.Errorf("API URL should be kept, got %q", got.APIURL)
	}
}

func TestLogoutNeverPersistsEnvironmentKey(t *testing.T) {
	home := withHome(t)
	t.Setenv(config.EnvAPIKey, "airbuild_from_env")
	if err := logoutCmd.RunE(logoutCmd, nil); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(home, ".airbuild", "config.json"))
	if strings.Contains(string(data), "airbuild_from_env") {
		t.Error("environment API key leaked into config.json")
	}
}

func TestLoginRequiresAKey(t *testing.T) {
	withHome(t)
	loginAPIKey, loginAPIKeyStdin, loginAPIURL = "", false, ""
	if err := loginCmd.RunE(loginCmd, nil); !errors.Is(err, errSilent) {
		t.Errorf("login without any key should fail, got %v", err)
	}
}

func TestInitYesRefusesToClobberExistingConfig(t *testing.T) {
	withHome(t)
	t.Setenv(config.EnvAPIKey, "airbuild_test")
	dir := t.TempDir()
	chdir(t, dir)
	const original = `{"appId":"app_keep"}`
	if err := os.WriteFile(filepath.Join(dir, ".airbuild.json"), []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	oldYes, oldForce, oldAgents, oldApp := initYes, initForce, initAgents, initAppID
	t.Cleanup(func() { initYes, initForce, initAgents, initAppID = oldYes, oldForce, oldAgents, oldApp })
	initYes, initForce, initAgents, initAppID = true, false, false, ""

	// Must fail (non-zero exit), not silently "succeed" doing nothing.
	err := initCmd.RunE(initCmd, nil)
	if !errors.Is(err, errSilent) {
		t.Fatalf("init --yes over an existing config must fail, got %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, ".airbuild.json"))
	if string(b) != original {
		t.Errorf("existing config was modified: %s", b)
	}
}
