package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	pipe2 "github.com/pipe2-ai/sdk-go"
)

func newPipelinesCmd() *cobra.Command {
	c := &cobra.Command{
		Use:     "pipelines",
		Aliases: []string{"pipeline"},
		Short:   "List and run Pipe2.ai pipelines",
	}
	c.AddCommand(newPipelinesListCmd(), newPipelinesRunCmd())
	return c
}

func newPipelinesListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all available pipelines",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := MustClient()
			if err != nil {
				return err
			}
			resp, err := pipe2.GetPipelines(cmd.Context(), client)
			if err != nil {
				return classifyAPIError(err)
			}
			return Out(resp.Pipelines)
		},
	}
}

func newPipelinesRunCmd() *cobra.Command {
	var (
		slug      string
		inputFile string
		inputStr  string
		wait      bool
		waitTO    time.Duration
	)
	c := &cobra.Command{
		Use:   "run",
		Short: "Dispatch a pipeline run",
		Long: `Dispatch a pipeline run with the given JSON input.

Examples:
  pipe2 pipelines run --pipeline video-generator --input ./input.json
  echo '{"prompt":"a cat"}' | pipe2 pipelines run --pipeline video-generator --input -
  pipe2 pipelines run --pipeline video-generator --input-json '{"prompt":"a cat"}' --wait`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if slug == "" {
				return &ExitError{Code: ExitUsage, Err: fmt.Errorf("--pipeline is required")}
			}
			raw, err := readInput(inputFile, inputStr)
			if err != nil {
				return &ExitError{Code: ExitUsage, Err: err}
			}

			client, err := MustClient()
			if err != nil {
				return err
			}
			Status("dispatching pipeline %s...", slug)
			resp, err := pipe2.RunPipeline(cmd.Context(), client, slug, json.RawMessage(raw))
			if err != nil {
				return classifyAPIError(err)
			}
			result := map[string]any{"run": resp.Run_pipeline}

			if wait && resp.Run_pipeline != nil {
				runID := resp.Run_pipeline.Run_id
				Status("waiting for run %s...", runID)
				final, err := waitForRun(cmd.Context(), client, runID, waitTO)
				if err != nil {
					return err
				}
				result["final"] = final
			}
			return Out(result)
		},
	}
	c.Flags().StringVar(&slug, "pipeline", "", "pipeline slug (required)")
	c.Flags().StringVar(&inputFile, "input", "", `path to JSON input file, or "-" for stdin`)
	c.Flags().StringVar(&inputStr, "input-json", "", "inline JSON input")
	c.Flags().BoolVar(&wait, "wait", false, "block until the run reaches a terminal status")
	c.Flags().DurationVar(&waitTO, "wait-timeout", 10*time.Minute, "max time to wait when --wait is set")
	return c
}

// readInput resolves a file/stdin/inline-json combo into raw bytes. Exactly
// one source must be supplied.
func readInput(file, inline string) ([]byte, error) {
	if (file == "" && inline == "") || (file != "" && inline != "") {
		return nil, fmt.Errorf("exactly one of --input or --input-json is required")
	}
	if inline != "" {
		// Validate it parses.
		var tmp any
		if err := json.Unmarshal([]byte(inline), &tmp); err != nil {
			return nil, fmt.Errorf("--input-json is not valid JSON: %w", err)
		}
		return []byte(inline), nil
	}
	var r io.Reader
	if file == "-" {
		r = os.Stdin
	} else {
		f, err := os.Open(file)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		r = f
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var tmp any
	if err := json.Unmarshal(data, &tmp); err != nil {
		return nil, fmt.Errorf("input is not valid JSON: %w", err)
	}
	return data, nil
}
