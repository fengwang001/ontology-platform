package scd

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

// TestOrderIndependence 乱序重放得到与正序完全相同的历史（可复现）。
func TestOrderIndependence(t *testing.T) {
	events := []Event{
		{Key: "k1", At: 50, Op: OpUpdate, Value: "e"},
		{Key: "k1", At: 10, Op: OpUpdate, Value: "a"},
		{Key: "k1", At: 30, Op: OpDelete},
		{Key: "k1", At: 40, Op: OpUpdate, Value: "d"},
		{Key: "k2", At: 20, Op: OpUpdate, Value: "p"},
		{Key: "k2", At: 60, Op: OpUpdate, Value: "q"},
	}

	build := func(order []int) *History {
		h := NewHistory()
		for _, i := range order {
			if err := h.Commit([]Event{events[i]}); err != nil {
				t.Fatalf("单事件提交被拒: %v", err)
			}
		}
		return h
	}

	canonical := build([]int{0, 1, 2, 3, 4, 5})

	rng := rand.New(rand.NewSource(42))
	for iter := 0; iter < 5; iter++ {
		order := rng.Perm(len(events))
		got := build(order)
		t.Logf("乱序排列 #%d: %v", iter, order)
		for _, key := range []string{"k1", "k2"} {
			if !intervalsEqual(got.Intervals(key), canonical.Intervals(key)) {
				t.Fatalf("迭代 %d key=%s 乱序结果 %v 与正序 %v 不一致",
					iter, key, got.Intervals(key), canonical.Intervals(key))
			}
		}
	}

	// 分批增量提交的最终结果必须等于一次性批量重算。
	replayed, err := Replay(
		[]Event{events[1], events[4]},
		[]Event{events[0], events[2]},
		[]Event{events[3], events[5]},
	)
	if err != nil {
		t.Fatalf("Replay 被拒: %v", err)
	}
	for _, key := range []string{"k1", "k2"} {
		if !intervalsEqual(canonical.Intervals(key), replayed[key]) {
			t.Fatalf("key=%s 增量 %v 与批量重算 %v 不一致", key, canonical.Intervals(key), replayed[key])
		}
	}
	t.Logf("判定依据: 5 组随机到达顺序与正序逐行一致；分批 Replay 与 Recompute 逐行一致")
}

// TestBatchRecomputeEquivalence 随机多键事件流：每步增量结果与纯重算一致。
func TestBatchRecomputeEquivalence(t *testing.T) {
	h := NewHistory(WithMaxPointsPerKey(500))
	rng := rand.New(rand.NewSource(7))
	var accepted []Event

	keys := []string{"ka", "kb", "kc"}
	for step := 0; step < 40; step++ {
		batch := make([]Event, 0, 1+rng.Intn(4))
		for i := 0; i < cap(batch); i++ {
			op := OpUpdate
			if rng.Intn(4) == 0 {
				op = OpDelete
			}
			batch = append(batch, Event{
				Key:   keys[rng.Intn(len(keys))],
				At:    int64(rng.Intn(200)),
				Op:    op,
				Value: fmt.Sprintf("v%d", rng.Intn(5)),
			})
		}
		if err := h.Commit(batch); err != nil {
			t.Fatalf("步骤 %d 合法批次被拒: %v", step, err)
		}
		accepted = append(accepted, batch...)

		want, err := Recompute(accepted)
		if err != nil {
			t.Fatalf("步骤 %d 重算失败: %v", step, err)
		}
		for _, key := range keys {
			got := h.Intervals(key)
			if !intervalsEqual(got, want[key]) {
				t.Fatalf("步骤 %d key=%s 增量 %v 与重算 %v 不一致", step, key, got, want[key])
			}
		}
		if err := h.SelfCheck(); err != nil {
			t.Fatalf("步骤 %d 自检失败: %v", step, err)
		}
	}
	t.Logf("判定依据: 40 步随机事件流，每步增量区间与 Recompute(全部已接受事件) 逐行一致")
}

// TestConcurrentReadWrite 多执行体并发提交、读取、点查询与自检。
// 使用 -race 运行时验证无数据竞争；读取与提交并发进行。
func TestConcurrentReadWrite(t *testing.T) {
	h := NewHistory(WithMaxPointsPerKey(10_000))
	const writers = 4
	const readers = 4
	const batches = 50

	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(w)))
			for b := 0; b < batches; b++ {
				ev := Event{
					Key:   fmt.Sprintf("key-%d", w%2), // 写者两两共享键，制造同键并发
					At:    int64(rng.Intn(1000)),
					Op:    Op(rng.Intn(2) + 1),
					Value: fmt.Sprintf("w%d-b%d", w, b),
				}
				if err := h.Commit([]Event{ev}); err != nil {
					t.Errorf("提交被拒: %v", err)
					return
				}
			}
		}(w)
	}
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func(r int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				_ = h.Snapshot()
				_ = h.Intervals(fmt.Sprintf("key-%d", r%2))
				_, _ = h.ValueAt(fmt.Sprintf("key-%d", r%2), int64(i))
				if err := h.SelfCheck(); err != nil {
					t.Errorf("并发自检失败: %v", err)
					return
				}
			}
		}(r)
	}
	wg.Wait()

	if err := h.SelfCheck(); err != nil {
		t.Fatalf("最终自检失败: %v", err)
	}
	for _, key := range h.Keys() {
		ivs := h.Intervals(key)
		for i := 1; i < len(ivs); i++ {
			if ivs[i].Start < ivs[i-1].End {
				t.Fatalf("key=%s 区间 %d 与 %d 重叠", key, i-1, i)
			}
		}
	}
	t.Logf("判定依据: %d 写者 × %d 批与 %d 读者并发结束后自检通过、区间无重叠", writers, batches, readers)
}
