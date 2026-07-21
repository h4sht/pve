package config

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// Config holds user-level settings persisted to ~/.config/pv/config.json
// (or %AppData%\pv\config.json on Windows).
type Config struct {
	Nodes       []string `json:"nodes"`                  // PVE node hostnames or IPs
	Token       string   `json:"token"`                  // full "PVEAPIToken=USER@REALM!TOKENID=UUID" header value
	Repo        string   `json:"repo"`                   // "owner/name" path used by the updater (GitHub Releases)
	BaseURL     string   `json:"base_url,omitempty"`     // http(s)://host[:port] of self-hosted update server; takes precedence over Repo
	Secure      bool     `json:"secure,omitempty"`       // true=use https://<node>:8006
	SSHUser     string   `json:"ssh_user,omitempty"`     // SSH user for pct exec fallback (default: token user or root)
	SSHPassword string   `json:"ssh_password,omitempty"` // SSH password for PVE node (fallback when no keys are configured)
	Lang        string   `json:"lang,omitempty"`         // CLI language: "es" or "en" (default: detect from system)
}

// Path returns the platform-appropriate config location.
func Path() (string, error) {
	if dir := os.Getenv("PV_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "config.json"), nil
	}
	cfgDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cfgDir, "pve", "config.json"), nil
}

// Load reads the config or returns a zero-value Config if missing.
func Load() (*Config, error) {
	p, err := Path()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return &Config{}, nil
		}
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// Save writes the config atomically (temp+rename) and locks perms to 0600.
func Save(c *Config) error {
	p, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp := p + ".tmp"
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, p); err != nil {
		os.Remove(tmp)
		return err
	}
	// Lock down permissions wherever possible.
	_ = os.Chmod(p, 0o600)
	return nil
}
