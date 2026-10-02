package ontology

import (
	"errors"
	"testing"
)

func TestRejectionOrderAndNoStateChange(t *testing.T) {
	if _, err := NewAllocator(0, 1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("constructor readers error = %v, want invalid", err)
	}
	if _, err := NewAllocator(1, 101); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("constructor quarantine error = %v, want invalid", err)
	}

	a := newTestAllocator(t, 2, 2)
	if err := a.AddSplits([]int64{1_000_000_001}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid split error = %v, want invalid", err)
	}
	a.Seal()
	if err := a.AddSplits([]int64{1}); !errors.Is(err, ErrSealed) {
		t.Fatalf("sealed invalid split error = %v, want sealed", err)
	}
	if err := a.Checkpoint(2); !errors.Is(err, ErrCheckpointOutOfOrder) {
		t.Fatalf("checkpoint order error = %v, want out of order", err)
	}
	if err := a.Complete(0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("complete invalid error = %v, want invalid", err)
	}
	if err := a.Complete(1); !errors.Is(err, ErrCheckpointAhead) {
		t.Fatalf("complete ahead error = %v, want ahead", err)
	}
	if err := a.Checkpoint(1); err != nil {
		t.Fatal(err)
	}
	if err := a.Complete(1); err != nil {
		t.Fatal(err)
	}
	if err := a.Complete(1); !errors.Is(err, ErrCheckpointStale) {
		t.Fatalf("complete stale error = %v, want stale", err)
	}

	b := newTestAllocator(t, 1, 2)
	if err := b.AddSplits([]int64{0}); err != nil {
		t.Fatal(err)
	}
	if err := b.AddSplits([]int64{1, 0, 2}); !errors.Is(err, ErrDuplicateSplit) {
		t.Fatalf("duplicate batch error = %v, want duplicate", err)
	}
	if err := b.AddSplits([]int64{2, 2}); !errors.Is(err, ErrDuplicateSplit) {
		t.Fatalf("internal duplicate batch error = %v, want duplicate", err)
	}
	for _, split := range []int64{1, 2} {
		if _, err := b.State(split); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("state(%d) error = %v, want invalid for non-registered split", split, err)
		}
	}

	if _, err := b.RequestSplit(2); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("request invalid reader error = %v, want invalid", err)
	}
	if got := mustRequest(t, b, 0); got.Split != 0 {
		t.Fatalf("split = %d, want 0", got.Split)
	}
	if _, err := b.ReaderFailed(0); err != nil {
		t.Fatal(err)
	}
	if _, err := b.RequestSplit(0); !errors.Is(err, ErrReaderFailed) {
		t.Fatalf("request failed reader error = %v, want failed", err)
	}
	if err := b.Finished(2, 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("finished invalid reader error = %v, want invalid", err)
	}
	if err := b.Finished(0, 0); !errors.Is(err, ErrReaderFailed) {
		t.Fatalf("finished failed reader error = %v, want failed", err)
	}
	if _, err := b.ReaderFailed(2); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("failed invalid reader error = %v, want invalid", err)
	}
	if _, err := b.ReaderFailed(0); !errors.Is(err, ErrReaderFailed) {
		t.Fatalf("double failed error = %v, want failed", err)
	}
	if err := b.ReaderRestarted(2); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("restart invalid reader error = %v, want invalid", err)
	}
}

func TestProbeCounterBound(t *testing.T) {
	a := newTestAllocator(t, 8, 10)
	splits := make([]int64, 0, 64)
	for split := int64(0); split < 64; split++ {
		splits = append(splits, split)
	}
	if err := a.AddSplits(splits); err != nil {
		t.Fatal(err)
	}
	if got := mustRequest(t, a, 7); got.Split != 7 {
		t.Fatalf("preferred split = %d, want 7", got.Split)
	}
	if _, err := a.ReaderFailed(7); err != nil {
		t.Fatal(err)
	}
	for reader := 0; reader < 7; reader++ {
		if _, err := a.ReaderFailed(reader); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.ReaderRestarted(6); err != nil {
		t.Fatal(err)
	}
	for _, want := range []int64{6, 14, 22, 30, 38, 46, 54, 62} {
		if got := mustRequest(t, a, 6); got.Split != want {
			t.Fatalf("reader 6 preferred split = %d, want %d", got.Split, want)
		}
	}
	for _, want := range []int64{0, 1, 2, 3, 4, 5, 7} {
		if got := mustRequest(t, a, 6); got.Split != want {
			t.Fatalf("reader 6 stolen split = %d, want %d", got.Split, want)
		}
	}
	if err := a.ReaderRestarted(7); err != nil {
		t.Fatal(err)
	}
	before := a.probes
	if got := mustRequest(t, a, 7); got.Split != 15 {
		t.Fatalf("stolen split = %d, want 15", got)
	}
	if increment := a.probes - before; increment > int64(a.readers) {
		t.Fatalf("probe increment = %d, want at most R=%d", increment, a.readers)
	}
}

func TestOwnedCounterScaleIndependent(t *testing.T) {
	for _, total := range []int{10, 100_000} {
		a := newTestAllocator(t, 4, 100)
		splits := make([]int64, 0, total)
		for split := 0; split < total; split++ {
			splits = append(splits, int64(split))
		}
		for start := 0; start < len(splits); start += 1000 {
			end := start + 1000
			if end > len(splits) {
				end = len(splits)
			}
			if err := a.AddSplits(splits[start:end]); err != nil {
				t.Fatal(err)
			}
		}
		ownedByReaderTwo := 0
		for i := 0; i < 2; i++ {
			result, err := a.RequestSplit(2)
			if err != nil {
				t.Fatal(err)
			}
			if result.Kind == RequestAssigned {
				ownedByReaderTwo++
			}
			if i == 0 && result.Kind != RequestAssigned {
				t.Fatalf("scale %d first request = %+v, want assigned", total, result)
			}
		}
		before := a.ownedCount
		if _, err := a.ReaderFailed(2); err != nil {
			t.Fatal(err)
		}
		if increment := a.ownedCount - before; increment != int64(ownedByReaderTwo) {
			t.Fatalf("scale %d owned increment = %d, want %d", total, increment, ownedByReaderTwo)
		}
	}
}

func TestOwnedCounterSameOwnedCountAcrossScales(t *testing.T) {
	increments := make(map[int]int64)
	for _, total := range []int{10, 100_000} {
		a := newTestAllocator(t, 4, 100)
		splits := make([]int64, 0, total)
		for split := 0; split < total; split++ {
			splits = append(splits, int64(split))
		}
		for start := 0; start < len(splits); start += 1000 {
			end := start + 1000
			if end > len(splits) {
				end = len(splits)
			}
			if err := a.AddSplits(splits[start:end]); err != nil {
				t.Fatal(err)
			}
		}
		for reader := 1; reader < 4; reader++ {
			if _, err := a.ReaderFailed(reader); err != nil {
				t.Fatal(err)
			}
		}
		for i := 0; i < 2; i++ {
			if got := mustRequest(t, a, 0); got.Split != int64(i*4) {
				t.Fatalf("scale %d assigned %d, want %d", total, got.Split, i*4)
			}
		}
		before := a.ownedCount
		if _, err := a.ReaderFailed(0); err != nil {
			t.Fatal(err)
		}
		increments[total] = a.ownedCount - before
	}
	if increments[10] != 2 || increments[100_000] != 2 || increments[10] != increments[100_000] {
		t.Fatalf("owned increments = %v, want equal increment 2", increments)
	}
}
