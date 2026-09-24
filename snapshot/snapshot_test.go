package snapshot

import (
	"fmt"
	"math/rand"
	"strconv"
	"sync"
	"testing"
)

// TestBatchReferenceConsistency pins invariant 1: every read equals the
// batch reference "last Update wins per key" of a published version.
func TestBatchReferenceConsistency(t *testing.T) {
	s := NewStore()
	ref := map[string]string{}
	rng := rand.New(rand.NewSource(42))
	for round := 0; round < 500; round++ {
		k := "k" + strconv.Itoa(rng.Intn(12))
		v := "v" + strconv.Itoa(round)
		s.Update(k, v)
		ref[k] = v
		h := s.Snapshot() // one complete published version
		if h.Len() != len(ref) {
			t.Fatalf("round %d: len %d != ref %d", round, h.Len(), len(ref))
		}
		for k, want := range ref {
			if got, ok := h.Read(k); !ok || got != want {
				t.Fatalf("round %d key %s: got %q,%v want %q", round, k, got, ok, want)
			}
		}
	}
	ks := []string{"k0", "k5", "k11"}
	got := s.ReadKeys(ks)
	for _, k := range ks {
		if got[k] != ref[k] {
			t.Fatalf("ReadKeys %s: %q != %q", k, got[k], ref[k])
		}
	}
	if v, _ := s.Read("k3"); v != ref["k3"] {
		t.Fatalf("Read k3: %q != %q", v, ref["k3"])
	}
}

// TestSnapshotHandleImmutable pins invariant 2: later Updates never
// mutate a previously taken handle.
func TestSnapshotHandleImmutable(t *testing.T) {
	s := NewStore()
	s.Update("A", "1")
	s.Update("B", "2")
	h := s.Snapshot()
	for i := 0; i < 200; i++ {
		s.Update("A", strconv.Itoa(9+i))
		s.Update("C", strconv.Itoa(i))
	}
	if v, ok := h.Read("A"); !ok || v != "1" {
		t.Fatalf("old handle A = %q,%v, want 1", v, ok)
	}
	if v, ok := h.Read("B"); !ok || v != "2" {
		t.Fatalf("old handle B = %q,%v, want 2", v, ok)
	}
	if _, ok := h.Read("C"); ok {
		t.Fatal("old handle saw key C added after it was taken")
	}
	if h.ID() != 2 || h.Len() != 2 {
		t.Fatalf("old handle id/len = %d/%d, want 2/2", h.ID(), h.Len())
	}
}

// TestReadAccessCounterDoesNotGrowWithMapSize pins invariant 3's cost:
// reading 2 keys touches 2 entries regardless of table size. The
// unexported counter is read in-package, never via an exported API.
func TestReadAccessCounterDoesNotGrowWithMapSize(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	prev := int64(-1)
	for _, m := range []int{100, 1000, 10000} {
		s := NewStore()
		for i := 0; i < m; i++ {
			s.Update("k"+strconv.Itoa(i), "x")
		}
		pick := []string{"k" + strconv.Itoa(rng.Intn(m)), "k" + strconv.Itoa(rng.Intn(m))}
		out := s.ReadKeys(pick)
		hits := s.readHits.Load()
		if hits > int64(len(pick))+1 || hits != int64(len(pick)) || len(out) != len(pick) {
			t.Fatalf("m=%d: hits=%d out=%d, want %d entries for %d keys", m, hits, len(out), len(pick), len(pick))
		}
		if prev >= 0 && hits != prev {
			t.Fatalf("accessed entries grew with table: %d then %d", prev, hits)
		}
		prev = hits
	}
}

// TestConcurrentReadersSeeWholeVersion pins invariant 3 under race:
// every ReadKeys result equals one fully published version recorded by
// the sole writer. No sleeps; joins use WaitGroups.
func TestConcurrentReadersSeeWholeVersion(t *testing.T) {
	s := NewStore()
	keys := []string{"k0", "k1", "k2", "k3"}
	for _, k := range keys {
		s.Update(k, "r0")
	}
	sig := func(m map[string]string) string { return fmt.Sprint(m) }
	var mu sync.Mutex
	exp := map[string]bool{sig(map[string]string{"k0": "r0", "k1": "r0", "k2": "r0", "k3": "r0"}): true}
	var wwg sync.WaitGroup
	wwg.Add(1)
	go func() { // sole writer; one single-load ReadKeys is the version just published
		defer wwg.Done()
		for r := 1; r <= 300; r++ {
			for _, k := range keys {
				s.Update(k, "r"+strconv.Itoa(r))
				mu.Lock()
				exp[sig(s.ReadKeys(keys))] = true
				mu.Unlock()
			}
		}
	}()
	var omu sync.Mutex
	obs := []string{}
	var rwg sync.WaitGroup
	for g := 0; g < 8; g++ {
		rwg.Add(1)
		go func() {
			defer rwg.Done()
			for i := 0; i < 2000; i++ {
				omu.Lock()
				obs = append(obs, sig(s.ReadKeys(keys)))
				omu.Unlock()
			}
		}()
	}
	wwg.Wait()
	rwg.Wait()
	mu.Lock()
	defer mu.Unlock()
	for i, o := range obs {
		if !exp[o] {
			t.Fatalf("read %d is a torn view matching no published version: %s", i, o)
		}
	}
}
