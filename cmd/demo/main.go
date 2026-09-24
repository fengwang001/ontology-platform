package main

import (
	"errors"
	"fmt"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/fold"
)

func report(name string, ok bool) {
	if ok {
		fmt.Println("OK", name)
	} else {
		fmt.Println("FAIL", name)
	}
}

func main() {
	samples := []string{"STRASSE", "strasse", "straße", "İ"}
	folder := fold.New()
	results := make([]string, len(samples))
	for i, sample := range samples {
		results[i] = folder.Fold(sample)
	}
	idempotent := fold.Fold(fold.Fold("ß")) == fold.Fold("ß") &&
		fold.Fold(fold.Fold("İ")) == fold.Fold("İ")
	report("fold idempotent", idempotent)
	for i, sample := range samples {
		report("fold "+sample+" => "+results[i], results[i] == folder.Fold(sample))
	}
	table := api.NewTable(8, 3)
	_ = table.Put("Beta", 2)
	_ = table.Put("Alpha", 1)
	alpha, _ := table.Get("ALPHA")
	_ = table.Put("ALPHA", 2)
	report("hit and original key", alpha == 1 && table.Keys()[0] == "Alpha")
	report("keys sorted by folded key", reflect.DeepEqual(table.Keys(), []string{"Alpha", "Beta"}))
	report("three sentinel errors", errors.Is(table.Put("", nil), api.ErrEmptyKey) &&
		errors.Is(table.Put("ABCDEFGHI", nil), api.ErrKeyTooLong) &&
		errors.Is(func() error { third := api.NewTable(8, 1); _ = third.Put("a", 1); return third.Put("b", 2) }(), api.ErrTableFull))
	report("self-check after rejection", table.SelfCheck() == nil)
	var wait sync.WaitGroup
	queryResults := make([][]string, 8)
	for i := range queryResults {
		wait.Add(1)
		go func(i int) { defer wait.Done(); queryResults[i] = table.Keys() }(i)
	}
	wait.Wait()
	same := true
	for _, result := range queryResults[1:] {
		if !reflect.DeepEqual(queryResults[0], result) {
			same = false
		}
	}
	report("concurrent queries identical", same)
}
