// Package setup provides diagnostic, installation, and initialization
// helpers for `airbuild codepush <framework> doctor|install|init`.
//
// Each framework (Flutter, React Native) defines a Checklist of SetupChecks.
// The doctor command evaluates them read-only; install runs the fix actions;
// init performs project-level initialization (shorebird.yaml, app.json, etc).
package setup

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// CheckResult is the outcome of evaluating one SetupCheck.
type CheckResult struct {
	Name    string
	Status  CheckStatus
	Message string // what was found (e.g. "found at /usr/local/bin/dart")
	Fix     string // suggestion for fixing a failure
}

// SetupCheck is a single diagnostic check. Run evaluates it and returns a
// CheckResult. Fix (optional) performs the corrective action.
type SetupCheck struct {
	Name  string
	Run   func() CheckResult
	Fix   func() error // nil = not fixable by `install`/`init`
	FixOn string      // "install" or "init" — which command runs the fix
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
	n := 0
	for _, r := range results {
		if r.Status == StatusFail {
			n++
		}
	}
	return n
}

// CountWarnings returns the number of checks that produced a warning.
func CountWarnings(results []CheckResult) int {
	n := 0
	for _, r := range results {
		if r.Status == StatusWarn {
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

// JSONFileHasKey checks if a JSON file has a top-level key (or nested key
// via dot notation like "expo.updates.url").
func JSONFileHasKey(path, keyPath string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	// Simple check: look for the key name in the JSON string.
	// This is a heuristic — not a full JSON parse — but sufficient for
	// detecting whether a key like "updates" or "expo-updates" exists.
	parts := strings.Split(keyPath, ".")
	return strings.Contains(string(data), `"`+parts[len(parts)-1]+`"`)
}

// FindFile looks for a file by name in the current directory or one level up.
func FindFile(name string) string {
	if FileExists(name) {
		return name
	}
	parent := filepath.Join("..", name)
	if FileExists(parent) {
		return parent
	}
	return ""
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

// Failf is like Fail with formatted message and fix.
func Failf(name, format, fixFormat string, args ...interface{}) CheckResult {
	return Fail(name,
		fmt.Sprintf(format, args...),
		fmt.Sprintf(fixFormat, args...),
	)
}
