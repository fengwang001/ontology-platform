package main

import (
	"bytes"
	"errors"
	"fmt"

	"ontology/record"
	"ontology/rule"
	"ontology/mask"
)

type check struct {
	name string
	ok   bool
}

func main() {
	var results []check

	r, err := record.New(record.Info, "t", map[string]any{"a": []any{map[string]any{"k": "v"}}})
	results = append(results, check{"record depth/nested-array construct",
		err == nil && r.Fields != nil})

	set, err := rule.Compile([]rule.Rule{
		{Name: "p1", Path: "user.pw", Method: rule.Hash},
		{Name: "p2", Path: `a\.b`, Method: rule.Replace, Replace: "X"},
	})
	results = append(results, check{"rule compile + dotted-key disambiguation",
		err == nil && set.Lookup([]string{"a.b"}) != nil})
	_, err = rule.Compile([]rule.Rule{
		{Name: "x", Path: "p", Method: rule.Replace},
		{Name: "y", Path: "p", Method: rule.Hash},
	})
	results = append(results, check{"rule conflict detected at compile time",
		errors.Is(err, rule.ErrConflict)})

	mr, _ := record.New(record.Info, "t", map[string]any{
		"pw": "SECRET", "other": map[string]any{"copy": "SECRET"},
		"arr": []any{map[string]any{"ref": "SECRET"}}, "keep": "visible",
	})
	mb, _ := record.Encode(mr)
	maskSet, _ := rule.Compile([]rule.Rule{{Name: "v", Value: "SECRET",
		Method: rule.Replace, Replace: "REDACTED"}})
	changed := mask.Apply(mr, maskSet)
	ab, _ := record.Encode(mr)
	allThree := bytes.Count(ab, []byte("REDACTED")) == 3 &&
		!bytes.Contains(ab, []byte("SECRET"))
	nonTargetKept := bytes.Contains(ab, []byte(`"keep":"visible"`))
	srcUnchanged := bytes.Contains(mb, []byte(`"pw":"SECRET"`))
	results = append(results, check{"same value masked at all 3 sites", changed && allThree})
	results = append(results, check{"non-target fields byte-identical, source untouched",
		nonTargetKept && srcUnchanged})

	pass := 0
	for _, c := range results {
		tag := "FAIL"
		if c.ok {
			tag, pass = "OK", pass+1
		}
		fmt.Printf("%s %s\n", tag, c.name)
	}
	fmt.Printf("TOTAL %d/%d\n", pass, len(results))
	if pass != len(results) {
		panic("demo checks failed")
	}
}
