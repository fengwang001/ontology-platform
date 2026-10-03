package rewrite

import "testing"

func TestRewrite(t *testing.T) {
	rules, err := NewRuleSet([]Rule{
		{From: "/a", To: "/b"},
		{From: "/b", To: "/a"},
		{From: "/root", To: "/", Final: true},
		{From: "/stop", To: "/next", Final: true},
		{From: "/next", To: "/ignored"},
		{From: "/deep", To: "/deeper"},
		{From: "/deeper", To: "/deep"},
	})
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name   string
		start  string
		k      int
		status Status
		path   string
		hops   int
	}{
		{"to root rest", "/root/x/y", 3, StatusComplete, "/x/y", 1},
		{"to root exact", "/root", 3, StatusComplete, "/", 1},
		{"final stops", "/stop/x", 3, StatusComplete, "/next/x", 1},
		{"no final continues", "/next/x", 3, StatusComplete, "/ignored/x", 1},
		{"zero hops", "/a", 0, StatusHopLimit, "/a", 0},
		{"hop at k", "/a", 1, StatusHopLimit, "/b", 1},
		{"loop before hop limit", "/a", 5, StatusLoop, "/a", 1},
		{"hop first with loop possible", "/deep", 1, StatusHopLimit, "/deeper", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			outcome, status := rules.Rewrite(tc.start, tc.k)
			if status != tc.status || outcome.Path != tc.path || outcome.Hops != tc.hops {
				t.Fatalf("Rewrite(%q,%d) = %+v,%v, want path=%q hops=%d status=%v", tc.start, tc.k, outcome, status, tc.path, tc.hops, tc.status)
			}
			if status == StatusComplete && (len(outcome.Chain) != tc.hops+1 || outcome.Chain[0] != tc.start || outcome.Chain[len(outcome.Chain)-1] != tc.path) {
				t.Fatalf("bad chain %+v", outcome.Chain)
			}
		})
	}
}

func TestNewRuleSetValidation(t *testing.T) {
	cases := []struct {
		name  string
		rules []Rule
		err   error
	}{
		{"invalid from", []Rule{{From: "/a/", To: "/b"}}, ErrInvalidFrom},
		{"invalid to", []Rule{{From: "/a", To: "/b/"}}, ErrInvalidTo},
		{"duplicate", []Rule{{From: "/a", To: "/b"}, {From: "/a", To: "/c"}}, ErrDuplicateRule},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewRuleSet(tc.rules); err != tc.err {
				t.Fatalf("err=%v want %v", err, tc.err)
			}
		})
	}
}
