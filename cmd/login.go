package cmd

import (
	"bufio"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/airbuild/airbuild-cli/internal/api"
	"github.com/airbuild/airbuild-cli/internal/config"
	"github.com/airbuild/airbuild-cli/internal/ui"
	"github.com/spf13/cobra"
)

var (
	loginAPIKey      string
	loginAPIKeyStdin bool
	loginAPIURL      string
)

// warnInsecureURL warns when an API URL would send the key over plain HTTP to
// a non-local host.
func warnInsecureURL(raw string) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" {
		return
	}
	switch u.Hostname() {
	case "localhost", "127.0.0.1", "::1":
		return
	}
	ui.Warn("%s uses plain HTTP — your API key will be sent unencrypted. Use https:// unless this is a trusted private network.", raw)
}

// readKeyFromStdin reads the first line of r as an API key.
func readKeyFromStdin(r io.Reader) (string, error) {
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Authenticate with your AirBuild API key",
	Long: `Saves your API key to ~/.airbuild/config.json for use by other commands.

Get your API key from the AirBuild dashboard under Settings > API Keys.

The key is read from, in order: --api-key-stdin, --api-key, or the
AIRBUILD_API_KEY environment variable. Prefer --api-key-stdin or the
environment variable: a --api-key value ends up in your shell history and in
the process list.

Examples:
  echo "$AIRBUILD_API_KEY" | airbuild login --api-key-stdin
  AIRBUILD_API_KEY=airbuild_xxx airbuild login
  airbuild login --api-key airbuild_xxx   # convenient, but visible in history`,
	RunE: func(cmd *cobra.Command, args []string) error {
		key := loginAPIKey
		switch {
		case loginAPIKeyStdin:
			var err error
			if key, err = readKeyFromStdin(os.Stdin); err != nil {
				return failf("Could not read API key from stdin: %v", err)
			}
		case key != "":
			ui.Warn("--api-key is visible in shell history and process listings; prefer --api-key-stdin or AIRBUILD_API_KEY.")
		default:
			key = os.Getenv(config.EnvAPIKey)
		}
		if key == "" {
			return failf("No API key provided. Use --api-key-stdin, --api-key, or set %s.", config.EnvAPIKey)
		}

		// Use provided URL or the configured/default one.
		baseURL := loginAPIURL
		if baseURL == "" {
			cfg, _ := config.LoadFile()
			if cfg != nil {
				baseURL = cfg.APIURL
			}
			if baseURL == "" {
				baseURL = config.DefaultAPIURL
			}
		}
		warnInsecureURL(baseURL)

		// Verify the key before saving
		ui.Info("Verifying API key...")
		client := api.New(baseURL, key)
		resp, err := client.Verify()
		if err != nil {
			return failf("Authentication failed: %v", err)
		}

		cfg := &config.Config{
			APIKey:  key,
			APIURL:  baseURL,
			OrgID:   resp.Organization.ID,
			OrgName: resp.Organization.Name,
		}
		if err := cfg.Save(); err != nil {
			return failf("Could not save config: %v", err)
		}

		if jsonOutput {
			ui.PrintJSON(map[string]interface{}{
				"loggedIn":     true,
				"apiUrl":       baseURL,
				"organization": resp.Organization,
				"apiKeyId":     resp.APIKeyID,
			})
			return nil
		}

		ui.Success("Logged in to %s as organization: %s (%s)",
			baseURL, resp.Organization.Name, resp.Organization.Slug)
		return nil
	},
}

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Remove the stored API key",
	Long: `Removes the API key (and cached organization) from ~/.airbuild/config.json.
The API URL is kept. An AIRBUILD_API_KEY environment variable is not touched
and keeps working until you unset it.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// LoadFile, not Load: never persist environment overrides.
		cfg, err := config.LoadFile()
		if err != nil {
			return failf("Could not load config: %v", err)
		}
		hadKey := cfg.APIKey != ""
		cfg.APIKey, cfg.OrgID, cfg.OrgName = "", "", ""
		if err := cfg.Save(); err != nil {
			return failf("Could not save config: %v", err)
		}
		envSet := os.Getenv(config.EnvAPIKey) != ""
		if jsonOutput {
			ui.PrintJSON(map[string]interface{}{"loggedOut": true, "hadStoredKey": hadKey, "envKeyStillSet": envSet})
			return nil
		}
		if hadKey {
			ui.Success("Removed the stored API key.")
		} else {
			ui.Info("No stored API key to remove.")
		}
		if envSet {
			ui.Warn("%s is still set in your environment and will keep authenticating.", config.EnvAPIKey)
		}
		return nil
	},
}

func init() {
	loginCmd.Flags().StringVar(&loginAPIKey, "api-key", "", "AirBuild API key (airbuild_xxx) — prefer --api-key-stdin")
	loginCmd.Flags().BoolVar(&loginAPIKeyStdin, "api-key-stdin", false, "Read the API key from the first line of stdin")
	loginCmd.Flags().StringVar(&loginAPIURL, "api-url", "", "AirBuild API URL (defaults to https://airbuild.dev)")
	rootCmd.AddCommand(loginCmd, logoutCmd)
}
