package main

import (
	"fmt"

	"ontology/snapshot"
	"ontology/store"
)

type result struct {
	name   string
	ok     bool
	detail string
}

func (r result) print() {
	tag := "OK"
	if !r.ok {
		tag = "FAIL"
	}
	fmt.Printf("%s %s %s\n", tag, r.name, r.detail)
}

func main() {
	st := store.New()
	mgr := snapshot.NewManager(st)
	for i := 0; i < 100; i++ {
		st.Put(keyOf(i), []byte("v0"))
	}
	results := []result{
		checkCOW(st, mgr),
		checkOverlap(st, mgr),
	}
	pass := 0
	for _, r := range results {
		r.print()
		if r.ok {
			pass++
		}
	}
	fmt.Printf("TOTAL %d/%d\n", pass, len(results))
	if pass != len(results) {
		panic("demo failed")
	}
}

func checkCOW(st *store.Store, mgr *snapshot.Manager) result {
	snap := mgr.Open()
	keys, _ := mgr.Keys(snap, 1)
	for i := range keys {
		if i == 49 {
			mgr.Put(keys[79], []byte("v1"))
		}
	}
	c, _ := mgr.Read(snap, keys[79])
	cur := st.Current(keys[79])
	ok := string(c.Value) == "v0" && string(cur.Value) == "v1"
	mgr.Close(snap)
	return result{name: "cow-old-value", ok: ok, detail: "key80 stays snapshot value"}
}

func checkOverlap(st *store.Store, mgr *snapshot.Manager) result {
	a := mgr.Open()
	b := mgr.Open()
	mgr.Put("shared-overlap", []byte("x"))
	first := a.Retained()
	mgr.Close(b)
	mid := a.Retained()
	mgr.Close(a)
	after := a.Retained()
	ok := first == 1 && mid == 1 && after == 0
	return result{name: "overlap-release", ok: ok, detail: "retained freed only after both close"}
}

func keyOf(i int) string {
	return fmt.Sprintf("key-%04d", i)
}
