package api_test

import (
	"fmt"
	"maps"
	"math/rand"
	"sync"
	"testing"

	"ontology/api"
)

// ref is the snapshot naive model; sn tracks record count for log-full.
type ref struct {
	d           map[string]int
	sp          map[int]map[string]int
	sn          map[int]int
	nxt, lim, n int
}

func newRef(lim int) *ref {
	return &ref{d: map[string]int{}, sp: map[int]map[string]int{}, sn: map[int]int{}, lim: lim}
}
func (r *ref) set(k string, v int) error {
	if k == "" {
		return api.ErrEmptyKey
	}
	if r.n >= r.lim {
		return api.ErrLogFull
	}
	r.d[k] = v
	r.n++
	return nil
}
func (r *ref) save() int {
	id := r.nxt
	r.nxt++
	r.sp[id], r.sn[id] = maps.Clone(r.d), r.n
	return id
}
func (r *ref) do(id int, undo bool) error {
	c, ok := r.sp[id]
	if !ok {
		return api.ErrSavepoint
	}
	if undo {
		r.d, r.n = c, r.sn[id]
	}
	for x := range r.sp {
		if x >= id {
			delete(r.sp, x)
			delete(r.sn, x)
		}
	}
	return nil
}

var keys = []string{"a", "b", "c", "d"}

func sameData(t *testing.T, k *api.KV, r *ref) {
	for _, key := range keys {
		v1, ok1 := k.Get(key)
		v2, ok2 := r.d[key]
		if ok1 != ok2 || ok1 && v1 != v2 {
			t.Fatalf("diverge %q (%d,%v) vs (%d,%v) %v", key, v1, ok1, v2, ok2, r.d)
		}
	}
}

// replay drives one random arrival sequence, checking every beat against naive.
func replay(t *testing.T, seed int64, lim int) {
	k, _ := api.New(lim)
	r := newRef(lim)
	rng := rand.New(rand.NewSource(seed))
	for i := 0; i < 200; i++ {
		key := keys[rng.Intn(len(keys))]
		if rng.Intn(20) == 0 {
			key = ""
		}
		id := rng.Intn(6)
		var e1, e2 error
		switch "SSSSPBX"[rng.Intn(7)] {
		case 'S':
			v := rng.Intn(9)
			e1, e2 = k.Set(key, v), r.set(key, v)
		case 'P':
			if k.Savepoint() != r.save() {
				t.Fatal("id diverged")
			}
		case 'B':
			e1, e2 = k.RollbackTo(id), r.do(id, true)
		case 'X':
			e1, e2 = k.Release(id), r.do(id, false)
		}
		if e1 != e2 {
			t.Fatalf("seed=%d lim=%d beat=%d %v vs %v", seed, lim, i, e1, e2)
		}
		sameData(t, k, r)
	}
}

// TestNaiveReplay covers limit tiers (incl. log-full) and random seeds.
func TestNaiveReplay(t *testing.T) {
	for _, seed := range []int64{1, 413, 999983} {
		for _, lim := range []int{1, 2, 5, 64} {
			replay(t, seed, lim)
		}
	}
}
func TestSelfCheckAndLocate(t *testing.T) {
	k, _ := api.New(100)
	if !k.SelfCheck() || !k.CheckLocateO1() {
		t.Fatal("SelfCheck or O(1) locate failed")
	}
}

// TestConcurrentGet: N goroutines behind a start barrier (no sleeps) read the
// same batch concurrently and must observe identical per-key data.
func TestConcurrentGet(t *testing.T) {
	k, _ := api.New(1000)
	const batch = 200
	want := make(map[string]int, batch)
	for i := 0; i < batch; i++ {
		key := fmt.Sprintf("key-%d", i)
		k.Set(key, i)
		want[key] = i
	}
	const n = 16
	start, wg := make(chan struct{}), sync.WaitGroup{}
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-start
			for key, v := range want {
				if gv, ok := k.Get(key); !ok || gv != v {
					t.Errorf("g=%d key=%s (%d,%v)", g, key, gv, ok)
					return
				}
			}
		}(g)
	}
	close(start)
	wg.Wait()
}
