package rule_test

import (
	"errors"
	"fmt"
	"testing"

	"ontology/record"
	"ontology/rule"
)

func TestParsePath(t *testing.T) {
	cases := []struct {
		in      string
		keys    []string
		indexes []int
		wantErr error
	}{
		{"a.b", []string{"a", "b"}, []int{-1, -1}, nil},
		{`a\.b`, []string{"a.b"}, []int{-1}, nil},
		{`a\\.b`, []string{`a\`, "b"}, []int{-1, -1}, nil},
		{"a[2].b", []string{"a", "", "b"}, []int{-1, 2, -1}, nil},
		{"a\\", nil, nil, rule.ErrInvalidPath},
		{"a[x]", nil, nil, rule.ErrInvalidPath},
		{"a[", nil, nil, rule.ErrInvalidPath},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			segs, err := rule.ParsePath(tc.in)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err got %v want %v", err, tc.wantErr)
			}
			if err != nil {
				return
			}
			if len(segs) != len(tc.keys) {
				t.Fatalf("seg count %d want %d (%v)", len(segs), len(tc.keys), segs)
			}
			for i, s := range segs {
				if s.Key != tc.keys[i] || s.Index != tc.indexes[i] {
					t.Fatalf("seg %d = %+v want key=%q idx=%d", i, s, tc.keys[i], tc.indexes[i])
				}
			}
		})
	}
}

func TestCompileConflictAndLookup(t *testing.T) {
	cases := []struct {
		name    string
		rules   []rule.Rule
		wantErr error
	}{
		{"ok single", []rule.Rule{{Path: "a.b", Action: rule.Hash}}, nil},
		{"same action twice", []rule.Rule{{Path: "a", Action: rule.Replace}, {Path: "a", Action: rule.Replace}}, nil},
		{"conflict", []rule.Rule{{Path: "a.b", Action: rule.Replace}, {Path: "a.b", Action: rule.Hash}}, rule.ErrConflict},
		{"literal dot distinct", []rule.Rule{{Path: `a\.b`, Action: rule.Hash}, {Path: "a.b", Action: rule.Replace}}, nil},
		{"bad pattern", []rule.Rule{{Pattern: "(", Action: rule.Hash}}, rule.ErrInvalidPattern},
		{"unknown action", []rule.Rule{{Path: "a", Action: "wipe"}}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			set, err := rule.Compile(tc.rules)
			if tc.name == "unknown action" {
				if err == nil {
					t.Fatal("want error for unknown action")
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("got %v want %v", err, tc.wantErr)
			}
			if err != nil {
				return
			}
			segs, _ := rule.ParsePath(tc.rules[0].Path)
			if len(segs) > 0 {
				if _, ok := set.Lookup(segs); !ok {
					t.Fatalf("compiled path %q not found", tc.rules[0].Path)
				}
				if _, ok := set.Lookup([]record.Seg{{Key: "missing", Index: -1}}); ok {
					t.Fatal("missing path unexpectedly matched")
				}
			}
		})
	}
}

func TestMatchCountBound(t *testing.T) {
	rules := make([]rule.Rule, 100)
	for i := range rules {
		rules[i] = rule.Rule{Path: fmt.Sprintf("f%d", i), Action: rule.Hash}
	}
	set, err := rule.Compile(rules)
	if err != nil {
		t.Fatal(err)
	}
	const records = 100000
	const leaves = 4
	segs := []record.Seg{{Key: "f0", Index: -1}}
	for i := 0; i < records*leaves; i++ {
		set.Lookup(segs)
	}
	got := set.MatchCount()
	bound := uint64(records * (leaves + 8))
	if got > bound {
		t.Fatalf("match count %d exceeds bound %d (would be ~%d with per-rule scan)",
			got, bound, uint64(records)*100*leaves)
	}
}

func TestPatterns(t *testing.T) {
	set, err := rule.Compile([]rule.Rule{{Pattern: `\d{3}-\d{4}`, Action: rule.Replace}})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		v    string
		want bool
	}{
		{"call 555-1234 now", true},
		{"no digits here", false},
	}
	for _, tc := range cases {
		_, ok := set.MatchPattern(tc.v)
		if ok != tc.want {
			t.Fatalf("MatchPattern(%q)=%v want %v", tc.v, ok, tc.want)
		}
	}
}
