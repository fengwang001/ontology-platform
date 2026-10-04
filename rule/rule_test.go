package rule

import "testing"

func TestDecide(t *testing.T) {
	cases := []struct {
		when      When
		bad       bool
		skip      bool
		want      Decision
		rationale string
	}{
		{OnSuccess, false, false, ToPending, "clean upstream runs"},
		{OnSuccess, true, false, ToSkipped, "bad upstream skips"},
		{OnSuccess, false, true, ToSkipped, "skipped upstream skips"},
		{OnFailure, true, false, ToPending, "bad upstream triggers"},
		{OnFailure, true, true, ToPending, "bad dominates skip"},
		{OnFailure, false, false, ToSkipped, "no needs is not a failure"},
		{OnFailure, false, true, ToSkipped, "skipped upstream is not a failure"},
		{Always, false, false, ToPending, "always runs"},
		{Always, true, true, ToPending, "always runs despite bad+skip"},
		{Manual, false, false, ToManual, "clean upstream waits for human"},
		{Manual, true, false, ToSkipped, "bad upstream skips gate"},
		{Manual, false, true, ToSkipped, "skipped upstream skips gate"},
	}
	for _, tc := range cases {
		got := Decide(tc.when, tc.bad, tc.skip)
		if got != tc.want {
			t.Fatalf("Decide(%v, bad=%v, skip=%v)=%v, want %v (%s)",
				tc.when, tc.bad, tc.skip, got, tc.want, tc.rationale)
		}
		t.Logf("Decide(%v, bad=%v, skip=%v)=%v <- %s", tc.when, tc.bad, tc.skip, got, tc.rationale)
	}
}

func TestParseWhen(t *testing.T) {
	for _, s := range []string{"on_success", "on_failure", "always", "manual"} {
		if _, ok := ParseWhen(s); !ok {
			t.Fatalf("ParseWhen(%q) rejected", s)
		}
	}
	for _, s := range []string{"", "ON_SUCCESS", "sometimes"} {
		if _, ok := ParseWhen(s); ok {
			t.Fatalf("ParseWhen(%q) accepted", s)
		}
	}
}
