package main

import (
	"fmt"
	"path/filepath"
	"reflect"

	"ontology/change"
	"ontology/view"
)

// scenarioCrash injects a crash at each of Apply / Recompute / PreCommit
// on the final change, recovers from the journal each time, and requires
// the recovered state to be identical to a clean prefix-only run.
func scenarioCrash(tmp string) bool {
	const total = 100
	for _, phase := range []view.Phase{
		view.PhaseApply, view.PhaseRecompute, view.PhasePreCommit,
	} {
		dir := filepath.Join(tmp, "crash-"+string(phase))
		crashPath := filepath.Join(dir, "log")
		refPath := filepath.Join(dir, "ref")

		refV := openView(refPath, view.Options{})
		seedChanges(refV, total-1)
		want := refV.Groups()
		must(refV.Close())

		v := openView(crashPath, view.Options{
			CrashHook: func(p view.Phase, c change.Change) error {
				if p == phase && c.Version == total {
					return view.ErrInjectedCrash
				}
				return nil
			},
		})
		var crashed bool
		for i := 1; i <= total; i++ {
			err := v.Submit(seedChange(uint64(i)))
			if err != nil {
				if i == total && err == view.ErrInjectedCrash {
					crashed = true
					break
				}
				must(err)
			}
		}
		must(v.Close())
		if !crashed {
			return false
		}

		rec := openView(crashPath, view.Options{})
		got := rec.Groups()
		correctVer := rec.MaxVersion() == total-1
		must(rec.Close())
		if !correctVer || !reflect.DeepEqual(got, want) {
			return false
		}
	}
	return true
}

func seedChange(vn uint64) change.Change {
	group := "g"
	if vn%2 == 0 {
		group = "h"
	}
	return ins(vn, keyFor(int(vn)), group, float64(vn))
}

func seedChanges(v *view.View, total int) {
	for i := 1; i <= total; i++ {
		must(v.Submit(seedChange(uint64(i))))
	}
}

func keyFor(i int) string {
	return fmt.Sprintf("k%04d", i)
}
