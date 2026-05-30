package cookbook

import "testing"

func TestParseCorrections_Empty(t *testing.T) {
	for _, in := range []string{"", "   ", "\n"} {
		got, err := ParseCorrections(in)
		if err != nil {
			t.Errorf("empty input %q: unexpected error %v", in, err)
		}
		if got != nil {
			t.Errorf("empty input %q: want nil map, got %v", in, got)
		}
	}
}

func TestParseCorrections_HappyPath(t *testing.T) {
	got, err := ParseCorrections("Qube=Cube, Kyub=Cube ,Pavle=Pavel")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := map[string]string{"Qube": "Cube", "Kyub": "Cube", "Pavle": "Pavel"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("got[%q] = %q, want %q", k, got[k], v)
		}
	}
}

func TestParseCorrections_Malformed(t *testing.T) {
	cases := []string{
		"Qube",       // no =
		"=Cube",      // empty from
		"Qube=",      // empty to
		"  =  ",      // both empty
		"a=b,broken", // second pair malformed
	}
	for _, in := range cases {
		_, err := ParseCorrections(in)
		if err == nil {
			t.Errorf("%q: expected error, got nil", in)
		}
	}
}

func TestApplyCorrections_NoOp(t *testing.T) {
	body := "1\n00:00:01,000 --> 00:00:02,000\nCloud goes to Qube\n"
	if got := ApplyCorrections(body, nil); got != body {
		t.Errorf("nil map: body changed")
	}
	if got := ApplyCorrections(body, map[string]string{}); got != body {
		t.Errorf("empty map: body changed")
	}
	if got := ApplyCorrections("", map[string]string{"a": "b"}); got != "" {
		t.Errorf("empty body: got %q", got)
	}
}

func TestApplyCorrections_WordBoundary(t *testing.T) {
	body := "Qube is great. Don't rebuke it. Qubed."
	got := ApplyCorrections(body, map[string]string{"Qube": "Cube"})
	want := "Cube is great. Don't rebuke it. Qubed."
	if got != want {
		t.Errorf("\n got:  %q\nwant:  %q", got, want)
	}
}

func TestApplyCorrections_SRTSafe(t *testing.T) {
	// Timestamps + cue numbers must survive untouched.
	body := "1\n00:00:01,000 --> 00:00:02,000\nQube ships agentic analytics\n"
	got := ApplyCorrections(body, map[string]string{"Qube": "Cube"})
	want := "1\n00:00:01,000 --> 00:00:02,000\nCube ships agentic analytics\n"
	if got != want {
		t.Errorf("\n got:  %q\nwant:  %q", got, want)
	}
}

func TestApplyCorrections_MultiwordPhrase(t *testing.T) {
	body := "Use Cloud Course to chat with the cluster."
	got := ApplyCorrections(body, map[string]string{"Cloud Course": "Claude Code"})
	want := "Use Claude Code to chat with the cluster."
	if got != want {
		t.Errorf("\n got:  %q\nwant:  %q", got, want)
	}
}

func TestApplyCorrections_OverlapLongerWins(t *testing.T) {
	// "Cube Cloud" must rewrite to "Cube DB" BEFORE the bare-"Cube"
	// rule fires — otherwise the bare rule eats the prefix first
	// and "Cube Cloud" never matches as a phrase.
	body := "Cube Cloud is hosted Cube."
	got := ApplyCorrections(body, map[string]string{
		"Cube":       "Acme",
		"Cube Cloud": "Acme Cloud",
	})
	want := "Acme Cloud is hosted Acme."
	if got != want {
		t.Errorf("\n got:  %q\nwant:  %q", got, want)
	}
}

func TestApplyCorrections_RegexMetaIsLiteral(t *testing.T) {
	// A user-supplied "from" with regex metacharacters must be
	// treated literally — typing "C.ube" should not match "Cube".
	body := "Cube and C.ube."
	got := ApplyCorrections(body, map[string]string{"C.ube": "Hit"})
	want := "Cube and Hit."
	if got != want {
		t.Errorf("\n got:  %q\nwant:  %q", got, want)
	}
}

func TestApplyCorrections_DeterministicOrder(t *testing.T) {
	// Same map, two distinct iteration orders (Go randomises) must
	// produce the same output. Verified by running ApplyCorrections
	// many times and checking the result is constant.
	body := "Cube Cloud and Cube and Kyub."
	corr := map[string]string{
		"Cube":       "A",
		"Cube Cloud": "AC",
		"Kyub":       "A",
	}
	first := ApplyCorrections(body, corr)
	for i := 0; i < 100; i++ {
		if got := ApplyCorrections(body, corr); got != first {
			t.Fatalf("non-deterministic at iter %d:\n got %q\nwant %q", i, got, first)
		}
	}
	want := "AC and A and A."
	if first != want {
		t.Errorf("\n got:  %q\nwant:  %q", first, want)
	}
}
