package cookbook

import (
	"encoding/json"
	"testing"
)

func TestManifestJSONIncludesDefaultCLIFlag(t *testing.T) {
	raw, err := json.Marshal(Manifest{Inputs: []Input{{Name: "source", Type: AssetURL}}})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Inputs map[string]struct {
			CLIArg string `json:"cli_arg"`
		} `json:"inputs"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Inputs["source"].CLIArg != "--source" {
		t.Fatalf("cli_arg = %q, want --source", got.Inputs["source"].CLIArg)
	}
}
