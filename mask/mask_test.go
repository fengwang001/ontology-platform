package mask_test

import (
	"strings"
	"testing"

	"ontology/mask"
	"ontology/record"
	"ontology/rule"
)

func mustCompile(t *testing.T, rs []rule.Rule) *rule.Set {
	t.Helper()
	s, err := rule.Compile(rs)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestTransforms(t *testing.T) {
	set := mustCompile(t, []rule.Rule{
		{Path: "rp", Action: rule.Replace},
		{Path: "hp", Action: rule.Hash},
		{Path: "tp", Action: rule.Truncate, Keep: 3},
		{Path: "short", Action: rule.Truncate, Keep: 10},
	})
	mk := mask.New(set)

	rec, err := record.New(record.Info, "t", record.Fields{
		"rp":    "secret-value",
		"hp":    "secret-value",
		"tp":    "abcdefghij",
		"short": "ab",
		"keep":  "untouched",
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := mk.Apply(rec)
	if err != nil {
		t.Fatal(err)
	}

	h1 := mask.Transform("x", rule.BoundAction{Action: rule.Hash})
	h2 := mask.Transform("x", rule.BoundAction{Action: rule.Hash})
	hOther := mask.Transform("y", rule.BoundAction{Action: rule.Hash})
	cases := []struct {
		name string
		got  any
		ok   bool
	}{
		{"replace fixed token", out.Fields["rp"], out.Fields["rp"] == "***"},
		{"hash not original", out.Fields["hp"], out.Fields["hp"] != "secret-value"},
		{"hash deterministic", h1, h1 == h2},
		{"hash distinct inputs", h1, h1 != hOther},
		{"truncate keeps N + marker", out.Fields["tp"], out.Fields["tp"] == "abc\u2026(truncated)"},
		{"truncate short unchanged", out.Fields["short"], out.Fields["short"] == "ab"},
		{"non target unchanged", out.Fields["keep"], out.Fields["keep"] == "untouched"},
		{"original not mutated", rec.Fields["rp"], rec.Fields["rp"] == "secret-value"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.ok {
				t.Fatalf("got %v", tc.got)
			}
		})
	}
}

func TestSameValueEverywhereMasked(t *testing.T) {
	// One sensitive value appears at three distinct locations, including an
	// array element; a value-pattern rule must catch all of them.
	set := mustCompile(t, []rule.Rule{{Pattern: "SENSITIVE", Action: rule.Replace}})
	mk := mask.New(set)
	rec, _ := record.New(record.Info, "t", record.Fields{
		"a":   "SENSITIVE-1",
		"b":   record.Fields{"c": "xx SENSITIVE xx"},
		"arr": []any{"SENSITIVE-2", "safe"},
		"ok":  "safe",
	})
	out, err := mk.Apply(rec)
	if err != nil {
		t.Fatal(err)
	}
	left := []string{}
	record.WalkStrings(out.Fields, func(_ []record.Seg, v string) {
		if strings.Contains(v, "SENSITIVE") {
			left = append(left, v)
		}
	})
	if len(left) != 0 {
		t.Fatalf("sensitive value survived at %v", left)
	}
	if out.Fields["ok"] != "safe" {
		t.Fatalf("non-target field changed: %v", out.Fields["ok"])
	}
}

func TestNonTargetsByteIdentical(t *testing.T) {
	set := mustCompile(t, []rule.Rule{{Path: "secret", Action: rule.Hash}})
	mk := mask.New(set)
	rec, _ := record.New(record.Info, "t", record.Fields{
		"secret": "shh",
		"nested": record.Fields{"a": "x", "b": []any{int64(1), true, nil, 1.5}},
		"keep":   "same",
	})
	before, _ := rec.Encode()
	out, err := mk.Apply(rec)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := out.Encode()
	// The only byte differences must be inside the secret value: replacing
	// `"secret":"shh"` with the hash token preserves every other byte.
	old := `"secret":"shh"`
	neu := `"secret":"` + out.Fields["secret"].(string) + `"`
	want := strings.Replace(string(before), old, neu, 1)
	if string(after) != want {
		t.Fatalf("non-target bytes changed:\nbefore %s\nafter  %s\nwant   %s", before, after, want)
	}
}

func TestPathEdgeCases(t *testing.T) {
	// Rule targets a.b (nested). Record instead has a literal key "a.b" and a
	// non-map at "a" in variants: none of those must be masked.
	set := mustCompile(t, []rule.Rule{{Path: "a.b", Action: rule.Replace}})
	mk := mask.New(set)
	cases := []record.Fields{
		{"a.b": "v"},                   // literal-dot key, not nested path
		{"a": "not-a-map"},             // mid-path type mismatch
		{"x": record.Fields{"b": "v"}}, // path absent
	}
	for i, f := range cases {
		rec, _ := record.New(record.Info, "t", f)
		out, err := mk.Apply(rec)
		if err != nil {
			t.Fatal(err)
		}
		record.WalkStrings(out.Fields, func(p []record.Seg, v string) {
			if v == "***" {
				t.Fatalf("case %d: non-matching path %v got masked", i, p)
			}
		})
	}
	// Literal-dot rule must match the literal key.
	litSet := mustCompile(t, []rule.Rule{{Path: `a\.b`, Action: rule.Replace}})
	out, _ := mask.New(litSet).Apply(mustRec(record.Fields{"a.b": "v"}))
	if out.Fields["a.b"] != "***" {
		t.Fatalf("literal-dot key not masked: %v", out.Fields)
	}
}

func mustRec(f record.Fields) *record.Record {
	r, _ := record.New(record.Info, "t", f)
	return r
}
