package compensate

import (
	"context"
	"sync/atomic"
	"testing"
)

// 在每项副作用事务提交成功后注入崩溃，验证「部分生效 → 重投续作」：
// 已生效前缀被精确识别跳过，剩余项补齐，每项副作用对最终状态只贡献一次。
func TestResumeAfterCrashAtEveryBoundary(t *testing.T) {
	const n = 5
	for crashAfter := 0; crashAfter < n; crashAfter++ {
		store := NewMemoryStore()
		createTarget(store, "obj")
		reg := NewRegistry()
		cnt := make([]atomic.Int32, n)
		multiSpec(reg, "A", n, "biz/obj", cnt)
		cp := NewCrashAwareProcessor(Config{Store: store, Specs: reg})
		ev := testEvent("evX", "A", map[string]string{"object": "obj"})

		cp.SetCrashHook(OneShotHook("evX", crashAfter))
		r1 := cp.Handle(context.Background(), ev)
		if r1.Err == nil || r1.Err.Class != "INJECTED_CRASH" {
			t.Fatalf("crashAfter=%d: want injected crash, got %+v", crashAfter, r1)
		}
		// 崩溃时：前 crashAfter+1 项已提交。
		for i := 0; i < n; i++ {
			_, markerExists := store.SnapshotGet(DefaultEffectKey(ev, i))
			want := i <= crashAfter
			if markerExists != want {
				t.Fatalf("crashAfter=%d effect %d marker exists=%v want %v",
					crashAfter, i, markerExists, want)
			}
		}

		// 「重启」：同一处理器/存储（内存即持久化磁盘的替身）再次消费同一事件。
		r2 := cp.Handle(context.Background(), ev)
		if r2.Outcome != OutcomeCompleted || !r2.Resumed {
			t.Fatalf("crashAfter=%d: want resumed COMPLETED, got %+v", crashAfter, r2)
		}
		// 每项副作用的业务效果恰好出现一次（检查已提交业务值，而非进程内计数）。
		occ := committedBusinessOccurrences(store, "biz/obj")
		for i := 0; i < n; i++ {
			if got := occ["fx"+itoa(i)+":evX"]; got != 1 {
				t.Fatalf("crashAfter=%d effect %d observable occurrences=%d want 1",
					crashAfter, i, got)
			}
		}
		// 第三次投递：纯重复，不再有任何新副作用。
		r3 := cp.Handle(context.Background(), ev)
		if r3.Outcome != OutcomeCompleted {
			t.Fatalf("third delivery must be idempotent, got %+v", r3)
		}
		occ2 := committedBusinessOccurrences(store, "biz/obj")
		for i := 0; i < n; i++ {
			if got := occ2["fx"+itoa(i)+":evX"]; got != 1 {
				t.Fatalf("effect %d occurrences=%d after redelivery", i, got)
			}
		}
	}
}

// E2：崩溃部分生效后续作，但历史记录被注入「缺失」——必须报告 E2 并冻结边界。
func TestHistoryMissingFreezesBoundary(t *testing.T) {
	store := NewMemoryStore()
	createTarget(store, "obj")
	reg := NewRegistry()
	cnt := make([]atomic.Int32, 3)
	multiSpec(reg, "A", 3, "biz/obj", cnt)
	cp := NewCrashAwareProcessor(Config{Store: store, Specs: reg})
	ev := testEvent("evH", "A", map[string]string{"object": "obj"})
	cp.SetCrashHook(OneShotHook("evH", 0))
	_ = cp.Handle(context.Background(), ev)

	// 新处理器带「历史不可读」故障（模拟续作所需历史记录丢失）。
	faulty := &FaultyHistory{Base: DefaultHistory}
	faulty.MakeMissing("evH", 1) // 需要判定第 1 项是否已生效时，历史缺失
	p2 := NewProcessor(Config{Store: store, Specs: reg, History: faulty})
	r := p2.Handle(context.Background(), ev)
	if r.Err == nil || r.Err.Class != ErrHistoryMissing {
		t.Fatalf("want E2, got %+v", r)
	}
	// 边界冻结：第 0 项标记保持已生效，第 1、2 项标记绝不出现。
	if committedEffectCount(store, ev, 0) != 1 ||
		committedEffectCount(store, ev, 1) != 0 ||
		committedEffectCount(store, ev, 2) != 0 {
		t.Fatalf("boundary not frozen: %d %d %d",
			committedEffectCount(store, ev, 0),
			committedEffectCount(store, ev, 1),
			committedEffectCount(store, ev, 2))
	}
	// 再次投递原样返回 E2，且仍不扩大副作用。
	r2 := p2.Handle(context.Background(), ev)
	if r2.Err == nil || r2.Err.Class != ErrHistoryMissing {
		t.Fatalf("frozen boundary must keep reporting E2, got %+v", r2)
	}
	if committedEffectCount(store, ev, 1) != 0 ||
		committedEffectCount(store, ev, 2) != 0 {
		t.Fatalf("side effects widened after repeat")
	}
}

