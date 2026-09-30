package directorycache

import (
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
)

type recordedOperation struct {
	kind   string
	cache  int
	block  int
	value  int
	result Result
}

func TestRandomOperationsMatchSingleMemoryReference(t *testing.T) {
	const (
		cacheCount = 4
		capacity   = 2
		blockCount = 7
		operations = 150
	)

	for seed := int64(1); seed <= 5; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			primary, err := New(cacheCount, capacity)
			if err != nil {
				t.Fatal(err)
			}
			replay, err := New(cacheCount, capacity)
			if err != nil {
				t.Fatal(err)
			}

			latest := make(map[int]int)
			recorded := make([]recordedOperation, 0, operations)

			for step := 0; step < operations; step++ {
				cacheID := rng.Intn(cacheCount)
				block := rng.Intn(blockCount)
				var result Result
				var operation recordedOperation

				if rng.Intn(2) == 0 {
					value := rng.Intn(200) - 100
					result, err = primary.Write(cacheID, block, value)
					if err != nil {
						t.Fatalf("step %d write: %v", step, err)
					}
					latest[block] = value
					operation = recordedOperation{"write", cacheID, block, value, result}
					t.Logf("step %d input: write(cache=%d,block=%d,value=%d) output: %+v basis: latest single-memory value is now %d", step, cacheID, block, value, result, value)
				} else {
					result, err = primary.Read(cacheID, block)
					if err != nil {
						t.Fatalf("step %d read: %v", step, err)
					}
					expected := latest[block]
					operation = recordedOperation{"read", cacheID, block, 0, result}
					t.Logf("step %d input: read(cache=%d,block=%d) output: %+v basis: latest single-memory value is %d", step, cacheID, block, result, expected)
					if result.Value != expected {
						t.Fatalf("step %d read block %d = %d, want %d", step, block, result.Value, expected)
					}
				}

				recorded = append(recorded, operation)
				assertProtocolInvariants(t, primary, latest, step)
			}

			for step, operation := range recorded {
				var result Result
				var replayErr error
				if operation.kind == "write" {
					result, replayErr = replay.Write(operation.cache, operation.block, operation.value)
				} else {
					result, replayErr = replay.Read(operation.cache, operation.block)
				}
				if replayErr != nil {
					t.Fatalf("replay step %d: %v", step, replayErr)
				}
				if result != operation.result {
					t.Fatalf("replay step %d = %+v, want %+v", step, result, operation.result)
				}
			}
		})
	}
}

func TestConcurrentReadWrite(t *testing.T) {
	sim, err := New(8, 3)
	if err != nil {
		t.Fatal(err)
	}

	const goroutines = 16
	var operations atomic.Int64
	var wg sync.WaitGroup
	for worker := 0; worker < goroutines; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for step := 0; step < 100; step++ {
				block := 100 + worker
				value := worker*1000 + step
				_, writeErr := sim.Write(worker%8, block, value)
				if writeErr != nil {
					t.Error(writeErr)
					return
				}
				operations.Add(1)

				_, readErr := sim.Read((worker+1)%8, 42)
				if readErr != nil {
					t.Error(readErr)
					return
				}
				operations.Add(1)

				sharedValue := worker*17 + step
				_, writeErr = sim.Write(worker%8, 42, sharedValue)
				if writeErr != nil {
					t.Error(writeErr)
					return
				}
				operations.Add(1)
			}
		}(worker)
	}
	wg.Wait()

	assertProtocolInvariants(t, sim, nil, -1)
	t.Logf("concurrent input: %d serialized read/write operations on private and shared blocks; basis: race detector and final protocol invariants validate atomic serialization", operations.Load())
}

func assertProtocolInvariants(t *testing.T, sim *Simulator, latest map[int]int, step int) {
	t.Helper()

	for cacheID := range sim.caches {
		if sim.caches[cacheID].order.Len() > sim.capacity {
			t.Fatalf("step %d cache %d exceeded capacity", step, cacheID)
		}
		if sim.caches[cacheID].order.Len() != len(sim.caches[cacheID].lines) {
			t.Fatalf("step %d cache %d LRU/table size mismatch", step, cacheID)
		}
	}

	for block, entry := range sim.directory {
		modifiers := 0

		for cacheID := range sim.caches {
			current, exists := sim.caches[cacheID].lines[block]
			if !exists {
				continue
			}
			if current.state == Modified {
				modifiers++
				if !entry.hasModifier || entry.modifier != cacheID {
					t.Fatalf("step %d block %d modifier missing from directory", step, block)
				}
				if len(entry.holders) != 1 {
					t.Fatalf("step %d modified block %d has another holder", step, block)
				}
			}
			if current.state == Shared && current.value != sim.memory[block] {
				t.Fatalf("step %d shared block %d value differs from memory", step, block)
			}
		}

		if modifiers > 1 {
			t.Fatalf("step %d block %d has %d modifiers", step, block, modifiers)
		}
		if entry.hasModifier && modifiers != 1 {
			t.Fatalf("step %d block %d directory modifier has no modified cache line", step, block)
		}

		for cacheID := range entry.holders {
			if current, exists := sim.caches[cacheID].lines[block]; exists && entry.hasModifier && current.state == Shared {
				t.Fatalf("step %d block %d has modifier and shared cache %d", step, block, cacheID)
			}
		}

		if !entry.hasModifier && latest != nil && sim.memory[block] != latest[block] {
			t.Fatalf("step %d block %d memory = %d, latest = %d", step, block, sim.memory[block], latest[block])
		}
	}
}
