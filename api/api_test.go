package api_test

import (
	"errors"
	"maps"
	"reflect"
	"strings"
	"sync"
	"testing"

	"ontology/api"
)

func ch(k string, d int64) api.Change { return api.Change{Key: k, Delta: d} }

func feed(t *testing.T, g *api.Aggregator, evs ...api.Change) []api.Change {
	t.Helper()
	ps, err := g.Feed(evs)
	if err != nil {
		t.Fatalf("Feed(%v): %v", evs, err)
	}
	return ps
}

// TestInvariants pins I3 (view replays from pushes after each batch), I2
// (FlushKey closes the exact view->batch gap), I1 (post-flush == batch sum).
func TestInvariants(t *testing.T) {
	notes := [][]api.Change{
		{ch("A", 5)}, {ch("A", 3)}, {ch("A", 2)}, {ch("A", -4)},
		{ch("B", 7), ch("B", 2)}, {ch("A", 1), ch("B", 1)},
		{ch("C", 2), ch("C", 3), ch("C", 1)}, {ch("C", 1)},
	}
	cases := []struct {
		name    string
		h       int64
		batches [][]api.Change
		flush   bool
	}{
		{"notes", 3, notes, false},
		{"lex", 100, [][]api.Change{{ch("D", 1), ch("A", 1), ch("C", 1), ch("B", 1)}}, true},
	}
	for _, c := range cases {
		g, _ := api.New(c.h)
		replay, total := map[string]int64{}, map[string]int64{}
		for bi, batch := range c.batches {
			for _, x := range feed(t, g, batch...) {
				replay[x.Key] += x.Delta
			}
			for _, e := range batch {
				total[e.Key] += e.Delta
			}
			if !maps.Equal(replay, g.View()) { // I3 after each batch
				t.Fatalf("%s I3 batch %d: %v != %v", c.name, bi+1, g.View(), replay)
			}
			if c.name == "notes" && bi == 4 { // 甲: B buffered {9,2}, invisible
				if _, ok := g.View()["B"]; ok {
					t.Fatalf("B visible after batch 5: %v", g.View())
				}
			}
		}
		if c.name == "notes" {
			if g.View()["B"] != 1 || strings.Join(g.Hot(), ",") != "A,B,C" {
				t.Fatalf("after batch 8: view=%v hot=%v", g.View(), g.Hot())
			}
			pre := g.View()
			for k, want := range total { // I2: flush closes exactly the gap
				var gap int64
				for _, x := range g.FlushKey(k) {
					gap += x.Delta
				}
				if pre[k]+gap != want {
					t.Fatalf("I2 %s: %d+%d != %d", k, pre[k], gap, want)
				}
			}
		}
		if c.flush {
			want := []api.Change{ch("A", 1), ch("B", 1), ch("C", 1), ch("D", 1)}
			if ps := g.FlushAll(); !reflect.DeepEqual(ps, want) {
				t.Fatalf("FlushAll %v, want %v", ps, want)
			}
		}
		if extra := g.FlushAll(); len(extra) != 0 || !maps.Equal(g.View(), total) { // I1
			t.Fatalf("I1 leftover=%v view=%v total=%v", extra, g.View(), total)
		}
	}
}

// TestRejections: four distinct sentinels, whole-batch no-trace reject, reuse OK.
func TestRejections(t *testing.T) {
	sents := []error{api.ErrInvalidH, api.ErrEmptyBatch, api.ErrEmptyKey, api.ErrZeroDelta}
	for i := range sents {
		for _, s2 := range sents[i+1:] {
			if errors.Is(sents[i], s2) || sents[i].Error() == s2.Error() {
				t.Fatalf("sentinels not distinct: %v / %v", sents[i], s2)
			}
		}
	}
	if _, err := api.New(-1); !errors.Is(err, api.ErrInvalidH) {
		t.Fatalf("New(-1): %v", err)
	}
	g, _ := api.New(2)
	feed(t, g, ch("X", 1), ch("Y", 1))
	v0, h0 := g.View(), strings.Join(g.Hot(), ",")
	bad := [][]api.Change{nil, {ch("X", 1), ch("", 1)}, {ch("X", 1), ch("Z", 0)}}
	want := []error{api.ErrEmptyBatch, api.ErrEmptyKey, api.ErrZeroDelta}
	for i, evs := range bad {
		if _, err := g.Feed(evs); !errors.Is(err, want[i]) ||
			!maps.Equal(g.View(), v0) || strings.Join(g.Hot(), ",") != h0 {
			t.Fatalf("reject %v: err=%v or trace left (view=%v hot=%v)", evs, err, g.View(), g.Hot())
		}
	}
	if ps := feed(t, g, ch("X", 1)); len(ps) != 1 || ps[0] != ch("X", 2) {
		t.Fatalf("use after rejection: %v", ps) // X 1+1 pushes +2
	}
	if err := g.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

// TestConcurrentReaders: N goroutines released by channel (no sleeps) all agree.
func TestConcurrentReaders(t *testing.T) {
	g, _ := api.New(3)
	for _, evs := range [][]api.Change{{ch("A", 5)}, {ch("A", 3)}, {ch("A", 2)}, {ch("B", 7), ch("B", 2)}} {
		feed(t, g, evs...)
	}
	const n = 32
	var wg sync.WaitGroup
	start := make(chan struct{})
	vs := make([]map[string]int64, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			vs[i] = g.View()
			_ = g.Hot()
			if err := g.SelfCheck(); err != nil {
				t.Errorf("SelfCheck in reader %d: %v", i, err)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	for i := 1; i < n; i++ {
		if !maps.Equal(vs[0], vs[i]) {
			t.Fatalf("reader %d: %v != %v", i, vs[i], vs[0])
		}
	}
}
