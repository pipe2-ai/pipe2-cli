package cli

import (
	"github.com/spf13/cobra"

	pipe2 "github.com/pipe2-ai/sdk-go"
)

func newCreditsCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "credits",
		Short: "Credit balance and history",
	}
	c.AddCommand(&cobra.Command{
		Use:   "balance",
		Short: "Show current credit balance",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := MustClient()
			if err != nil {
				return err
			}
			resp, err := pipe2.GetCreditBalance(cmd.Context(), client)
			if err != nil {
				return classifyAPIError(err)
			}
			return Out(resp.Get_credit_balance)
		},
	})
	return c
}
