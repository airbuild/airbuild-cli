package cmd

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	_ "embed"
	"encoding/pem"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/airbuild/airbuild-cli/internal/download"
	"github.com/airbuild/airbuild-cli/internal/ui"
	"github.com/spf13/cobra"
)

// GitHubRepo is the GitHub repository for release checks.
const GitHubRepo = "airbuild/airbuild-cli"

var (
	upgradeCheck   bool
	upgradeYes     bool
	upgradeVersion string
)

var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Upgrade AirBuild CLI to the latest (or a specific) version",
	Long: `Check for a newer version of the AirBuild CLI and upgrade if available.

The command checks the release on GitHub, compares it with the current
version, downloads the binary for your platform, verifies its SHA-256 against
the release's checksums.txt, and replaces the binary in place. A release
without a matching checksum is refused.

Examples:
  airbuild upgrade                    # Upgrade to the latest version
  airbuild upgrade --check            # Only check if an update is available
  airbuild upgrade --version v1.2.0   # Install a specific version (also downgrades)`,
	RunE: func(cmd *cobra.Command, args []string) error {
		assumeYes = upgradeYes
		return runUpgrade()
	},
}

func init() {
	upgradeCmd.Flags().BoolVar(&upgradeCheck, "check", false, "Only check if an update is available (don't upgrade)")
	upgradeCmd.Flags().BoolVar(&upgradeYes, "yes", false, "Non-interactive: answer yes to prompts")
	upgradeCmd.Flags().StringVar(&upgradeVersion, "version", "", "Install this version (e.g. v1.2.0) instead of the latest")
	rootCmd.AddCommand(upgradeCmd)
}

// githubAsset is a release asset.
type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

// githubRelease represents the relevant fields from GitHub's release API.
type githubRelease struct {
	TagName string        `json:"tag_name"`
	Name    string        `json:"name"`
	Assets  []githubAsset `json:"assets"`
}

// githubAPI is the API base, a variable so tests can point it at a stub.
var githubAPI = "https://api.github.com"

// executablePath locates the running binary; tests replace it so they never
// touch the real executable.
var executablePath = os.Executable

// checksumsAsset is the release asset listing SHA-256 sums for every binary.
// checksumSigAsset is the RSA-SHA256 signature over checksums.txt, produced by
// the release workflow and verified against the release public key embedded in
// this binary. Signature (authenticity) + checksum (integrity) means a tampered
// release fails even if every asset on it was replaced.
const checksumsAsset = "checksums.txt"
const checksumSigAsset = "checksums.txt.sig"

// releasePubPEM is the AirBuild release-signing public key, baked in at build
// time. The matching private key lives only in the GitHub release workflow's
// secrets — never in this repository.
//
//go:embed release-sign-pub.pem
var releasePubPEM []byte

// releasePublicKey parses the embedded RSA public key. A var so tests can
// substitute a throwaway keypair.
var releasePublicKey = func() (*rsa.PublicKey, error) {
	block, _ := pem.Decode(releasePubPEM)
	if block == nil {
		return nil, fmt.Errorf("embedded release public key is not valid PEM")
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("could not parse release public key: %w", err)
	}
	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("release public key is not RSA")
	}
	return rsaPub, nil
}

// verifyReleaseSignature checks an RSA-PKCS1v15 SHA-256 signature over data.
func verifyReleaseSignature(data, sig []byte) error {
	pub, err := releasePublicKey()
	if err != nil {
		return err
	}
	digest := sha256.Sum256(data)
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], sig); err != nil {
		return fmt.Errorf("signature verification failed — the release was not signed by AirBuild")
	}
	return nil
}

