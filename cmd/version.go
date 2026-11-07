package cmd

import (
	"fmt"
	"runtime"

	"github.com/airbuild/airbuild-cli/internal/ui"
	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the AirBuild CLI version",
	RunE: func(cmd *cobra.Command, args []string) error {
		if jsonOutput {
			ui.PrintJSON(map[string]string{
				"version": cliVersion,
				"os":      runtime.GOOS,
				"arch":    runtime.GOARCH,
			})
			return nil
		}
		fmt.Printf("AirBuild CLI %s\n", cliVersion)
		ui.Muted("  %s/%s", runtime.GOOS, runtime.GOARCH)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
