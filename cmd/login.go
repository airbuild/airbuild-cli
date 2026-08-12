package cmd

import (
	"github.com/airbuild/cli/internal/api"
	"github.com/airbuild/cli/internal/config"
	"github.com/airbuild/cli/internal/ui"
	"github.com/spf13/cobra"
)

var loginAPIKey string
var loginAPIURL string

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Authenticate with your AirBuild API key",
	Long: `Saves your API key to ~/.airbuild/config.json for use by other commands.

Get your API key from the AirBuild dashboard under Settings > API Keys.`,
	Run: func(cmd *cobra.Command, args []string) {
		if loginAPIKey == "" {
			ui.Error("--api-key is required")
			cmd.Help()
			return
		}

		// Use provided URL or default
		baseURL := loginAPIURL
		if baseURL == "" {
			cfg, _ := config.Load()
			baseURL = cfg.APIURL
		}

		// Verify the key before saving
		ui.Info("Verifying API key...")
		client := api.New(baseURL, loginAPIKey)
		resp, err := client.Verify()
		if err != nil {
			ui.Error("Authentication failed: %v", err)
			return
		}

		// Save config
		cfg := &config.Config{
			APIKey:  loginAPIKey,
			APIURL:  baseURL,
			OrgID:   resp.Organization.ID,
			OrgName: resp.Organization.Name,
		}
		if err := cfg.Save(); err != nil {
			ui.Error("Could not save config: %v", err)
			return
		}

		ui.Success("Logged in to %s as organization: %s (%s)",
			baseURL, resp.Organization.Name, resp.Organization.Slug)
	},
}

func init() {
	loginCmd.Flags().StringVar(&loginAPIKey, "api-key", "", "AirBuild API key (airbuild_xxx)")
	loginCmd.Flags().StringVar(&loginAPIURL, "api-url", "", "AirBuild API URL (defaults to https://airbuild.dev)")
	rootCmd.AddCommand(loginCmd)
}
