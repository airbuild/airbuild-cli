package cmd

import (
	"fmt"
	"os"

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
}

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
	SilenceUsage: true,
	Version:      cliVersion,
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

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// mustLoadConfig loads the config and exits with an error if not logged in.
func mustLoadConfig() *config.Config {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}
	if !cfg.IsLoggedIn() {
		fmt.Fprintln(os.Stderr, "Not logged in. Run `airbuild login --api-key airbuild_xxx` first, or set AIRBUILD_API_KEY.")
		os.Exit(1)
	}
	return cfg
}

// fail prints an error to stderr and exits non-zero. Commands use this
// instead of `ui.Error(...) + return` so scripts and agents can detect
// failure from the exit code.
func fail(format string, args ...interface{}) {
	ui.Error(format, args...)
	os.Exit(1)
}
