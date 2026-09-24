package api_test

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"

	"ontology/api"
	"ontology/exp"
	"ontology/wlog"
)

type writeRec struct {
	key string
	val int
}

// runInterleaving plays seed-driven writes/Nexts, drains, finishes.
func runInterleaving(t *testing.T, seed int64, writesBefore int) ([]int, []writeRec, int, int, int) {
	t.Helper()
	ex := api.New(4)
	var writes []writeRec
	put := func() {
		w := writeRec{fmt.Sprintf("k%d", len(writes)), len(writes)}
		if _, err := ex.Write(w.key, w.val); err != nil {
			t.Fatal(err)
		}
		writes = append(writes, w)
	}
	for i := 0; i < writesBefore; i++ {
		put()
	}
	s, err := ex.StartExport()
	if err != nil {
		t.Fatal(err)
	}
	S := len(writes)
	var emitted []int
	r := rand.New(rand.NewSource(seed))
	for step := 0; step < 60; step++ {
		if r.Intn(2) == 0 {
			put()
			continue
		}
		for {
			e, ok, err := ex.Next(s)
			if err != nil {
				t.Fatal(err)
			}
			if !ok {
				break
			}
			emitted = append(emitted, e.Seq)
		}
	}
	for {
		e, ok, err := ex.Next(s)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
		emitted = append(emitted, e.Seq)
	}
	hi, lo, incHi, err := ex.Finish(s)
	if err != nil {
		t.Fatal(err)
	}
	if hi != S || lo != S+1 || incHi != emitted[len(emitted)-1] {
		t.Fatalf("ranges [%d][%d,%d] S=%d", hi, lo, incHi, S)
	}
	return emitted, writes, S, lo, incHi
}

func TestNaiveConsistency(t *testing.T) {
	for seed := int64(0); seed < 30; seed++ {
		emitted, writes, _, _, _ := runInterleaving(t, seed, 5)
		for i, seq := range emitted { // emitted == naive read of first n
			if seq != i+1 || writes[i].key != fmt.Sprintf("k%d", seq-1) {
				t.Fatalf("seed=%d: emitted %v != naive 1..%d", seed, emitted, len(emitted))
			}
		}
	}
}

func TestSegmentSplit(t *testing.T) {
	emitted, _, S, lo, incHi := runInterleaving(t, 7, 3)
	for i, seq := range emitted {
		snap := seq <= S
		if snap != (i < S) {
			t.Fatalf("seq %d at pos %d: wrong segment", seq, i)
		}
	}
	if incHi > S && lo != S+1 {
		t.Fatalf("incremental lo=%d want %d", lo, S+1)
	}
}

func TestStrictlyIncreasing(t *testing.T) {
	for seed := int64(100); seed < 110; seed++ {
		emitted, _, S, _, _ := runInterleaving(t, seed, 4)
		seen := map[int]bool{}
		for i, seq := range emitted {
			if i > 0 && seq <= emitted[i-1] || seen[seq] {
				t.Fatalf("seed=%d: not strictly increasing/unique: %v", seed, emitted)
			}
			seen[seq] = true
			if seq > S && seq != S+1 && !seen[seq-1] {
				t.Fatalf("seed=%d: first incremental must be S+1", seed)
			}
		}
	}
}

func TestRejectedNoSideEffect(t *testing.T) {
	ex := api.New(1)
	ex.Write("a", 1)
	s, _ := ex.StartExport()
	if _, err := ex.Write("", 1); !errors.Is(err, wlog.ErrEmptyKey) {
		t.Fatal("empty key")
	}
	if _, err := ex.StartExport(); !errors.Is(err, api.ErrTooManyExports) {
		t.Fatal("bound")
	}
	seq, _ := ex.Write("b", 2)
	if seq != 2 {
		t.Fatalf("rejected write consumed Seq, got %d", seq)
	}
	e, ok, _ := ex.Next(s)
	if !ok || e.Seq != 1 {
		t.Fatalf("session state changed: %v %v", e, ok)
	}
	ex.Finish(s)
	if _, _, err := s.Next(); !errors.Is(err, exp.ErrFinished) {
		t.Fatal("next after finish")
	}
	if err := s.Start(0); !errors.Is(err, exp.ErrFinished) {
		t.Fatal("restart after finish")
	}
	fresh := exp.NewSession(wlog.New(), nil)
	if _, _, err := fresh.Next(); !errors.Is(err, exp.ErrNotStarted) {
		t.Fatal("next before start")
	}
}

func TestFaultInjectionDistinct(t *testing.T) {
	errs := []error{wlog.ErrEmptyKey, exp.ErrNotStarted, exp.ErrFinished, api.ErrTooManyExports}
	for i := range errs {
		for j := range errs {
			if i != j && errors.Is(errs[i], errs[j]) {
				t.Fatalf("errors %d and %d not distinct", i, j)
			}
		}
	}
}

func TestConcurrentExports(t *testing.T) {
	ex := api.New(8)
	for i := 0; i < 50; i++ {
		ex.Write(fmt.Sprintf("k%d", i), i)
	}
	type result struct {
		seqs          []int
		hi, lo, incHi int
	}
	results := make([]result, 8)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			s, err := ex.StartExport()
			if err != nil {
				t.Error(err)
				return
			}
			for {
				e, ok, err := ex.Next(s)
				if err != nil || !ok {
					break
				}
				results[g].seqs = append(results[g].seqs, e.Seq)
			}
			results[g].hi, results[g].lo, results[g].incHi, _ = ex.Finish(s)
		}(g)
	}
	wg.Wait()
	for g := 1; g < 8; g++ {
		if !reflect.DeepEqual(results[g], results[0]) {
			t.Fatalf("goroutine %d differs: %+v vs %+v", g, results[g], results[0])
		}
	}
}
