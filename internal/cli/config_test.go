package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// TestConfigRoundTrip writes a config, reads it back, and verifies the
// values survive — the file is the agent's only persistent state, so a
// silent corruption here would force every command to ask for re-auth.
func TestConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	// Override the global config path for this test.
	prev := Globals.ConfigPath
	Globals.ConfigPath = path
	t.Cleanup(func() { Globals.ConfigPath = prev })

	in := &Config{
		APIURL: "https://api.example.test/v1/graphql",
		Token:  "test-token-abc123",
	}
	if err := SaveConfig(in); err != nil {
		t.Fatalf("save: %v", err)
	}

	// File should be 0600 — it contains a bearer token.
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := st.Mode().Perm(); got != 0o600 {
		t.Errorf("config perms = %#o, want 0600", got)
	}

	out, err := LoadConfig()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if out.APIURL != in.APIURL {
		t.Errorf("APIURL: got %q, want %q", out.APIURL, in.APIURL)
	}
	if out.Token != in.Token {
		t.Errorf("Token: got %q, want %q", out.Token, in.Token)
	}
}

// TestEffectiveResolutionOrder verifies flag > env > file > default for
// both APIURL and Token. Agents rely on the env override path; if it
// regresses they'd silently use the wrong endpoint.
func TestEffectiveResolutionOrder(t *testing.T) {
	saved := *Globals
	t.Cleanup(func() { *Globals = saved })

	cfg := &Config{APIURL: "from-file", Token: "tok-file"}

	// 1. file only
	*Globals = GlobalFlags{}
	t.Setenv("PIPE2_API_URL", "")
	t.Setenv("PIPE2_TOKEN", "")
	if got := cfg.EffectiveAPIURL(); got != "from-file" {
		t.Errorf("file only: APIURL=%q", got)
	}

	// 2. env wins over file
	t.Setenv("PIPE2_API_URL", "from-env")
	t.Setenv("PIPE2_TOKEN", "tok-env")
	if got := cfg.EffectiveAPIURL(); got != "from-env" {
		t.Errorf("env wins: APIURL=%q", got)
	}
	if got := cfg.EffectiveToken(); got != "tok-env" {
		t.Errorf("env wins: Token=%q", got)
	}

	// 3. flag wins over env
	Globals.APIURL = "from-flag"
	Globals.Token = "tok-flag"
	if got := cfg.EffectiveAPIURL(); got != "from-flag" {
		t.Errorf("flag wins: APIURL=%q", got)
	}
	if got := cfg.EffectiveToken(); got != "tok-flag" {
		t.Errorf("flag wins: Token=%q", got)
	}

	// 4. default kicks in when nothing set and config empty
	*Globals = GlobalFlags{}
	t.Setenv("PIPE2_API_URL", "")
	empty := &Config{}
	if got := empty.EffectiveAPIURL(); got != "https://api.pipe2.ai/v1/graphql" {
		t.Errorf("default: APIURL=%q", got)
	}
}
