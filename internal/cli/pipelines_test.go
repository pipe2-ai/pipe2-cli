package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"

	pipe2 "github.com/pipe2-ai/sdk-go"
)

func TestPipelinesGetReturnsSchemas(t *testing.T) {
	input := json.RawMessage(`{"type":"object","properties":{"video":{"type":"string"}},"required":["video"]}`)
	output := json.RawMessage(`{"type":"object","properties":{"video_url":{"type":"string"}},"required":["video_url"]}`)
	for _, test := range []struct {
		name     string
		status   int
		missing  bool
		wantCode int
	}{
		{name: "schemas", status: http.StatusOK},
		{name: "missing", status: http.StatusOK, missing: true, wantCode: ExitNotFound},
		{name: "forbidden", status: http.StatusForbidden, wantCode: ExitForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var request struct {
					OperationName string            `json:"operationName"`
					Variables     map[string]string `json:"variables"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer fixture-token" || request.OperationName != "GetPipelineBySlug" || request.Variables["slug"] != "fixture-video" {
					t.Error("pipeline inspection did not use the configured SDK connection and selected slug")
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(test.status)
				if test.status != http.StatusOK {
					_, _ = w.Write([]byte(`{"errors":[{"message":"forbidden"}]}`))
					return
				}
				pipelines := []pipe2.GetPipelineBySlugPipelines{}
				if !test.missing {
					pipelines = append(pipelines, pipe2.GetPipelineBySlugPipelines{Slug: "fixture-video", Name: "Fixture", Input_schema: input, Output_schema: output})
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"data": pipe2.GetPipelineBySlugResponse{Pipelines: pipelines}})
			}))
			defer server.Close()
			t.Setenv("PIPE2_API_URL", server.URL)
			t.Setenv("PIPE2_TOKEN", "fixture-token")
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			data, err := executePipelineGetForTest(t, "fixture-video")
			if calls.Load() != 1 {
				t.Fatalf("requests=%d, want one read-only query", calls.Load())
			}
			if test.wantCode != 0 {
				if err == nil || ExitCodeFor(err) != test.wantCode {
					t.Fatalf("error=%v, want exit %d", err, test.wantCode)
				}
				if len(data) != 0 {
					t.Fatal("failed inspection printed a successful record")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var got pipe2.GetPipelineBySlugPipelines
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			var schemas struct {
				Input struct {
					Required []string `json:"required"`
				} `json:"input_schema"`
				Output struct {
					Required []string `json:"required"`
				} `json:"output_schema"`
			}
			if json.Unmarshal(data, &schemas) != nil || got.Slug != "fixture-video" || len(schemas.Input.Required) != 1 || schemas.Input.Required[0] != "video" || len(schemas.Output.Required) != 1 || schemas.Output.Required[0] != "video_url" {
				t.Fatal("pipeline get did not return the selected record with usable input and output schemas")
			}
		})
	}
}

func TestPipelinesGetRequiresOneSlug(t *testing.T) {
	for _, args := range [][]string{nil, {"first", "second"}} {
		_, err := executePipelineGetForTest(t, args...)
		if err == nil || ExitCodeFor(err) != ExitUsage {
			t.Fatalf("args=%v error=%v, want usage error", args, err)
		}
	}
}

func executePipelineGetForTest(t *testing.T, args ...string) ([]byte, error) {
	t.Helper()
	previousFlags, previousStdout := Globals, os.Stdout
	Globals = &GlobalFlags{}
	file, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = file
	defer func() { Globals, os.Stdout = previousFlags, previousStdout; _ = file.Close() }()
	root := NewRootCmd("test")
	root.SilenceErrors = true
	root.SetArgs(append([]string{"pipelines", "get", "--json"}, args...))
	runErr := root.Execute()
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}
	return data, runErr
}
