package liveness

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
)

func TestErrorPrioritiesAndStateUnchanged(t *testing.T) {
	t.Run("add: negative then duplicate then sealed", func(t *testing.T) {
		a := NewAnalyzer()
		if err := a.AddBlock(BlockSpec{ID: 5}); err != nil {
			t.Fatal(err)
		}
		// Negative id is checked before duplicate, even when both hold.
		if err := a.AddBlock(BlockSpec{ID: -5}); !errors.Is(err, ErrNegativeBlockID) {
			t.Fatalf("got %v, want ErrNegativeBlockID", err)
		}
		// Duplicate is checked before sealed, even when both hold.
		if err := a.Seal(); err != nil {
			t.Fatal(err)
		}
		if err := a.AddBlock(BlockSpec{ID: 5}); !errors.Is(err, ErrDuplicateBlockID) {
			t.Fatalf("got %v, want ErrDuplicateBlockID", err)
		}
		if err := a.AddBlock(BlockSpec{ID: 6}); !errors.Is(err, ErrAlreadySealed) {
			t.Fatalf("got %v, want ErrAlreadySealed", err)
		}
		t.Logf("judgement: add error order negative < duplicate < sealed verified")
	})

	t.Run("seal: empty then twice then missing successor in order", func(t *testing.T) {
		if err := NewAnalyzer().Seal(); !errors.Is(err, ErrNoBlocks) {
			t.Fatalf("empty seal got %v, want ErrNoBlocks", err)
		}
		a := buildSealed(t, []BlockSpec{{ID: 0}})
		if err := a.Seal(); !errors.Is(err, ErrSealedTwice) {
			t.Fatalf("re-seal got %v, want ErrSealedTwice", err)
		}

		b := NewAnalyzer()
		_ = b.AddBlock(BlockSpec{ID: 2, Successors: []int{9}})
		_ = b.AddBlock(BlockSpec{ID: 1, Successors: []int{2, 4}})
		err := b.Seal()
		// Blocks checked by ascending id (1 before 2), successors in order: 4 first.
		if !errors.Is(err, ErrMissingSucc) {
			t.Fatalf("got %v, want ErrMissingSucc", err)
		}
		ms := err.(*MissingSuccessorError)
		if ms.BlockID != 1 || ms.Successor != 4 {
			t.Fatalf("got block %d succ %d, want block 1 succ 4", ms.BlockID, ms.Successor)
		}
		t.Logf("judgement: first missing successor by ascending block id and occurrence order: %v", err)

		// Failed seal leaves state intact: fill references and retry.
		if err := b.AddBlock(BlockSpec{ID: 4}); err != nil {
			t.Fatal(err)
		}
		if err := b.AddBlock(BlockSpec{ID: 9}); err != nil {
			t.Fatal(err)
		}
		if err := b.Seal(); err != nil {
			t.Fatalf("retry Seal = %v, want nil", err)
		}
		t.Logf("judgement: after failed seal, adding missing blocks and re-sealing succeeds")
	})

	t.Run("query: not sealed then unknown block", func(t *testing.T) {
		a := NewAnalyzer()
		_ = a.AddBlock(BlockSpec{ID: 0})
		if _, err := a.LiveIn(0); !errors.Is(err, ErrNotSealed) {
			t.Fatalf("got %v, want ErrNotSealed", err)
		}
		if _, err := a.LiveOut(7); !errors.Is(err, ErrNotSealed) {
			t.Fatalf("got %v, want ErrNotSealed (not-sealed checked first)", err)
		}
		if _, err := a.Results(); !errors.Is(err, ErrNotSealed) {
			t.Fatalf("got %v, want ErrNotSealed", err)
		}
		if _, err := a.EntryLiveIn(); !errors.Is(err, ErrNotSealed) {
			t.Fatalf("got %v, want ErrNotSealed", err)
		}
		if err := a.Seal(); err != nil {
			t.Fatal(err)
		}
		if _, err := a.LiveIn(7); !errors.Is(err, ErrNoSuchBlock) {
			t.Fatalf("got %v, want ErrNoSuchBlock", err)
		}
		t.Logf("judgement: query error order not-sealed < no-such-block verified")
	})
}

// Replaying the exact same sequence produces identical, lexicographically
// ordered output; concurrent interleavings are equivalent to some serial
// ordering of the same additions.
func TestReplayDeterminism(t *testing.T) {
	specs := []BlockSpec{
		{ID: 3, Instructions: []Instruction{uins("z", "a"), dins("z")}, Successors: []int{1, 1}},
		{ID: 1, Instructions: []Instruction{udins([]string{"b"}, "q"), uins("a")}, Successors: []int{2}},
		{ID: 2, Instructions: []Instruction{uins("c", "a", "b"), dins("c")}, Successors: []int{1}},
		{ID: 0, Instructions: []Instruction{dins("a"), uins("z")}, Successors: []int{3, 1}},
	}
	logSpecs(t, specs)

	play := func() []BlockResult {
		a := NewAnalyzer()
		for _, s := range specs {
			if err := a.AddBlock(s); err != nil {
				t.Fatal(err)
			}
		}
		if err := a.Seal(); err != nil {
			t.Fatal(err)
		}
		r, err := a.Results()
		if err != nil {
			t.Fatal(err)
		}
		return r
	}

	first := play()
	firstStr := fmt.Sprintf("%v", first)
	for i := 0; i < 5; i++ {
		if got := fmt.Sprintf("%v", play()); got != firstStr {
			t.Fatalf("replay %d differs:\n%s\nvs\n%s", i, got, firstStr)
		}
	}
	for _, r := range first {
		if !sort.StringsAreSorted(r.LiveIn) || !sort.StringsAreSorted(r.LiveOut) ||
			!sort.StringsAreSorted(r.UpwardExposed) || !sort.StringsAreSorted(r.Defined) {
			t.Fatalf("block %d sets not lexicographically sorted: %+v", r.ID, r)
		}
	}
	assertAgainstNaive(t, buildSealed(t, specs), specs)
	t.Logf("output (stable across replays): %s", firstStr)
}

