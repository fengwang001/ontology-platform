package handler

import (
	"fmt"
	"sync"
	"testing"

	"ontology/history"
)

// TestConcurrentSameAndDistinctUIDs hammers one instance from many goroutines
// under -race: same uid validated once, history seqs gapless.
func TestConcurrentSameAndDistinctUIDs(t *testing.T) {
	h := New()
	mustCreate(t, h, inst, MaxCap, 1_000)

	const goroutines, perG = 8, 200
	results := sync.Map{} // uid -> []Result
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				uid := fmt.Sprintf("g%d-%d", g, i) // 唯一
				if i%2 == 0 {
					uid = fmt.Sprintf("shared-%d", i%50) // 跨 goroutine 共享
				}
				r, err := h.Update(inst, uid, 1)
				if err != nil {
					t.Errorf("Update(%s): %v", uid, err)
					return
				}
				v, _ := results.LoadOrStore(uid, &sync.Map{})
				v.(*sync.Map).Store(g, r)
			}
		}(g)
	}
	// 并发 Step 消费者。
	stop := make(chan struct{})
	var stepWg sync.WaitGroup
	for s := 0; s < 2; s++ {
		stepWg.Add(1)
		go func() {
			defer stepWg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					h.Step(inst) // ErrEmpty 忽略
				}
			}
		}()
	}
	wg.Wait()
	close(stop)
	stepWg.Wait()
	in, _ := h.lookup(inst)
	for len(in.queue) > 0 {
		h.Step(inst) // 排空剩余队列
	}

	// 同一 uid 只被校验一次：并发 Step 会把登记结果从 Accepted 就地推进到
	// Completed，故归一化后所有并发观察必须完全相同（Seq 一致）。
	norm := func(r Result) Result {
		if r.Kind == Completed {
			r.Kind, r.Value = Accepted, 0
		}
		return r
	}
	results.Range(func(key, value any) bool {
		seen := map[Result]bool{}
		value.(*sync.Map).Range(func(_, r any) bool {
			seen[norm(r.(Result))] = true
			return true
		})
		if len(seen) != 1 {
			t.Errorf("uid %s 的并发观察结果不一致: %v", key, seen)
		}
		return true
	})

	// 历史序号从 1 连续无洞；同一 uid 至多一条 U（只校验一次）。
	evts := h.History(inst)
	uCount := map[string]int{}
	applied := int64(0)
	for i, e := range evts {
		if e.Seq != int64(i)+1 {
			t.Fatalf("序号洞: events[%d].Seq=%d", i, e.Seq)
		}
		switch e.Kind {
		case history.UpdateAccepted:
			uCount[e.UID]++
		case history.UpdateApplied:
			applied++
		}
	}
	for uid, n := range uCount {
		if n != 1 {
			t.Fatalf("uid %s 被校验 %d 次，期望恰好 1 次", uid, n)
		}
	}
	if in.applied != applied || in.applied < 0 || in.applied > MaxCap {
		t.Fatalf("applied=%d, want %d（A 事件数）且在界内", in.applied, applied)
	}
	if in.table.Len() > 1_000 {
		t.Fatalf("去重表大小 %d 超过 K", in.table.Len())
	}
	t.Logf("%d 个 goroutine 完成：历史 %d 条连续无洞，%d 个 uid 各只入史一次",
		goroutines, len(evts), len(uCount))
}

// TestConcurrentHotUID: many goroutines submit the very same uid at once.
func TestConcurrentHotUID(t *testing.T) {
	h := New()
	mustCreate(t, h, inst, MaxCap, 10)
	const n = 32
	start := make(chan struct{})
	resCh := make(chan Result, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			r, err := h.Update(inst, "hot", 5)
			if err != nil {
				t.Errorf("Update: %v", err)
				return
			}
			resCh <- r
		}()
	}
	close(start)
	wg.Wait()
	close(resCh)
	var first Result
	for r := range resCh {
		if first == (Result{}) {
			first = r
		} else if r != first {
			t.Fatalf("并发同 uid 得到不同结果: %+v vs %+v", r, first)
		}
	}
	t.Logf("%d 个 goroutine 同时提交 uid=hot，全部得到 %+v", n, first)
	if first.Kind != Accepted || first.Seq != 1 {
		t.Fatalf("hot 应被接受且序号为 1: %+v", first)
	}
}

// TestRecoverEveryPrefix crashes at every history prefix and rebuilds.
func TestRecoverEveryPrefix(t *testing.T) {
	h := New()
	m := newModel(10, 3)
	mustCreate(t, h, inst, m.maxCap, m.k)
	h.Update(inst, "u1", 6)
	m.update("u1", 6)
	h.Update(inst, "u2", 6) // Rejected，不入史
	m.update("u2", 6)
	h.Step(inst)
	m.step()
	h.Update(inst, "u3", -4)
	m.update("u3", -4)
	h.Update(inst, "u4", 5)
	m.update("u4", 5)
	h.Step(inst)
	m.step()
	h.Close(inst)
	m.close()
	full := h.History(inst)
	for n := 0; n <= len(full); n++ { // 在每个崩溃点重建
		h2 := New()
		mustCreate(t, h2, inst, m.maxCap, m.k)
		in, _ := h2.lookup(inst)
		for _, e := range full[:n] {
			switch e.Kind {
			case history.UpdateAccepted:
				in.log.AppendUpdate(e.UID, e.Delta)
			case history.UpdateApplied:
				in.log.AppendApplied(e.Ref)
			case history.Closed:
				in.log.AppendClosed()
			}
		}
		if err := h2.Recover(inst); err != nil {
			t.Fatalf("prefix %d Recover: %v", n, err)
		}
		m2 := newModel(m.maxCap, m.k)
		m2.recoverFrom(full[:n])
		checkState(t, h2, inst, m2, []string{"u1", "u2", "u3", "u4"})
	}
	t.Logf("对全部 %d 个历史前缀逐一 Recover，状态均与模型一致", len(full)+1)
}
