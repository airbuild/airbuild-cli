package cmd

import (
	"errors"
	"fmt"
	"os"
	"runtime"

	"github.com/airbuild/airbuild-cli/internal/api"

	"github.com/airbuild/airbuild-cli/internal/config"
	"github.com/airbuild/airbuild-cli/internal/ui"
	"github.com/spf13/cobra"
)

// cliVersion holds the current CLI version, set by main.go from build-time ldflags.
var cliVersion = "dev"

// SetVersion is called by main() to inject the build-time version.
func SetVersion(v string) {
	cliVersion = v
	rootCmd.Version = v
	api.UserAgent = fmt.Sprintf("airbuild-cli/%s (%s/%s)", v, runtime.GOOS, runtime.GOARCH)
}

// userAgent is the User-Agent the CLI sends on every outbound request.
func userAgent() string { return api.UserAgent }

// rootCmd is the entry point for the AirBuild CLI.
// jsonOutput is bound to the global --json flag. When set, human output is
// routed to stderr and stdout carries only machine-readable JSON — the
// mode agents and CI pipelines should always use.
var jsonOutput bool

var rootCmd = &cobra.Command{
	Use:   "airbuild",
	Short: "AirBuild CLI — upload builds and manage apps from the terminal",
	Long: `AirBuild CLI is a command-line tool for uploading iOS/Android builds
to AirBuild and managing your apps, builds, and install links.

Quick start:
  airbuild login --api-key airbuild_xxx   # or: export AIRBUILD_API_KEY=airbuild_xxx
  airbuild init                    # Set up project config (.airbuild.json)
  airbuild push                    # Upload build using project config

Or upload a file directly:
  airbuild upload ./app-release.apk --app-id app_xxx

Scripting / AI agents: pass --json to any command for machine-readable
stdout (progress and errors go to stderr).`,
	SilenceUsage:  true,
	SilenceErrors: true,
	Version:       cliVersion,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		if jsonOutput {
			ui.SetJSONMode(true)
		}
	},
}

func init() {
	rootCmd.PersistentFlags().BoolVar(&jsonOutput, "json", false,
		"Output machine-readable JSON on stdout (logs/errors go to stderr)")
}

// exitError makes the process exit with a specific code without printing
// anything further (the command already reported the problem).
type exitError struct{ code int }

func (e exitError) Error() string { return fmt.Sprintf("exit status %d", e.code) }

// errSilent is returned by commands that already printed their error.
var errSilent error = exitError{code: 1}

// Execute runs the root command and exits once, here, so deferred cleanup in
// commands always runs and commands stay testable.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		var ee exitError
		if errors.As(err, &ee) {
			os.Exit(ee.code)
		}
		ui.Error("%v", err)
		os.Exit(1)
	}
}

// loadConfig loads the config and returns an error if not logged in.
func loadConfig() (*config.Config, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, failf("Error loading config: %v", err)
	}
	if !cfg.IsLoggedIn() {
		return nil, failf("Not logged in. Run `airbuild login --api-key airbuild_xxx` first, or set AIRBUILD_API_KEY.")
	}
	return cfg, nil
}

// failf prints an error to stderr and returns errSilent. Commands return it
// (`return failf(...)`) so scripts and agents see a non-zero exit code.
func failf(format string, args ...interface{}) error {
	ui.Error(format, args...)
	return errSilent
}
