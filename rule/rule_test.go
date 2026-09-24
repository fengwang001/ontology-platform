package rule_test

import (
	"errors"
	"strings"
	"testing"

	"ontology/mask"
	"ontology/record"
	"ontology/rule"
)

func compile(t *testing.T, rules ...rule.Rule) *rule.Set {
	t.Helper()
	s, err := rule.Compile(rules)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return s
}

func masked(t *testing.T, fields map[string]any, rules ...rule.Rule) map[string]any {
	t.Helper()
	rec, err := record.New("t", record.LevelInfo, fields)
	if err != nil {
		t.Fatal(err)
	}
	return mask.Apply(rec, compile(t, rules...)).Fields
}

func TestCompile(t *testing.T) {
	cases := []struct {
		name  string
		rules []rule.Rule
		want  error
	}{
		{"replace-vs-hash-conflict", []rule.Rule{
			{Path: "a.b", Action: rule.ActionReplace},
			{Path: "a.b", Action: rule.ActionHash},
		}, rule.ErrConflict},
		{"quoted-vs-nested-no-conflict", []rule.Rule{
			{Path: `"a.b"`, Action: rule.ActionReplace},
			{Path: "a.b", Action: rule.ActionHash},
		}, nil},
		{"identical-duplicate-ok", []rule.Rule{
			{Path: "a.b", Action: rule.ActionHash},
			{Path: "a.b", Action: rule.ActionHash},
		}, nil},
		{"bad-pattern", []rule.Rule{{Pattern: "[", Action: rule.ActionHash}}, nil},
		{"empty-rule", []rule.Rule{{Action: rule.ActionHash}}, nil},
		{"unterminated-quote", []rule.Rule{{Path: `"a.b`, Action: rule.ActionHash}}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := rule.Compile(tc.rules)
			if tc.want != nil {
				if !errors.Is(err, tc.want) {
					t.Fatalf("got %v, want %v", err, tc.want)
				}
				if !strings.Contains(err.Error(), "rule #0") || !strings.Contains(err.Error(), "rule #1") {
					t.Fatalf("conflict error must name both rules: %v", err)
				}
				return
			}
			if tc.name == "identical-duplicate-ok" || tc.name == "quoted-vs-nested-no-conflict" {
				if err != nil {
					t.Fatalf("unexpected: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestPathSemantics(t *testing.T) {
	both := map[string]any{"a.b": "literal", "a": map[string]any{"b": "nested"}}
	cases := []struct {
		name   string
		fields map[string]any
		path   string
		get    func(map[string]any) any
		want   any
	}{
		{"nested-hit", map[string]any{"a": map[string]any{"b": map[string]any{"c": "secret"}}},
			"a.b.c", func(f map[string]any) any { return f["a"].(map[string]any)["b"].(map[string]any)["c"] }, "***"},
		{"dot-name-rule-misses-literal", both,
			"a.b", func(f map[string]any) any { return f["a.b"] }, "literal"},
		{"dot-name-rule-hits-nested", both,
			"a.b", func(f map[string]any) any { return f["a"].(map[string]any)["b"] }, "***"},
		{"quoted-rule-hits-literal", both,
			`"a.b"`, func(f map[string]any) any { return f["a.b"] }, "***"},
		{"quoted-rule-misses-nested", both,
			`"a.b"`, func(f map[string]any) any { return f["a"].(map[string]any)["b"] }, "nested"},
		{"non-map-intermediate", map[string]any{"a": "str"},
			"a.b", func(f map[string]any) any { return f["a"] }, "str"},
		{"missing-path", map[string]any{"x": "y"},
			"a.b", func(f map[string]any) any { return f["x"] }, "y"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := masked(t, tc.fields, rule.Rule{Path: tc.path, Action: rule.ActionReplace})
			if v := tc.get(got); v != tc.want {
				t.Fatalf("got %v, want %v", v, tc.want)
			}
		})
	}
}

func TestMaskActions(t *testing.T) {
	hashRule := rule.Rule{Path: "v", Action: rule.ActionHash}
	got1 := masked(t, map[string]any{"v": "secret"}, hashRule)["v"].(string)
	got2 := masked(t, map[string]any{"v": "secret"}, hashRule)["v"].(string)
	got3 := masked(t, map[string]any{"v": "other"}, hashRule)["v"].(string)
	if got1 != got2 || got1 == got3 || got1 == "secret" || !strings.HasPrefix(got1, "sha256:") {
		t.Fatalf("hash invariants broken: %q %q %q", got1, got2, got3)
	}
	cases := []struct {
		name  string
		value string
		rule  rule.Rule
		want  string
	}{
		{"replace-fixed", "a-much-longer-secret", rule.Rule{Path: "v", Action: rule.ActionReplace}, "***"},
		{"replace-custom", "x", rule.Rule{Path: "v", Action: rule.ActionReplace, Param: "REDACTED"}, "REDACTED"},
		{"truncate", "abcdefg", rule.Rule{Path: "v", Action: rule.ActionTruncate, Param: "3"}, "abc" + mask.TruncatedMark},
		{"truncate-short-untouched", "ab", rule.Rule{Path: "v", Action: rule.ActionTruncate, Param: "5"}, "ab"},
		{"truncate-runes", "你好世界啊", rule.Rule{Path: "v", Action: rule.ActionTruncate, Param: "2"}, "你好" + mask.TruncatedMark},
		{"truncate-empty", "", rule.Rule{Path: "v", Action: rule.ActionTruncate, Param: "3"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := masked(t, map[string]any{"v": tc.value}, tc.rule)["v"]; got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMaskEverywhereAndNonTarget(t *testing.T) {
	fields := map[string]any{
		"user":  map[string]any{"token": "SECRET-1", "name": "amy"},
		"tags":  []any{"SECRET-1", "ok"},
		"token": "SECRET-1",
		"n":     42.0,
	}
	rec, _ := record.New("t", record.LevelInfo, fields)
	before, _ := rec.Encode()
	out := mask.Apply(rec, compile(t, rule.Rule{Pattern: "^SECRET-", Action: rule.ActionHash}))
	u := out.Fields["user"].(map[string]any)
	tok, _ := u["token"].(string)
	tag, _ := out.Fields["tags"].([]any)[0].(string)
	top, _ := out.Fields["token"].(string)
	if !strings.HasPrefix(tok, "sha256:") || tok != tag || tok != top {
		t.Fatalf("same value not masked everywhere: %q %q %q", tok, tag, top)
	}
	if u["name"] != "amy" || out.Fields["tags"].([]any)[1] != "ok" || out.Fields["n"] != 42.0 {
		t.Fatal("non-target field changed")
	}
	after, _ := rec.Encode()
	if string(before) != string(after) {
		t.Fatal("input record mutated")
	}
}

func TestMatchCountBound(t *testing.T) {
	rules := make([]rule.Rule, 100)
	for i := range rules {
		rules[i] = rule.Rule{Path: "f" + strings.Repeat("x", 3) + string(rune('a'+i%26)) + strings.Repeat("y", i/26), Action: rule.ActionHash}
		if i == 0 {
			rules[i] = rule.Rule{Path: "f0", Action: rule.ActionHash}
		}
	}
	set := compile(t, rules...)
	rec, _ := record.New("t", record.LevelInfo, map[string]any{"f0": "a", "f1": "b", "f2": "c", "f3": "d", "f4": "e"})
	const recs = 100000
	for i := 0; i < recs; i++ {
		mask.Apply(rec, set)
	}
	const fieldsPerRec = 5
	if got, want := set.MatchCount(), int64(recs*fieldsPerRec); got != want {
		t.Fatalf("match count %d, want exactly %d (records*(fields))", got, want)
	}
	if set.MatchCount() > recs*(fieldsPerRec+1) {
		t.Fatalf("match count exceeds records*(fields+const)")
	}
}
