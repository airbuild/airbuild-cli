// Package setup provides diagnostic, installation, and initialization
// helpers for `airbuild codepush <framework> doctor|install|init`.
//
// Each framework (Flutter, React Native) defines a Checklist of SetupChecks.
// The doctor command evaluates them read-only; install runs the fix actions;
// init performs project-level initialization (shorebird.yaml, app.json, etc).
package setup

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/airbuild/airbuild-cli/internal/api"
	"github.com/airbuild/airbuild-cli/internal/config"
	"github.com/airbuild/airbuild-cli/internal/project"
)

// CheckStatus is the result of a single diagnostic check.
type CheckStatus int

const (
	StatusPass CheckStatus = iota
	StatusFail
	StatusWarn
)

func (s CheckStatus) String() string {
	switch s {
	case StatusPass:
		return "✓"
	case StatusFail:
		return "✗"
	case StatusWarn:
		return "⚠"
	default:
		return "?"
	}
}

// ErrManualAction is returned by a Fix when the change could not be applied
// automatically and the user has been shown instructions instead.
var ErrManualAction = errors.New("manual change required (see instructions above)")

// CheckResult is the outcome of evaluating one SetupCheck.
type CheckResult struct {
	Name    string
	Status  CheckStatus
	Message string // what was found (e.g. "found at /usr/local/bin/dart")
	Fix     string // suggestion for fixing a failure
}

// SetupCheck is a single diagnostic check. Run evaluates it and returns a
// CheckResult. Fix (optional) performs the corrective action. Modifies lists
// the files/tools the fix touches, shown to the user before confirming.
type SetupCheck struct {
	Name     string
	Run      func() CheckResult
	Fix      func() error // nil = not fixable by `install`/`init`
	Modifies string
}

// Checklist is an ordered list of checks for a framework.
type Checklist []SetupCheck

// Evaluate runs every check and returns the results.
func (cl Checklist) Evaluate() []CheckResult {
	results := make([]CheckResult, 0, len(cl))
	for _, c := range cl {
		results = append(results, c.Run())
	}
	return results
}

// CountFailures returns the number of checks that failed.
func CountFailures(results []CheckResult) int {
	return countStatus(results, StatusFail)
}

// CountWarnings returns the number of checks that produced a warning.
func CountWarnings(results []CheckResult) int {
	return countStatus(results, StatusWarn)
}

func countStatus(results []CheckResult, s CheckStatus) int {
	n := 0
	for _, r := range results {
		if r.Status == s {
			n++
		}
	}
	return n
}

// --- shared helpers ---

// Which checks if a binary is on PATH.
func Which(name string) (string, bool) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", false
	}
	return path, true
}

// FileExists checks if a file exists at the given path.
func FileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// FileContains checks if a file exists and contains the given substring.
func FileContains(path, substring string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.Contains(string(data), substring)
}

// RunCmd executes a command, streaming output to stdout/stderr.
func RunCmd(dir string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

// apiURL returns the configured AirBuild API base URL without a trailing slash.
func apiURL() string {
	cfg, err := config.Load()
	if err != nil || cfg.APIURL == "" {
		return config.DefaultAPIURL
	}
	return strings.TrimRight(cfg.APIURL, "/")
}

var cachedDistributionKey string

// distributionKey resolves the linked app's public distribution key from the
// AirBuild API. Requires .airbuild.json and a configured API key.
func distributionKey() (string, error) {
	if cachedDistributionKey != "" {
		return cachedDistributionKey, nil
	}
	proj, err := project.Load()
	if err != nil {
		return "", fmt.Errorf("no linked app — run `airbuild init` first")
	}
	cfg, err := config.Load()
	if err != nil || !cfg.IsLoggedIn() {
		return "", fmt.Errorf("not logged in — run `airbuild login --api-key airbuild_xxx` first")
	}
	resp, err := api.New(apiURL(), cfg.APIKey).ListApps()
	if err != nil {
		return "", fmt.Errorf("could not fetch app details: %w", err)
	}
	for _, a := range resp.Apps {
		if a.ID == proj.AppID {
			if a.DistributionKey == "" {
				return "", fmt.Errorf("app %s has no distribution key — enable OTA Updates for it in the dashboard", a.ID)
			}
			cachedDistributionKey = a.DistributionKey
			return a.DistributionKey, nil
		}
	}
	return "", fmt.Errorf("app %s from .airbuild.json was not found in your organization", proj.AppID)
}

func checkAirbuildJSON() CheckResult {
	if !project.Exists() {
		return Fail(".airbuild.json", "not found", "Run `airbuild init` to link your app")
	}
	cfg, err := project.Load()
	if err != nil {
		return Fail(".airbuild.json", fmt.Sprintf("invalid: %v", err), "Run `airbuild init` to recreate it")
	}
	return Passf(".airbuild.json", "found (app: %s)", cfg.AppID)
}

func checkAPIKey() CheckResult {
	cfg, err := config.Load()
	if err != nil || !cfg.IsLoggedIn() {
		return Fail("api key", "not configured", "Run `airbuild login --api-key airbuild_xxx`")
	}
	return Passf("api key", "configured (%s)", apiURL())
}

func checkConnectivity() CheckResult {
	target := apiURL()
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(target + "/api/health")
	if err != nil {
		return Fail("connectivity", fmt.Sprintf("could not reach %s: %v", target, err),
			"Check your network, or run `airbuild config set --api-url <url>`")
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return Fail("connectivity", fmt.Sprintf("%s returned HTTP %d", target, resp.StatusCode),
			"Check that the API URL is correct: `airbuild config show`")
	}
	return Passf("connectivity", "%s reachable", target)
}

// Pass creates a passing CheckResult.
func Pass(name, message string) CheckResult {
	return CheckResult{Name: name, Status: StatusPass, Message: message}
}

// Fail creates a failing CheckResult with a fix suggestion.
func Fail(name, message, fix string) CheckResult {
	return CheckResult{Name: name, Status: StatusFail, Message: message, Fix: fix}
}

// Warn creates a warning CheckResult.
func Warn(name, message, fix string) CheckResult {
	return CheckResult{Name: name, Status: StatusWarn, Message: message, Fix: fix}
}

// Passf is like Pass with formatted message.
func Passf(name, format string, args ...interface{}) CheckResult {
	return Pass(name, fmt.Sprintf(format, args...))
}