// runUpgrade checks for a new version and performs the upgrade.
func runUpgrade() error {
	currentVersion := cliVersion
	if currentVersion == "dev" {
		ui.Warn("You're running a development build (version unknown).")
		if !upgradeCheck && upgradeVersion == "" {
			if !confirm("Upgrade to the latest release anyway?", true) {
				ui.Info("Aborted.")
				return nil
			}
		}
	}

	ui.Info("Checking for updates...")
	release, err := fetchRelease(upgradeVersion)
	if err != nil {
		ui.Muted("You can manually download from https://github.com/%s/releases", GitHubRepo)
		return failf("Could not check for updates: %v", err)
	}

	// Parse version from tag (cli-v1.1.0 → v1.1.0)
	targetVersion := strings.TrimPrefix(release.TagName, "cli-")
	ui.Info("Current version: %s", currentVersion)
	ui.Info("Target version:  %s", targetVersion)

	if currentVersion == targetVersion {
		ui.Success("You're already on %s.", targetVersion)
		return nil
	}
	// Pinning a version always installs it (including downgrades); the
	// "latest" path never goes backwards.
	if upgradeVersion == "" && currentVersion != "dev" && !isNewerVersion(currentVersion, targetVersion) {
		ui.Info("Current version (%s) is newer than latest release (%s).", currentVersion, targetVersion)
		return nil
	}
	if upgradeCheck {
		ui.Info("Update available! Run `airbuild upgrade` to install it.")
		return nil
	}

	assetName := buildAssetName()
	asset := findAsset(release, assetName)
	if asset == nil {
		ui.Muted("Available assets:")
		for _, a := range release.Assets {
			ui.Muted("  %s", a.Name)
		}
		return failf("No binary found for %s in release %s", assetName, targetVersion)
	}
	sums := findAsset(release, checksumsAsset)
	if sums == nil {
		return failf("Release %s has no %s — refusing to install an unverified binary. "+
			"Download it manually from https://github.com/%s/releases", targetVersion, checksumsAsset, GitHubRepo)
	}
	sig := findAsset(release, checksumSigAsset)
	if sig == nil {
		return failf("Release %s has no %s — refusing to install an unsigned release. "+
			"Download it manually from https://github.com/%s/releases", targetVersion, checksumSigAsset, GitHubRepo)
	}

	binaryPath, err := executablePath()
	if err != nil {
		return failf("Could not determine current binary path: %v", err)
	}
	binaryPath, _ = filepath.EvalSymlinks(binaryPath)
	dir := filepath.Dir(binaryPath)
	if err := checkWritable(dir); err != nil {
		return failf("%s is not writable (%v).\n  Re-run with elevated permissions (e.g. `sudo airbuild upgrade`), "+
			"or reinstall into a user directory: AIRBUILD_INSTALL_DIR=$HOME/.local/bin (see install.sh).", dir, err)
	}

	ui.Info("Downloading %s (%s)...", asset.Name, formatFileSize(asset.Size))
	raw, err := download.GetBytes(sums.BrowserDownloadURL, 1<<20)
	if err != nil {
		return failf("Could not fetch %s: %v", checksumsAsset, err)
	}
	sigBytes, err := download.GetBytes(sig.BrowserDownloadURL, 1<<20)
	if err != nil {
		return failf("Could not fetch %s: %v", checksumSigAsset, err)
	}
	if err := verifyReleaseSignature(raw, sigBytes); err != nil {
		return failf("%v", err)
	}
	ui.Muted("Release signature verified")
	checksums := download.ParseChecksums(string(raw))

	tmpPath := binaryPath + ".new"
	res, err := download.ToFile(asset.BrowserDownloadURL, tmpPath, runtime.GOOS != "windows")
	if err != nil {
		os.Remove(tmpPath) //nolint:errcheck
		return failf("Download failed: %v", err)
	}
	if err := download.VerifySHA256(checksums, asset.Name, res.SHA256); err != nil {
		os.Remove(tmpPath) //nolint:errcheck
		return failf("Integrity check failed: %v", err)
	}
	ui.Muted("SHA-256 verified: %s", res.SHA256)

	// Swap the binary. On Windows a running exe can be renamed but not
	// deleted, so the old file may be left behind as .old (removed next time).
	backupPath := binaryPath + ".old"
	os.Remove(backupPath) //nolint:errcheck
	if err := os.Rename(binaryPath, backupPath); err != nil {
		os.Remove(tmpPath) //nolint:errcheck
		return failf("Could not back up current binary: %v", err)
	}
	if err := os.Rename(tmpPath, binaryPath); err != nil {
		os.Rename(backupPath, binaryPath) //nolint:errcheck
		os.Remove(tmpPath)                //nolint:errcheck
		return failf("Could not install new binary: %v", err)
	}
	os.Remove(backupPath) //nolint:errcheck

	ui.Success("Upgraded to %s", targetVersion)
	ui.Info("Run `airbuild version` to verify.")

	// Refresh AirBuild-managed agent files in the current project using the NEW
	// binary (it carries the new content). Best effort; never creates files.
	syncCmd := exec.Command(binaryPath, "agents", "sync", "--quiet")
	syncCmd.Stdout, syncCmd.Stderr = os.Stderr, os.Stderr
	_ = syncCmd.Run()
	return nil
}

// checkWritable verifies we can create files in dir (needed for the atomic
// rename) and returns a short reason when we can't.
func checkWritable(dir string) error {
	f, err := os.CreateTemp(dir, ".airbuild-write-test-*")
	if err != nil {
		var pe *fs.PathError
		if errors.As(err, &pe) {
			return pe.Err
		}
		return err
	}
	name := f.Name()
	f.Close()       //nolint:errcheck
	os.Remove(name) //nolint:errcheck
	return nil
}

// fetchRelease queries the GitHub API for the latest release, or for the
// given version (v1.2.0 or cli-v1.2.0) when pinned.
func fetchRelease(version string) (*githubRelease, error) {
	url := fmt.Sprintf("%s/repos/%s/releases/latest", githubAPI, GitHubRepo)
	if version != "" {
		tag := version
		if !strings.HasPrefix(tag, "cli-") {
			if !strings.HasPrefix(tag, "v") {
				tag = "v" + tag
			}
			tag = "cli-" + tag
		}
		url = fmt.Sprintf("%s/repos/%s/releases/tags/%s", githubAPI, GitHubRepo, tag)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", userAgent())

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode == http.StatusNotFound && version != "" {
		return nil, fmt.Errorf("version %s not found", version)
	}
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
func findAsset(release *githubRelease, name string) *githubAsset {
	for i := range release.Assets {
		if release.Assets[i].Name == name {
			return &release.Assets[i]
		}
	}
	return nil
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
