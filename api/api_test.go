package api

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

// The worked example from the spec: ordering, then refcount-aware delete.
func TestSection3(t *testing.T) {
	a := New()
	for _, op := range [][2]any{{"apricot", 5}, {"apple", 5}, {"app", 3}, {"application", 2}} {
		if err := a.Insert(op[0].(string), op[1].(int)); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := a.Complete("ap", 3)
	if want := []string{"apple", "apricot", "app"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Complete(ap,3)=%v, want %v", got, want)
	}
	if err := a.Delete("app"); err != nil {
		t.Fatal(err)
	}
	got, _ = a.Complete("ap", 3)
	if want := []string{"apple", "apricot", "application"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("after delete Complete(ap,3)=%v, want %v", got, want)
	}
	if a.Count() != 3 {
		t.Fatalf("Count()=%d, want 3", a.Count())
	}
}

// The four failure modes must be distinguishable and leave no trace.
func TestErrorsDistinctAndStateless(t *testing.T) {
	a := New()
	a.Insert("apple", 5)
	bad := "a\xffb"
	var u *UTF8Error
	checks := []struct {
		name string
		err  error
		ok   bool
	}{
		{"empty insert", a.Insert("", 1), errors.Is(a.Insert("", 1), ErrEmpty)},
		{"utf8 insert", a.Insert(bad, 1), errors.As(a.Insert(bad, 1), &u) && u.Offset == 1},
		{"delete missing", a.Delete("ghost"), errors.Is(a.Delete("ghost"), ErrNotFound)},
	}
	if _, err := a.Complete("a", 0); !errors.Is(err, ErrBadK) {
		t.Errorf("k=0: err=%v, want ErrBadK", err)
	}
	for _, c := range checks {
		if c.err == nil || !c.ok {
			t.Errorf("%s: err=%v not matched", c.name, c.err)
		}
	}
	for _, pair := range [][2]error{{ErrEmpty, ErrNotFound}, {ErrEmpty, ErrBadK}, {ErrNotFound, ErrBadK}} {
		if errors.Is(pair[0], pair[1]) {
			t.Errorf("%v and %v not distinguishable", pair[0], pair[1])
		}
	}
	var u2 *UTF8Error
	if errors.Is(&UTF8Error{0}, ErrEmpty) || errors.As(ErrEmpty, &u2) {
		t.Error("UTF8Error not distinguishable from sentinels")
	}
	// State unchanged, still usable.
	if a.Count() != 1 {
		t.Fatalf("Count()=%d after rejections, want 1", a.Count())
	}
	got, _ := a.Complete("a", 10)
	if !reflect.DeepEqual(got, []string{"apple"}) {
		t.Fatalf("Complete after rejections=%v", got)
	}
}

// Random operation sequences must match the naive reference throughout.
func TestMatchesNaiveRandom(t *testing.T) {
	for _, seed := range []int64{1, 2, 3} {
		r := rand.New(rand.NewSource(seed))
		a := New()
		ref := map[string]int{}
		var keys []string
		for step := 0; step < 300; step++ {
			s := fmt.Sprintf("k%03d", r.Intn(60))
			if r.Intn(3) == 0 && len(keys) > 0 {
				idx := r.Intn(len(keys))
				if err := a.Delete(keys[idx]); err != nil {
					t.Fatal(err)
				}
				delete(ref, keys[idx])
				keys[idx] = keys[len(keys)-1]
				keys = keys[:len(keys)-1]
			} else {
				f := r.Intn(9) + 1
				if err := a.Insert(s, f); err != nil {
					t.Fatal(err)
				}
				if ref[s] == 0 {
					keys = append(keys, s)
				}
				ref[s] += f
			}
		}
		if err := verifyAll(a, ref); err != nil {
			t.Fatalf("seed=%d: %v", seed, err)
		}
	}
}

// N goroutines insert disjoint sets; the merged view must equal naive.
func TestConcurrentInsert(t *testing.T) {
	const n, per = 8, 200
	a := New()
	var wg sync.WaitGroup
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < per; i++ {
				if err := a.Insert(fmt.Sprintf("g%02d-%04d", g, i), i+1); err != nil {
					t.Error(err)
				}
			}
		}(g)
	}
	wg.Wait()
	ref := map[string]int{}
	for g := 0; g < n; g++ {
		for i := 0; i < per; i++ {
			ref[fmt.Sprintf("g%02d-%04d", g, i)] = i + 1
		}
	}
	if a.Count() != n*per {
		t.Fatalf("Count()=%d, want %d", a.Count(), n*per)
	}
	got, err := a.Complete("g", n*per)
	if err != nil || !reflect.DeepEqual(got, naive(ref, "g", n*per)) {
		t.Fatalf("concurrent merge mismatch: got %d entries, err=%v", len(got), err)
	}
}

func TestSelfCheck(t *testing.T) {
	if err := New().SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
