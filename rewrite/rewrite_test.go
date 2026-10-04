package rewrite

import (
	"errors"
	"reflect"
	"testing"
)

func mustSet(t *testing.T, rules ...Rule) *RuleSet {
	t.Helper()
	rs, err := NewRuleSet(rules)
	if err != nil {
		t.Fatalf("NewRuleSet: %v", err)
	}
	return rs
}

func TestRewrite(t *testing.T) {
	cases := []struct {
		name      string
		rules     []Rule
		k         int
		p0        string
		wantFin   string
		wantHops  int
		wantChain []string
		wantErr   string // "", "hoplimit" or "loop"
		wantStep  int    // for loop: step that produced the duplicate
	}{
		{
			name:  "no rule matches",
			rules: []Rule{{From: "/a", To: "/b"}},
			k:     3, p0: "/x",
			wantFin: "/x", wantHops: 0, wantChain: []string{"/x"},
		},
		{
			name:  "single hop",
			rules: []Rule{{From: "/a", To: "/b"}},
			k:     3, p0: "/a",
			wantFin: "/b", wantHops: 1, wantChain: []string{"/a", "/b"},
		},
		{
			name:  "rest appended",
			rules: []Rule{{From: "/a", To: "/b"}},
			k:     3, p0: "/a/x/y",
			wantFin: "/b/x/y", wantHops: 1, wantChain: []string{"/a/x/y", "/b/x/y"},
		},
		{
			name:  "longest prefix rule wins",
			rules: []Rule{{From: "/a", To: "/b"}, {From: "/a/x", To: "/c"}},
			k:     3, p0: "/a/x/y",
			wantFin: "/c/y", wantHops: 1, wantChain: []string{"/a/x/y", "/c/y"},
		},
		{
			name:  "to root concatenation",
			rules: []Rule{{From: "/a", To: "/"}},
			k:     3, p0: "/a/b/c",
			wantFin: "/b/c", wantHops: 1, wantChain: []string{"/a/b/c", "/b/c"},
		},
		{
			name:  "to root exact",
			rules: []Rule{{From: "/a", To: "/"}},
			k:     3, p0: "/a",
			wantFin: "/", wantHops: 1, wantChain: []string{"/a", "/"},
		},
		{
			name:  "from root",
			rules: []Rule{{From: "/", To: "/z", Final: true}},
			k:     3, p0: "/a/b",
			wantFin: "/z/a/b", wantHops: 1, wantChain: []string{"/a/b", "/z/a/b"},
		},
		{
			name:  "multi hop chain",
			rules: []Rule{{From: "/a", To: "/b"}, {From: "/b", To: "/c"}, {From: "/c", To: "/d"}},
			k:     3, p0: "/a",
			wantFin: "/d", wantHops: 3, wantChain: []string{"/a", "/b", "/c", "/d"},
		},
		{
			name:  "hops exactly K succeeds",
			rules: []Rule{{From: "/a", To: "/b"}, {From: "/b", To: "/c"}},
			k:     2, p0: "/a",
			wantFin: "/c", wantHops: 2, wantChain: []string{"/a", "/b", "/c"},
		},
		{
			name:  "K+1th hop exceeds limit",
			rules: []Rule{{From: "/a", To: "/b"}, {From: "/b", To: "/c"}},
			k:     1, p0: "/a",
			wantErr: "hoplimit",
		},
		{
			name:  "K=0 with matching rule exceeds limit",
			rules: []Rule{{From: "/a", To: "/b"}},
			k:     0, p0: "/a",
			wantErr: "hoplimit",
		},
		{
			name:  "K=0 without rule succeeds",
			rules: []Rule{{From: "/a", To: "/b"}},
			k:     0, p0: "/x",
			wantFin: "/x", wantHops: 0, wantChain: []string{"/x"},
		},
		{
			name:  "hop limit precedes loop",
			rules: []Rule{{From: "/a", To: "/b"}, {From: "/b", To: "/a"}},
			k:     1, p0: "/a",
			wantErr: "hoplimit",
		},
		{
			name:  "loop to p0 found at step 2",
			rules: []Rule{{From: "/a", To: "/b"}, {From: "/b", To: "/a"}},
			k:     5, p0: "/a",
			wantErr: "loop", wantStep: 2,
		},
		{
			name:  "loop to intermediate found at step 3",
			rules: []Rule{{From: "/a", To: "/b"}, {From: "/b", To: "/c"}, {From: "/c", To: "/b"}},
			k:     10, p0: "/a",
			wantErr: "loop", wantStep: 3,
		},
		{
			name:  "self loop found at step 1",
			rules: []Rule{{From: "/a", To: "/a"}},
			k:     5, p0: "/a",
			wantErr: "loop", wantStep: 1,
		},
		{
			name:  "final stops chain",
			rules: []Rule{{From: "/old", To: "/new", Final: true}, {From: "/new", To: "/x"}},
			k:     3, p0: "/old/k",
			wantFin: "/new/k", wantHops: 1, wantChain: []string{"/old/k", "/new/k"},
		},
		{
			name:  "non-final continues through same target",
			rules: []Rule{{From: "/old", To: "/new"}, {From: "/new", To: "/x"}},
			k:     3, p0: "/old/k",
			wantFin: "/x/k", wantHops: 2, wantChain: []string{"/old/k", "/new/k", "/x/k"},
		},
		{
			name:  "K=0 final rule still exceeds limit",
			rules: []Rule{{From: "/old", To: "/new", Final: true}},
			k:     0, p0: "/old/k",
			wantErr: "hoplimit",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rs := mustSet(t, c.rules...)
			out, err := rs.Rewrite(c.p0, c.k)
			switch c.wantErr {
			case "":
				if err != nil {
					t.Fatalf("Rewrite(%q, %d) error: %v", c.p0, c.k, err)
				}
				if out.Final != c.wantFin || out.Hops != c.wantHops {
					t.Fatalf("Rewrite = (%q, %d), want (%q, %d)", out.Final, out.Hops, c.wantFin, c.wantHops)
				}
				if !reflect.DeepEqual(out.Chain, c.wantChain) {
					t.Fatalf("chain = %v, want %v", out.Chain, c.wantChain)
				}
				if out.Hops > c.k {
					t.Fatalf("hops %d > K %d", out.Hops, c.k)
				}
				seen := map[string]bool{}
				for _, p := range out.Chain {
					if seen[p] {
						t.Fatalf("chain has duplicate %q: %v", p, out.Chain)
					}
					seen[p] = true
				}
			case "hoplimit":
				var hl *HopLimitError
				if !errors.As(err, &hl) {
					t.Fatalf("err = %v, want HopLimitError", err)
				}
				if hl.Hops != c.k {
					t.Fatalf("HopLimitError.Hops = %d, want %d", hl.Hops, c.k)
				}
			case "loop":
				var lp *LoopError
				if !errors.As(err, &lp) {
					t.Fatalf("err = %v, want LoopError", err)
				}
				if lp.Step != c.wantStep {
					t.Fatalf("loop found at step %d, want step %d", lp.Step, c.wantStep)
				}
			}
		})
	}
}

func TestNewRuleSetValidation(t *testing.T) {
	cases := []struct {
		name  string
		rules []Rule
		ok    bool
	}{
		{"empty ok", nil, true},
		{"ok", []Rule{{From: "/a", To: "/b"}}, true},
		{"from not normalized", []Rule{{From: "/a/", To: "/b"}}, false},
		{"to not normalized", []Rule{{From: "/a", To: "//b"}}, false},
		{"from relative", []Rule{{From: "a", To: "/b"}}, false},
		{"duplicate from", []Rule{{From: "/a", To: "/b"}, {From: "/a", To: "/c"}}, false},
		{"same to ok", []Rule{{From: "/a", To: "/b"}, {From: "/c", To: "/b"}}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := NewRuleSet(c.rules)
			if (err == nil) != c.ok {
				t.Fatalf("NewRuleSet ok=%v, want %v (err=%v)", err == nil, c.ok, err)
			}
		})
	}
}
