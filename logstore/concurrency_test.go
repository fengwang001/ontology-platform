package logstore

import (
	"errors"
	"fmt"
	"math/rand"
	"strconv"
	"sync"
	"testing"
)

// TestConcurrentReadWriteClean hammers the store with concurrent writers,
// deleters and readers while cleaning triggers constantly (tiny segments).
// Run with -race. Readers of a hot key must never observe a regression:
// once value N is visible, no older value may be returned later.
func TestConcurrentReadWriteClean(t *testing.T) {
	s := newStore(t, 64, 8)
	const writers = 4
	const ops = 300

	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < ops; i++ {
				key := fmt.Sprintf("k%d", rng.Intn(16))
				switch rng.Intn(3) {
				case 0, 1:
					err := s.Put(key, make([]byte, rng.Intn(20)))
					if err != nil && !errors.Is(err, ErrSpaceExhausted) {
						t.Errorf("Put: unexpected error %v", err)
						return
					}
				case 2:
					err := s.Delete(key)
					if err != nil && !errors.Is(err, ErrSpaceExhausted) && !errors.Is(err, ErrKeyNotFound) {
						t.Errorf("Delete: unexpected error %v", err)
						return
					}
				}
			}
		}(int64(w + 1))
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < ops; i++ {
			err := s.Put("hot", []byte(strconv.Itoa(i)))
			if err != nil && !errors.Is(err, ErrSpaceExhausted) {
				t.Errorf("Put hot: unexpected error: %v", err)
				return
			}
		}
	}()

	// Readers must never see the hot value go backwards, even while cleaning
	// migrates the block between segments.
	for reader := 0; reader < 3; reader++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			last := -1
			for i := 0; i < ops*2; i++ {
				v, ok := s.Get("hot")
				if !ok {
					continue
				}
				n, err := strconv.Atoi(string(v))
				if err != nil {
					t.Errorf("hot value %q not a number", v)
					return
				}
				if n < last {
					t.Errorf("reader observed regression: %d after %d", n, last)
					return
				}
				last = n
			}
		}()
	}
	wg.Wait()

	if err := s.VerifyAccounting(); err != nil {
		t.Fatalf("accounting invariant violated after concurrent run: %v", err)
	}
	t.Logf("output: concurrent run finished, invariant holds; final layout:\n%s", s.Dump())
}

// TestDeterministicLayout runs the same operation sequence against two fresh
// stores and requires byte-identical layouts.
func TestDeterministicLayout(t *testing.T) {
	script := func(s *Store) {
		rng := rand.New(rand.NewSource(42))
		for i := 0; i < 120; i++ {
			key := fmt.Sprintf("k%d", rng.Intn(10))
			if rng.Intn(4) == 0 {
				_ = s.Delete(key)
			} else {
				_ = s.Put(key, make([]byte, rng.Intn(24)))
			}
		}
	}
	s1 := newStore(t, 80, 5)
	script(s1)
	s2 := newStore(t, 80, 5)
	script(s2)
	d1, d2 := s1.Dump(), s2.Dump()
	if d1 != d2 {
		t.Fatalf("same op sequence produced different layouts:\nrun1:\n%s\nrun2:\n%s", d1, d2)
	}
	mustVerify(t, s1)
	t.Logf("output: two identical runs produced identical layout (%d bytes)", len(d1))
}

// TestAccountingInvariantFuzz drives a seeded random operation sequence and
// checks the per-segment live-bytes invariant after every single operation.
func TestAccountingInvariantFuzz(t *testing.T) {
	s := newStore(t, 96, 6)
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 500; i++ {
		key := fmt.Sprintf("k%d", rng.Intn(20))
		var op string
		if rng.Intn(4) == 0 {
			op = "del"
			_ = s.Delete(key)
		} else {
			op = "put"
			_ = s.Put(key, make([]byte, rng.Intn(30)))
		}
		if err := s.VerifyAccounting(); err != nil {
			t.Fatalf("op %d (%s %s): %v\nlayout:\n%s", i, op, key, err, s.Dump())
		}
	}
	t.Logf("output: 500 random ops, invariant held after every op; final layout:\n%s", s.Dump())
}
