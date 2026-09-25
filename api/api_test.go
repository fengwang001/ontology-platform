package api_test

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"ontology/api"
)

type ch struct {
	op   api.Op
	k, v string
}

var seq8 = []ch{
	{api.Set, "a", "1"}, {api.Set, "b", "2"}, {api.Set, "c", "3"}, {api.Del, "b", ""},
	{api.Set, "d", "4"}, {api.Set, "a", "9"}, {api.Del, "c", ""}, {api.Set, "e", "5"},
}

func naive(cs []ch) map[string]string {
	m := map[string]string{}
	for _, c := range cs {
		if c.op == api.Del {
			delete(m, c.k)
		} else {
			m[c.k] = c.v
		}
	}
	return m
}
func feed(limit int, cs []ch) *api.Txn {
	tx, _ := api.New(limit)
	for _, c := range cs {
		tx.Mutate(c.op, c.k, c.v)
	}
	tx.Commit()
	return tx
}
func asMap(tx *api.Txn) map[string]string {
	m := map[string]string{}
	for _, p := range tx.View() {
		m[p.Key] = p.Val
	}
	return m
}
func TestNaiveReference(t *testing.T) {
	cases := []struct {
		limit int
		cs    []ch
	}{
		{3, seq8},
		{5, []ch{{api.Set, "x", "1"}, {api.Del, "y", ""}}},
	}
	for _, c := range cases {
		if got := asMap(feed(c.limit, c.cs)); !reflect.DeepEqual(got, naive(c.cs)) {
			t.Fatalf("got %v want %v", got, naive(c.cs))
		}
	}
}

func TestNoLoss(t *testing.T) {
	tx := feed(3, seq8)
	if tx.Spilled() != 2 {
		t.Fatalf("Spilled=%d want 2", tx.Spilled())
	}
	for _, k := range []string{"b", "c"} {
		if _, ok := tx.Get(k); ok {
			t.Fatalf("deleted key %q survived replay", k)
		}
	}
	if got := asMap(tx); !reflect.DeepEqual(got, naive(seq8)) {
		t.Fatalf("a spilled Del was lost: %v", got)
	}
}

func TestFIFOOrder(t *testing.T) {
	tx := feed(3, seq8)
	if tx.Spilled() != 2 {
		t.Fatalf("blocks=%d want 2", tx.Spilled())
	}
	if v, _ := tx.Get("a"); v != "9" {
		t.Fatalf("a=%q want 9 (FIFO broken)", v)
	}
	last := feed(1, []ch{{api.Set, "k", "0"}, {api.Set, "k", "1"}, {api.Set, "k", "2"}, {api.Set, "k", "3"}})
	if last.Spilled() != 3 {
		t.Fatalf("blocks=%d want 3", last.Spilled())
	}
	if v, _ := last.Get("k"); v != "3" {
		t.Fatalf("k=%q want 3 (FIFO broken)", v)
	}
}

func TestRejectedLeavesNoTrace(t *testing.T) {
	live, _ := api.New(2)
	live.Mutate(api.Set, "a", "1")
	done := feed(2, []ch{{api.Set, "a", "1"}})
	cases := []struct {
		name string
		f    func() error
		want error
	}{
		{"badlimit", func() error { _, e := api.New(0); return e }, api.ErrBadLimit},
		{"badop", func() error { return live.Mutate(api.Op(7), "z", "1") }, api.ErrInvalidOp},
		{"emptykey", func() error { return live.Mutate(api.Set, "", "1") }, api.ErrEmptyKey},
		{"closed", func() error { return done.Mutate(api.Set, "z", "1") }, api.ErrClosed},
	}
	lb, dv := live.Spilled(), len(done.View())
	seen := map[error]bool{}
	for _, c := range cases {
		if err := c.f(); seen[c.want] || !errors.Is(err, c.want) {
			t.Fatalf("%s: err=%v want %v (or duplicate sentinel)", c.name, err, c.want)
		}
		seen[c.want] = true
	}
	if live.Spilled() != lb {
		t.Fatal("a rejected Mutate changed the live buffer/blocks")
	}
	if live.Mutate(api.Set, "g", "2") != nil {
		t.Fatal("txn unusable after a rejection")
	}
	if done.Spilled() != 0 || len(done.View()) != dv {
		t.Fatal("post-commit Mutate left a trace")
	}
}

func TestConcurrentReaders(t *testing.T) {
	tx, _ := api.New(4)
	for i := 0; i < 60; i++ {
		tx.Mutate(api.Set, fmt.Sprintf("k%02d", i), "v")
	}
	tx.Commit()
	read := func() []api.KV { _, _ = tx.Get("k00"); _ = tx.Spilled(); _ = tx.SelfCheck(); return tx.View() }
	const N = 32
	var wg sync.WaitGroup
	snap, start := make([][]api.KV, N), make(chan struct{})
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(g int) { defer wg.Done(); <-start; snap[g] = read() }(g)
	}
	close(start)
	wg.Wait()
	for i := 1; i < N; i++ {
		if !reflect.DeepEqual(snap[i], snap[0]) { // field-by-field identical, no sleep
			t.Fatalf("reader %d view %v != reader 0 %v", i, snap[i], snap[0])
		}
	}
}
