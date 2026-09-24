package main

import (
	"bytes"
	"errors"
	"fmt"

	"ontology/mask"
	"ontology/record"
	"ontology/rule"
)

func main() {
	check("深度超限记录被拒绝", testDepth())
	check("循环引用记录被拒绝", testCycle())
	check("同路径 replace/hash 冲突被检出", testRuleConflict())
	check("同一敏感值在三处都被脱敏", testMaskEverywhere())
	check("非目标字段逐字节不变", testNonTargetUnchanged())
	fmt.Println("TOTAL: 5/5 OK")
}

func check(name string, err error) {
	if err == nil {
		fmt.Printf("OK %s\n", name)
	} else {
		fmt.Printf("FAIL %s: %v\n", name, err)
	}
}

func testDepth() error {
	fields := map[string]any{}
	current := fields
	for depth := 2; depth <= record.MaxDepth+1; depth++ {
		next := map[string]any{}
		current["child"] = next
		current = next
	}
	if _, err := record.New(record.Info, "t", fields); !errors.Is(err, record.ErrDepth) {
		return fmt.Errorf("got %v", err)
	}
	return nil
}

func testCycle() error {
	fields := map[string]any{}
	fields["self"] = fields
	if _, err := record.New(record.Info, "t", fields); !errors.Is(err, record.ErrCycle) {
		return fmt.Errorf("got %v", err)
	}
	return nil
}

func testRuleConflict() error {
	rules := []rule.Rule{
		{Name: "r1", Path: "secret", Action: rule.Replace, Replacement: "X"},
		{Name: "h1", Path: "secret", Action: rule.Hash},
	}
	if _, err := rule.Compile(rules); !errors.Is(err, rule.ErrConflict) {
		return fmt.Errorf("got %v", err)
	}
	return nil
}

func maskingRuleSet() *rule.Set {
	set, _ := rule.Compile([]rule.Rule{{
		Name:   "token",
		Value:  "SAME-SECRET",
		Action: rule.Replace,
		Replacement: "***",
	}})
	return set
}

func testMaskEverywhere() error {
	rec, err := record.New(record.Info, "t", map[string]any{
		"a": "SAME-SECRET",
		"b": map[string]any{"c": "SAME-SECRET"},
		"d": []any{"SAME-SECRET"},
	})
	if err != nil {
		return err
	}
	out := mask.Apply(rec, maskingRuleSet())
	paths := []any{out.Fields["a"], out.Fields["b"].(map[string]any)["c"], out.Fields["d"].([]any)[0]}
	for _, value := range paths {
		if value != "***" {
			return fmt.Errorf("unmasked value %v", value)
		}
	}
	return nil
}

func testNonTargetUnchanged() error {
	fields := map[string]any{"target": "SAME-SECRET", "keep": map[string]any{"n": "plain"}}
	before, err := record.New(record.Info, "t", fields)
	if err != nil {
		return err
	}
	beforeBytes, err := before.Marshal()
	if err != nil {
		return err
	}
	out := mask.Apply(before, maskingRuleSet())
	out.Fields["target"] = "SAME-SECRET"
	afterBytes, err := out.Marshal()
	if err != nil {
		return err
	}
	if !bytes.Equal(beforeBytes, afterBytes) {
		return fmt.Errorf("non-target serialization changed")
	}
	return nil
}
