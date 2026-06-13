package cli

import (
	"testing"

	pipe2 "github.com/pipe2-ai/sdk-go"
)

func TestCreditsMC(t *testing.T) {
	cases := []struct {
		mc   int
		want string
	}{
		{510000, "510"},   // the bug from PIP-4: 510 credits, not 510000
		{0, "0"},          // empty wallet
		{1000, "1"},       // one whole credit
		{500, "0.5"},      // sub-credit
		{510500, "510.5"}, // whole + half
		{6445, "6.445"},   // metered run charge — full millicredit precision
		{1, "0.001"},      // smallest unit
		{-2500, "-2.5"},   // defensive: negative never crashes
	}
	for _, c := range cases {
		if got := string(creditsMC(c.mc)); got != c.want {
			t.Errorf("creditsMC(%d) = %q, want %q", c.mc, got, c.want)
		}
	}
}

func TestCreditBalanceView(t *testing.T) {
	b := &pipe2.GetCreditBalanceGet_credit_balanceCredit_balance_output{
		Balance:   510000,
		Available: 509000,
		Reserved:  1000,
	}
	v := creditBalanceView(b)
	if string(v.Balance) != "510" || string(v.Available) != "509" || string(v.Reserved) != "1" {
		t.Fatalf("view = %+v, want balance=510 available=509 reserved=1", v)
	}

	// Nil balance (e.g. brand-new account) renders as zeros, never panics.
	got := creditBalanceView(nil).String()
	want := "Balance:   0 credits\nAvailable: 0 credits\nReserved:  0 credits"
	if got != want {
		t.Errorf("nil view String() = %q, want %q", got, want)
	}
}
