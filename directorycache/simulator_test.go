package directorycache

import (
	"errors"
	"testing"
)

func TestReadDowngradesModifierAndWritesBack(t *testing.T) {
	sim, err := New(2, 2)
	if err != nil {
		t.Fatal(err)
	}

	write := ok(sim.Write(0, 10, 7))
	read := ok(sim.Read(1, 10))

	t.Logf("input: write(cache=0,block=10,value=7) => %+v", write)
	t.Logf("input: read(cache=1,block=10) => %+v; basis: modifier 0 writes back and becomes shared, reader receives 7", read)

	if write != (Result{Value: 7}) {
		t.Fatalf("first write = %+v", write)
	}
	if read != (Result{Value: 7, Writebacks: 1}) {
		t.Fatalf("read from modified owner = %+v", read)
	}

	entry := sim.directory[10]
	if entry.hasModifier || len(entry.holders) != 2 {
		t.Fatalf("directory after downgrade = %+v", entry)
	}
	if sim.caches[0].lines[10].state != Shared || sim.caches[1].lines[10].state != Shared {
		t.Fatal("both caches must hold the block shared")
	}
	if sim.memory[10] != 7 {
		t.Fatalf("memory = %d", sim.memory[10])
	}
}

func TestSharedUpgradeDoesNotInvalidateSelf(t *testing.T) {
	sim, _ := New(3, 2)
	ok(sim.Write(0, 5, 3))
	ok(sim.Read(1, 5))
	upgrade := ok(sim.Write(1, 5, 9))

	t.Logf("input: shared cache 1 upgrades block 5 to 9 => %+v; basis: cache 1 receives no invalidation for itself", upgrade)
	if upgrade != (Result{Value: 9, Invalidations: 1}) {
		t.Fatalf("shared upgrade = %+v", upgrade)
	}
	if _, exists := sim.caches[0].lines[5]; exists {
		t.Fatal("old modifier must be invalidated")
	}
	if sim.caches[1].lines[5].state != Modified {
		t.Fatal("upgrading cache must become modifier")
	}
}

func TestSilentEvictionCausesEmptyInvalidation(t *testing.T) {
	sim, _ := New(3, 1)
	ok(sim.Read(0, 1))
	ok(sim.Read(1, 1))
	ok(sim.Read(0, 2))
	write := ok(sim.Write(2, 1, 4))

	t.Logf("input: cache 0 silently evicts block 1, then cache 2 writes => %+v; basis: directory sends stale holders one real and one empty invalidation", write)
	if write != (Result{Value: 4, Invalidations: 2, EmptyInvalidations: 1}) {
		t.Fatalf("empty invalidation write = %+v", write)
	}
}

func TestEvictingModifiedLineWritesBack(t *testing.T) {
	sim, _ := New(1, 1)
	ok(sim.Write(0, 1, 8))
	read := ok(sim.Read(0, 2))

	t.Logf("input: modified cache fills capacity and reads another block => %+v; basis: modified LRU line is written back and removed from directory", read)
	if read != (Result{Value: 0, Writebacks: 1}) {
		t.Fatalf("modified eviction = %+v", read)
	}
	if sim.memory[1] != 8 {
		t.Fatalf("memory after eviction = %d", sim.memory[1])
	}
	if _, exists := sim.caches[0].lines[1]; exists {
		t.Fatal("modified line must be evicted")
	}
}

func TestHitRefreshesLRUOrder(t *testing.T) {
	sim, _ := New(1, 2)
	ok(sim.Read(0, 1))
	ok(sim.Read(0, 2))
	ok(sim.Read(0, 1))
	ok(sim.Read(0, 3))

	t.Logf("input: read 1, read 2, read 1, read 3 with capacity 2; basis: hit on block 1 moves block 2 to oldest, so block 2 is silently evicted")
	if _, exists := sim.caches[0].lines[1]; !exists {
		t.Fatal("block 1 should remain after LRU refresh")
	}
	if _, exists := sim.caches[0].lines[2]; exists {
		t.Fatal("block 2 should be evicted after block 1 was refreshed")
	}
	if _, exists := sim.caches[0].lines[3]; !exists {
		t.Fatal("block 3 should be cached")
	}
}

func TestRejectedOperationChangesNothing(t *testing.T) {
	_, err := New(0, 1)
	if !errors.Is(err, ErrInvalidCacheCount) {
		t.Fatalf("constructor error = %v", err)
	}
	_, err = New(1, 0)
	if !errors.Is(err, ErrInvalidCapacity) {
		t.Fatalf("constructor error = %v", err)
	}

	sim, _ := New(1, 1)
	ok(sim.Write(0, 1, 2))
	_, err = sim.Read(2, -1)
	if !errors.Is(err, ErrCacheOutOfRange) {
		t.Fatalf("expected out of range first, got %v", err)
	}
	_, err = sim.Read(0, -1)
	if !errors.Is(err, ErrNegativeBlock) {
		t.Fatalf("expected negative block, got %v", err)
	}

	if sim.memory[1] != 0 || sim.caches[0].lines[1].value != 2 || sim.caches[0].order.Len() != 1 {
		t.Fatal("rejected operation changed simulator state")
	}
}

func ok(result Result, err error) Result {
	if err != nil {
		panic(err)
	}
	return result
}
