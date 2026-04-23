package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Config is persisted to disk under $XDG_CONFIG_HOME/pipe2/config.json.
// JSON is used (not TOML) to keep the dep tree at zero — the SDK and Cobra
// are already large enough.
type Config struct {
	APIURL string `json:"api_url,omitempty"`
	Token  string `json:"token,omitempty"`
}

// configPath returns the resolved config file path, honouring the global
// --config flag, $XDG_CONFIG_HOME, and the XDG default of $HOME/.config.
func configPath() (string, error) {
	if Globals.ConfigPath != "" {
		return Globals.ConfigPath, nil
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home: %w", err)
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "pipe2", "config.json"), nil
}

// LoadConfig reads the config file. Missing file returns an empty Config,
// not an error — first-run users have no file yet.
func LoadConfig() (*Config, error) {
	p, err := configPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", p, err)
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", p, err)
	}
	return &c, nil
}

// SaveConfig writes the config atomically (write-then-rename) with 0600
// permissions — the file contains a bearer token.
func SaveConfig(c *Config) error {
	p, err := configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := os.Rename(tmp, p); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// EffectiveAPIURL returns the API URL honouring CLI flag > env > config > default.
func (c *Config) EffectiveAPIURL() string {
	if Globals.APIURL != "" {
		return Globals.APIURL
	}
	if v := os.Getenv("PIPE2_API_URL"); v != "" {
		return v
	}
	if c != nil && c.APIURL != "" {
		return c.APIURL
	}
	return "https://api.pipe2.ai/v1/graphql"
}

// EffectiveToken returns the token honouring CLI flag > env > config.
// Empty result means the user is not authenticated.
func (c *Config) EffectiveToken() string {
	if Globals.Token != "" {
		return Globals.Token
	}
	if v := os.Getenv("PIPE2_TOKEN"); v != "" {
		return v
	}
	if c != nil {
		return c.Token
	}
	return ""
}
