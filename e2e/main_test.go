// Package e2e contains end-to-end tests for the pipe2 binary. They are
// guarded by the `e2e` build tag so `go test ./...` in normal CI doesn't
// try to talk to a real Hasura — only the dedicated e2e job opts in.
//
// To run locally against a docker-compose stack:
//
//	cd packages/pipe2-cli
//	docker compose -f ../../compose.yml -f ../../compose.test.yml up -d
//	go test -tags=e2e ./e2e/... -v
//
//go:build e2e
// +build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	defaultHasuraURL    = "http://localhost:8180/v1/graphql"
	defaultAdminSecret  = "hasura_admin_secret"
	defaultUserEmail    = "user@example.com"
)

// testEnv is shared across all e2e tests in this package.
type testEnv struct {
	binary      string // absolute path to the freshly-built pipe2 binary
	hasuraURL   string
	adminSecret string
	pat         string // a freshly-minted personal access token
	userID      string
	configDir   string
}

var env *testEnv

// TestMain builds the binary once, mints a PAT once, and shares them
// across every test. Each test gets its own --config path so they can
// run in parallel without stomping on each other.
func TestMain(m *testing.M) {
	env = &testEnv{
		hasuraURL:   getenvOr("PIPE2_E2E_HASURA_URL", defaultHasuraURL),
		adminSecret: getenvOr("PIPE2_E2E_ADMIN_SECRET", defaultAdminSecret),
	}

	tmp, err := os.MkdirTemp("", "pipe2-cli-e2e-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, "mktemp:", err)
		os.Exit(2)
	}
	defer os.RemoveAll(tmp)
	env.configDir = tmp

	bin := filepath.Join(tmp, "pipe2")
	build := exec.Command("go", "build",
		"-trimpath", "-ldflags", "-s -w -X main.version=e2e",
		"-o", bin, "./cmd/pipe2",
	)
	build.Dir = ".."
	build.Stdout = os.Stderr
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "build failed:", err)
		os.Exit(2)
	}
	env.binary = bin

	uid, err := lookupDevUserID(env.hasuraURL, env.adminSecret)
	if err != nil {
		fmt.Fprintln(os.Stderr, "lookup dev user:", err)
		os.Exit(2)
	}
	env.userID = uid

	pat, err := mintPAT(env.hasuraURL, env.adminSecret, uid, "pipe2-cli-e2e")
	if err != nil {
		fmt.Fprintln(os.Stderr, "mint PAT:", err)
		os.Exit(2)
	}
	env.pat = pat

	os.Exit(m.Run())
}

// runCLI invokes the binary with --json forced on (so we always get
// machine-readable stdout) and the per-test PAT injected via env. It
// returns stdout, stderr, and the exit code so tests can assert on all
// three independently — agents care about exit code as much as payload.
func runCLI(t *testing.T, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cfg := filepath.Join(t.TempDir(), "config.json")
	full := append([]string{"--json", "--config", cfg, "--api-url", env.hasuraURL}, args...)
	cmd := exec.CommandContext(ctx, env.binary, full...)
	cmd.Env = append(os.Environ(), "PIPE2_TOKEN="+env.pat)

	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	exitCode = 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		exitCode = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("run %v: %v", args, err)
	}
	return out.String(), errb.String(), exitCode
}

func decodeJSON(t *testing.T, s string, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(strings.TrimSpace(s)), v); err != nil {
		t.Fatalf("decode JSON: %v\nbody: %q", err, s)
	}
}

func getenvOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// lookupDevUserID finds the seeded dev user's UUID via Hasura admin.
// The seed script (scripts/seed-kratos-user.sh) creates user@example.com.
func lookupDevUserID(hasuraURL, adminSecret string) (string, error) {
	const q = `query { users(where: {email: {_eq: "` + defaultUserEmail + `"}}, limit: 1) { id } }`
	resp, err := graphqlAdmin(hasuraURL, adminSecret, q, nil)
	if err != nil {
		return "", err
	}
	var body struct {
		Data struct {
			Users []struct {
				ID string `json:"id"`
			} `json:"users"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp, &body); err != nil {
		return "", fmt.Errorf("parse users: %w (body=%s)", err, resp)
	}
	if len(body.Data.Users) == 0 {
		return "", fmt.Errorf("dev user %s not found — did the seed step run?", defaultUserEmail)
	}
	return body.Data.Users[0].ID, nil
}

// mintPAT creates a personal access token by invoking the Hasura action
// with admin secret + user impersonation headers. The action handler reads
// x-hasura-user-id from session_variables and creates the row + JWT.
func mintPAT(hasuraURL, adminSecret, userID, name string) (string, error) {
	const m = `mutation Mint($name: String!) {
		create_personal_access_token(name: $name) { token }
	}`
	resp, err := graphqlImpersonate(hasuraURL, adminSecret, userID, m,
		map[string]any{"name": name})
	if err != nil {
		return "", err
	}
	var body struct {
		Data struct {
			Create struct {
				Token string `json:"token"`
			} `json:"create_personal_access_token"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(resp, &body); err != nil {
		return "", fmt.Errorf("parse mint: %w (body=%s)", err, resp)
	}
	if len(body.Errors) > 0 {
		return "", fmt.Errorf("mint PAT: %s", body.Errors[0].Message)
	}
	if body.Data.Create.Token == "" {
		return "", fmt.Errorf("mint PAT: empty token in response: %s", resp)
	}
	return body.Data.Create.Token, nil
}

func graphqlAdmin(url, secret, query string, vars map[string]any) ([]byte, error) {
	return graphqlPost(url, map[string]string{
		"x-hasura-admin-secret": secret,
	}, query, vars)
}

func graphqlImpersonate(url, secret, userID, query string, vars map[string]any) ([]byte, error) {
	return graphqlPost(url, map[string]string{
		"x-hasura-admin-secret": secret,
		"x-hasura-role":         "user",
		"x-hasura-user-id":      userID,
	}, query, vars)
}

func graphqlPost(url string, headers map[string]string, query string, vars map[string]any) ([]byte, error) {
	body, _ := json.Marshal(map[string]any{"query": query, "variables": vars})
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		buf := make([]byte, 4096)
		n, _ := resp.Body.Read(buf)
		return nil, fmt.Errorf("graphql %d: %s", resp.StatusCode, buf[:n])
	}
	out := make([]byte, 0, 4096)
	for {
		buf := make([]byte, 4096)
		n, _ := resp.Body.Read(buf)
		if n == 0 {
			break
		}
		out = append(out, buf[:n]...)
	}
	return out, nil
}
