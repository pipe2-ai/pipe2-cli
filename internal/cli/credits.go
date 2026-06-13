package cli

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

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
			return Out(creditBalanceView(resp.Get_credit_balance))
		},
	})
	return c
}

// creditBalanceView wraps the raw API balance (in millicredits) and renders
// it as credits. The API and TigerBeetle keep everything in millicredits
// (1 credit = 1000 mc); converting to credits is purely a display concern,
// applied here at the user-facing surface — the same boundary the web UI
// (lib/credits.formatCredits) and the MCP balance message already use.
func creditBalanceView(b *pipe2.GetCreditBalanceGet_credit_balanceCredit_balance_output) balanceView {
	if b == nil {
		return balanceView{}
	}
	return balanceView{
		Balance:   creditsMC(b.Balance),
		Available: creditsMC(b.Available),
		Reserved:  creditsMC(b.Reserved),
	}
}

// balanceView is the rendered, credits-denominated balance. json.Number keeps
// the JSON output a bare number (510, not "510") so agents parsing `--json`
// still get a numeric field, while String() drives the human form.
type balanceView struct {
	Balance   json.Number `json:"balance"`
	Available json.Number `json:"available"`
	Reserved  json.Number `json:"reserved"`
}

func (v balanceView) String() string {
	num := func(n json.Number) json.Number {
		if n == "" {
			return "0"
		}
		return n
	}
	return fmt.Sprintf("Balance:   %s credits\nAvailable: %s credits\nReserved:  %s credits",
		num(v.Balance), num(v.Available), num(v.Reserved))
}

// creditsMC converts millicredits to a credit string with exact precision —
// whole credits render with no decimals (510000 → "510"), sub-credit values
// keep up to three decimal places with trailing zeros trimmed (510500 →
// "510.5", 6445 → "6.445"). Unlike fmtCreditsMC (the estimate footer's
// compact %.1f form) this never rounds, so a displayed balance always matches
// the ledger to the millicredit. Integer math avoids float artifacts.
func creditsMC(mc int) json.Number {
	neg := mc < 0
	if neg {
		mc = -mc
	}
	whole := mc / 1000
	frac := mc % 1000
	var s string
	if frac == 0 {
		s = strconv.Itoa(whole)
	} else {
		s = strings.TrimRight(fmt.Sprintf("%d.%03d", whole, frac), "0")
	}
	if neg {
		s = "-" + s
	}
	return json.Number(s)
}
