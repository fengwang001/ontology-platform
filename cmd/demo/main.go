package main

import (
	"fmt"

	"ontology/snapshot"
	"ontology/store"
)

type check struct {
	name string
	ok   bool
	detail string
}

func main() {
	var results []check

	st := store.New()
	st.Put("k", []byte("old"))
	snap := snapshot.Take(st)
	st.Put("k", []byte("new"))
	v, _, _ := snap.Get("k")
	results = append(results, check{"overwritten key keeps snapshot-old value", string(v) == "old", ""})
	snap.Close()

	st2 := store.New()
	st2.Put("a", []byte("0"))
	oldSnap := snapshot.Take(st2)
	st2.Put("a", []byte("1"))
	newSnap := snapshot.Take(st2)
	st2.Put("a", []byte("2"))
	newSnap.Close()
	mid := st2.Retained() > 0
	oldSnap.Close()
	results = append(results, check{"retained values freed only after both snapshots close", mid && st2.Retained() == 0, ""})

	pass := 0
	for _, r := range results {
		status := "FAIL"
		if r.ok {
			status, pass = "OK", pass+1
		}
		fmt.Printf("%s %s\n", status, r.name)
	}
	fmt.Printf("TOTAL %d/%d\n", pass, len(results))
	if pass != len(results) {
		panic("checks failed")
	}
}
