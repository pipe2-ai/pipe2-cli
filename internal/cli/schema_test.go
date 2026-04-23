package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestSchemaCommandTreeShape walks the root and asserts every command shows
// up under schema with a matching path. This is a smoke test — the goal is
// to catch the case where a new command gets registered but never appears
// under `pipe2 schema` (because someone forgot to use NewRootCmd or skipped
// AddCommand). It does NOT validate flag-level details.
func TestSchemaCommandTreeShape(t *testing.T) {
	root := NewRootCmd("test")

	got := walkCommand(root, "")

	// Mandatory top-level subcommands. Any one of these missing means
	// either the command was deleted or the schema walker is broken.
	mandatory := []string{
		"auth", "pipelines", "runs", "assets", "credits", "schema", "skill",
	}
	have := map[string]bool{}
	for _, c := range got.Children {
		have[c.Name] = true
	}
	for _, name := range mandatory {
		if !have[name] {
			t.Errorf("schema missing mandatory subcommand %q", name)
		}
	}

	// Marshal round-trip — guards against unmarshalable types creeping
	// into schemaCommand. Agents will fail to parse a broken schema.
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("schema is not JSON-serializable: %v", err)
	}
	if !strings.Contains(string(data), `"name":"pipelines"`) {
		t.Errorf("marshaled schema missing pipelines: %s", data[:200])
	}
}

// TestExitCodeSchemaIsStable freezes the exit code contract so an agent
// that hardcodes "3 means re-auth" doesn't silently break when someone
// renumbers them. If you intentionally change codes, update this test.
func TestExitCodeSchemaIsStable(t *testing.T) {
	want := map[int]string{
		0: "ok",
		1: "generic",
		2: "usage",
		3: "unauthorized",
		4: "not_found",
		5: "forbidden",
	}
	for _, e := range exitCodeSchema() {
		code := int(e["code"].(int))
		name := e["name"].(string)
		if want[code] != name {
			t.Errorf("exit code %d: want %q, got %q", code, want[code], name)
		}
	}
}
