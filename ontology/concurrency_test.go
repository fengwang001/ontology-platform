package ontology

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"
)

// TestConcurrentVisibility hammers overlapping batches while readers observe
// multiple instances at once. Every ReadMany result must share one tick and be
// self-consistent: a reader must never see two instances from different
// commits, and after a tick is observed all future reads are at that tick or
// later.
func TestConcurrentVisibility(t *testing.T) {
	p := testPlatform()
	p.RegisterObjectType(ObjectType{Name: "C"})
	const n = 8
	var ops []Operation
	for i := 0; i < n; i++ {
		id := InstanceID(fmt.Sprintf("c%d", i))
		ops = append(ops, Operation{Instance: id, Type: "C", BaseVersion: 0, Props: props(map[string]any{"v": 0})})
	}
	p.Commit(Batch{ID: "init", Ops: ops})

	stop := make(chan struct{})
	var wg sync.WaitGroup
	ids := make([]InstanceID, n)
	for i := range ids {
		ids[i] = InstanceID(fmt.Sprintf("c%d", i))
	}

	// Writers: each bumps two instances with the versions it last observed.
	for w := 0; w < 6; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for {
				select {
				case <-stop:
					return
				default:
				}
				i, j := rng.Intn(n), rng.Intn(n)
				for j == i {
					j = rng.Intn(n)
				}
				a, b := p.Read(ids[i]), p.Read(ids[j])
				r := p.Commit(Batch{ID: fmt.Sprintf("w-%d-%d", seed, a.Version+b.Version), Ops: []Operation{
					{Instance: ids[i], Type: "C", BaseVersion: a.Version, Props: props(map[string]any{"v": a.Version + 1})},
					{Instance: ids[j], Type: "C", BaseVersion: b.Version, Props: props(map[string]any{"v": b.Version + 1})},
				}})
				_ = r
			}
		}(int64(w + 1))
	}

	// Readers: every multi-read must be a consistent tick, monotonic per
	// reader, and each returned value must be that instance's value at the tick.
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lastTick := int64(0)
			for {
				select {
				case <-stop:
					return
				default:
				}
				snaps := p.ReadMany(ids...)
				tick := snaps[0].Tick
				for _, s := range snaps {
					if s.Tick != tick {
						t.Errorf("inconsistent ticks in one ReadMany: %d vs %d", s.Tick, tick)
						return
					}
				}
				if tick < lastTick {
					t.Errorf("observed tick went backwards: %d -> %d", lastTick, tick)
					return
				}
				lastTick = tick
			}
		}()
	}

	time.Sleep(300 * time.Millisecond)
	close(stop)
	wg.Wait()
}
