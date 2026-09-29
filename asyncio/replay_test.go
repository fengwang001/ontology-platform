package asyncio

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"testing"
	"time"
)

type replayOp struct {
	kind string // push / wm / done
	id   string
	val  any
	wm   int64
}

func naiveContains(n *naiveModel, id string) bool {
	for _, it := range n.items {
		if it.kind == nvElement && it.id == id {
			return true
		}
	}
	return false
}

// TestRandomReplayMatchesNaive 对两种模式、多种容量与大量随机操作序列，
// 逐操作比对接受/拒绝、逐条比对输出与占用数，要求与朴素判定完全一致。
func TestRandomReplayMatchesNaive(t *testing.T) {
	for _, mode := range []Mode{Ordered, Unordered} {
		for _, capv := range []int{1, 2, 4, 8} {
			for seed := int64(1); seed <= 30; seed++ {
				t.Run(fmt.Sprintf("%s/cap%d/seed%d", mode, capv, seed), func(t *testing.T) {
					rng := rand.New(rand.NewSource(seed))
					ops := generateOpsWithModel(rng, 180)

					nb := newNaive(mode, capv)
					bb, err := New(mode, capv, testLogger())
					if err != nil {
						t.Fatal(err)
					}

					var got []Event
					for i, op := range ops {
						var nr naiveResult
						switch op.kind {
						case "push":
							nr = nb.apply("push", op.id, op.val, 0)
							err = bb.PushElement(op.id, op.val)
						case "wm":
							nr = nb.apply("wm", "", nil, op.wm)
							err = bb.PushWatermark(op.wm)
						case "done":
							nr = nb.apply("done", op.id, nil, 0)
							err = bb.Complete(op.id)
						}
						if !sameErr(nr.err, err) {
							t.Fatalf("step %d %s id=%q wm=%d: 接受判定不一致 naive=%v buffer=%v",
								i, op.kind, op.id, op.wm, nr.err, err)
						}
						got = append(got, bb.PopOutputs()...)
						if !eventsEqual(got, nb.snapshot()) {
							t.Fatalf("step %d 后输出不一致:\n naive %s\n buffer %s",
								i, eventsSummary(nb.snapshot()), eventsSummary(got))
						}
						if st := bb.Stats(); st.InFlight != nb.inFlight {
							t.Fatalf("step %d 占用不一致 naive=%d buffer=%d",
								i, nb.inFlight, st.InFlight)
						}
						t.Logf("step %d %s id=%q wm=%d -> err=%v | 输出: %s",
							i, op.kind, op.id, op.wm, err, eventsSummary(nb.snapshot()))
					}
				})
			}
		}
	}
}

func sameErr(a, b error) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return errors.Is(a, b)
}

// generateOpsWithModel 在生成期用一个“大容量参考模型”跟踪哪些标识仍在队中，
// 以便刻意构造“重复输入”操作；生成出的序列随后会以目标小容量重新重放。
func generateOpsWithModel(rng *rand.Rand, n int) []replayOp {
	tracker := newNaive(Unordered, 1_000_000)
	ops := make([]replayOp, 0, n)
	var pushed []string
	nextID := 0
	nextWM := int64(1)

	for i := 0; i < n; i++ {
		r := rng.Intn(100)
		switch {
		case r < 45:
			id := fmt.Sprintf("e%d", nextID)
			nextID++
			ops = append(ops, replayOp{kind: "push", id: id, val: nextID})
			tracker.apply("push", id, nextID, 0)
			pushed = append(pushed, id)
		case r < 54:
			ops = append(ops, replayOp{kind: "push", id: ""})
		case r < 63:
			var queued []string
			for _, id := range pushed {
				if naiveContains(tracker, id) {
					queued = append(queued, id)
				}
			}
			if len(queued) == 0 {
				i--
				continue
			}
			id := queued[rng.Intn(len(queued))]
			ops = append(ops, replayOp{kind: "push", id: id, val: -1})
		case r < 82:
			if len(pushed) == 0 {
				i--
				continue
			}
			id := pushed[rng.Intn(len(pushed))]
			ops = append(ops, replayOp{kind: "done", id: id})
			tracker.apply("done", id, nil, 0)
		case r < 88:
			ops = append(ops, replayOp{kind: "done", id: fmt.Sprintf("ghost%d", rng.Intn(5))})
		case r < 92:
			ops = append(ops, replayOp{kind: "done", id: ""})
		case r < 96:
			ops = append(ops, replayOp{kind: "wm", wm: nextWM})
			tracker.apply("wm", "", nil, nextWM)
			nextWM += int64(1 + rng.Intn(3))
		default:
			ops = append(ops, replayOp{kind: "wm", wm: nextWM - 1})
		}
	}
	return ops
}

// TestDrainChannel 验证 Drain 并发取走输出、不重不漏。
func TestDrainChannel(t *testing.T) {
	b, _ := New(Ordered, 3, testLogger())
	for _, id := range []string{"a", "b", "c"} {
		if err := b.PushElement(id, id); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"a", "b", "c"} {
		if err := b.Complete(id); err != nil {
			t.Fatal(err)
		}
	}

	ch := make(chan Event, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b.Drain(ctx, ch)
		}()
	}

	received := make([]Event, 0, 3)
	deadline := time.After(2 * time.Second)
	for len(received) < 3 {
		select {
		case ev := <-ch:
			received = append(received, ev)
		case <-deadline:
			cancel()
			wg.Wait()
			t.Fatalf("Drain 超时，仅收到 %d 条", len(received))
		}
	}
	cancel()
	wg.Wait()

	if len(received) != 3 {
		t.Fatalf("应收到 3 条，实际 %d", len(received))
	}
	ids := []string{received[0].ID, received[1].ID, received[2].ID}
	sort.Strings(ids)
	if ids[0] != "a" || ids[1] != "b" || ids[2] != "c" {
		t.Fatalf("Drain 丢消息: %v", ids)
	}
	if st := b.Stats(); st.Buffered != 0 {
		t.Fatalf("Drain 后 ready 应为空，实际 %d", st.Buffered)
	}
}
