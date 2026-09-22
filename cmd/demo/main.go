// Command demo runs the incremental aggregate view maintainer through its
// acceptance scenarios with no arguments and no network access. Every
// check prints one OK/FAIL line; the final line is the tally.
package main

import (
	"fmt"
	"os"

	"ontology/change"
	"ontology/view"
)

func main() {
	tmp, err := os.MkdirTemp("", "ontology-demo-")
	must(err)
	defer os.RemoveAll(tmp)

	var pass, fail int
	report := func(name string, ok bool, detail string) {
		if ok {
			pass++
			fmt.Printf("OK   %s %s\n", name, detail)
		} else {
			fail++
			fmt.Printf("FAIL %s %s\n", name, detail)
		}
	}

	minRecomp, sumRecomp := scenarioRecompute(tmp)
	report("min-extremum-recompute=3 sum=0",
		minRecomp == 3 && sumRecomp == 0,
		fmt.Sprintf("(min=%d sum=%d)", minRecomp, sumRecomp))

	eq, where := scenarioAudit()
	report("incremental==full-recompute (bitwise Sum)", eq, where)

	gone := scenarioEmptyGroup()
	report("emptied group lookup => not-exist", gone, "")

	cats := scenarioTruncation(tmp)
	report("byte-truncation four categories",
		cats["header-incomplete"] && cats["length-prefix-incomplete"] &&
			cats["body-incomplete"] && cats["crc-mismatch"],
		fmt.Sprintf("%v", cats))

	rejects := scenarioOutOfOrder()
	report("out-of-order changes rejected", rejects == 2,
		fmt.Sprintf("(rejected=%d)", rejects))

	crashOK := scenarioCrash(tmp)
	report("three crash points recover identically", crashOK, "")

	concOK := scenarioConcurrent()
	report("concurrent commits aggregate correct", concOK, "")

	fmt.Printf("TOTAL %d passed, %d failed\n", pass, fail)
	if fail != 0 {
		os.Exit(1)
	}
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func openView(path string, hook view.Options) *view.View {
	if hook.JournalPath == "" && path != "" {
		hook.JournalPath = path
	}
	v, err := view.New(hook)
	must(err)
	return v
}

func ins(vn uint64, key, group string, val float64) change.Change {
	return change.Change{Version: vn, Op: change.OpInsert, Key: key,
		From: change.Row{Group: group, GroupPresent: true, Value: val}}
}

func del(vn uint64, key, group string, val float64) change.Change {
	return change.Change{Version: vn, Op: change.OpDelete, Key: key,
		From: change.Row{Group: group, GroupPresent: true, Value: val}}
}
