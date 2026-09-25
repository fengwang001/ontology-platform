package main

import (
	"fmt"
	"os"

	"ontology/hook"
	"ontology/snapshot"
)

type judgment struct {
	name string
	ok   bool
	info string
}

func main() {
	var results []judgment

	results = append(results, skeleton())
	results = append(results, frozenSnapshot())
	results = append(results, matchingEfficiency())

	failed := 0
	for _, r := range results {
		mark := "OK"
		if !r.ok {
			mark = "FAIL"
			failed++
		}
		fmt.Printf("%-4s %s %s\n", mark, r.name, r.info)
	}
	if failed > 0 {
		fmt.Printf("\n%d judgment(s) FAILED\n", failed)
		os.Exit(1)
	}
	fmt.Println("\nall semantics verified")
}

func matchingEfficiency() judgment {
	measure := func(total int) int {
		r := hook.NewRegistry()
		for i := 0; i < total; i++ {
			bucket := "noise"
			if i < 3 {
				bucket = "task"
			}
			r.MustRegister(fmt.Sprintf("h%d", i), bucket, hook.PhasePre, 0,
				func(*snapshot.Snapshot) (bool, string) { return true, "" })
		}
		r.Matching("task")
		return r.LookupVisits()
	}
	v100 := measure(100)
	v10000 := measure(10000)
	return judgment{
		name: "matching visits do not grow with registry size (100 vs 10000)",
		ok:   v100 == 3 && v10000 == 3,
		info: fmt.Sprintf("visits@100=%d visits@10000=%d", v100, v10000),
	}
}

func skeleton() judgment {
	return judgment{name: "demo skeleton runs", ok: true}
}

func frozenSnapshot() judgment {
	a := snapshot.Freeze("task", "t1", map[string]string{"start": "1", "end": "9", "owner": "ann"})
	b := snapshot.Freeze("task", "t1", map[string]string{"owner": "ann", "end": "9", "start": "1"})
	deterministic := a.Equal(b)

	src := map[string]string{"a": "1"}
	s := snapshot.Freeze("task", "t1", src)
	src["a"] = "999"
	raw := s.Bytes()
	for i := range raw {
		raw[i] = 0xFF
	}
	v, _ := s.Get("a")
	isolated := v == "1" && s.Equal(snapshot.Freeze("task", "t1", map[string]string{"a": "1"}))

	return judgment{
		name: "frozen snapshot is byte-deterministic and immutable",
		ok:   deterministic && isolated,
		info: fmt.Sprintf("deterministic=%v isolated=%v", deterministic, isolated),
	}
}
