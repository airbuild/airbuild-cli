package cmd

import (
	"fmt"
	"runtime"

	"github.com/airbuild/cli/internal/ui"
	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the AirBuild CLI version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("AirBuild CLI %s\n", cliVersion)
		ui.Muted("  %s/%s", runtime.GOOS, runtime.GOARCH)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
