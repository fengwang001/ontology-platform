package mvcc

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func TestFaultSentinels(t *testing.T) {
	s := New()
	live, done := s.Begin(), s.Begin()
	must(t, s.Write(done, "k", "v"))
	must(t, s.Commit(done))
	sentinels := []error{ErrEmptyKey, ErrEmptyValue, ErrTxNotBegun, ErrTxCommitted}
	for i := range sentinels {
		for j := i + 1; j < len(sentinels); j++ {
			if sentinels[i] == sentinels[j] {
				t.Fatalf("sentinels %d,%d identical", i, j)
			}
		}
	}
	cases := []struct {
		name string
		err  error
		want error
	}{
		{"empty key", s.Write(live, "", "v"), ErrEmptyKey},
		{"empty value", s.Write(live, "k", ""), ErrEmptyValue},
		{"write not begun", s.Write(1<<30, "k", "v"), ErrTxNotBegun},
		{"commit not begun", s.Commit(1 << 30), ErrTxNotBegun},
		{"readtx not begun", errOf(s.ReadTx(1<<30, "k")), ErrTxNotBegun},
		{"write after commit", s.Write(done, "a", "b"), ErrTxCommitted},
		{"commit after commit", s.Commit(done), ErrTxCommitted},
		{"readtx after commit", errOf(s.ReadTx(done, "k")), ErrTxCommitted},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !errors.Is(c.err, c.want) {
				t.Fatalf("got %v want %v", c.err, c.want)
			}
		})
	}
}
func errOf(_ string, _ bool, err error) error { return err }

func TestReadCostConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		t.Run(fmt.Sprintf("m=%d", m), func(t *testing.T) {
			s := New()
			for i := 0; i < m; i++ {
				tx := s.Begin()
				must(t, s.Write(tx, "k", fmt.Sprintf("v%d", i)))
				must(t, s.Commit(tx))
			}
			v, ok := s.Read("k")
			if !ok || v != fmt.Sprintf("v%d", m-1) {
				t.Fatalf("latest=%q,%v want v%d", v, ok, m-1)
			}
			if got := s.probes.Load(); got != readCostBound {
				t.Fatalf("inspected %d want constant %d (m=%d)", got, readCostBound, m)
			}
		})
	}
}
func TestRejectsNoStateChange(t *testing.T) {
	s := New()
	tx := s.Begin()
	must(t, s.Write(tx, "K", "1"))
	must(t, s.Commit(tx))
	liveID := s.Begin()
	live := s.txs[liveID]
	c0, nKeys := s.c, len(s.keys)
	rejects := []func() error{
		func() error { return s.Write(liveID, "", "v") },
		func() error { return s.Write(liveID, "K", "") },
		func() error { return s.Write(1<<30, "K", "z") },
		func() error { return s.Commit(1 << 30) },
		func() error { return s.Write(tx, "K", "2") },
		func() error { return s.Commit(tx) },
	}
	for i, rej := range rejects {
		if err := rej(); err == nil {
			t.Fatalf("reject %d succeeded", i)
		}
		if s.c != c0 || len(s.keys) != nKeys || live.pending.Len() != 0 {
			t.Fatalf("state changed after reject %d", i)
		}
	}
	must(t, s.Write(liveID, "K", "2"))
	must(t, s.Commit(liveID))
	if s.c != c0+1 {
		t.Fatalf("c=%d want %d", s.c, c0+1)
	}
	if v, ok := s.Read("K"); !ok || v != "2" {
		t.Fatalf("Read K=%q,%v want 2", v, ok)
	}
}
func TestConcurrentReadersAgree(t *testing.T) {
	s := New()
	const nk, nr = 64, 16
	ref := make([]string, nk)
	for i := range ref {
		ref[i] = fmt.Sprintf("v%d", i)
		tx := s.Begin()
		must(t, s.Write(tx, fmt.Sprintf("k%d", i), ref[i]))
		must(t, s.Commit(tx))
	}
	var wg sync.WaitGroup
	var bad atomic.Bool
	for g := 0; g < nr; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := 0; r < 100; r++ {
				for i, w := range ref {
					if v, ok := s.Read(fmt.Sprintf("k%d", i)); !ok || v != w {
						bad.Store(true)
					}
				}
			}
		}()
	}
	wg.Wait()
	if bad.Load() {
		t.Fatal("concurrent readers disagreed")
	}
}
