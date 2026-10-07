package compensate

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// 独立可验证的 O(1) 证据：判定「新事件是否与历史事件重复」所需的记录探测次数
// 恒为 1，不随系统累计已处理事件总量 N 增长。
//
// 验证方式：审计 DEDUP_DECISION 记录里的 probes 字段即「按 EventID 的索引探测次数」。
// 在已有 N 条历史事件后投递第 N+1 条，probes 必须仍然等于 1。
// 取 N ∈ {1, 64, 4096} 三个规模点给出上界证明式断言。
func TestDedupLookupConstantTime(t *testing.T) {
	// 普通模式取三个规模点（含 4096）给出与历史总量无关的上界证据；
	// race 模式下二进制内存开销大，使用较小规模（逻辑断言完全相同）。
	sizes := []int{1, 64, 4096}
	if raceEnabled {
		sizes = []int{1, 64, 512}
	}
	for _, n := range sizes {
		t.Run(fmt.Sprintf("N=%d", n), func(t *testing.T) {
			store := NewMemoryStore()
			reg := NewRegistry()
			cnt := make([]atomic.Int32, 1)
			multiSpec(reg, "A", 1, "biz/const", cnt)
			p := NewProcessor(Config{Store: store, Specs: reg})

			// 预置 N 条不同事件（不同对象以避免业务键竞争）。
			for i := 0; i < n; i++ {
				obj := fmt.Sprintf("c%d", i)
				createTarget(store, obj)
				ev := testEvent(fmt.Sprintf("hist%d", i), "A", map[string]string{"object": obj})
				if r := p.Handle(context.Background(), ev); r.Outcome != OutcomeCompleted {
					t.Fatalf("seed %d: %+v", i, r)
				}
			}
			// 新事件与对它的重复投递：去重探测次数都必须恒为 1。
			createTarget(store, "probe")
			candidate := testEvent("candidate", "A", map[string]string{"object": "probe"})
			if r := p.Handle(context.Background(), candidate); r.Outcome != OutcomeCompleted {
				t.Fatalf("candidate: %+v", r)
			}
			if r := p.Handle(context.Background(), candidate); r.Outcome != OutcomeCompleted {
				t.Fatalf("candidate redelivery: %+v", r)
			}
			recs := (AuditLog{}).Snapshot(store)
			var got []string
			for _, r := range recs {
				if r.Kind == AuditDedup && r.EventID == "candidate" {
					got = append(got, r.Detail["probes"])
				}
			}
			if len(got) != 2 || got[0] != "1" || got[1] != "1" {
				t.Fatalf("N=%d probes must be [1 1], got %v", n, got)
			}
		})
	}
}

// 无关并发修改共存：补偿续作进行时，另一条无关事件持续修改目标对象的「其他字段」。
// 续作不得因为「对象被修改过」就拒绝；两类操作的最终效果必须各自完整、互不覆盖。
func TestResumeCoexistsWithUnrelatedConcurrentModification(t *testing.T) {
	store := NewMemoryStore()
	createTarget(store, "shared")
	reg := NewRegistry()
	cnt := make([]atomic.Int32, 4)
	multiSpec(reg, "A", 4, "biz/shared", cnt)
	cp := NewCrashAwareProcessor(Config{Store: store, Specs: reg})
	ev := testEvent("evC", "A", map[string]string{"object": "shared"})
	cp.SetCrashHook(OneShotHook("evC", 1)) // 前 2 项提交后崩溃
	r := cp.Handle(context.Background(), ev)
	if r.Err == nil || r.Err.Class != "INJECTED_CRASH" {
		t.Fatalf("setup crash: %+v", r)
	}

	// 无关事件的效果只读写「额外字段键」（与补偿读集合不相交）。
	_, extraKey := targetKeys(ev)
	stop := make(chan struct{})
	warmed := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		i := 0
		warmupDone := false
		for {
			select {
			case <-stop:
				return
			default:
			}
			i++
			_ = store.Update(func(txn Txn) error {
				txn.Put(extraKey, []byte(fmt.Sprintf("v%d", i)))
				return nil
			})
			if !warmupDone && i >= 3 {
				warmupDone = true
				close(warmed)
			}
		}
	}()
	<-warmed // 确保无关修改在续作开始前已并发进行

	// 续作：必须成功完成，不能因无关字段被反复修改而中止。
	r2 := cp.Handle(context.Background(), ev)
	close(stop)
	wg.Wait()
	if r2.Outcome != OutcomeCompleted || !r2.Resumed {
		t.Fatalf("resume must succeed amid unrelated writes, got %+v", r2)
	}
	occ := committedBusinessOccurrences(store, "biz/shared")
	for i := 0; i < 4; i++ {
		if got := occ["fx"+itoa(i)+":evC"]; got != 1 {
			t.Fatalf("effect %d observable occurrences=%d want 1", i, got)
		}
	}
	if v, ok := store.SnapshotGet(extraKey); !ok || string(v) == "v0" {
		t.Fatalf("unrelated concurrent writes must survive, got %q ok=%v", v, ok)
	}
}

