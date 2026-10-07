package compensate

import (
	"context"
	"math/rand"
	"sort"
	"sync/atomic"
	"testing"
)

func contextBackground() context.Context { return context.Background() }

// op 是随机操作序列中的一条抽象操作。
type op struct {
	kind  string // "deliver" | "redeliver" | "equivalent" | "undo"
	event Event
}

type generated struct {
	op      op
	modelFn func()
}

// 随机差分：真实处理器（允许重复投递与并发批处理）与朴素一次性模型逐条对照。
func TestDifferentialAgainstNaiveModel(t *testing.T) {
	for seed := int64(0); seed < 40; seed++ {
		rng := rand.New(rand.NewSource(seed))
		store := NewMemoryStore()
		reg := NewRegistry()
		counters := map[string][]atomic.Int32{}

		objects := []string{"oA", "oB", "oC"}
		for _, o := range objects {
			createTarget(store, o)
		}
		mkSpec := func(obj string) {
			n := 1 + rng.Intn(3)
			cnt := make([]atomic.Int32, n)
			counters[obj] = cnt
			dynamicSpec(reg, "A", n, cnt)
		}
		for _, o := range objects {
			mkSpec(o)
		}
		real := NewProcessor(Config{Store: store, Specs: reg})
		model := NewNaiveModel(reg)

		// 生成抽象操作：每一轮以一个「主事件」为根，派生重复投递与独立等价调用；
		// 撤销可能在首次投递前插入（赢得领取）。
		var seq []generated
		eventSeq := 0
		for round := 0; round < 12; round++ {
			eventSeq++
			obj := objects[rng.Intn(len(objects))]
			id := "ev" + itoa(eventSeq)
			ev := testEvent(id, "A", map[string]string{"object": obj})

			if rng.Intn(4) == 0 {
				// 撤销先于首次投递
				seq = append(seq, generated{
					op:      op{kind: "undo", event: ev},
					modelFn: func() { model.Undo(ev) },
				})
			}
			seq = append(seq, generated{
				op: op{kind: "deliver", event: ev},
				modelFn: func() {
					r := model.Deliver(ev)
					_ = r
				},
			})
			// 0~2 次完全重复投递（网络重试）。
			for k := rng.Intn(3); k > 0; k-- {
				dup := ev
				seq = append(seq, generated{
					op: op{kind: "redeliver", event: dup},
					modelFn: func() {
						_ = model.Deliver(dup)
					},
				})
			}
			// 以 1/3 概率追加一次「独立等价调用」（新 EventID，同内容）。
			if rng.Intn(3) == 0 {
				eventSeq++
				ev2 := testEvent("ev"+itoa(eventSeq), "A", map[string]string{"object": obj})
				seq = append(seq, generated{
					op:      op{kind: "equivalent", event: ev2},
					modelFn: func() { _ = model.Deliver(ev2) },
				})
				for k := rng.Intn(2); k > 0; k-- {
					dup := ev2
					seq = append(seq, generated{
						op:      op{kind: "redeliver", event: dup},
						modelFn: func() { _ = model.Deliver(dup) },
					})
				}
			}
		}

		// 以「事件组」为单位并发：一组包含某个主事件的撤销/首次投递及其全部重复投递，
		// 组内允许乱序并发（真实流中重试本就可能乱序），但模型在每组并发之前
		// 先按抽象顺序推进——从而模型永远先见到首次投递再见到重复（这是流的因果序）。
		groups := splitGroups(seq)
		for _, grp := range groups {
			done := make(chan struct{}, len(grp))
			for _, g := range grp {
				g := g
				go func() {
					defer func() { done <- struct{}{} }()
					switch g.op.kind {
					case "undo":
						real.UndoArrived(g.op.event)
					default:
						real.Handle(contextBackground(), g.op.event)
					}
				}()
			}
			for range grp {
				<-done
			}
			for _, g := range grp {
				g.modelFn()
			}
		}

		// 对照全部业务键（效果多重集，组内并发顺序不影响集合语义）与全部副作用标记键。
		var mismatches []string
		for _, k := range model.Keys() {
			mv, _ := model.Get(k)
			rv, ok := store.SnapshotGet(k)
			if !ok {
				mismatches = append(mismatches, "real missing key "+k)
				continue
			}
			if !sameEffectMultiset(string(mv), string(rv)) {
				mismatches = append(mismatches, "key "+k+": model="+string(mv)+" real="+string(rv))
			}
		}
		realFX := store.SnapshotPrefix("comp/fx/")
		for k, rv := range realFX {
			if _, ok := model.Get(k); !ok {
				mismatches = append(mismatches, "real has unexpected key "+k+"="+string(rv))
			}
		}
		if len(mismatches) > 0 {
			sort.Strings(mismatches)
			t.Fatalf("seed=%d differential mismatch:\n%v", seed, mismatches)
		}

		// 审计 Seq 必须构成覆盖全部已处理事件的全序证据。
		recs := (AuditLog{}).Snapshot(store)
		var lastSeq int64
		for _, r := range recs {
			if r.Seq <= lastSeq {
				t.Fatalf("audit seq not strictly increasing: %d after %d", r.Seq, lastSeq)
			}
			lastSeq = r.Seq
		}
	}
}

// splitGroups 把操作序列切成「一个事件根 + 其重复投递」的并发组。
// 新的 deliver / equivalent / undo-of-future-event 均开启新组。
func splitGroups(seq []generated) [][]generated {
	var groups [][]generated
	var cur []generated
	flush := func() {
		if len(cur) > 0 {
			groups = append(groups, cur)
			cur = nil
		}
	}
	for _, g := range seq {
		if g.op.kind == "redeliver" {
			cur = append(cur, g)
		} else {
			flush()
			cur = append(cur, g)
		}
	}
	flush()
	return groups
}

func sameEffectMultiset(a, b string) bool {
	tokens := func(s string) map[string]int {
		out := map[string]int{}
		start := 0
		for i := 0; i <= len(s); i++ {
			if i == len(s) || s[i] == ';' {
				if i > start {
					out[s[start:i]]++
				}
				start = i + 1
			}
		}
		return out
	}
	ta, tb := tokens(a), tokens(b)
	if len(ta) != len(tb) {
		return false
	}
	for k, v := range ta {
		if tb[k] != v {
			return false
		}
	}
	return true
}
