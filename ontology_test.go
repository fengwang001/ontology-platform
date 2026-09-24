package ontology_test

import (
	"errors"
	"fmt"
	"testing"

	"ontology/mask"
	"ontology/record"
	"ontology/rule"
)

func TestRecordTable(t *testing.T) {
	deep := map[string]any{}
	cursor := deep
	for range record.MaxDepth + 1 {
		child := map[string]any{}
		cursor["child"] = child
		cursor = child
	}
	cyclic := map[string]any{}
	cyclic["self"] = cyclic
	encoded, err := record.New(record.Info, "t", map[string]any{"x": []any{map[string]any{"y": ""}}})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := encoded.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name   string
		level  record.Level
		trace  string
		fields map[string]any
		want   error
	}{
		{"empty", record.Info, "", map[string]any{}, nil},
		{"depth one", record.Info, "t", map[string]any{"a": "x"}, nil},
		{"array nested map", record.Info, "t", map[string]any{"a": []any{map[string]any{"b": "x"}}}, nil},
		{"depth exceeded", record.Info, "t", deep, record.ErrDepth},
		{"cyclic map", record.Info, "t", cyclic, record.ErrCycle},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := record.New(tc.level, tc.trace, tc.fields)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			if tc.want == nil && got == nil {
				t.Fatal("record constructor returned nil")
			}
		})
	}

	decoded, err := record.Unmarshal(payload)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	second, err := decoded.Marshal()
	if err != nil || string(second) != string(payload) {
		t.Fatalf("round trip = %q, want %q (err=%v)", second, payload, err)
	}
}

func TestRuleTable(t *testing.T) {
	conflict := []rule.Rule{
		{Name: "r", Path: "secret", Action: rule.Replace, Replacement: "X"},
		{Name: "h", Path: "secret", Action: rule.Hash},
	}
	if _, err := rule.Compile(conflict); !errors.Is(err, rule.ErrConflict) {
		t.Fatalf("conflict error = %v", err)
	}

	set, err := rule.Compile([]rule.Rule{
		{Name: "literal", Path: "a.b", Action: rule.Replace, Replacement: "L"},
		{Name: "nested", Path: "a.b.c", Action: rule.Hash},
		{Name: "value", Value: "TOKEN-.+", Action: rule.Replace, Replacement: "V"},
	})
	if err != nil {
		t.Fatal(err)
	}
	rootRules, _ := set.RulesAt("")
	if _, ok := rootRules["a.b"]; !ok {
		t.Fatal("literal dotted key must be indexed at root")
	}
	childRules, _ := set.RulesAt("a.b")
	if _, ok := childRules["c"]; !ok {
		t.Fatal("nested suffix must be indexed under parent")
	}
	if matched := set.MatchValues("TOKEN-42"); len(matched) != 1 {
		t.Fatalf("value matches = %d, want 1", len(matched))
	}

	many := make([]rule.Rule, 100)
	for index := range many {
		many[index] = rule.Rule{Name: "r", Path: fmt.Sprintf("path%d", index), Action: rule.Hash}
	}
	compiledMany, err := rule.Compile(many)
	if err != nil {
		t.Fatal(err)
	}
	for range 100000 {
		compiledMany.RulesAt("")
	}
	if got := compiledMany.MatchCount(); got > 100000*3 {
		t.Fatalf("match count %d exceeds O(records*constant)", got)
	}
}

func TestMaskTable(t *testing.T) {
	valueSet, err := rule.Compile([]rule.Rule{{Name: "value", Value: "SAME", Action: rule.Replace, Replacement: "***"}})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := record.New(record.Info, "t", map[string]any{
		"a": "SAME", "b": map[string]any{"c": "SAME"}, "arr": []any{"SAME"},
		"keep": map[string]any{"n": "plain"},
	})
	if err != nil {
		t.Fatal(err)
	}
	out := mask.Apply(rec, valueSet)
	cases := []struct {
		name string
		got  any
		want any
	}{
		{"path one", out.Fields["a"], "***"},
		{"path two", out.Fields["b"].(map[string]any)["c"], "***"},
		{"array item", out.Fields["arr"].([]any)[0], "***"},
		{"non-target", out.Fields["keep"], map[string]any{"n": "plain"}},
		{"original untouched", rec.Fields["a"], "SAME"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if fmt.Sprint(tc.got) != fmt.Sprint(tc.want) {
				t.Fatalf("got %v, want %v", tc.got, tc.want)
			}
		})
	}

	pathSet, err := rule.Compile([]rule.Rule{
		{Name: "literal", Path: "a.b", Action: rule.Replace, Replacement: "L"},
		{Name: "truncate", Path: "nested.text", Action: rule.Truncate, Keep: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	pathRecord, err := record.New(record.Info, "t", map[string]any{
		"a.b": "literal", "a": map[string]any{"b": "nested"},
		"nested": map[string]any{"text": "abcdef", "missing": map[string]any{"x": "y"}},
		"notmap": "abc", "empty": "",
		"arrmaps": []any{map[string]any{"b": "deep"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	pathOut := mask.Apply(pathRecord, pathSet)
	pathCases := []struct {
		name string
		got  any
		want any
	}{
		{"literal dotted wins", pathOut.Fields["a.b"], "L"},
		{"literal shadows nested interpretation", pathOut.Fields["a"], map[string]any{"b": "nested"}},
		{"truncation", pathOut.Fields["nested"].(map[string]any)["text"], "ab\u2026<truncated>"},
		{"missing path", pathOut.Fields["nested"].(map[string]any)["missing"], map[string]any{"x": "y"}},
		{"non map layer", pathOut.Fields["notmap"], "abc"},
		{"empty string", pathOut.Fields["empty"], ""},
		{"array map prefix", pathOut.Fields["arrmaps"].([]any)[0], map[string]any{"b": "deep"}},
	}
	for _, tc := range pathCases {
		t.Run(tc.name, func(t *testing.T) {
			if fmt.Sprint(tc.got) != fmt.Sprint(tc.want) {
				t.Fatalf("got %v, want %v", tc.got, tc.want)
			}
		})
	}

	hashSet, _ := rule.Compile([]rule.Rule{{Name: "h", Path: "secret", Action: rule.Hash}})
	hashRecord, _ := record.New(record.Info, "t", map[string]any{"secret": "sensitive"})
	first := mask.Apply(hashRecord, hashSet).Fields["secret"]
	second := mask.Apply(hashRecord, hashSet).Fields["secret"]
	if first != second || first == "sensitive" {
		t.Fatalf("hash invariant failed: %v", first)
	}
}
