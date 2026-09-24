package main

import (
	"fmt"
	"os"
	"reflect"

	"ontology/clog"
	"ontology/stg"
)

var failed bool

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK  " + name)
	} else {
		failed = true
		fmt.Println("FAIL " + name)
	}
}

func main() {
	// stg: ordered staging, in-place overwrite, wholesale drain.
	s := stg.New()
	s.Put("k1", "a")
	s.Put("k2", "b")
	s.Put("k1", "c")
	s.Del("k3")
	ops := s.Take()
	ok := s.Len() == 0 && len(ops) == 3 &&
		ops[0] == (stg.Change{Key: "k1", Val: "c"}) &&
		ops[1] == (stg.Change{Key: "k2", Val: "b"}) &&
		ops[2] == (stg.Change{Key: "k3", Del: true})
	check("stg: ordered stage, overwrite, drain", ok)

	// clog: crash between phases leaves view untouched; Recover adjudicates.
	l := clog.New()
	l.Commit([]stg.Change{{Key: "k1", Val: "a"}})
	l.CommitCrash([]stg.Change{{Key: "k1", Val: "c"}, {Key: "k2", Del: true}})
	mid := l.View()
	rec := l.Recover()
	es := l.Entries()
	ok = reflect.DeepEqual(mid, map[string]string{"k1": "a"}) &&
		rec == 2 && l.Recover() == 0 && len(es) == 2 &&
		es[0].State == clog.Finalized && es[1].State == clog.Discarded &&
		reflect.DeepEqual(l.View(), map[string]string{"k1": "a"})
	check("clog: two-phase, crash view-untouched, recover idempotent", ok)

	if failed {
		os.Exit(1)
	}
}
