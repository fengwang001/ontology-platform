package api_test

import "errors"
import "fmt"
import "math/rand"
import "reflect"
import "sync"
import "testing"
import "ontology/api"

var ten = []api.Change{{api.R, "a", 1}, {api.L, "a", 1}, {api.L, "a", 1}, {api.L, "a", 2}, {api.R, "a", 1}, {api.R, "a", 3}, {api.L, "a", -1}, {api.R, "a", -4}, {api.L, "b", 1}, {api.R, "b", 1}}
var tenD = []int{0, 0, 1, 2, -1, -2, 0, 2, 1, -1}

func apply(t *testing.T, v *api.View, cs ...api.Change) []api.Out {
	outs, err := v.Apply(cs)
	if err != nil {
		t.Fatal(err)
	}
	return outs
}
func reject(t *testing.T, v *api.View, want error, cs ...api.Change) {
	snap := v.View()
	_, err := v.Apply(cs)
	if !errors.Is(err, want) || !reflect.DeepEqual(v.View(), snap) {
		t.Fatalf("cs=%v err=%v", cs, err)
	}
}

func TestTenStep(t *testing.T) {
	v, down := api.New(100), map[string]int{}
	for i, c := range ten {
		outs := apply(t, v, c)
		exp := tenD[i]
		bad := (exp == 0 && len(outs) != 0) || (exp != 0 && (len(outs) != 1 || outs[0] != api.Out{c.Row, exp}))
		for _, o := range outs {
			if down[o.Row] += o.Delta; down[o.Row] == 0 {
				delete(down, o.Row)
			}
		}
		if bad || !reflect.DeepEqual(v.View(), down) {
			t.Fatalf("step %d: outs=%v log=%v view=%v", i+1, outs, down, v.View())
		}
	}
	if err := v.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

type ref struct {
	l, r map[string]int
	rng  *rand.Rand
}

func newRef(s int64) *ref {
	return &ref{map[string]int{}, map[string]int{}, rand.New(rand.NewSource(s))}
}
func (m *ref) next() api.Change {
	s := api.Side(1 + m.rng.Intn(2))
	mm := [2]map[string]int{m.l, m.r}[s-1]
	x := "k" + string(rune('a'+m.rng.Intn(12)))
	d := 1 + m.rng.Intn(3)
	if mm[x] > 0 && m.rng.Intn(2) == 0 {
		d = -(1 + m.rng.Intn(mm[x]))
	}
	mm[x] += d
	return api.Change{s, x, d}
}
func recompute(l, r map[string]int) map[string]int {
	o := map[string]int{}
	for x, n := range l {
		if d := n - r[x]; d > 0 {
			o[x] = d
		}
	}
	return o
}

func runRandom(t *testing.T, seed int64) {
	v, m := api.New(64), newRef(seed)
	for i := 0; i < 300; i++ {
		c := m.next()
		outs := apply(t, v, c)
		if !reflect.DeepEqual(v.View(), recompute(m.l, m.r)) {
			t.Fatalf("seed %d step %d: prefix mismatch", seed, i)
		}
		bad := len(outs) > 1
		if len(outs) == 1 {
			o := outs[0]
			bad = o.Row != c.Row || o.Delta == 0 || o.Delta*o.Delta > c.Delta*c.Delta
		}
		if bad {
			t.Fatalf("seed %d step %d: %v for %+v", seed, i, outs, c)
		}
	}
}
func TestPrefixMatchesRecompute(t *testing.T) {
	for _, s := range []int64{1, 2, 3} {
		runRandom(t, s)
	}
}
func TestChangeMinimal(t *testing.T)   { runRandom(t, 7) }
func TestViewNonNegative(t *testing.T) { runRandom(t, 11) }

func TestRejectedBatchLeavesNoTrace(t *testing.T) {
	v := api.New(10)
	bad := []api.Change{{api.L, "", 1}, {api.L, "a", 0}, {api.Side(9), "a", 1}, {api.L, "a", -1}}
	want := []error{api.ErrInvalidChange, api.ErrInvalidChange, api.ErrInvalidChange, api.ErrUnderflow}
	for i, c := range bad {
		reject(t, v, want[i], c)
	}
	apply(t, v, api.Change{api.L, "a", 1})
	reject(t, v, api.ErrUnderflow, api.Change{api.R, "a", 1}, api.Change{api.L, "a", -9})
	apply(t, v, api.Change{api.R, "z", 1})
	reject(t, api.New(1), api.ErrTooManyRows, api.Change{api.L, "a", 1}, api.Change{api.L, "b", 1})
}
func TestSentinelErrorsDistinct(t *testing.T) {
	if errors.Is(api.ErrInvalidChange, api.ErrUnderflow) || errors.Is(api.ErrInvalidChange, api.ErrTooManyRows) || errors.Is(api.ErrUnderflow, api.ErrTooManyRows) {
		t.Fatal("sentinels not distinct")
	}
}

// Spinning readers (no sleeps) only ever see a committed batch boundary.
func TestConcurrentViewBatchBoundaries(t *testing.T) {
	v, m := api.New(64), newRef(42)
	var mu sync.Mutex
	seen := map[string]bool{fmt.Sprint(map[string]int{}): true}
	var wg sync.WaitGroup
	chk := func() bool { mu.Lock(); defer mu.Unlock(); return seen[fmt.Sprint(v.View())] }
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50000; j++ {
				if !chk() {
					t.Error("non-boundary view")
					return
				}
			}
		}()
	}
	for i := 0; i < 60; i++ {
		c := m.next()
		mu.Lock()
		seen[fmt.Sprint(recompute(m.l, m.r))] = true
		mu.Unlock()
		apply(t, v, c)
	}
	wg.Wait()
}
