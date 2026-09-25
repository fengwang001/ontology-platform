package api_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/api"
	"ontology/evol"
)

func TestSelfCheck(t *testing.T) {
	if err := api.New().SelfCheck(); err != nil { // pins invariants 1-4 + O(1) defaults
		t.Fatalf("SelfCheck: %v", err)
	}
}

// TestEightSteps pins the eight-step derivation from NOTES.md.
func TestEightSteps(t *testing.T) {
	x := api.New()
	r1, _ := x.Write(1, map[string]int{"a": 9, "b": 7})
	r2, _ := x.Write(2, map[string]int{"a": 5})
	r3, _ := x.Write(3, map[string]int{"a": 1, "c": 2, "d": 3})
	cases := []struct {
		step int
		rec  api.Record
		r    int
		want map[string]int
	}{
		{2, r1, 3, map[string]int{"a": 9, "c": 10, "d": 20}},
		{4, r2, 3, map[string]int{"a": 5, "c": 10, "d": 20}},
		{6, r3, 2, map[string]int{"a": 1, "b": 2, "c": 2}},
		{7, r2, 2, map[string]int{"a": 5, "b": 2, "c": 10}},
		{8, r1, 1, map[string]int{"a": 9, "b": 7}},
	}
	for _, c := range cases {
		got := must(x.Read(c.rec, c.r))
		if fmt.Sprint(got) != fmt.Sprint(c.want) {
			t.Errorf("step %d: got %v, want %v", c.step, got, c.want)
		}
	}
}
func TestBackwardCompatible(t *testing.T) {
	x := api.New()
	full := []map[string]int{{"a": 1, "b": 2}, {"a": 1, "b": 2, "c": 10}, {"a": 1, "c": 30, "d": 20}}
	for w := 1; w <= 3; w++ {
		rec := must(x.Write(w, full[w-1]))
		for r := w; r <= 3; r++ {
			got := must(x.Read(rec, r))
			for f, v := range full[w-1] {
				if _, common := full[r-1][f]; common && got[f] != v {
					t.Errorf("W=%d R=%d %s: got %d, want %d", w, r, f, got[f], v)
				}
			}
		}
	}
}
func TestFrozenDefaults(t *testing.T) {
	x := api.New()
	r1, _ := x.Write(1, map[string]int{"a": 9, "b": 7})
	r2, _ := x.Write(2, map[string]int{"a": 5})
	cases := []struct {
		rec api.Record
		r   int
		f   string
		v   int
	}{
		{r1, 2, "c", 10}, {r1, 3, "c", 10}, // c frozen at introduction default
		{r1, 3, "d", 20}, {r2, 2, "c", 10}, {r2, 3, "c", 10},
	}
	for _, c := range cases {
		if got := must(x.Read(c.rec, c.r))[c.f]; got != c.v {
			t.Errorf("W=%d R=%d %s: got %d, want %d", c.rec.W(), c.r, c.f, got, c.v)
		}
	}
}

func TestRejectionLeavesNoTrace(t *testing.T) {
	x := api.New()
	r1 := must(x.Write(1, map[string]int{"a": 1}))
	writes := []struct {
		w int
		v map[string]int
		e error
	}{
		{0, nil, evol.ErrInvalidWriteVersion}, {4, nil, evol.ErrInvalidWriteVersion},
		{1, map[string]int{"z": 1}, evol.ErrUnknownField},
		{1, map[string]int{"": 1}, evol.ErrUnknownField},
	}
	n0 := x.Len()
	for i, b := range writes {
		if _, err := x.Write(b.w, b.v); !errors.Is(err, b.e) {
			t.Errorf("write case %d: got %v, want %v", i, err, b.e)
		}
	}
	for i, r := range []int{0, 4} {
		if _, err := x.Read(r1, r); !errors.Is(err, evol.ErrInvalidReadVersion) {
			t.Errorf("read case %d: got %v, want %v", i, err, evol.ErrInvalidReadVersion)
		}
	}
	if x.Len() != n0 {
		t.Fatalf("Len changed: %d -> %d", n0, x.Len())
	}
	if _, err := x.Write(2, map[string]int{"a": 8}); err != nil { // still usable
		t.Fatalf("write after rejects: %v", err)
	}
}

func TestConcurrentReads(t *testing.T) {
	x := api.New()
	rec := must(x.Write(1, map[string]int{"a": 9, "b": 7}))
	want := map[int]string{}
	for r := 1; r <= 3; r++ {
		want[r] = fmt.Sprint(must(x.Read(rec, r)))
	}
	const n = 24
	var wg sync.WaitGroup
	var once sync.Once
	var fail error
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for k := 0; k < 200; k++ {
				if _, err := x.Write(3, map[string]int{"a": i, "d": k}); err != nil {
					once.Do(func() { fail = err })
					return
				}
				r := 1 + k%3
				got, err := x.Read(rec, r)
				if err != nil || fmt.Sprint(got) != want[r] {
					once.Do(func() { fail = fmt.Errorf("R=%d got %v", r, got) })
					return
				}
			}
		}(i)
	}
	wg.Wait()
	if fail != nil {
		t.Fatal(fail)
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
