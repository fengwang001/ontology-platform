// Command demo exercises the incremental aggregate view maintainer.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"ontology/agg"
	"ontology/change"
	"ontology/journal"
)

var passed, failed int

func check(name string, ok bool, detail string) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed++
	} else {
		passed++
	}
	fmt.Printf("%s %s %s\n", status, name, detail)
}

func checkCodec() {
	cases := []change.Change{
		{Version: 1, Op: change.OpInsert, ID: "a", Group: "g", HasGroup: true, Value: 3.5},
		{Version: 2, Op: change.OpUpdate, ID: "b", Group: "", HasGroup: true, Value: -0.0},
		{Version: 3, Op: change.OpDelete, ID: "a"},
	}
	ok := true
	for _, c := range cases {
		got, err := change.Decode(c.Encode())
		if err != nil || got != c {
			ok = false
		}
	}
	check("change-codec", ok, "encode/decode roundtrip")
}

func checkAggFlags() {
	want := map[string]bool{"count": false, "sum": false, "min": true, "max": true, "distinct": true}
	ok := true
	for _, n := range agg.Names() {
		a := agg.New(n)
		if a == nil || a.NeedsMembersOnDelete() != want[n] {
			ok = false
		}
	}
	mn := agg.New("min")
	mn.Insert(5)
	mn.Insert(9)
	if mn.Delete(9) != true || mn.Delete(5) != false {
		ok = false // recompute only when the extremum itself is retracted
	}
	check("agg-retraction", ok, "NeedsMembersOnDelete flags + min delete gating")
}

// buildLog writes n fixed-width records and returns the log image.
func buildLog(dir string, n int) []byte {
	j, err := journal.Create(filepath.Join(dir, "demo.log"))
	if err != nil {
		panic(err)
	}
	for i := 0; i < n; i++ {
		c := change.Change{Version: uint64(i + 1), Op: change.OpInsert,
			ID: fmt.Sprintf("r%04d", i), Group: fmt.Sprintf("g%02d", i%10),
			HasGroup: true, Value: float64(i%7 + 1)}
		if err := j.Append(c); err != nil {
			panic(err)
		}
	}
	j.Close()
	data, err := os.ReadFile(filepath.Join(dir, "demo.log"))
	if err != nil {
		panic(err)
	}
	return data
}

func checkTruncation(dir string) {
	data := buildLog(dir, 200)
	first := map[journal.Class]int{}
	var firstErr [5]*journal.ReplayError
	for t := 1; t < len(data); t++ {
		_, err := journal.ReplayBytes(data[:t])
		re, ok := err.(*journal.ReplayError)
		if !ok {
			continue // boundary-aligned cut replays cleanly
		}
		if _, seen := first[re.Class]; !seen {
			first[re.Class] = t
			firstErr[re.Class] = re
		}
	}
	for _, cl := range []journal.Class{journal.ClassHeaderIncomplete,
		journal.ClassLengthIncomplete, journal.ClassRecordIncomplete} {
		t, ok := first[cl]
		check("truncate-"+cl.String(), ok, fmt.Sprintf("e.g. cut@%d -> %v", t, firstErr[cl]))
	}
	bad := append([]byte(nil), data...)
	bad[100] ^= 0xFF
	_, err := journal.ReplayBytes(bad)
	re, ok := err.(*journal.ReplayError)
	check("truncate-"+journal.ClassCRCMismatch.String(), ok && re.Class == journal.ClassCRCMismatch,
		fmt.Sprintf("bitflip@100 -> %v", err))
}

func main() {
	dir, err := os.MkdirTemp("", "ontology-demo")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)
	checkCodec()
	checkAggFlags()
	checkTruncation(dir)
	fmt.Printf("TOTAL %d/%d OK\n", passed, passed+failed)
	if failed > 0 {
		os.Exit(1)
	}
}
