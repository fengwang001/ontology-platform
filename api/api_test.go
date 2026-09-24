package api_test

import (
	"errors"
	"fmt"
	"maps"
	"math/rand"
	"slices"
	"strings"
	"sync"
	"testing"

	"ontology/api"
)

func sign(cs []api.Change) string {
	parts := make([]string, len(cs))
	for i, c := range cs {
		parts[i] = "+" + c.Elem
		if !c.Add {
			parts[i] = "-" + c.Elem
		}
	}
	return strings.Join(parts, " ")
}
func keysSorted(m map[string]struct{}) []string { return slices.Sorted(maps.Keys(m)) }

// runRandom: new changelog entries must alternate strictly against the
// replayed view; finally View == union == replayed view.
func runRandom(t *testing.T, nPart, ops int, seed int64) {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	e, _ := api.New(nPart)
	parts := make([]map[string]struct{}, nPart)
	for i := range parts {
		parts[i] = map[string]struct{}{}
	}
	replay := map[string]struct{}{}
	prev := 0
	for i := 0; i < ops; i++ {
		add := rng.Intn(2) == 0
		p, el := rng.Intn(nPart), fmt.Sprintf("e%d", rng.Intn(8))
		_ = e.Apply([]api.Op{{Add: add, P: p, Elem: el}})
		if add {
			parts[p][el] = struct{}{}
		} else {
			delete(parts[p], el)
		}
		if cs := e.Changes(); len(cs) > prev {
			ch := cs[len(cs)-1]
			_, present := replay[ch.Elem]
			if ch.Add == present {
				t.Fatalf("op %d: invalid %+v present=%v", i, ch, present)
			}
			if ch.Add {
				replay[ch.Elem] = struct{}{}
			} else {
				delete(replay, ch.Elem)
			}
			prev = len(cs)
		}
	}
	union := map[string]struct{}{}
	for _, s := range parts {
		maps.Copy(union, s)
	}
	if got := fmt.Sprint(e.View()); got != fmt.Sprint(keysSorted(union)) ||
		got != fmt.Sprint(keysSorted(replay)) {
		t.Fatalf("view %s union %v replay %v", got, union, replay)
	}
}
func TestCanonicalEightSteps(t *testing.T) {
	e, _ := api.New(2)
	ops := []api.Op{{true, 0, "a"}, {true, 1, "a"}, {true, 0, "b"}, {false, 0, "a"}, {false, 1, "a"}, {false, 0, "b"}, {true, 1, "b"}, {false, 0, "b"}}
	logs := []string{"+a", "+a", "+a +b", "+a +b", "+a +b -a", "+a +b -a -b", "+a +b -a -b +b", "+a +b -a -b +b"}
	views := []string{"[a]", "[a]", "[a b]", "[a b]", "[b]", "[]", "[b]", "[b]"}
	for i, o := range ops {
		if err := e.Apply([]api.Op{o}); err != nil ||
			sign(e.Changes()) != logs[i] || fmt.Sprint(e.View()) != views[i] {
			t.Errorf("step %d: log=%q view=%v err=%v", i+1, sign(e.Changes()), e.View(), err)
		}
	}
}
func TestViewMatchesUnion(t *testing.T)     { runRandom(t, 4, 500, 11); runRandom(t, 16, 1000, 12) }
func TestChangelogAlternation(t *testing.T) { runRandom(t, 6, 800, 7) }
func TestRefCountConservation(t *testing.T) { runRandom(t, 8, 1200, 99) }
func TestSentinelErrors(t *testing.T) {
	e, _ := api.New(2)
	_, errNew := api.New(0)
	cases := []struct{ got, want error }{{errNew, api.ErrInvalidNPart}, {e.Add(-1, "a"), api.ErrPartitionOutOfRange},
		{e.Remove(2, "a"), api.ErrPartitionOutOfRange}, {e.Add(0, ""), api.ErrEmptyElement}}
	for i, c := range cases {
		if !errors.Is(c.got, c.want) {
			t.Errorf("case %d: %v want %v", i, c.got, c.want)
		}
	}
	if errors.Is(api.ErrEmptyElement, api.ErrPartitionOutOfRange) {
		t.Fatal("sentinel errors must be distinct")
	}
}
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	e, _ := api.New(2)
	_ = e.Add(0, "a")
	snap := fmt.Sprint(e.View(), e.Changes())
	for i, o := range []api.Op{{true, -1, "q"}, {false, 2, "q"}, {true, 0, ""}} {
		if e.Apply([]api.Op{o}) == nil || fmt.Sprint(e.View(), e.Changes()) != snap {
			t.Fatalf("bad op %d changed state or had no error", i)
		}
	}
	if e.Apply([]api.Op{{true, 0, "q"}, {true, 9, "r"}}) == nil ||
		fmt.Sprint(e.View(), e.Changes()) != snap {
		t.Fatal("invalid batch was not all-or-nothing")
	}
	if err := e.Add(1, "z"); err != nil {
		t.Fatal("engine not usable after rejections")
	}
}
func TestConcurrentReaders(t *testing.T) {
	e, _ := api.New(8)
	for p := 0; p < 8; p++ {
		_ = e.Apply([]api.Op{{true, p, "a"}, {true, p, "b"}, {true, p, "c"}})
	}
	want := fmt.Sprint(e.View())
	var start, done sync.WaitGroup
	start.Add(1)
	done.Add(32)
	results := make([]string, 32)
	for g := range results {
		go func(i int) { defer done.Done(); start.Wait(); results[i] = fmt.Sprint(e.View()) }(g)
	}
	start.Done()
	done.Wait()
	for i, got := range results {
		if got != want {
			t.Fatalf("reader %d: %s want %s", i, got, want)
		}
	}
}
func TestSelfCheck(t *testing.T) {
	for _, n := range []int{2, 5} { // built-in sequence needs 2 partitions
		e, err := api.New(n)
		if err != nil || e.SelfCheck() != nil {
			t.Fatalf("n=%d: %v / %v", n, err, e.SelfCheck())
		}
	}
}
