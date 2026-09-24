package rule_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"ontology/mask"
	"ontology/record"
	"ontology/rule"
)

func TestCompileAndConflict(t *testing.T) {
	cases := []struct {
		name    string
		rules   []rule.Rule
		wantErr error
	}{
		{"ok single", []rule.Rule{{Kind: rule.KindPath, Path: "a.b", Action: rule.ActionReplace, Name: "r1"}}, nil},
		{"same action twice", []rule.Rule{
			{Kind: rule.KindPath, Path: "a.b", Action: rule.ActionHash, Name: "r1"},
			{Kind: rule.KindPath, Path: "a.b", Action: rule.ActionHash, Name: "r2"},
		}, nil},
		{"replace vs hash", []rule.Rule{
			{Kind: rule.KindPath, Path: "secret", Action: rule.ActionReplace, Name: "rep"},
			{Kind: rule.KindPath, Path: "secret", Action: rule.ActionHash, Name: "hsh"},
		}, rule.ErrConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := rule.Compile(tc.rules)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err=%v want %v", err, tc.wantErr)
			}
			if tc.wantErr == rule.ErrConflict &&
				!(strings.Contains(err.Error(), "rep") && strings.Contains(err.Error(), "hsh")) {
				t.Fatalf("conflict error must name both rules: %v", err)
			}
		})
	}
}

func TestPathSemantics(t *testing.T) {
	set, err := rule.Compile([]rule.Rule{
		{Kind: rule.KindPath, Path: "a.b", Action: rule.ActionReplace, Name: "p"},
		{Kind: rule.KindValue, Pattern: "^SENSITIVE$", Action: rule.ActionHash, Name: "v"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		fields record.Fields
		dig    func(f record.Fields) any
		want   any
	}{
		{"nested hit", record.Fields{"a": record.Fields{"b": "x", "keep": 1}},
			func(f record.Fields) any { return f["a"].(record.Fields)["b"] }, "***REDACTED***"},
		{"literal dotted key wins", record.Fields{"a.b": "lit", "a": record.Fields{"b": "nested"}},
			func(f record.Fields) any { return f["a.b"] }, "***REDACTED***"},
		{"mid layer not map", record.Fields{"a": "scalar"},
			func(f record.Fields) any { return f["a"] }, "scalar"},
		{"path missing", record.Fields{"x": 1},
			func(f record.Fields) any { return f["x"] }, int64(1)},
		{"value rule in array", record.Fields{"arr": []any{"SENSITIVE", "ok"}},
			func(f record.Fields) any { return f["arr"].([]any)[0] }, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := record.New(record.LevelInfo, "t", tc.fields)
			if err != nil {
				t.Fatal(err)
			}
			out, err := mask.New(set).Apply(r)
			if err != nil {
				t.Fatal(err)
			}
			got := tc.dig(out.Fields)
			if tc.want != nil && got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
			if tc.name == "value rule in array" {
				s, ok := got.(string)
				if !ok || s == "SENSITIVE" || len(s) < 4 {
					t.Fatalf("array sensitive not hashed: %v", got)
				}
			}
		})
	}
}

func TestMatchComplexityBound(t *testing.T) {
	var rules []rule.Rule
	for i := 0; i < 100; i++ {
		rules = append(rules, rule.Rule{Kind: rule.KindPath,
			Path: fmt.Sprintf("k%d.nested.x", i%10), Action: rule.ActionHash, Name: fmt.Sprintf("r%d", i)})
	}
	set, err := rule.Compile(rules)
	if err != nil {
		t.Fatal(err)
	}
	p := mask.New(set)
	for i := 0; i < 100000; i++ {
		r, _ := record.New(record.LevelInfo, "t", record.Fields{
			"k1": record.Fields{"nested": record.Fields{"x": "v", "y": "z"}}, "other": 2,
		})
		if _, err := p.Apply(r); err != nil {
			t.Fatal(err)
		}
	}
	const fieldCount = 4 // k1, x, y, other
	bound := int64(100000 * (fieldCount + 2))
	if got := set.Matches(); got > bound {
		t.Fatalf("matches=%d > bound %d", got, bound)
	}
}
