package cli

import (
	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	pipe2 "github.com/pipe2-ai/sdk-go"
)

// listAssetsVars omits $where so the schema default `{}` applies. Same
// rationale as listPipelineRunsVars in runs.go — the generated
// `*Assets_bool_exp` struct has no omitempty tags and marshals as
// `{..., "pipeline_run": null, ...}`, which Hasura rejects.
type listAssetsVars struct {
	Limit  *int `json:"limit,omitempty"`
	Offset *int `json:"offset,omitempty"`
}

func newAssetsCmd() *cobra.Command {
	c := &cobra.Command{
		Use:     "assets",
		Aliases: []string{"asset"},
		Short:   "Inspect and delete generated assets",
	}
	c.AddCommand(newAssetsListCmd(), newAssetsDeleteCmd())
	return c
}

func newAssetsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List your assets",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := MustClient()
			if err != nil {
				return err
			}
			// Call the operation directly so we can omit $where and let
			// the schema default `{}` apply. See listAssetsVars above.
			req := &graphql.Request{
				OpName:    "GetUserAssets",
				Query:     pipe2.GetUserAssets_Operation,
				Variables: listAssetsVars{},
			}
			data := &pipe2.GetUserAssetsResponse{}
			if err := client.MakeRequest(cmd.Context(), req, &graphql.Response{Data: data}); err != nil {
				return classifyAPIError(err)
			}
			return Out(data.Assets)
		},
	}
}

func newAssetsDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <asset-id>",
		Short: "Delete an asset (DB row + S3 object)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := MustClient()
			if err != nil {
				return err
			}
			Status("deleting asset %s...", args[0])
			resp, err := pipe2.DeleteAssetAction(cmd.Context(), client, args[0])
			if err != nil {
				return classifyAPIError(err)
			}
			return Out(resp.Delete_asset)
		},
	}
}