// 全局可串行化：大量不同事件并发消费（含重复投递），最终状态必须等价于
// 按审计 Seq 的某个串行顺序执行——即每个 EventID 的补偿恰好一次、副作用计数精确。
func TestConcurrentExecutionsAreSerializable(t *testing.T) {
	store := NewMemoryStore()
	reg := NewRegistry()
	events := 80
	if raceEnabled {
		events = 24 // race 二进制内存开销大，缩小规模；并发交错覆盖保持充分
	}
	counters := map[string]*[]atomic.Int32{}
	for i := 0; i < events; i++ {
		obj := fmt.Sprintf("s%d", i)
		createTarget(store, obj)
		c := make([]atomic.Int32, 3)
		counters[obj] = &c
		dynamicSpec(reg, "A", 3, c)
	}
	p := NewProcessor(Config{Store: store, Specs: reg})

	var wg sync.WaitGroup
	for i := 0; i < events; i++ {
		obj := fmt.Sprintf("s%d", i)
		id := fmt.Sprintf("sev%d", i)
		ev := testEvent(id, "A", map[string]string{"object": obj})
		// 每个事件 4 个并发投递（模拟重复投递 + 多消费者）。
		for k := 0; k < 4; k++ {
			evLocal := ev // 捕获到本轮事件，避免 goroutine 读到后续轮次的循环变量
			wg.Add(1)
			go func(evLocal Event) {
				defer wg.Done()
				r := p.Handle(context.Background(), evLocal)
				if r.Err != nil && r.Err.Class != "INJECTED_CRASH" {
					t.Errorf("unexpected error for %s: %v", id, r.Err)
				}
			}(evLocal)
		}
	}
	wg.Wait()

	for i := 0; i < events; i++ {
		obj := fmt.Sprintf("s%d", i)
		id := "sev" + itoa(i)
		ev := testEvent(id, "A", map[string]string{"object": obj})
		occ := committedBusinessOccurrences(store, "biz/"+obj)
		for fx := 0; fx < 3; fx++ {
			if _, ok := store.SnapshotGet(DefaultEffectKey(ev, fx)); !ok {
				t.Fatalf("object %s effect %d marker missing", obj, fx)
			}
			if got := occ["fx"+itoa(fx)+":"+id]; got != 1 {
				t.Fatalf("object %s effect %d occurrences=%d want 1", obj, fx, got)
			}
		}
	}
	// 审计 Seq 给出严格全序；按该顺序重放决策，COMPLETE 每事件恰一次。
	recs := (AuditLog{}).Snapshot(store)
	completes := map[string]int{}
	var prev int64
	for _, r := range recs {
		if r.Seq <= prev {
			t.Fatalf("seq order violated: %d after %d", r.Seq, prev)
		}
		prev = r.Seq
		if r.Kind == AuditComplete {
			completes[r.EventID]++
		}
	}
	for i := 0; i < events; i++ {
		id := fmt.Sprintf("sev%d", i)
		if completes[id] != 1 {
			t.Fatalf("event %s completed %d times in audit", id, completes[id])
		}
	}
}

// 撤销与补偿并发竞争：无论交织如何，结果对每个 EventID 必须是确定的二者之一，
// 且与审计中 CLAIM / SUPERSEDED 的先后严格一致（线性化分界）。
func TestUndoRaceIsDeterministic(t *testing.T) {
	for iter := 0; iter < 200; iter++ {
		store := NewMemoryStore()
		reg := NewRegistry()
		cnt := make([]atomic.Int32, 2)
		multiSpec(reg, "A", 2, "biz/race", cnt)
		p := NewProcessor(Config{Store: store, Specs: reg})
		ev := testEvent(fmt.Sprintf("race%d", iter), "A", map[string]string{"object": "race"})
		createTarget(store, "race")

		var wg sync.WaitGroup
		wg.Add(2)
		var outcome HandleResult
		var undo UndoOutcome
		go func() { defer wg.Done(); outcome = p.Handle(context.Background(), ev) }()
		go func() { defer wg.Done(); undo = p.UndoArrived(ev) }()
		wg.Wait()

		// 一致性不变量：
		//  补偿完成 ⇒ 撤销必然 TooLate，两项副作用恰好各一次；
		//  补偿被放弃 ⇒ 撤销必然 Wins，零副作用，且之后任何重复投递仍为 SUPERSEDED。
		switch undo {
		case UndoTooLate:
			if outcome.Outcome != OutcomeCompleted {
				t.Fatalf("iter %d: undo too late but outcome=%+v", iter, outcome)
			}
			occ := committedBusinessOccurrences(store, "biz/race")
			if occ["fx0:"+ev.EventID] != 1 || occ["fx1:"+ev.EventID] != 1 {
				t.Fatalf("iter %d: effects occurrences=%v", iter, occ)
			}
		case UndoWins:
			occ := committedBusinessOccurrences(store, "biz/race")
			if totalOcc(occ) != 0 {
				t.Fatalf("iter %d: undo won but effects %v outcome=%+v",
					iter, occ, outcome)
			}
			if r := p.Handle(context.Background(), ev); r.Outcome != OutcomeSuperseded {
				t.Fatalf("iter %d: post-race delivery not superseded: %+v", iter, r)
			}
		}
	}
}