// E4：原子性校验失败——后继副作用已生效而前驱缺失（存储被外部破坏的模拟）。
func TestAtomicityViolationDetected(t *testing.T) {
	store := NewMemoryStore()
	createTarget(store, "obj")
	reg := NewRegistry()
	cnt := make([]atomic.Int32, 3)
	multiSpec(reg, "A", 3, "biz/obj", cnt)
	ev := testEvent("evV", "A", map[string]string{"object": "obj"})

	// 手工构造一个损坏状态：记录 CLAIMED 且位图全 false，但 effect#2 的痕迹已存在。
	_ = store.Update(func(txn Txn) error {
		bad := &Record{State: StateClaimed, EventID: "evV", ActionType: "A",
			Fingerprint: Fingerprint(ev), EffectCount: 3, Applied: []bool{false, false, false}}
		txn.Put(recordKey("evV"), encodeRecord(bad))
		txn.Put(DefaultEffectKey(ev, 2), []byte("1"))
		return nil
	})
	p := NewProcessor(Config{Store: store, Specs: reg})
	r := p.Handle(context.Background(), ev)
	if r.Err == nil || r.Err.Class != ErrAtomicityViolation {
		t.Fatalf("want E4, got %+v", r)
	}
	for i := range cnt {
		if i != 2 && committedEffectCount(store, ev, i) != 0 {
			t.Fatalf("E4 must not widen effects, but effect %d present", i)
		}
	}
}

// E1：目标对象在消费时已不存在——报告 E1，且不施加任何副作用。
func TestTargetGoneReportsE1(t *testing.T) {
	store := NewMemoryStore()
	reg := NewRegistry()
	cnt := make([]atomic.Int32, 2)
	multiSpec(reg, "A", 2, "biz/gone", cnt)
	p := NewProcessor(Config{Store: store, Specs: reg})
	ev := testEvent("evG", "A", map[string]string{"object": "gone"})
	r := p.Handle(context.Background(), ev)
	if r.Err == nil || r.Err.Class != ErrTargetGone {
		t.Fatalf("want E1, got %+v", r)
	}
	if committedEffectCount(store, ev, 0) != 0 ||
		committedEffectCount(store, ev, 1) != 0 {
		t.Fatalf("E1 must apply no effects")
	}
}

// 错误优先级：同一条事件同时满足多类条件时，只报告最高优先级的一类。
// E3（同 ID 不同内容）优先于一切：即使历史也坏了、目标也没了，仍只报 E3。
func TestErrorPriority_E3BeatsAll(t *testing.T) {
	store := NewMemoryStore()
	reg := NewRegistry()
	cnt := make([]atomic.Int32, 2)
	multiSpec(reg, "A", 2, "biz/p", cnt)
	p := NewProcessor(Config{Store: store, Specs: reg})
	first := testEvent("evP", "A", map[string]string{"object": "p"})
	// 先制造一个 FAILED(E1) 的冻结记录（目标从不存在）。
	r0 := p.Handle(context.Background(), first)
	if r0.Err == nil || r0.Err.Class != ErrTargetGone {
		t.Fatalf("setup: want E1, got %+v", r0)
	}
	// 同 ID 但内容不同：E3 必须压过冻结记录上的 E1。
	r := p.Handle(context.Background(), testEvent("evP", "A", map[string]string{"object": "OTHER"}))
	if r.Err == nil || r.Err.Class != ErrAmbiguousIdentity {
		t.Fatalf("want E3 overriding frozen E1, got %+v", r)
	}
}

// 部分续作时若同时满足 E2 与 E1：E2 优先（历史不可判定时，连目标检查都不能继续）。
func TestErrorPriority_E2BeatsE1(t *testing.T) {
	store := NewMemoryStore()
	createTarget(store, "obj")
	reg := NewRegistry()
	cnt := make([]atomic.Int32, 3)
	multiSpec(reg, "A", 3, "biz/obj", cnt)
	cp := NewCrashAwareProcessor(Config{Store: store, Specs: reg})
	ev := testEvent("evPE", "A", map[string]string{"object": "obj"})
	cp.SetCrashHook(OneShotHook("evPE", 0))
	_ = cp.Handle(context.Background(), ev)

	deleteTarget(store, "obj") // 现在目标也没了（满足 E1）
	faulty := &FaultyHistory{Base: DefaultHistory}
	faulty.MakeMissing("evPE", 1) // 同时历史缺失（满足 E2）
	p2 := NewProcessor(Config{Store: store, Specs: reg, History: faulty})
	r := p2.Handle(context.Background(), ev)
	if r.Err == nil || r.Err.Class != ErrHistoryMissing {
		t.Fatalf("E2 must outrank E1, got %+v", r)
	}
}

// 部分续作时若同时满足 E4 与 E1：E4 优先（原子性不满足时不得继续到目标检查）。
func TestErrorPriority_E4BeatsE1(t *testing.T) {
	store := NewMemoryStore()
	// 目标从不存在（满足 E1）
	reg := NewRegistry()
	cnt := make([]atomic.Int32, 3)
	multiSpec(reg, "A", 3, "biz/obj", cnt)
	ev := testEvent("evP4", "A", map[string]string{"object": "obj"})
	_ = store.Update(func(txn Txn) error {
		bad := &Record{State: StateClaimed, EventID: "evP4", ActionType: "A",
			Fingerprint: Fingerprint(ev), EffectCount: 3, Applied: []bool{false, false, false}}
		txn.Put(recordKey("evP4"), encodeRecord(bad))
		txn.Put(DefaultEffectKey(ev, 2), []byte("1")) // 后继已生效（满足 E4）
		return nil
	})
	p := NewProcessor(Config{Store: store, Specs: reg})
	r := p.Handle(context.Background(), ev)
	if r.Err == nil || r.Err.Class != ErrAtomicityViolation {
		t.Fatalf("E4 must outrank E1, got %+v", r)
	}
}
