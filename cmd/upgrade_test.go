package cmd

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const newBinary = "#!/bin/sh\nexit 0\n"

type upgradeFixture struct {
	dir      string
	exe      string
	requests []string
}

// setupUpgrade stands up a fake GitHub (release JSON + assets) and points the
// upgrade command at a throwaway "installed" binary. sums customizes
// checksums.txt: nil serves a correct one, otherwise the returned string is
// served; omitSums drops the asset from the release entirely.
func setupUpgrade(t *testing.T, sums func(assetName, sha string) string, omitSums bool) *upgradeFixture {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fixture binary")
	}
	fx := &upgradeFixture{dir: t.TempDir()}
	fx.exe = filepath.Join(fx.dir, "airbuild")
	if err := os.WriteFile(fx.exe, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}

	sum := sha256.Sum256([]byte(newBinary))
	sha := hex.EncodeToString(sum[:])
	asset := buildAssetName()

	// Throwaway signing keypair for the fixture — the real key never enters tests.
	testKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	sumsBody := func() []byte {
		body := sha + "  " + asset + "\n"
		if sums != nil {
			body = sums(asset, sha)
		}
		return []byte(body)
	}
	sigFor := func(data []byte) []byte {
		digest := sha256.Sum256(data)
		sig, err := rsa.SignPKCS1v15(rand.Reader, testKey, crypto.SHA256, digest[:])
		if err != nil {
			t.Fatal(err)
		}
		return sig
	}

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fx.requests = append(fx.requests, r.URL.Path)
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"), strings.Contains(r.URL.Path, "/releases/tags/"):
			tag := "cli-v2.0.0"
			if i := strings.Index(r.URL.Path, "/releases/tags/"); i >= 0 {
				tag = r.URL.Path[i+len("/releases/tags/"):]
			}
			assets := fmt.Sprintf(`{"name":%q,"browser_download_url":"%s/bin","size":%d}`, asset, srv.URL, len(newBinary))
			if !omitSums {
				assets += fmt.Sprintf(`,{"name":"checksums.txt","browser_download_url":"%s/sums","size":10}`+
					`,{"name":"checksums.txt.sig","browser_download_url":"%s/sig","size":10}`, srv.URL, srv.URL)
			}
			fmt.Fprintf(w, `{"tag_name":%q,"assets":[%s]}`, tag, assets)
		case r.URL.Path == "/bin":
			w.Write([]byte(newBinary)) //nolint:errcheck
		case r.URL.Path == "/sums":
			w.Write(sumsBody()) //nolint:errcheck
		case r.URL.Path == "/sig":
			w.Write(sigFor(sumsBody())) //nolint:errcheck
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	oldAPI, oldExe, oldVer := githubAPI, executablePath, cliVersion
	oldCheck, oldYes, oldPin := upgradeCheck, upgradeYes, upgradeVersion
	oldPub := releasePublicKey
	releasePublicKey = func() (*rsa.PublicKey, error) { return &testKey.PublicKey, nil }
	githubAPI = srv.URL
	executablePath = func() (string, error) { return fx.exe, nil }
	cliVersion = "v1.0.0"
	upgradeCheck, upgradeYes, upgradeVersion = false, true, ""
	t.Cleanup(func() {
		githubAPI, executablePath, cliVersion = oldAPI, oldExe, oldVer
		upgradeCheck, upgradeYes, upgradeVersion = oldCheck, oldYes, oldPin
		releasePublicKey = oldPub
	})
	return fx
}

