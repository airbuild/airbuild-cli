package cmd

import (
	"github.com/airbuild/cli/internal/api"
	"github.com/airbuild/cli/internal/ui"
	"github.com/spf13/cobra"
)

var appsCmd = &cobra.Command{
	Use:   "apps",
	Short: "Manage apps",
}

var appsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all apps in your organization",
	Run: func(cmd *cobra.Command, args []string) {
		cfg := mustLoadConfig()
		client := api.New(cfg.APIURL, cfg.APIKey)

		resp, err := client.ListApps()
		if err != nil {
			ui.Error("Failed to list apps: %v", err)
			return
		}

		if len(resp.Apps) == 0 {
			ui.Info("No apps found in your organization.")
			return
		}

		ui.Header("Apps (%d)", len(resp.Apps))
		rows := make([][]string, 0, len(resp.Apps))
		for _, app := range resp.Apps {
			platforms := ""
			for i, p := range app.Platforms {
				if i > 0 {
					platforms += ", "
				}
				platforms += p
			}
			rows = append(rows, []string{
				app.ID,
				app.Name,
				platforms,
				app.BundleID,
				formatCount(app.Count.Builds),
			})
		}
		ui.Table([]string{"ID", "Name", "Platforms", "Bundle ID", "Builds"}, rows)
	},
}

func init() {
	appsCmd.AddCommand(appsListCmd)
	rootCmd.AddCommand(appsCmd)
}

// formatCount returns "0" or "N" for a build count.
func formatCount(n int) string {
	if n == 0 {
		return "0"
	}
	return formatInt(n)
}

// formatInt converts an int to string without importing strconv everywhere.
func formatInt(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}
