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

var checks []check

func add(name string, ok bool) { checks = append(checks, check{name, ok}) }

func main() {
	add("skeleton", true)

	cycle := map[string]record.Value{"self": record.M(nil)}
	cyc := record.M(cycle)
	cycle["self"] = cyc
	deepOK := (&record.Record{Fields: map[string]record.Value{
		"a": record.String(""),
		"b": record.A(record.M(map[string]record.Value{"c": record.String("")})),
	}}).Validate() == nil
	add("record: cycle rejected", errorsIs((&record.Record{Fields: map[string]record.Value{"x": cyc}}).Validate(), record.ErrCycle))
	add("record: depth exceeded rejected", makeDeep(record.MaxDepth+1).Validate() == record.ErrDepthExceeded)
	add("record: empty/nested/array valid", deepOK)
	_, conflict := rule.Compile([]rule.Rule{
		{Path: "user.token", Action: rule.Replace},
		{Path: "user.token", Action: rule.Hash},
	})
	add("rule: replace+hash conflict detected", errors.Is(conflict, rule.ErrConflict))

	set, _ := rule.Compile([]rule.Rule{
		{Path: "user.token", Action: rule.Replace},
		{Path: "user.hash", Action: rule.Hash},
		{Path: "user.note", Action: rule.Truncate, Keep: 3},
		{Path: "a.b", Action: rule.Replace},
		{Pattern: "SAME-SECRET", Action: rule.Replace},
	})
	rec := &record.Record{Fields: map[string]record.Value{
		"user": record.M(map[string]record.Value{
			"token": record.String("abcdef"), "hash": record.String("abcdef"),
			"note": record.String("123456789"),
		}),
		"a":      record.M(map[string]record.Value{"b": record.String("nested")}),
		"a.b":    record.String("dotted-key"),
		"x":      record.String("SAME-SECRET"),
		"y":      record.String("SAME-SECRET"),
		"arr":    record.A(record.String("SAME-SECRET")),
		"keepme": record.String("unchanged"),
		"n":      record.Number(42),
	}}
	masked := mask.Apply(rec, set)
	mf := masked.Fields
	user := mf["user"].Map
	threePlaces := mf["x"].Str == "***" && mf["y"].Str == "***" && mf["arr"].Arr[0].Str == "***"
	actionsOK := user["token"].Str == "***" &&
		user["hash"].Str != "abcdef" && user["hash"].Str == maskHash("abcdef") &&
		user["note"].Str == "123…"
	disambig := mf["a.b"].Str == "***" && mf["a"].Map["b"].Str == "nested"
	untouched := mf["keepme"].Str == "unchanged" && mf["n"].Num == 42 &&
		rec.Fields["keepme"].Str == "unchanged"
	add("mask: replace/hash/truncate invariants", actionsOK)
	add("mask: same value masked at 3 places", threePlaces)
	add("mask: dotted key vs nested path", disambig)
	add("mask: non-target fields byte-identical", untouched)

	fail := 0
	for _, c := range checks {
		tag := "OK"
		if !c.ok {
			tag, fail = "FAIL", fail+1
		}
		fmt.Printf("%s %s\n", tag, c.name)
	}
	fmt.Printf("TOTAL %d/%d pass\n", len(checks)-fail, len(checks))
	if fail > 0 {
		panic("checks failed")
	}
}

func errorsIs(err, target error) bool { return err != nil && errors.Is(err, target) }

func makeDeep(d int) *record.Record {
	v := record.M(map[string]record.Value{})
	for i := 1; i < d; i++ {
		v = record.M(map[string]record.Value{"n": v})
	}
	return &record.Record{Fields: map[string]record.Value{"root": v}}
}

func maskHash(s string) string {
	h := newFNV()
	h.Write([]byte(s))
	return fmt.Sprintf("%016x", h.Sum64())
}
