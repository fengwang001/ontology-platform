package ontology

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
)

// 并发追加与并发重建：最终事件顺序与基于该顺序的重建结果，
// 必须等价于把全部操作按某个全局顺序串行执行的结果。
// 验证方式：所有追加完成后，Rebuilder 的结果必须与朴素重放
// 完全一致；并发期间的重建只允许报已知错误类别。
func TestConcurrentAppendAndRebuild(t *testing.T) {
	schema := testSchema()
	store := NewEventStore()
	rb := NewRebuilder(store, schema, 8)

	const instances = 8
	const appendsPerWorker = 200

	// 每个实例的逻辑时钟：原子递增，保证同实例内时刻互不相同。
	var clocks [instances]atomic.Int64
	for i := 0; i < instances; i++ {
		id := fmt.Sprintf("inst-%d", i)
		store.Append(create(id, 0, "A"))
		clocks[i].Store(1)
	}

	var appendWg sync.WaitGroup
	for w := 0; w < instances; w++ {
		appendWg.Add(1)
		go func(w int) {
			defer appendWg.Done()
			id := fmt.Sprintf("inst-%d", w)
			cur := "A"
			for i := 0; i < appendsPerWorker; i++ {
				tm := clocks[w].Add(1)
				if i%3 == 2 {
					next := "B"
					if cur == "B" {
						next = "A"
					}
					store.Append(evolve(id, tm, next))
					cur = next
				} else {
					store.Append(set(id, tm, "score", IntVal(int64(i%101))))
				}
			}
		}(w)
	}

	// 重建协程：在追加进行的同时不断重建。
	var rebuildWg sync.WaitGroup
	var rebuildErrs atomic.Int64
	stop := make(chan struct{})
	for r := 0; r < 4; r++ {
		rebuildWg.Add(1)
		go func(r int) {
			defer rebuildWg.Done()
			id := fmt.Sprintf("inst-%d", r%instances)
			for {
				select {
				case <-stop:
					return
				default:
				}
				cutoff := clocks[r%instances].Load()
				_, _, err := rb.Rebuild(id, cutoff)
				if err != nil && !errors.Is(err, ErrCutoffBeforeFirstEvent) &&
					!errors.Is(err, ErrAmbiguousOrder) {
					rebuildErrs.Add(1)
				}
			}
		}(r)
	}

	appendWg.Wait()
	close(stop)
	rebuildWg.Wait()

	if rebuildErrs.Load() != 0 {
		t.Fatalf("unexpected rebuild errors during concurrency: %d", rebuildErrs.Load())
	}

	// 最终一致性：每个实例的重建结果必须等于朴素重放。
	for i := 0; i < instances; i++ {
		id := fmt.Sprintf("inst-%d", i)
		cutoff := clocks[i].Load()
		got, _, gerr := rb.Rebuild(id, cutoff)
		want, werr := NaiveRebuild(schema, store.Events(id), cutoff)
		if !errors.Is(gerr, werr) {
			t.Fatalf("%s: err %v vs %v", id, gerr, werr)
		}
		if gerr == nil && !reflect.DeepEqual(got, want) {
			t.Fatalf("%s:\n got %+v\nwant %+v", id, got, want)
		}
	}
}
