//go:build e2e
// +build e2e

package e2e

import (
	"strings"
	"testing"
)

// TestSchemaWorksOffline doesn't need the server at all — it's a smoke test
// that the binary built and that schema introspection produces parseable
// JSON. Always runs first so failures point at the build, not the API.
func TestSchemaWorksOffline(t *testing.T) {
	stdout, stderr, code := runCLI(t, "schema")
	if code != 0 {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	var got map[string]any
	decodeJSON(t, stdout, &got)
	if got["command"] == nil {
		t.Fatalf("schema missing 'command' key: %s", stdout)
	}
	if got["exit_codes"] == nil {
		t.Fatalf("schema missing 'exit_codes' key")
	}
}

// TestAuthWhoamiHappy verifies the PAT we minted works against the server
// and that the authenticated user matches the seeded dev account.
func TestAuthWhoamiHappy(t *testing.T) {
	stdout, stderr, code := runCLI(t, "auth", "whoami")
	if code != 0 {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	var user map[string]any
	decodeJSON(t, stdout, &user)
	if email, _ := user["email"].(string); email != defaultUserEmail {
		t.Errorf("whoami email=%q, want %q", email, defaultUserEmail)
	}
}

// TestAuthUnauthorizedExitCode verifies the typed exit code path: a clearly
// invalid token must produce ExitUnauthorized (3), not generic 1. Agents
// branch on this to decide whether to re-auth vs surface the error.
func TestAuthUnauthorizedExitCode(t *testing.T) {
	stdout, stderr, code := runCLI(t,
		"--token", "obviously-not-a-real-token",
		"auth", "whoami",
	)
	if code != 3 {
		t.Errorf("exit code = %d, want 3 (unauthorized)\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
}

// TestPipelinesListReturnsJSON is the "happy path" smoke test for read
// commands. We don't assert specific pipelines (seeds may evolve) — only
// that we get an array of objects back.
func TestPipelinesListReturnsJSON(t *testing.T) {
	stdout, stderr, code := runCLI(t, "pipelines", "list")
	if code != 0 {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	var arr []map[string]any
	decodeJSON(t, stdout, &arr)
	if len(arr) == 0 {
		t.Logf("warning: pipelines list returned empty — seeds may be empty")
	}
}

// TestRunsListReturnsJSON same shape as pipelines but for runs. Empty
// is acceptable on a fresh stack.
func TestRunsListReturnsJSON(t *testing.T) {
	stdout, stderr, code := runCLI(t, "runs", "list")
	if code != 0 {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	var arr []map[string]any
	decodeJSON(t, stdout, &arr)
	_ = arr
}

// TestRunsGetNotFoundExitCode verifies the not-found path. Agents map
// exit code 4 to "skip / fall through" rather than retry.
func TestRunsGetNotFoundExitCode(t *testing.T) {
	stdout, stderr, code := runCLI(t, "runs", "get", "00000000-0000-0000-0000-000000000000")
	if code != 4 {
		t.Errorf("exit code = %d, want 4 (not_found)\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
}

// TestCreditsBalanceReturnsObject — credits endpoint returns a numeric
// balance even on a fresh user, so the only thing we assert is shape.
func TestCreditsBalanceReturnsObject(t *testing.T) {
	stdout, stderr, code := runCLI(t, "credits", "balance")
	if code != 0 {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	var obj map[string]any
	decodeJSON(t, stdout, &obj)
	// Don't lock down a specific field name — the SDK type may evolve.
	// Just ensure the response wasn't empty.
	if len(obj) == 0 {
		t.Errorf("balance object is empty: %s", stdout)
	}
}

// TestUsageErrorExitCode — running a command with a missing required flag
// must exit 2 (usage), not 1 (generic). Same agent-branching contract.
func TestUsageErrorExitCode(t *testing.T) {
	_, stderr, code := runCLI(t, "pipelines", "run") // missing --pipeline
	if code != 2 {
		t.Errorf("exit code = %d, want 2 (usage); stderr=%s", code, stderr)
	}
	if !strings.Contains(stderr, "pipeline") {
		t.Errorf("expected error mentioning 'pipeline' in stderr: %q", stderr)
	}
}
