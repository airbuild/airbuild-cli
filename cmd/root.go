package cmd

import (
	"fmt"
	"os"

	"github.com/airbuild/cli/internal/config"
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
var rootCmd = &cobra.Command{
	Use:   "airbuild",
	Short: "AirBuild CLI — upload builds and manage apps from the terminal",
	Long: `AirBuild CLI is a command-line tool for uploading iOS/Android builds
to AirBuild and managing your apps, builds, and install links.

Quick start:
  airbuild login --api-key airbuild_xxx
  airbuild init                    # Set up project config (.airbuild.json)
  airbuild push                    # Upload build using project config

Or upload a file directly:
  airbuild upload ./app-release.apk --app-id app_xxx`,
	SilenceUsage: true,
	Version:      cliVersion,
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
		fmt.Fprintln(os.Stderr, "Not logged in. Run `airbuild login --api-key airbuild_xxx` first.")
		os.Exit(1)
	}
	return cfg
}
