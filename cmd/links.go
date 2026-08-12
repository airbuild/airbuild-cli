package cmd

import (
	"fmt"

	"github.com/airbuild/cli/internal/api"
	"github.com/airbuild/cli/internal/ui"
	"github.com/spf13/cobra"
)

var linksAppID string
var linksBuildID string

var linksCmd = &cobra.Command{
	Use:   "links",
	Short: "Manage install links",
}

var linksListCmd = &cobra.Command{
	Use:   "list",
	Short: "List install links for an app",
	Run: func(cmd *cobra.Command, args []string) {
		if linksAppID == "" {
			ui.Error("--app-id is required. Run `airbuild apps list` to see your apps.")
			return
		}

		cfg := mustLoadConfig()
		client := api.New(cfg.APIURL, cfg.APIKey)

		resp, err := client.ListLinks(linksAppID)
		if err != nil {
			ui.Error("Failed to list links: %v", err)
			return
		}

		if len(resp.Links) == 0 {
			ui.Info("No install links found for this app.")
			return
		}

		ui.Header("Install Links (%d)", len(resp.Links))
		rows := make([][]string, 0, len(resp.Links))
		for _, l := range resp.Links {
			status := "active"
			if !l.IsActive {
				status = "revoked"
			}
			expires := "never"
			if l.ExpiresAt != nil && *l.ExpiresAt != "" {
				expires = *l.ExpiresAt
			}
			rows = append(rows, []string{
				l.Slug,
				status,
				l.Build.Version,
				l.Build.Platform,
				formatInt(l.DownloadCount),
				expires,
			})
		}
		ui.Table([]string{"Slug", "Status", "Version", "Platform", "Downloads", "Expires"}, rows)
	},
}

var linksCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new install link for a build",
	Run: func(cmd *cobra.Command, args []string) {
		if linksBuildID == "" {
			ui.Error("--build-id is required. Run `airbuild builds list --app-id xxx` to see builds.")
			return
		}

		cfg := mustLoadConfig()
		client := api.New(cfg.APIURL, cfg.APIKey)

		resp, err := client.CreateLink(linksBuildID)
		if err != nil {
			ui.Error("Failed to create link: %v", err)
			return
		}

		ui.Success("Install link created")
		fmt.Println()
		ui.Table(
			[]string{"Field", "Value"},
			[][]string{
				{"Link ID", resp.Link.ID},
				{"Slug", resp.Link.Slug},
				{"Active", fmt.Sprintf("%v", resp.Link.IsActive)},
				{"Created", resp.Link.CreatedAt},
			},
		)
		fmt.Println()
		ui.Info("Install URL: %s/i/%s", cfg.APIURL, resp.Link.Slug)
	},
}

func init() {
	linksListCmd.Flags().StringVar(&linksAppID, "app-id", "", "App ID (required)")
	linksCreateCmd.Flags().StringVar(&linksBuildID, "build-id", "", "Build ID (required)")
	linksCmd.AddCommand(linksListCmd)
	linksCmd.AddCommand(linksCreateCmd)
	rootCmd.AddCommand(linksCmd)
}
