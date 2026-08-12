package cmd

import (
	"fmt"

	"github.com/airbuild/cli/internal/config"
	"github.com/airbuild/cli/internal/ui"
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
	Run: func(cmd *cobra.Command, args []string) {
		cfg, err := config.Load()
		if err != nil {
			ui.Error("Could not load config: %v", err)
			return
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
		ui.Table(
			[]string{"Key", "Value"},
			[][]string{
				{"API URL", cfg.APIURL},
				{"API Key", apiKeyDisplay},
				{"Org ID", cfg.OrgID},
				{"Org Name", cfg.OrgName},
				{"Default App ID", cfg.AppID},
			},
		)
	},
}

var configSetCmd = &cobra.Command{
	Use:   "set",
	Short: "Set configuration values",
	Long: `Set configuration values directly without using login.

Examples:
  airbuild config set --api-key airbuild_xxx
  airbuild config set --api-url https://staging.airbuild.dev`,
	Run: func(cmd *cobra.Command, args []string) {
		cfg, err := config.Load()
		if err != nil {
			ui.Error("Could not load config: %v", err)
			return
		}

		changed := false
		if configSetAPIKey != "" {
			cfg.APIKey = configSetAPIKey
			changed = true
		}
		if configSetAPIURL != "" {
			cfg.APIURL = configSetAPIURL
			changed = true
		}

		if !changed {
			ui.Error("No values to set. Use --api-key or --api-url.")
			return
		}

		if err := cfg.Save(); err != nil {
			ui.Error("Could not save config: %v", err)
			return
		}

		ui.Success("Configuration updated")
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
	_ = fmt.Sprintf // avoid unused import if fmt is not used elsewhere
}
