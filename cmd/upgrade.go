package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/airbuild/cli/internal/ui"
	"github.com/spf13/cobra"
)

// GitHubRepo is the GitHub repository for release checks.
const GitHubRepo = "airbuild/airbuild-cli"

var upgradeCheck bool

var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Upgrade AirBuild CLI to the latest version",
	Long: `Check for a newer version of the AirBuild CLI and upgrade if available.

The command checks the latest release on GitHub, compares it with the
current version, and downloads + replaces the binary in place.

Examples:
  airbuild upgrade           # Upgrade to latest version
  airbuild upgrade --check   # Only check if an update is available`,
	Run: func(cmd *cobra.Command, args []string) {
		runUpgrade()
	},
}

func init() {
	upgradeCmd.Flags().BoolVar(&upgradeCheck, "check", false, "Only check if an update is available (don't upgrade)")
	rootCmd.AddCommand(upgradeCmd)
}

// githubRelease represents the relevant fields from GitHub's release API.
type githubRelease struct {
	TagName string `json:"tag_name"`
	Name    string `json:"name"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
		Size               int64  `json:"size"`
	} `json:"assets"`
}

// runUpgrade checks for a new version and performs the upgrade.
func runUpgrade() {
	currentVersion := cliVersion
	if currentVersion == "dev" {
		ui.Warn("You're running a development build (version unknown).")
		if !upgradeCheck {
			if !confirm("Upgrade to the latest release anyway?") {
				ui.Info("Aborted.")
				return
			}
		}
	}

	// Fetch latest release from GitHub API
	ui.Info("Checking for updates...")
	latest, err := fetchLatestRelease()
	if err != nil {
		ui.Error("Could not check for updates: %v", err)
		ui.Muted("You can manually download from https://github.com/%s/releases", GitHubRepo)
		return
	}

	// Parse version from tag (cli-v1.1.0 → v1.1.0)
	latestVersion := strings.TrimPrefix(latest.TagName, "cli-")

	ui.Info("Current version: %s", currentVersion)
	ui.Info("Latest version:  %s", latestVersion)

	if currentVersion == latestVersion {
		ui.Success("You're already on the latest version.")
		return
	}

	if currentVersion != "dev" && !isNewerVersion(currentVersion, latestVersion) {
		ui.Info("Current version (%s) is newer than latest release (%s).", currentVersion, latestVersion)
		return
	}

	if upgradeCheck {
		ui.Info("Update available! Run `airbuild upgrade` to install it.")
		return
	}

	// Find the matching asset for this platform
	assetName := buildAssetName()
	asset := findAsset(latest, assetName)
	if asset == nil {
		ui.Error("No binary found for %s in release %s", assetName, latestVersion)
		ui.Muted("Available assets:")
		for _, a := range latest.Assets {
			ui.Muted("  %s", a.Name)
		}
		return
	}

	// Find the current binary path
	binaryPath, err := os.Executable()
	if err != nil {
		ui.Error("Could not determine current binary path: %v", err)
		return
	}
	binaryPath, _ = filepath.EvalSymlinks(binaryPath)

	ui.Info("Downloading %s (%s)...", asset.Name, formatFileSize(asset.Size))

	// Download to a temp file
	tmpPath := binaryPath + ".new"
	if err := downloadFile(asset.BrowserDownloadURL, tmpPath); err != nil {
		ui.Error("Download failed: %v", err)
		os.Remove(tmpPath)
		return
	}

	// Make it executable (Unix only)
	if runtime.GOOS != "windows" {
		if err := os.Chmod(tmpPath, 0755); err != nil {
			ui.Error("Could not make binary executable: %v", err)
			os.Remove(tmpPath)
			return
		}
	}

	// Backup the old binary
	backupPath := binaryPath + ".old"
	os.Remove(backupPath)

	// On Windows, we can't rename a running binary directly.
	// Rename the current binary to .old first, then move the new one in.
	if err := os.Rename(binaryPath, backupPath); err != nil {
		ui.Error("Could not backup current binary: %v", err)
		os.Remove(tmpPath)
		return
	}

	// Move new binary into place
	if err := os.Rename(tmpPath, binaryPath); err != nil {
		// Restore the old binary
		os.Rename(backupPath, binaryPath)
		ui.Error("Could not install new binary: %v", err)
		os.Remove(tmpPath)
		return
	}

	// Clean up backup (best effort)
	os.Remove(backupPath)

	ui.Success("Upgraded to %s", latestVersion)
	ui.Info("Run `airbuild version` to verify.")
}

// fetchLatestRelease queries the GitHub API for the latest release.
func fetchLatestRelease() (*githubRelease, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", GitHubRepo)

	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned HTTP %d", resp.StatusCode)
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("could not parse release response: %w", err)
	}

	return &release, nil
}

// buildAssetName returns the expected binary asset name for the current platform.
func buildAssetName() string {
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	return fmt.Sprintf("airbuild-%s-%s%s", runtime.GOOS, runtime.GOARCH, ext)
}

// findAsset finds a release asset matching the given name.
func findAsset(release *githubRelease, name string) *struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
} {
	for i := range release.Assets {
		if release.Assets[i].Name == name {
			return &release.Assets[i]
		}
	}
	return nil
}

// downloadFile downloads a URL to a local file path.
func downloadFile(url, destPath string) error {
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	out, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

// isNewerVersion compares two version strings (e.g. "v1.0.0" vs "v1.1.0").
// Returns true if latest is newer than current.
func isNewerVersion(current, latest string) bool {
	// Strip "v" prefix and any "cli-" prefix
	c := strings.TrimPrefix(strings.TrimPrefix(current, "cli-"), "v")
	l := strings.TrimPrefix(strings.TrimPrefix(latest, "cli-"), "v")

	var cMajor, cMinor, cPatch int
	var lMajor, lMinor, lPatch int
	fmt.Sscanf(c, "%d.%d.%d", &cMajor, &cMinor, &cPatch)
	fmt.Sscanf(l, "%d.%d.%d", &lMajor, &lMinor, &lPatch)

	if lMajor != cMajor {
		return lMajor > cMajor
	}
	if lMinor != cMinor {
		return lMinor > cMinor
	}
	return lPatch > cPatch
}
