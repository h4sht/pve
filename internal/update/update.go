package update

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/h4sht/pve/internal/proxmox"
)

// CurrentVersion is replaced at build time via -ldflags "-X ...=vX.Y.Z".
// The default value must match DevVersion so development builds are detected.
var CurrentVersion = "v0.0.0"

// DevVersion is the placeholder version used for un-replaced development
// builds. It must match the default value of CurrentVersion.
const DevVersion = "v0.0.0"

// CheckURL is overridable via -ldflags for self-hosted GitHub releases.
var CheckURL = "https://api.github.com/repos/%s/releases/latest"

const httpTimeout = 15 * time.Second

// State is the minimal file we keep so we don't hammer GitHub on every call.
type State struct {
	LastCheck time.Time `json:"last_check"`
	Latest    string    `json:"latest"`
}

func statePath() (string, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	p := filepath.Join(cfg, "pve", "update.json")
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	return p, nil
}

func loadState() State {
	p, _ := statePath()
	var s State
	if b, err := os.ReadFile(p); err == nil {
		_ = json.Unmarshal(b, &s)
	}
	return s
}

func saveState(s State) error {
	p, err := statePath()
	if err != nil {
		return err
	}
	b, _ := json.MarshalIndent(s, "", "  ")
	return os.WriteFile(p, b, 0o644)
}

// Info describes an available update.
type Info struct {
	Latest    string // e.g. "v0.1.4"
	URL       string // direct download URL for our platform's binary
	AssetName string // filename ("pve-linux-amd64", "pve-darwin-arm64"…)
	Source    string // "github" | "local" — for logging only
	Repo      string // GitHub "owner/name", set when Source=="github"
	BaseURL   string // self-hosted base URL, set when Source=="local"
	Force     bool   // if true, reinstall even if versions are equal
}

// NeedsCheck returns true if we haven't checked in the past 24h.
func NeedsCheck() bool {
	s := loadState()
	return time.Since(s.LastCheck) > 24*time.Hour
}

func Check(repo string, force bool) (*Info, error) {
	if repo == "" {
		return nil, errors.New("no repo configured (set it via `pve config repo owner/name`)")
	}
	ghURL := strings.Replace(CheckURL, "%s", repo, 1)
	resp, err := getWithTimeout(ghURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("release API returned %d: %s", resp.StatusCode, string(body)[:min(300, len(body))])
	}
	var v proxmox.Version
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return nil, err
	}
	_ = saveState(State{LastCheck: time.Now(), Latest: v.TagName})
	if !force && !IsNewer(v.TagName, CurrentVersion) {
		return nil, nil
	}
	info := &Info{Latest: v.TagName, URL: v.HTMLURL, Source: "github", Repo: repo, Force: force}
	for _, a := range v.Assets {
		if assetMatches(a.Name) {
			info.AssetName = a.Name
			info.URL = a.BrowserDownloadURL
			return info, nil
		}
	}
	return nil, fmt.Errorf("new release %s found but no asset for %s/%s", v.TagName, runtime.GOOS, runtime.GOARCH)
}

func CheckLocal(baseURL string, force bool) (*Info, error) {
	baseURL = strings.TrimRight(baseURL, "/")
	if baseURL == "" {
		return nil, errors.New("base_url is empty")
	}
	if _, err := url.Parse(baseURL); err != nil {
		return nil, fmt.Errorf("invalid base_url: %w", err)
	}
	versionURL := baseURL + "/latest/version"
	resp, err := getWithTimeout(versionURL)
	if err != nil {
		return nil, fmt.Errorf("could not query %s: %w", versionURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return nil, fmt.Errorf("the server at %s does not expose /latest/version (is it a pve update server?)", baseURL)
	}
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("/latest/version returned HTTP %d: %s", resp.StatusCode, string(body)[:min(200, len(body))])
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", versionURL, err)
	}
	latest := strings.TrimSpace(string(body))
	if latest == "" {
		return nil, fmt.Errorf("/latest/version is empty at %s", baseURL)
	}
	_ = saveState(State{LastCheck: time.Now(), Latest: latest})
	if !force && !IsNewer(latest, CurrentVersion) {
		return nil, nil
	}
	asset := fmt.Sprintf("pve-%s-%s", runtime.GOOS, runtime.GOARCH)
	assetURL := baseURL + "/latest/" + asset
	return &Info{
		Latest:    latest,
		URL:       assetURL,
		AssetName: asset,
		Source:    "local",
		BaseURL:   baseURL,
		Force:     force,
	}, nil
}

func getWithTimeout(rawURL string) (*http.Response, error) {
	client := &http.Client{Timeout: httpTimeout}
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "pve-cli/"+CurrentVersion)
	return client.Do(req)
}

func FingerprintAsset(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil))[:8], nil
}

// (no helper needed: EACCES message is a single, fixed recommendation `sudo pve update`)

// IsNewer compares two semver-ish tags (e.g. v0.1.2 vs v0.1.10) using a
// simple runescan; falls back to lexicographic.
func IsNewer(latest, current string) bool {
	strip := func(s string) []int {
		s = strings.TrimPrefix(s, "v")
		parts := strings.Split(s, ".")
		out := make([]int, len(parts))
		for i, p := range parts {
			n := 0
			fmt.Sscanf(p, "%d", &n)
			out[i] = n
		}
		return out
	}
	a, b := strip(latest), strip(current)
	for i := 0; i < len(a) || i < len(b); i++ {
		var x, y int
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x > y {
			return true
		}
		if x < y {
			return false
		}
	}
	return false
}

// assetMatches returns true if the release asset name matches this platform.
func assetMatches(name string) bool {
	n := strings.ToLower(name)
	if !strings.Contains(n, runtime.GOOS) {
		return false
	}
	if !strings.Contains(n, runtime.GOARCH) && !strings.Contains(n, altArch(runtime.GOARCH)) {
		return false
	}
	return true
}

func altArch(arch string) string {
	switch arch {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "aarch64"
	case "386":
		return "i386"
	}
	return arch
}

func Apply(info *Info) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	realExe, err := filepath.EvalSymlinks(exe)
	if err != nil {
		realExe = exe
	}
	dir := filepath.Dir(realExe)
	newPath := filepath.Join(dir, "."+filepath.Base(realExe)+".new")

	resp, err := getWithTimeout(info.URL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("download %s -> HTTP %d", info.URL, resp.StatusCode)
	}
	out, err := os.OpenFile(newPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		if os.IsPermission(err) {
			return fmt.Errorf("no write permissions in %s — run `sudo pve update` and try again", dir)
		}
		return err
	}
	if _, err := io.Copy(out, resp.Body); err != nil {
		out.Close()
		os.Remove(newPath)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(newPath)
		return err
	}
	if err := smokeTest(newPath); err != nil {
		os.Remove(newPath)
		return fmt.Errorf("downloaded binary failed smoke test: %w", err)
	}
	if err := os.Rename(newPath, realExe); err != nil {
		if err := copyFile(newPath, realExe); err != nil {
			os.Remove(newPath)
			return err
		}
		os.Remove(newPath)
	}
	_ = os.Chmod(realExe, 0o755)
	return nil
}

func smokeTest(path string) error {
	cmd := exec.Command(path, "version")
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run()
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
