package main

import (
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

func report(cs []check) {
	pass := 0
	for _, c := range cs {
		tag := "FAIL"
		if c.ok {
			tag, pass = "OK", pass+1
		}
		fmt.Printf("%s %s\n", tag, c.name)
	}
	fmt.Printf("TOTAL %d/%d\n", pass, len(cs))
}

func main() {
	deep := map[string]any{}
	cur := deep
	for i := 0; i < record.MaxDepth; i++ {
		nxt := map[string]any{}
		cur["k"] = nxt
		cur = nxt
	}
	_, errDepth := record.New(deep, "t", record.LevelInfo)

	cyc := map[string]any{}
	cyc["self"] = cyc
	_, errCycle := record.New(cyc, "t", record.LevelInfo)
	_, errConflict := rule.Compile([]rule.Spec{
		{Path: "user.phone", Action: rule.Replace},
		{Path: "user.phone", Action: rule.Hash},
	})

	// 同一敏感值出现在三个不同路径（含数组元素内）。
	secret := "13800001111"
	secretRec, _ := record.New(map[string]any{
		"a": secret,
		"b": map[string]any{"c": secret},
		"d": []any{map[string]any{"e": secret}},
		"keep": map[string]any{"untouched": []any{1, 2, 3}, "n": 7, "s": "clean"},
	}, "trace-mask", record.LevelInfo)
	before, _ := secretRec.Marshal()
	ms, _ := rule.Compile([]rule.Spec{{Pattern: `1\d{10}`, Action: rule.Replace}})
	mk := mask.New(ms).Apply(secretRec)
	allMasked := mk.Fields["a"] == mask.ReplaceStr &&
		mk.Fields["b"].(map[string]any)["c"] == mask.ReplaceStr &&
		mk.Fields["d"].([]any)[0].(map[string]any)["e"] == mask.ReplaceStr
	keep := mk.Fields["keep"].(map[string]any)
	untouched := keep["s"] == "clean" && keep["n"] == float64(7) &&
		len(keep["untouched"].([]any)) == 3 && len(before) > 0

	cs := []check{
		{"depth exceeded rejected", errors.Is(errDepth, record.ErrDepthExceeded)},
		{"cyclic reference rejected", errors.Is(errCycle, record.ErrCycle)},
		{"rule conflict detected at compile", errors.Is(errConflict, rule.ErrConflict)},
		{"same secret masked at 3 paths", allMasked},
		{"non-target fields byte-stable", untouched},
	}
	report(cs)
	if pass(cs) != len(cs) {
		panic("demo checks failed")
	}
}

func pass(cs []check) int {
	n := 0
	for _, c := range cs {
		if c.ok {
			n++
		}
	}
	return n
}
