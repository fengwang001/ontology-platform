package main

import (
	"errors"
	"fmt"
	"math/rand"
	"ontology/api"
	"os"
	"sync"
	"sync/atomic"
)

var failed bool

func check(name string, ok bool) {
	failed = failed || !ok
	fmt.Printf("%s: %s\n", name, map[bool]string{true: "OK", false: "FAIL"}[ok])
}
func main() {
	check("seven-step trace pending/cp/sum + commits + dup", trace())
	check("checkpoint/sum vs naive reference", vsNaive())
	check("restore keeps cp/sum, clears pending", restoreKeeps())
	check("three distinct errors, rejection leaves no trace", rejections())
	check("dedup O(1) at m=10000 (count pinned by acc.TestProbeBound)", largeM())
	check("concurrent apply == serial, checkpoint reads monotonic", concurrent())
	check("selfcheck", new(api.A).SelfCheck() == nil)
	if failed {
		os.Exit(1)
	}
}

// trace replays the seven steps from NOTES.md section 3; o<0 means Commit.
func trace() bool {
	a, _ := api.New(8)
	type step struct{ pend, cp, sum string }
	want := []step{{"[0]", "-1", "0"}, {"[0 2]", "-1", "0"}, {"[0 2 3]", "-1", "0"}, {"[2 3]", "0", "10"},
		{"[2 3]", "0", "10"}, {"[1 2 3]", "0", "10"}, {"[]", "3", "100"},
	}
	ops := []struct{ o, d int64 }{{0, 10}, {2, 20}, {3, 30}, {-1, 0}, {2, 20}, {1, 40}, {-1, 0}}
	for i, op := range ops {
		if op.o < 0 {
			a.Commit()
		} else if err := a.Apply("k", op.o, op.d); err != nil {
			return false
		}
		got := step{fmt.Sprint(a.Pending()), fmt.Sprint(a.Checkpoint()), fmt.Sprint(a.Sum("k"))}
		if got != want[i] {
			return false
		}
	}
	return true
}

// vsNaive: brute-force reference; its checkpoint advances only on commit.
func vsNaive() bool {
	a, _ := api.New(64)
	seen := map[int64][2]int64{} // offset -> [key index, delta]
	cpN, want := int64(-1), [3]int64{}
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 400; i++ {
		if rng.Intn(3) > 0 {
			o, k, d := int64(rng.Intn(50)), rng.Intn(3), int64(rng.Intn(21)-10)
			if _, dup := seen[o]; !dup {
				seen[o] = [2]int64{int64(k), d}
			}
			if a.Apply(string(rune('a'+k)), o, d) != nil {
				return false
			}
		} else {
			a.Commit()
			for r, ok := seen[cpN+1]; ok; r, ok = seen[cpN+1] {
				cpN++
				want[r[0]] += r[1]
			}
		}
		if a.Checkpoint() != cpN || [3]int64{a.Sum("a"), a.Sum("b"), a.Sum("c")} != want {
			return false
		}
	}
	return true
}
func restoreKeeps() bool {
	a, _ := api.New(8)
	_ = a.Apply("k", 0, 5)
	_ = a.Apply("k", 1, 7)
	a.Commit()
	_ = a.Apply("k", 2, 9)
	a.Restore()
	return a.Checkpoint() == 1 && a.Sum("k") == 12 && len(a.Pending()) == 0
}
func rejections() bool {
	a, _ := api.New(1)
	_ = a.Apply("k", 0, 3)
	state := func() string { return fmt.Sprint(a.Checkpoint(), a.Sum("k"), a.Pending()) }
	before := state()
	for _, c := range []struct{ err, want error }{
		{a.Apply("", 1, 1), api.ErrEmptyKey},
		{a.Apply("k", -1, 1), api.ErrNegativeOffset},
		{a.Apply("k", 5, 1), api.ErrPendingFull},
	} {
		if !errors.Is(c.err, c.want) || state() != before {
			return false
		}
	}
	ok := api.ErrEmptyKey != api.ErrNegativeOffset && api.ErrEmptyKey != api.ErrPendingFull && api.ErrNegativeOffset != api.ErrPendingFull
	a.Commit()
	return ok && a.Checkpoint() == 0 && a.Sum("k") == 3
}
func largeM() bool {
	a, _ := api.New(10001)
	ok := true
	for o := int64(0); o < 10000; o++ {
		ok = ok && a.Apply("k", o, 1) == nil
	}
	a.Commit()
	return ok && a.Checkpoint() == 9999 && a.Sum("k") == 10000
}
func concurrent() bool {
	const n = 64
	a, _ := api.New(n)
	var mono, stop atomic.Bool
	mono.Store(true)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for r := 0; r < 4; r++ {
		go func() {
			prev := int64(-1)
			for !stop.Load() {
				cp := a.Checkpoint()
				if cp < prev {
					mono.Store(false)
				}
				prev = max(prev, cp)
			}
		}()
	}
	for g := int64(0); g < n; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_ = a.Apply("k", g, g+1)
		}()
	}
	close(start)
	wg.Wait()
	stop.Store(true)
	a.Commit()
	return mono.Load() && a.Checkpoint() == n-1 && a.Sum("k") == n*(n+1)/2
}
