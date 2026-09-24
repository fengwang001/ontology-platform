package api_test

import (
	"errors"
	"math/rand"
	"reflect"
	"sync"
	"testing"

	"ontology/api"
)

// replay 顺序重放 changelog：+ 须对缺席、- 须对在场（钉不变量 2），返回复现集合。
func replay(t *testing.T, v *api.View) map[string]struct{} {
	t.Helper()
	seen := map[string]struct{}{}
	for i, ch := range v.Changes() {
		_, on := seen[ch.Elem]
		if ch.Add == on {
			t.Fatalf("prefix %d bad +/- on %q", i, ch.Elem)
		}
		if ch.Add {
			seen[ch.Elem] = struct{}{}
		} else {
			delete(seen, ch.Elem)
		}
	}
	return seen
}

// driveRandom 喂一串随机 Add/Remove，随后同时核验批量并集一致与 changelog 自洽。
func driveRandom(t *testing.T, n, ops int, seed int64) {
	v, _ := api.New(n)
	ref := make([]map[string]struct{}, n)
	for i := range ref {
		ref[i] = map[string]struct{}{}
	}
	rng := rand.New(rand.NewSource(seed))
	for k := 0; k < ops; k++ {
		p, e := rng.Intn(n), string(rune('a'+rng.Intn(10)))
		if rng.Intn(2) == 0 {
			v.Add(p, e)
			ref[p][e] = struct{}{}
		} else {
			v.Remove(p, e)
			delete(ref[p], e)
		}
	}
	want := map[string]struct{}{}
	for _, s := range ref {
		for e := range s {
			want[e] = struct{}{}
		}
	}
	if !reflect.DeepEqual(want, v.View()) {
		t.Fatal("view != batch union")
	}
	if !reflect.DeepEqual(replay(t, v), v.View()) {
		t.Fatal("changelog prefix != view")
	}
}

func TestViewMatchesBatchUnion(t *testing.T) {
	for it := 0; it < 40; it++ {
		driveRandom(t, 5, 150, int64(it))
	}
}

func TestChangelogAlternation(t *testing.T) {
	for _, n := range []int{1, 2, 7} {
		driveRandom(t, n, 300, int64(n))
	}
}

func TestRefCountConservation(t *testing.T) {
	for _, k := range []int{1, 2, 5, 50} {
		v, _ := api.New(k)
		for p := 0; p < k; p++ {
			v.Add(p, "e")
		}
		for p := 0; p < k-1; p++ {
			n0 := len(v.Changes())
			v.Remove(p, "e")
			_, on := v.View()["e"]
			if len(v.Changes()) != n0 || !on {
				t.Fatalf("k=%d: e dropped while held", k)
			}
		}
		n0 := len(v.Changes())
		v.Remove(k-1, "e")
		if _, on := v.View()["e"]; len(v.Changes()) != n0+1 || on {
			t.Fatalf("k=%d: last-holder semantics wrong", k)
		}
	}
	if w, _ := api.New(2); w.SelfCheck() != nil {
		t.Fatal("SelfCheck failed")
	}
}

func TestRejectedOpsNoTrace(t *testing.T) {
	if _, e := api.New(0); !errors.Is(e, api.ErrBadNPart) {
		t.Fatal("New(0) not ErrBadNPart")
	}
	if api.ErrBadNPart == api.ErrBadPart || api.ErrBadPart == api.ErrEmptyElem || api.ErrBadNPart == api.ErrEmptyElem {
		t.Fatal("sentinel errors must be distinct")
	}
	bad := []func(*api.View) error{
		func(v *api.View) error { return v.Add(4, "e") },
		func(v *api.View) error { return v.Remove(-1, "e") },
		func(v *api.View) error { return v.Add(0, "") },
		func(v *api.View) error { return v.Remove(0, "") },
	}
	want := []error{api.ErrBadPart, api.ErrBadPart, api.ErrEmptyElem, api.ErrEmptyElem}
	for i, fn := range bad {
		v, _ := api.New(3)
		v.Add(0, "z")
		bv, bc := v.View(), v.Changes()
		if !errors.Is(fn(v), want[i]) {
			t.Fatal("missing or wrong sentinel")
		}
		if !reflect.DeepEqual(bv, v.View()) || !reflect.DeepEqual(bc, v.Changes()) {
			t.Fatal("rejected op left a trace")
		}
		if v.Add(1, "q") != nil {
			t.Fatal("instance unusable after rejection")
		}
	}
}

func TestConcurrentReadersIdentical(t *testing.T) {
	v, _ := api.New(10)
	for p := 0; p < 10; p++ {
		for _, e := range []string{"a", "b", "c"} {
			v.Add(p, e)
		}
	}
	const N = 32
	views := make([]map[string]struct{}, N)
	var wg sync.WaitGroup
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(g int) { defer wg.Done(); views[g] = v.View() }(g)
	}
	wg.Wait()
	for g := 1; g < N; g++ {
		if !reflect.DeepEqual(views[0], views[g]) {
			t.Fatalf("reader %d got a different view", g)
		}
	}
}
