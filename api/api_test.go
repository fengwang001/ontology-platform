package api_test

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"ontology/api"
)

type op struct {
	side api.Side
	val  string
	d    int
}

func batchRef(ops []op) map[string]int {
	c := [2]map[string]int{{}, {}}
	for _, o := range ops {
		c[o.side][o.val] += o.d
	}
	out := map[string]int{}
	for v, l := range c[0] {
		if m := min(l, c[1][v]); v != "<null>" && m > 0 {
			out[v] = m
		}
	}
	return out
}
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

var eightOps = []op{
	{api.L, "a", 1}, {api.R, "a", 1}, {api.R, "a", 1}, {api.L, "a", 1},
	{api.R, "a", 1}, {api.R, "a", -1}, {api.R, "a", -1}, {api.L, "a", -1},
}

func TestViewMatchesBatchReplay(t *testing.T) {
	seqs := [][]op{eightOps,
		{{api.L, "<null>", 2}, {api.R, "<null>", 3}, {api.L, "b", 1}, {api.R, "b", 1}},
		{{api.L, "p", 2}, {api.L, "q", 4}, {api.R, "q", 1}, {api.R, "p", 3}, {api.L, "q", -4}, {api.R, "p", -1}},
		{{api.L, "x", 3}, {api.R, "x", 5}, {api.L, "x", -1}, {api.R, "x", -4}}}
	for i, seq := range seqs {
		a := api.New()
		for j, o := range seq {
			_, err := a.Apply(o.side, o.val, o.d)
			must(t, err)
			if got, want := a.View(), batchRef(seq[:j+1]); !reflect.DeepEqual(got, want) {
				t.Fatalf("seq%d prefix%d: %v != %v", i, j, got, want)
			}
		}
	}
}

func TestChangeLogPrefixes(t *testing.T) {
	seq := []op{{api.L, "x", 3}, {api.R, "x", 5}, {api.L, "x", -1},
		{api.R, "x", -4}, {api.L, "y", 2}, {api.R, "y", 2}}
	a, down := api.New(), map[string]int{}
	for j, o := range seq {
		ch, err := a.Apply(o.side, o.val, o.d)
		must(t, err)
		for _, c := range ch {
			if !c.Add && down[c.Val] == 0 {
				t.Fatalf("op%d: minus with no live copy", j+1)
			}
			down[c.Val] += map[bool]int{true: 1, false: -1}[c.Add]
		}
		if !reflect.DeepEqual(down, batchRef(seq[:j+1])) {
			t.Fatalf("prefix %d: %v != batch", j+1, down)
		}
	}
}

func TestCrossingEightSteps(t *testing.T) {
	want := [8][3]int{{0, 0, 0}, {1, 1, 1}, {1, 0, 0}, {2, 1, 1},
		{2, 0, 0}, {2, 0, 0}, {1, 1, -1}, {1, 0, 0}}
	a := api.New()
	for i, o := range eightOps {
		ch, err := a.Apply(o.side, o.val, o.d)
		must(t, err)
		sign := 0
		if len(ch) > 0 {
			sign = map[bool]int{true: 1, false: -1}[ch[0].Add]
		}
		if a.View()["a"] != want[i][0] || len(ch) != want[i][1] || sign != want[i][2] {
			t.Fatalf("step%d: m=%d n=%d sign=%d want %v", i+1, a.View()["a"], len(ch), sign, want[i])
		}
	}
}

func TestErrorsDistinctAndAtomic(t *testing.T) {
	bad := []struct {
		side api.Side
		val  string
		d    int
		want error
	}{{api.L, "v", 0, api.ErrZeroDelta},
		{api.L, "v", -1, api.ErrCountNegative},
		{api.L, "", 1, api.ErrEmptyVal}}
	a, seen := api.New(), map[error]bool{}
	for _, tc := range bad {
		before := len(a.View())
		_, err := a.Apply(tc.side, tc.val, tc.d)
		if !errors.Is(err, tc.want) || len(a.View()) != before {
			t.Fatalf("want %v, err=%v, changed=%v", tc.want, err, len(a.View()) != before)
		}
		seen[err] = true
	}
	if len(seen) != 3 {
		t.Fatalf("errors not pairwise distinct: %d unique", len(seen))
	}
	_, err := a.Apply(api.L, "v", 1)
	must(t, err)
}

func TestConcurrentReaders(t *testing.T) {
	a := api.New()
	for i := 0; i < 300; i++ {
		v := fmt.Sprintf("k%d", i)
		_, e1 := a.Apply(api.L, v, 1)
		_, e2 := a.Apply(api.R, v, 1)
		must(t, e1)
		must(t, e2)
	}
	const N = 32
	var wg sync.WaitGroup
	start := make(chan struct{})
	got := make([]map[string]int, N)
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(i int) { defer wg.Done(); <-start; got[i] = a.View() }(i)
	}
	close(start)
	wg.Wait()
	for i := 1; i < N; i++ {
		if !reflect.DeepEqual(got[0], got[i]) {
			t.Fatalf("reader %d diverged", i)
		}
	}
}

func TestSelfCheck(t *testing.T) { must(t, api.New().SelfCheck()) }
