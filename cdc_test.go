package ontology_test

import (
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/api"
)

func ev(k string, s int64) api.Event { return api.Event{Key: k, Seq: s} }

func settle(c *api.CDC, ma, n int) {
	for i := 0; i < ma*n+1; i++ {
		c.Tick()
	}
}

// checkKey asserts key k's finalized trace ('+' applied, 'x' dead, in seq
// order per invariant 1) equals the naive serial reference.
func checkKey(t *testing.T, c *api.CDC, ma int, fl map[api.Event]int, k string, n int) {
	m := map[int64]byte{}
	for _, e := range c.Applied() {
		if e.Key == k {
			m[e.Seq] = '+'
		}
	}
	for _, e := range c.DeadLetters() {
		if e.Key == k {
			m[e.Seq] = 'x'
		}
	}
	got, want := "", ""
	for s := int64(1); s <= int64(n); s++ {
		mark := byte('+')
		if fl[ev(k, s)] >= ma {
			mark = 'x'
		}
		got, want = got+fmt.Sprintf("%d%c", s, m[s]), want+fmt.Sprintf("%d%c", s, mark)
	}
	if got != want {
		t.Fatalf("key=%s got=%s want=%s", k, got, want)
	}
}

// gen builds a random arrival sequence interleaving keys while
// preserving each key's seq order; fl is the failure table.
func gen(rng *rand.Rand, ma int) (map[api.Event]int, map[string]int, []api.Event) {
	fl, cnt, all := map[api.Event]int{}, map[string]int{}, []api.Event{}
	nk, total := 2+rng.Intn(5), 0
	left := make([]int, nk)
	for i := range left {
		left[i] = 1 + rng.Intn(7)
		total += left[i]
	}
	for total > 0 {
		if i := rng.Intn(nk); left[i] > 0 {
			k := string(rune('a' + i))
			e := ev(k, int64(cnt[k]+1))
			cnt[k], left[i], total = cnt[k]+1, left[i]-1, total-1
			all = append(all, e)
			if rng.Intn(3) == 0 {
				fl[e] = rng.Intn(ma + 2)
			}
		}
	}
	return fl, cnt, all
}

// TestIsolation pins invariant 2: a free key applies inside its Submit.
func TestIsolation(t *testing.T) {
	for _, nb := range []int{1, 3, 8} {
		fl := map[api.Event]int{}
		for i := 0; i < nb; i++ {
			fl[ev(fmt.Sprintf("b%d", i), 1)] = 5
		}
		c, _ := api.New(2, 1000, fl)
		for e := range fl {
			c.Submit(e)
		}
		rs, err := c.Submit(ev("free", 1))
		if err != nil || len(rs) != 1 || rs[0].Kind != api.Applied {
			t.Fatalf("nb=%d free key: %v %v", nb, rs, err)
		}
	}
}

// TestSerialReferenceRandom pins invariants 1 and 3 vs the serial reference.
func TestSerialReferenceRandom(t *testing.T) {
	for _, seed := range []int64{1, 278, 777} {
		rng := rand.New(rand.NewSource(seed))
		ma := 1 + rng.Intn(3)
		fl, cnt, all := gen(rng, ma)
		c, _ := api.New(ma, 1000, fl)
		for _, e := range all {
			c.Submit(e)
			if rng.Intn(2) == 0 {
				c.Tick()
			}
		}
		settle(c, ma, len(all))
		for k, n := range cnt {
			checkKey(t, c, ma, fl, k, n)
		}
	}
}

// TestConcurrentPerKey: N goroutines each drive one key while a ticker runs.
func TestConcurrentPerKey(t *testing.T) {
	const N, L, ma = 16, 40, 3
	fl := map[api.Event]int{}
	for i := 0; i < N; i++ {
		for s := int64(4); s <= L; s += 4 {
			fl[ev(string(rune('a'+i)), s)] = int(s) % 5 // 0..4: retries + DLs
		}
	}
	c, _ := api.New(ma, N*L, fl)
	var wg sync.WaitGroup
	var stop atomic.Bool
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(k string) {
			defer wg.Done()
			for s := int64(1); s <= L; s++ {
				c.Submit(ev(k, s))
			}
		}(string(rune('a' + i)))
	}
	go func() {
		for !stop.Load() {
			c.Tick()
		}
	}()
	wg.Wait()
	stop.Store(true)
	settle(c, ma, N*L)
	for i := 0; i < N; i++ {
		checkKey(t, c, ma, fl, string(rune('a'+i)), L)
	}
}
func TestSelfCheck(t *testing.T) {
	if c, err := api.New(1, 1, nil); err != nil {
		t.Fatal(err)
	} else if err := c.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
