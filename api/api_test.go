package api_test

import (
	"errors"
	"sync"
	"testing"

	"ontology/api"
)

func feed(t *testing.T, n int) *api.Cube {
	t.Helper()
	c, err := api.New(n)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// TestSelfCheck: the built-in self-check of all four invariants passes.
func TestSelfCheck(t *testing.T) {
	c := feed(t, 100000)
	if err := c.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

// TestAPIErrors pins the three pairwise distinct, detectable sentinels and
// proves a rejected operation leaves the cube usable.
func TestAPIErrors(t *testing.T) {
	if _, err := api.New(-3); !errors.Is(err, api.ErrInvalidMaxCells) {
		t.Fatalf("New(-3) err=%v", err)
	}
	c := feed(t, 8)
	if err := c.Add(api.Fact{A: "a", B: "b", C: "c", V: 1}); err != nil {
		t.Fatal(err)
	}
	snap := c.View()
	errLimit := c.Add(api.Fact{A: "p", B: "q", C: "r", V: 1})
	errMissing := c.Remove(api.Fact{A: "p", B: "q", C: "r", V: 1})
	if !errors.Is(errLimit, api.ErrCellLimit) || !errors.Is(errMissing, api.ErrFactNotFound) ||
		errLimit == errMissing || errors.Is(errLimit, api.ErrInvalidMaxCells) {
		t.Fatalf("sentinels wrong: %v %v", errLimit, errMissing)
	}
	if len(c.View()) != len(snap) {
		t.Fatal("rejected Add changed the view")
	}
	if err := c.Add(api.Fact{A: "a", B: "b", C: "c", V: -1}); err != nil {
		t.Fatalf("cube unusable after rejection: %v", err)
	}
}

// TestEmptyStringNotALL pins the sentinel design: the concrete empty
// string is a distinct value from ALL. Adding A="" creates a level-3 cell
// ("",b,c) that must not merge into the level-2 ALL cell (*,b,c).
func TestEmptyStringNotALL(t *testing.T) {
	c := feed(t, 1000)
	if err := c.Add(api.Fact{A: "a", B: "b", C: "c", V: 3}); err != nil {
		t.Fatal(err)
	}
	if err := c.Add(api.Fact{A: "", B: "b", C: "c", V: 4}); err != nil {
		t.Fatal(err)
	}
	var total, starBC, emptyBC int64
	var levels []int
	for _, cell := range c.View() {
		levels = append(levels, api.Level(cell))
		switch {
		case cell.Key.AAll && cell.Key.BAll && cell.Key.CAll:
			total = cell.Sum
		case cell.Key.AAll && !cell.Key.BAll && cell.Key.BVal == "b" && cell.Key.CVal == "c":
			starBC = cell.Sum
		case !cell.Key.AAll && cell.Key.AVal == "" && cell.Key.BVal == "b" && cell.Key.CVal == "c":
			emptyBC = cell.Sum
			if api.Level(cell) != 3 {
				t.Fatalf("(\"\",b,c) level=%d, want 3", api.Level(cell))
			}
		}
	}
	if total != 7 || starBC != 7 || emptyBC != 4 {
		t.Fatalf("total=%d (*,b,c)=%d (\"\",b,c)=%d", total, starBC, emptyBC)
	}
	_ = levels
}

// TestConcurrentReaders: N goroutines read one fed cube concurrently; every
// View must be cell-by-cell identical and Levels consistent. No sleep is
// used: a closed channel is the start barrier and a WaitGroup joins.
func TestConcurrentReaders(t *testing.T) {
	for _, n := range []int{2, 8, 32} {
		c := feed(t, 100000)
		facts := []api.Fact{
			{A: "a", B: "b", C: "c", V: 2}, {A: "a", B: "b", C: "c", V: 3},
			{A: "a", B: "d", C: "c", V: 5}, {A: "e", B: "b", C: "c", V: 7},
			{A: "", B: "b", C: "c", V: 4},
		}
		for _, f := range facts {
			if err := c.Add(f); err != nil {
				t.Fatal(err)
			}
		}
		ref := c.View()
		start := make(chan struct{})
		var wg sync.WaitGroup
		errs := make(chan error, n)
		for g := 0; g < n; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				for r := 0; r < 100; r++ {
					v := c.View()
					if len(v) != len(ref) {
						errs <- errLen{}
						return
					}
					for i := range v {
						if v[i] != ref[i] || api.Level(v[i]) != v[i].Key.Level() {
							errs <- errDiff{}
							return
						}
					}
					if err := c.SelfCheck(); err != nil {
						errs <- err
						return
					}
				}
			}()
		}
		close(start)
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Fatalf("n=%d: %v", n, err)
		}
	}
}

type errLen struct{}

func (errLen) Error() string { return "view length differs" }

type errDiff struct{}

func (errDiff) Error() string { return "view cell differs" }
