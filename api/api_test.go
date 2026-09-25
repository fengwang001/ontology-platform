package api_test

import (
	"errors"
	"math/rand"
	"reflect"
	"sync"
	"testing"

	"ontology/api"
)

func applyLog(cs []api.Change) map[string]string {
	r := map[string]string{}
	for _, c := range cs {
		if c.Kind == api.Removed {
			delete(r, c.Col)
		} else {
			r[c.Col] = c.New // Added and Changed both set the new value
		}
	}
	return r
}

var sixEvents = []map[string]string{
	{"a": "1", "b": "x"},
	{"a": "1", "b": "y", "c": ""},
	{"a": "2", "b": "y", "c": ""},
	{"a": "2", "c": ""},
	{"c": "z"},
	{},
}

var sixWant = [][]api.Change{
	{{Kind: api.Added, Col: "a", New: "1"}, {Kind: api.Added, Col: "b", New: "x"}},
	{{Kind: api.Changed, Col: "b", Old: "x", New: "y"}, {Kind: api.Added, Col: "c"}},
	{{Kind: api.Changed, Col: "a", Old: "1", New: "2"}},
	{{Kind: api.Removed, Col: "b", Old: "y"}},
	{{Kind: api.Removed, Col: "a", Old: "2"}, {Kind: api.Changed, Col: "c", New: "z"}},
	{{Kind: api.Removed, Col: "c", Old: "z"}},
}

func TestReplayMatchesView(t *testing.T) {
	a := api.New()
	var log []api.Change
	for i, ev := range sixEvents {
		cs, err := a.Apply("k", ev)
		if err != nil || !reflect.DeepEqual(cs, sixWant[i]) {
			t.Fatalf("step %d: got %v (%v), want %v", i+1, cs, err, sixWant[i])
		}
		log = append(log, cs...)
		cur := a.View()["k"]
		if !reflect.DeepEqual(applyLog(log), cur) || !reflect.DeepEqual(applyLog(a.Recompute("k")), cur) {
			t.Fatalf("step %d: replay/recompute diverges from view", i+1)
		}
	}
	for _, seed := range []int64{1, 2, 3, 42} { // loops generate randomized shapes/order
		rng, src := api.New(), rand.New(rand.NewSource(seed))
		cols, vals := []string{"a", "b", "c", "d", "e"}, []string{"", "x", "y", "0"}
		logs := map[string][]api.Change{}
		for round := 0; round < 200; round++ {
			key := "k" + string(rune('0'+src.Intn(4)))
			row := map[string]string{}
			for _, c := range cols {
				if src.Intn(2) == 0 {
					row[c] = vals[src.Intn(len(vals))]
				}
			}
			cs, err := rng.Apply(key, row)
			if err != nil {
				t.Fatal(err)
			}
			logs[key] = append(logs[key], cs...)
			cur := rng.View()[key]
			if !reflect.DeepEqual(applyLog(logs[key]), cur) || !reflect.DeepEqual(applyLog(rng.Recompute(key)), cur) {
				t.Fatalf("seed %d round %d key %s: divergence", seed, round, key)
			}
		}
	}
}

func TestRejectedEventsLeaveNoTrace(t *testing.T) {
	a := api.New()
	for _, tc := range []struct {
		key string
		ev  map[string]string
		err error
	}{
		{"", map[string]string{"a": "1"}, api.ErrEmptyKey},
		{"x", nil, api.ErrNilEvent},
		{"x", map[string]string{"": "v"}, api.ErrEmptyColumn},
	} {
		if _, err := a.Apply(tc.key, tc.ev); !errors.Is(err, tc.err) {
			t.Fatalf("Apply(%q): got %v, want %v", tc.key, err, tc.err)
		}
	}
	if api.ErrEmptyKey == api.ErrNilEvent || api.ErrEmptyKey == api.ErrEmptyColumn || api.ErrNilEvent == api.ErrEmptyColumn || len(a.View()) != 0 {
		t.Fatalf("sentinel errors not distinct or rejected events left a trace: %v", a.View())
	}
	if _, err := a.Apply("x", map[string]string{"ok": "1"}); err != nil {
		t.Fatalf("service unusable after rejections: %v", err)
	}
}

func TestConcurrentReaders(t *testing.T) {
	fed := api.New()
	for i := 0; i < 100; i++ {
		key := "k" + string(rune('0'+i/10)) + string(rune('0'+i%10))
		if _, err := fed.Apply(key, map[string]string{"a": "v", "b": ""}); err != nil {
			t.Fatal(err)
		}
	}
	want := fed.View()
	const n = 32
	var wg sync.WaitGroup
	ok := make([]bool, n)
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			ok[g] = reflect.DeepEqual(fed.View(), want) && len(fed.Recompute("k00")) == 2
		}(g)
	}
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = api.New().SelfCheck() }()
	}
	wg.Wait()
	for g, v := range ok {
		if !v {
			t.Fatalf("reader %d saw a divergent view", g)
		}
	}
	// Concurrent Applies on distinct keys on a separate instance.
	wr := api.New()
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := wr.Apply("w"+string(rune('A'+i)), map[string]string{"z": "1"}); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	if len(wr.View()) != 32 {
		t.Fatalf("concurrent applies lost updates: got %d keys", len(wr.View()))
	}
}