func TestConcurrentAddSealQuery(t *testing.T) {
	specs := []BlockSpec{
		{ID: 0, Instructions: []Instruction{uins("x")}, Successors: []int{1}},
		{ID: 1, Instructions: []Instruction{udins([]string{"y"}, "y"), dins("x")}, Successors: []int{2, 1}},
		{ID: 2, Instructions: []Instruction{uins("x", "y")}},
	}

	// checkSnapshot verifies that a returned snapshot satisfies the equations
	// exactly. A sealed graph reached under concurrency may be any prefix that
	// is closed under successors (some serial order sealed first); its
	// LiveIn/LiveOut must still be its own least fixpoint.
	checkSnapshot := func(r []BlockResult) error {
		byID := make(map[int]BlockResult, len(r))
		for _, blk := range r {
			byID[blk.ID] = blk
		}
		for _, blk := range r {
			for _, succ := range blk.Successors {
				if _, ok := byID[succ]; !ok {
					return fmt.Errorf("block %d references absent successor %d", blk.ID, succ)
				}
			}
		}
		asSet := func(xs []string) map[string]bool {
			m := make(map[string]bool, len(xs))
			for _, x := range xs {
				m[x] = true
			}
			return m
		}
		for _, blk := range r {
			wantOut := map[string]bool{}
			for _, succ := range blk.Successors {
				for _, v := range byID[succ].LiveIn {
					wantOut[v] = true
				}
			}
			wantIn := asSet(blk.UpwardExposed)
			def := asSet(blk.Defined)
			for v := range wantOut {
				if !def[v] {
					wantIn[v] = true
				}
			}
			if !reflect.DeepEqual(asSet(blk.LiveOut), wantOut) {
				return fmt.Errorf("block %d LiveOut=%v want %v", blk.ID, blk.LiveOut, wantOut)
			}
			if !reflect.DeepEqual(asSet(blk.LiveIn), wantIn) {
				return fmt.Errorf("block %d LiveIn=%v want %v", blk.ID, blk.LiveIn, wantIn)
			}
		}
		return nil
	}

	run := func() error {
		a := NewAnalyzer()

		var addWG sync.WaitGroup
		for _, s := range specs {
			addWG.Add(1)
			go func(spec BlockSpec) {
				defer addWG.Done()
				// In some serial orders seal wins first; ErrAlreadySealed is
				// then the correct result for the late adds.
				if err := a.AddBlock(spec); err != nil && !errors.Is(err, ErrAlreadySealed) {
					t.Errorf("concurrent AddBlock(%d) unexpected error: %v", spec.ID, err)
				}
			}(s)
		}

		// Querier races with adds and sealing: only ErrNotSealed or a fully
		// equation-consistent snapshot are acceptable at every instant.
		stop := make(chan struct{})
		var queryWG sync.WaitGroup
		queryWG.Add(1)
		go func() {
			defer queryWG.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				r, err := a.Results()
				if err != nil {
					if !errors.Is(err, ErrNotSealed) {
						t.Errorf("unexpected query error: %v", err)
						return
					}
					continue
				}
				if err := checkSnapshot(r); err != nil {
					t.Errorf("inconsistent concurrent snapshot: %v", err)
					return
				}
			}
		}()

		// Keep trying to seal until one attempt wins; failed seals
		// (no-blocks / missing-successor) leave the analyzer unsealed.
		for {
			err := a.Seal()
			if err == nil || errors.Is(err, ErrSealedTwice) {
				break
			}
			if !errors.Is(err, ErrNoBlocks) && !errors.Is(err, ErrMissingSucc) {
				return fmt.Errorf("unexpected seal error: %w", err)
			}
		}
		addWG.Wait()

		// Many concurrent re-seals: at most one may succeed.
		var postWG sync.WaitGroup
		var sealedCount int64
		for i := 0; i < 10; i++ {
			postWG.Add(2)
			go func() {
				defer postWG.Done()
				if err := a.Seal(); err == nil {
					atomic.AddInt64(&sealedCount, 1)
				} else if !errors.Is(err, ErrSealedTwice) {
					t.Errorf("concurrent Seal unexpected error: %v", err)
				}
			}()
			go func() {
				defer postWG.Done()
				r, err := a.Results()
				if err != nil {
					t.Errorf("post-seal query error: %v", err)
					return
				}
				if err := checkSnapshot(r); err != nil {
					t.Errorf("inconsistent post-seal snapshot: %v", err)
				}
			}()
		}
		postWG.Wait()
		close(stop)
		queryWG.Wait()
		if sealedCount > 1 {
			return fmt.Errorf("seal succeeded %d times concurrently, want at most 1", sealedCount)
		}
		finalResults, err := a.Results()
		if err != nil {
			return fmt.Errorf("final snapshot: %w", err)
		}
		return checkSnapshot(finalResults)
	}

	// Repeat to exercise many interleavings under -race.
	for i := 0; i < 50; i++ {
		if err := run(); err != nil {
			t.Fatalf("concurrent run %d: %v", i, err)
		}
	}
	t.Logf("judgement: 50 concurrent add/seal/query runs never corrupted state or produced torn snapshots")

	// Deterministic serial check of the final expected values.
	a := buildSealed(t, specs)
	assertAgainstNaive(t, a, specs)
}
