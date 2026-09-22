package view_test

import (
	"path/filepath"
	"reflect"
	"testing"

	"ontology/change"
	"ontology/view"
)

func seedView(t *testing.T, v *view.View, total int) {
	t.Helper()
	for i := 1; i <= total; i++ {
		group := "g"
		if i%2 == 0 {
			group = "h"
		}
		if err := v.Submit(ins(uint64(i), key(i), group, float64(i))); err != nil {
			t.Fatal(err)
		}
	}
}

func cleanPrefix(t *testing.T, path string, total int) (map[string]view.GroupResult, uint64) {
	t.Helper()
	v, err := view.New(view.Options{JournalPath: path})
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	seedView(t, v, total)
	return v.Groups(), v.MaxVersion()
}

// TestCrashPoints injects an error at each of the three crash points on
// the final change, then recovers from the journal. Because the hook
// fires before the durable append, the journal contains exactly the
// prefix; recovered state must be field-identical to a clean prefix run.
func TestCrashPoints(t *testing.T) {
	const total = 200
	for _, phase := range []view.Phase{
		view.PhaseApply, view.PhaseRecompute, view.PhasePreCommit,
	} {
		t.Run(string(phase), func(t *testing.T) {
			dir := t.TempDir()
			crashPath := filepath.Join(dir, "log")
			ref, refVer := cleanPrefix(t, filepath.Join(dir, "ref"), total-1)

			v, err := view.New(view.Options{
				JournalPath: crashPath,
				CrashHook: func(p view.Phase, c change.Change) error {
					if p == phase && c.Version == total {
						return view.ErrInjectedCrash
					}
					return nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			seedViewExpectCrash(t, v, total)
			_ = v.Close()

			rec, err := view.New(view.Options{JournalPath: crashPath})
			if err != nil {
				t.Fatal(err)
			}
			defer rec.Close()
			if rec.MaxVersion() != refVer {
				t.Fatalf("recovered version %d want %d", rec.MaxVersion(), refVer)
			}
			got := rec.Groups()
			if !reflect.DeepEqual(got, ref) {
				t.Fatalf("phase %s: recovered view differs from clean prefix", phase)
			}
		})
	}
}

func seedViewExpectCrash(t *testing.T, v *view.View, total int) {
	t.Helper()
	var sawCrash bool
	for i := 1; i <= total; i++ {
		group := "g"
		if i%2 == 0 {
			group = "h"
		}
		err := v.Submit(ins(uint64(i), key(i), group, float64(i)))
		if err != nil {
			if i == total && err == view.ErrInjectedCrash {
				sawCrash = true
				break
			}
			t.Fatalf("unexpected error at %d: %v", i, err)
		}
	}
	if !sawCrash {
		t.Fatal("crash hook never fired")
	}
}
