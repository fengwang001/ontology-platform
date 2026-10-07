package ontology

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"
)

// 随机化差分对照：把随机生成的批量操作序列并发提交给 Store，
// 再按判定日志中的逻辑时刻（Tick）重排为串行顺序，在独立实现的
// 朴素全局锁模型 Model 上重放，两者每个批次的判定结果、判定依据
// 以及所有实例的最终状态必须完全一致。
func TestDifferentialAgainstNaiveModel(t *testing.T) {
	for _, seed := range []int64{20261007, 1, 2, 3, 42} {
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			runDifferential(t, seed)
		})
	}
}

func runDifferential(t *testing.T, seed int64) {
	const (
		numInstances = 12
		numBatches   = 300
		workers      = 16
	)
	rng := rand.New(rand.NewSource(seed))

	s := NewStore()
	s.SetLinkLimit("member", 2)
	s.SetLinkLimit("owner", 1)
	ids := make([]InstanceID, numInstances)
	for i := range ids {
		ids[i] = InstanceID(fmt.Sprintf("inst-%02d", i))
	}
	mustCreate(t, s, ids...)

	linkTypes := []LinkTypeID{"member", "owner", "free"}
	batches := make([]Batch, numBatches)
	for i := range batches {
		numItems := 1 + rng.Intn(4)
		items := make([]Item, 0, numItems)
		for j := 0; j < numItems; j++ {
			inst := ids[rng.Intn(len(ids))]
			var expect Version
			if rng.Intn(2) == 0 {
				// 读取当前版本作为期望，提高成功提交的比例。
				if snap, ok := s.SnapshotOf(inst); ok {
					expect = snap.Version
				}
			} else {
				expect = Version(1 + rng.Intn(8))
			}
			item := Item{Instance: inst, Expect: expect}
			if rng.Intn(2) == 0 {
				item.SetAttrs = map[string]string{
					fmt.Sprintf("k%d", rng.Intn(3)): fmt.Sprintf("v%d", rng.Intn(1000)),
				}
			}
			if rng.Intn(3) == 0 {
				lt := linkTypes[rng.Intn(len(linkTypes))]
				item.AddLinks = []LinkRef{{Type: lt, Target: ids[rng.Intn(len(ids))]}}
			}
			if rng.Intn(4) == 0 {
				lt := linkTypes[rng.Intn(len(linkTypes))]
				item.RemoveLinks = []LinkRef{{Type: lt, Target: ids[rng.Intn(len(ids))]}}
			}
			items = append(items, item)
		}
		// 约 10% 的批次人为制造重复前置声明。
		if rng.Intn(10) == 0 && len(items) > 0 {
			items = append(items, items[rng.Intn(len(items))])
		}
		batches[i] = Batch{ID: fmt.Sprintf("batch-%03d", i), Items: items}
	}

	// 并发提交全部批次。
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for _, b := range batches {
		wg.Add(1)
		sem <- struct{}{}
		go func(b Batch) {
			defer wg.Done()
			defer func() { <-sem }()
			s.ApplyBatch(b)
		}(b)
	}
	wg.Wait()

	// 按判定逻辑时刻重排，得到等价串行顺序。
	log := s.Decisions()
	if len(log) != numBatches {
		t.Fatalf("判定日志应覆盖全部 %d 个批次，实际 %d", numBatches, len(log))
	}
	sort.Slice(log, func(i, j int) bool { return log[i].Tick < log[j].Tick })
	for i := 1; i < len(log); i++ {
		if log[i].Tick == log[i-1].Tick {
			t.Fatalf("Tick 必须唯一：%+v 与 %+v", log[i-1], log[i])
		}
	}

	// 在朴素模型上按该串行顺序重放，逐批次比对结果与判定依据。
	byID := make(map[string]Batch, numBatches)
	for _, b := range batches {
		byID[b.ID] = b
	}
	m := NewModel()
	m.SetLinkLimit("member", 2)
	m.SetLinkLimit("owner", 1)
	for _, id := range ids {
		m.Create(id)
	}
	committed := 0
	for _, want := range log {
		got := m.ApplyBatch(byID[want.BatchID])
		if got.Outcome != want.Outcome {
			t.Fatalf("批次 %s 结果不一致：并发执行 %v，串行重放 %v",
				want.BatchID, want.Outcome, got.Outcome)
		}
		if !reflect.DeepEqual(got.Observed, want.Observed) {
			t.Fatalf("批次 %s 判定依据不一致：并发执行 %+v，串行重放 %+v",
				want.BatchID, want.Observed, got.Observed)
		}
		if got.Reads != want.Reads {
			t.Fatalf("批次 %s 读取次数不一致：%d vs %d", want.BatchID, want.Reads, got.Reads)
		}
		if want.Outcome == OutcomeCommitted {
			committed++
		}
	}
	if committed == 0 {
		t.Fatalf("随机序列应产生若干成功提交的批次")
	}

	// 所有实例的最终版本、属性、关联必须一致。
	for _, id := range ids {
		snapS, okS := s.SnapshotOf(id)
		snapM, okM := m.SnapshotOf(id)
		if okS != okM || !reflect.DeepEqual(snapS, snapM) {
			t.Fatalf("实例 %q 最终状态不一致：Store %+v，Model %+v", id, snapS, snapM)
		}
	}
	t.Logf("重放校验通过：%d 个批次中 %d 个成功提交", numBatches, committed)
}
