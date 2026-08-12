package cmd

import (
	"github.com/airbuild/cli/internal/api"
	"github.com/airbuild/cli/internal/ui"
	"github.com/spf13/cobra"
)

var buildsAppID string

var buildsCmd = &cobra.Command{
	Use:   "builds",
	Short: "Manage builds",
}

var buildsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List builds for an app",
	Run: func(cmd *cobra.Command, args []string) {
		if buildsAppID == "" {
			ui.Error("--app-id is required. Run `airbuild apps list` to see your apps.")
			return
		}

		cfg := mustLoadConfig()
		client := api.New(cfg.APIURL, cfg.APIKey)

		resp, err := client.ListBuilds(buildsAppID)
		if err != nil {
			ui.Error("Failed to list builds: %v", err)
			return
		}

		if len(resp.Builds) == 0 {
			ui.Info("No builds found for this app.")
			return
		}

		ui.Header("Builds (%d)", len(resp.Builds))
		rows := make([][]string, 0, len(resp.Builds))
		for _, b := range resp.Builds {
			statusColored := b.Status
			switch b.Status {
			case "READY":
				statusColored = "\033[32mREADY\033[0m"
			case "PROCESSING":
				statusColored = "\033[33mPROCESSING\033[0m"
			case "FAILED":
				statusColored = "\033[31mFAILED\033[0m"
			}

			links := ""
			if len(b.InstallLinks) > 0 {
				for i, l := range b.InstallLinks {
					if i > 0 {
						links += ", "
					}
					if l.IsActive {
						links += l.Slug
					} else {
						links += l.Slug + " (revoked)"
					}
				}
			}

			rows = append(rows, []string{
				b.ID,
				b.Version,
				b.Platform,
				statusColored,
				b.FileName,
				links,
			})
		}
		ui.Table([]string{"ID", "Version", "Platform", "Status", "File", "Install Links"}, rows)
	},
}

func init() {
	buildsListCmd.Flags().StringVar(&buildsAppID, "app-id", "", "App ID (required)")
	buildsCmd.AddCommand(buildsListCmd)
	rootCmd.AddCommand(buildsCmd)
}