func installed(t *testing.T, fx *upgradeFixture) string {
	t.Helper()
	b, err := os.ReadFile(fx.exe)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestUpgradeInstallsVerifiedBinary(t *testing.T) {
	fx := setupUpgrade(t, nil, false)
	if err := runUpgrade(); err != nil {
		t.Fatalf("runUpgrade: %v", err)
	}
	if got := installed(t, fx); got != newBinary {
		t.Errorf("binary not replaced, content = %q", got)
	}
	if _, err := os.Stat(fx.exe + ".new"); err == nil {
		t.Error(".new temp file left behind")
	}
	if _, err := os.Stat(fx.exe + ".part"); err == nil {
		t.Error(".part temp file left behind")
	}
}

func TestUpgradeRefusesChecksumMismatchAndKeepsOldBinary(t *testing.T) {
	fx := setupUpgrade(t, func(asset, _ string) string {
		return strings.Repeat("0", 64) + "  " + asset + "\n"
	}, false)
	err := runUpgrade()
	if !errors.Is(err, errSilent) {
		t.Fatalf("expected a refusal, got %v", err)
	}
	if got := installed(t, fx); got != "OLD" {
		t.Errorf("binary was replaced despite a bad checksum: %q", got)
	}
	if _, err := os.Stat(fx.exe + ".new"); err == nil {
		t.Error("unverified download left behind")
	}
}

func TestUpgradeRefusesWhenAssetNotListedInChecksums(t *testing.T) {
	fx := setupUpgrade(t, func(_, sha string) string { return sha + "  some-other-file\n" }, false)
	if err := runUpgrade(); !errors.Is(err, errSilent) {
		t.Fatalf("expected a refusal, got %v", err)
	}
	if installed(t, fx) != "OLD" {
		t.Error("binary replaced without a listed checksum")
	}
}

func TestUpgradeRefusesReleaseWithoutChecksumsFile(t *testing.T) {
	fx := setupUpgrade(t, nil, true)
	if err := runUpgrade(); !errors.Is(err, errSilent) {
		t.Fatalf("expected a refusal, got %v", err)
	}
	if installed(t, fx) != "OLD" {
		t.Error("binary replaced from a release with no checksums.txt")
	}
}

func TestUpgradeCheckDoesNotDownload(t *testing.T) {
	fx := setupUpgrade(t, nil, false)
	upgradeCheck = true
	if err := runUpgrade(); err != nil {
		t.Fatal(err)
	}
	for _, p := range fx.requests {
		if p == "/bin" || p == "/sums" {
			t.Errorf("--check downloaded %s", p)
		}
	}
	if installed(t, fx) != "OLD" {
		t.Error("--check modified the binary")
	}
}

func TestUpgradeNeverGoesBackwardsOnLatestButPinningMay(t *testing.T) {
	fx := setupUpgrade(t, nil, false)
	cliVersion = "v3.0.0" // newer than the fake "latest" v2.0.0
	if err := runUpgrade(); err != nil {
		t.Fatal(err)
	}
	if installed(t, fx) != "OLD" {
		t.Error("latest-channel upgrade downgraded the binary")
	}

	upgradeVersion = "2.0.0" // explicit pin: installs even though it is older
	if err := runUpgrade(); err != nil {
		t.Fatal(err)
	}
	if installed(t, fx) != newBinary {
		t.Error("pinned version was not installed")
	}
	found := false
	for _, p := range fx.requests {
		if strings.HasSuffix(p, "/releases/tags/cli-v2.0.0") {
			found = true
		}
	}
	if !found {
		t.Errorf("pin did not request the cli-v2.0.0 tag: %v", fx.requests)
	}
}

func TestUpgradeExplainsUnwritableInstallDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write anywhere")
	}
	fx := setupUpgrade(t, nil, false)
	if err := os.Chmod(fx.dir, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(fx.dir, 0o755) //nolint:errcheck
	if err := runUpgrade(); !errors.Is(err, errSilent) {
		t.Fatalf("expected a failure, got %v", err)
	}
	if installed(t, fx) != "OLD" {
		t.Error("binary changed in a read-only directory")
	}
}

func TestIsNewerVersion(t *testing.T) {
	cases := []struct {
		cur, latest string
		want        bool
	}{
		{"v1.0.0", "v1.0.1", true}, {"v1.9.0", "v2.0.0", true}, {"v1.2.0", "cli-v1.3.0", true},
		{"v1.0.0", "v1.0.0", false}, {"v2.0.0", "v1.9.9", false}, {"v1.0.0-3-gabc-dirty", "v1.0.1", true},
	}
	for _, c := range cases {
		if got := isNewerVersion(c.cur, c.latest); got != c.want {
			t.Errorf("isNewerVersion(%q,%q) = %v, want %v", c.cur, c.latest, got, c.want)
		}
	}
}

func TestUpgradeRefusesUnsignedRelease(t *testing.T) {
	// Signature over the wrong content must be rejected.
	fx := setupUpgrade(t, nil, false)
	oldPub := releasePublicKey
	releasePublicKey = func() (*rsa.PublicKey, error) {
		other, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		return &other.PublicKey, nil
	}
	t.Cleanup(func() { releasePublicKey = oldPub })

	if err := runUpgrade(); !errors.Is(err, errSilent) {
		t.Fatalf("expected refusal for a bad signature, got %v", err)
	}
	if installed(t, fx) != "OLD" {
		t.Error("binary replaced despite an invalid signature")
	}
}
