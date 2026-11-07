package cmd

import (
	"github.com/airbuild/airbuild-cli/internal/config"
	"github.com/airbuild/airbuild-cli/internal/ui"
	"github.com/spf13/cobra"
)

var configSetAPIKey string
var configSetAPIURL string

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage CLI configuration",
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show current configuration",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return failf("Could not load config: %v", err)
		}

		ui.Header("Configuration")
		apiKeyDisplay := "not set"
		if cfg.APIKey != "" {
			// Mask the key — show only first 12 chars
			if len(cfg.APIKey) > 12 {
				apiKeyDisplay = cfg.APIKey[:12] + "..."
			} else {
				apiKeyDisplay = cfg.APIKey
			}
		}
		if jsonOutput {
			ui.PrintJSON(map[string]string{
				"apiUrl":  cfg.APIURL,
				"apiKey":  apiKeyDisplay,
				"orgId":   cfg.OrgID,
				"orgName": cfg.OrgName,
			})
			return nil
		}
		ui.Table(
			[]string{"Key", "Value"},
			[][]string{
				{"API URL", cfg.APIURL},
				{"API Key", apiKeyDisplay},
				{"Org ID", cfg.OrgID},
				{"Org Name", cfg.OrgName},
			},
		)
		return nil
	},
}

var configSetCmd = &cobra.Command{
	Use:   "set",
	Short: "Set configuration values",
	Long: `Set configuration values directly without using login.

Examples:
  airbuild config set --api-key airbuild_xxx
  airbuild config set --api-url https://staging.airbuild.dev`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// LoadFile, not Load — env overrides must never be persisted to
		// the config file (AIRBUILD_API_KEY would otherwise be written
		// to disk when the user only intended to change the URL).
		cfg, err := config.LoadFile()
		if err != nil {
			return failf("Could not load config: %v", err)
		}

		changed := false
		if configSetAPIKey != "" {
			cfg.APIKey = configSetAPIKey
			changed = true
		}
		if configSetAPIURL != "" {
			warnInsecureURL(configSetAPIURL)
			cfg.APIURL = configSetAPIURL
			changed = true
		}

		if !changed {
			return failf("No values to set. Use --api-key or --api-url.")
		}

		if err := cfg.Save(); err != nil {
			return failf("Could not save config: %v", err)
		}

		ui.Success("Configuration updated")
		return nil
	},
}

func init() {
	configSetCmd.Flags().StringVar(&configSetAPIKey, "api-key", "", "Set the API key")
	configSetCmd.Flags().StringVar(&configSetAPIURL, "api-url", "", "Set the API base URL")
	configCmd.AddCommand(configShowCmd)
	configCmd.AddCommand(configSetCmd)
	rootCmd.AddCommand(configCmd)

	// Suppress the default completion command
	rootCmd.CompletionOptions.DisableDefaultCmd = true
}
