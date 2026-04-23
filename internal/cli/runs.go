package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	pipe2 "github.com/pipe2-ai/sdk-go"
)

// listPipelineRunsVars omits $where so the operation's default value (`{}`)
// applies. We can't use pipe2.GetPipelineRuns because its generated struct
// always serialises Where — and `*Pipeline_runs_bool_exp` has no omitempty
// tags, so an empty struct marshals as `{..., "pipeline": null, ...}`,
// which Hasura rejects with "expected an object for type 'pipelines_bool_exp',
// but found null" on the nested relationship field.
type listPipelineRunsVars struct {
	Limit  *int `json:"limit,omitempty"`
	Offset *int `json:"offset,omitempty"`
}

// terminalStatuses lists pipeline_runs.status values that mean "won't change".
// Mirrors packages/api/internal/actions/router.go and worker conventions.
var terminalStatuses = map[string]bool{
	"completed": true,
	"failed":    true,
	"canceled":  true,
	"cancelled": true,
}

func newRunsCmd() *cobra.Command {
	c := &cobra.Command{
		Use:     "runs",
		Aliases: []string{"run"},
		Short:   "Inspect pipeline runs",
	}
	c.AddCommand(newRunsListCmd(), newRunsGetCmd(), newRunsWaitCmd())
	return c
}

func newRunsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List recent pipeline runs",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := MustClient()
			if err != nil {
				return err
			}
			// Call the operation directly (not via pipe2.GetPipelineRuns) so we
			// can omit `$where` entirely and fall through to the schema default
			// `{}`. See listPipelineRunsVars doc comment above.
			req := &graphql.Request{
				OpName:    "GetPipelineRuns",
				Query:     pipe2.GetPipelineRuns_Operation,
				Variables: listPipelineRunsVars{},
			}
			data := &pipe2.GetPipelineRunsResponse{}
			if err := client.MakeRequest(cmd.Context(), req, &graphql.Response{Data: data}); err != nil {
				return classifyAPIError(err)
			}
			return Out(data.Pipeline_runs)
		},
	}
}

func newRunsGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <run-id>",
		Short: "Show a single run by id",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := MustClient()
			if err != nil {
				return err
			}
			resp, err := pipe2.GetPipelineRun(cmd.Context(), client, args[0])
			if err != nil {
				return classifyAPIError(err)
			}
			if resp.Pipeline_runs_by_pk == nil {
				return &ExitError{Code: ExitNotFound, Err: fmt.Errorf("run %s not found", args[0])}
			}
			return Out(resp.Pipeline_runs_by_pk)
		},
	}
}

func newRunsWaitCmd() *cobra.Command {
	var timeout time.Duration
	c := &cobra.Command{
		Use:   "wait <run-id>",
		Short: "Block until the run reaches a terminal status",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := MustClient()
			if err != nil {
				return err
			}
			final, err := waitForRun(cmd.Context(), client, args[0], timeout)
			if err != nil {
				return err
			}
			return Out(final)
		},
	}
	c.Flags().DurationVar(&timeout, "timeout", 10*time.Minute, "max time to wait")
	return c
}

// waitForRun polls GetPipelineRun on a 2s interval until the status is
// terminal or the timeout fires. Cancellation via cmd context is honoured.
//
// Polling rather than subscribing keeps the CLI dependency footprint small —
// the SDK does have a WatchPipelineRun subscription but it pulls in a
// websocket transport we don't need for short-lived agent invocations.
func waitForRun(
	ctx context.Context,
	client graphql.Client,
	runID string,
	timeout time.Duration,
) (any, error) {
	deadline := time.Now().Add(timeout)
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()

	for {
		resp, err := pipe2.GetPipelineRun(ctx, client, runID)
		if err != nil {
			return nil, classifyAPIError(err)
		}
		if resp.Pipeline_runs_by_pk == nil {
			return nil, &ExitError{Code: ExitNotFound, Err: fmt.Errorf("run %s not found", runID)}
		}
		row := resp.Pipeline_runs_by_pk
		Status("run %s status=%s", runID, row.Status)
		if terminalStatuses[row.Status] {
			if row.Status != "completed" {
				// Non-zero exit even on --json so agent shell scripts can branch.
				return row, &ExitError{
					Code: ExitGeneric,
					Err:  fmt.Errorf("run terminated with status=%s", row.Status),
				}
			}
			return row, nil
		}
		if time.Now().After(deadline) {
			return row, &ExitError{
				Code: ExitGeneric,
				Err:  fmt.Errorf("timeout waiting for run %s (still %s)", runID, row.Status),
			}
		}
		select {
		case <-ctx.Done():
			return row, ctx.Err()
		case <-tick.C:
		}
	}
}
