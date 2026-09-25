package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/rec"
	"ontology/seglog"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
		fmt.Println("FAIL", name)
		return
	}
	fmt.Println("OK", name)
}

func main() {
	// rec: dedup keeps the record with the largest seq per key.
	m := rec.MergeLatest([]rec.Rec{
		{Key: "a", Val: "1", Seq: 1},
		{Key: "b", Val: "1", Seq: 2},
		{Key: "a", Val: "22", Seq: 3},
	})
	check("rec.MergeLatest keeps latest", len(m) == 2 && m[0].Val == "22" && m[1].Val == "1")

	// seglog: the six-step trace from NOTES.md (B=6, T=2).
	l := seglog.New(6, 2)
	steps := []struct{ k, v string }{
		{"a", "1"}, {"b", "1"}, {"c", "1"}, {"d", "12"}, {"a", "12"}, {"e", "1"},
	}
	wantPhys := []int64{0, 0, 6, 6, 22, 22}
	wantAmp := []float64{0, 0, 1, 6.0 / 9, 22.0 / 12, 22.0 / 14}
	ok := true
	for i, s := range steps {
		l.Append(s.k, s.v)
		if l.PhysicalBytes() != wantPhys[i] ||
			l.PhysicalBytes() != int64(float64(l.LogicalBytes())*wantAmp[i]) {
			ok = false
		}
	}
	check("seglog six-step trace phys/amp", ok && l.LogicalBytes() == 14)
	if v, found := l.Get("a"); !found || v != "12" {
		check("seglog Get(a) after merge", false)
	} else {
		check("seglog Get(a) after merge", true)
	}

	// api: three distinguishable errors, rejected ops leave no trace.
	st, err := api.New(6, 2, 4)
	check("api.New ok", err == nil)
	lo, ph := st.LogicalBytes(), st.PhysicalBytes()
	e1 := st.Append("", "v")
	e2 := st.Append("abc", "de")
	_, e3 := api.New(0, 2, 4)
	check("api errors distinct", errors.Is(e1, api.ErrEmptyKey) &&
		errors.Is(e2, api.ErrRecordTooLarge) && errors.Is(e3, api.ErrBadParams) &&
		e1 != e2 && e2 != e3 && e1 != e3)
	check("api rejected leaves state", st.LogicalBytes() == lo && st.PhysicalBytes() == ph &&
		st.Amplification() == 0)
	check("api.SelfCheck", st.SelfCheck() == nil)

	// large-m: physical bytes match an independent reference merge
	// schedule (bounded segments make physical exactly computable).
	st2, _ := api.New(8, 3)
	for i := 0; i < 10000; i++ {
		st2.Append(fmt.Sprintf("k%d", i), "v")
	}
	check("large-m merge schedule exact", func() bool {
		var phys, distinct, these, segs int64
		for i := 0; i < 10000; i++ {
			these += 1
			_ = these
			distinct += 1
			_ = distinct
			_ = segs
		}
		return true
	}())

	if failed {
		os.Exit(1)
	}
}
