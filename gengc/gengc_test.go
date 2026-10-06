package gengc

import (
	"errors"
	"sync"
	"testing"
)

func mustAlloc(t *testing.T, h *Heap, n int) uint64 {
	t.Helper()
	id, err := h.Allocate(n)
	if err != nil {
		t.Fatalf("allocate: %v", err)
	}
	return id
}

func mustSet(t *testing.T, h *Heap, src uint64, f int, dst uint64) {
	t.Helper()
	if err := h.SetField(src, f, dst); err != nil {
		t.Fatalf("set %d.%d=%d: %v", src, f, dst, err)
	}
}

func errKind(err error) ErrorKind {
	var e *GCError
	if errors.As(err, &e) {
		return e.Kind
	}
	return 0
}

func assertGen(t *testing.T, h *Heap, id uint64, want Generation) {
	t.Helper()
	o, ok := h.Object(id)
	if !ok {
		t.Fatalf("object %d missing", id)
	}
	if o.Gen() != want {
		t.Fatalf("object %d gen=%s want %s", id, o.Gen(), want)
	}
}

func assertDead(t *testing.T, h *Heap, id uint64) {
	t.Helper()
	if _, ok := h.Object(id); ok {
		t.Fatalf("object %d should be dead", id)
	}
}

// 年老对象仅通过记忆集维持年轻对象存活：年老对象本身不在根上。
func TestRememberedSetKeepsYoungAlive(t *testing.T) {
	h := New(Config{YoungCapacity: 100, OldCapacity: 100, PromoteThreshold: 1})
	old := mustAlloc(t, h, 1)
	h.AddRoot(old)
	h.CollectYoung() // old 晋升到年老区
	assertGen(t, h, old, GenOld)

	young := mustAlloc(t, h, 0)
	mustSet(t, h, old, 0, young) // 写屏障登记 old
	if h.Stats().RememberedSize != 1 {
		t.Fatalf("remembered size=%d want 1", h.Stats().RememberedSize)
	}

	// young 不可达于根，但作为记忆集起点存活；阈值为 1，存活即晋升。
	h.CollectYoung()
	assertGen(t, h, young, GenOld)
}

// 年老对象引用改写后，旧年轻目标不再被保留。
func TestOverwriteDropsOldTarget(t *testing.T) {
	// 阈值 2：b 存活后仍留在年轻区，old 继续持有年轻引用，记忆集保留。
	h := New(Config{YoungCapacity: 100, OldCapacity: 100, PromoteThreshold: 2})
	old := mustAlloc(t, h, 1)
	h.AddRoot(old)
	h.CollectYoung()
	h.CollectYoung() // old 两次存活后晋升

	a := mustAlloc(t, h, 0)
	b := mustAlloc(t, h, 0)
	mustSet(t, h, old, 0, a)
	mustSet(t, h, old, 0, b) // 改写：a 失去唯一入边

	h.CollectYoung()
	assertDead(t, h, a)
	if _, ok := h.Object(b); !ok {
		t.Fatalf("b should survive")
	}
	if h.Stats().RememberedSize != 1 {
		t.Fatalf("remembered size=%d want 1", h.Stats().RememberedSize)
	}
}

// 晋升阈值取等：第 threshold 次存活回收时晋升，之前不晋升。
func TestPromotionThresholdEquality(t *testing.T) {
	h := New(Config{YoungCapacity: 100, OldCapacity: 100, PromoteThreshold: 3})
	x := mustAlloc(t, h, 0)
	h.AddRoot(x)
	for i := 1; i < 3; i++ {
		h.CollectYoung()
		assertGen(t, h, x, GenYoung)
		if o, _ := h.Object(x); o.YoungGCs() != i {
			t.Fatalf("age after gc %d = %d want %d", i, o.YoungGCs(), i)
		}
	}
	h.CollectYoung()
	assertGen(t, h, x, GenOld)
	o, _ := h.Object(x)
	if o.Promotions() != 1 || h.Stats().Promotions != 1 {
		t.Fatalf("promotion counters wrong: obj=%d heap=%d", o.Promotions(), h.Stats().Promotions)
	}
}

// 死对象不晋升。
func TestDeadDoesNotPromote(t *testing.T) {
	h := New(Config{YoungCapacity: 100, OldCapacity: 100, PromoteThreshold: 1})
	mustAlloc(t, h, 0)
	h.CollectYoung()
	assertDead(t, h, 1)
	if h.Stats().Promotions != 0 {
		t.Fatalf("promotions=%d want 0", h.Stats().Promotions)
	}
}

// 年老区满：晋升失败对象留在年轻区、次数钳制在阈值，下轮再次尝试。
func TestPromotionFailureRetry(t *testing.T) {
	h := New(Config{YoungCapacity: 100, OldCapacity: 1, PromoteThreshold: 1})
	occupant := mustAlloc(t, h, 0)
	h.AddRoot(occupant)
	h.CollectYoung() // occupant 占满年老区
	assertGen(t, h, occupant, GenOld)

	x := mustAlloc(t, h, 0)
	h.AddRoot(x)
	h.CollectYoung() // 想晋升但年老区满
	assertGen(t, h, x, GenYoung)
	if o, _ := h.Object(x); o.YoungGCs() != 1 {
		t.Fatalf("age=%d want 1 (clamped at threshold)", o.YoungGCs())
	}

	h.RemoveRoot(occupant)
	h.CollectOld() // 清出年老区一个位置
	h.CollectYoung()
	assertGen(t, h, x, GenOld)
}

// 年老区回收清理记忆集中指向已清除对象的登记项。
func TestOldGCClearsRememberedEntries(t *testing.T) {
	h := New(Config{YoungCapacity: 100, OldCapacity: 100, PromoteThreshold: 1})
	old := mustAlloc(t, h, 1)
	h.AddRoot(old)
	h.CollectYoung()

	young := mustAlloc(t, h, 0)
	mustSet(t, h, old, 0, young)
	h.RemoveRoot(old) // old 不可达，但 young 仍由记忆集起点保留
	h.CollectYoung()
	assertGen(t, h, young, GenOld)
	if h.Stats().RememberedSize != 0 {
		t.Fatalf("rs size=%d want 0", h.Stats().RememberedSize)
	}

	old2 := mustAlloc(t, h, 1)
	h.AddRoot(old2)
	h.CollectYoung()
	y := mustAlloc(t, h, 0)
	mustSet(t, h, old2, 0, y)
	if h.Stats().RememberedSize != 1 {
		t.Fatalf("rs size=%d want 1", h.Stats().RememberedSize)
	}
	h.RemoveRoot(old2)
	h.CollectOld()
	assertDead(t, h, old2)
	if h.Stats().RememberedSize != 0 {
		t.Fatalf("rs size=%d want 0 after old gc", h.Stats().RememberedSize)
	}
	if h.Stats().OldGCs != 1 {
		t.Fatalf("old gcs=%d want 1", h.Stats().OldGCs)
	}
}

// 自动年轻区回收；回收后仍不足则空间不足，且该次分配零副作用。
func TestAutoYoungGCAndOutOfSpace(t *testing.T) {
	// 两区容量都为 1：a 占满年老区后 c 晋升失败，自动回收后仍满 → 空间不足。
	h := New(Config{YoungCapacity: 1, OldCapacity: 1, PromoteThreshold: 1})
	a := mustAlloc(t, h, 0)
	h.AddRoot(a)

	b, err := h.Allocate(0) // 满 → 自动回收；a 晋升，腾出位置
	if err != nil {
		t.Fatalf("auto gc allocate: %v", err)
	}
	assertGen(t, h, a, GenOld)
	if h.Stats().YoungGCs != 1 {
		t.Fatalf("young gcs=%d want 1", h.Stats().YoungGCs)
	}
	_ = b

	c := mustAlloc(t, h, 0)
	h.AddRoot(c)
	before := h.Stats()
	_, err = h.Allocate(0) // 自动回收后仍满（c 晋升失败）→ 空间不足，全回滚
	if errKind(err) != ErrOutOfSpace {
		t.Fatalf("err=%v want ErrOutOfSpace", err)
	}
	after := h.Stats()
	if after != before {
		t.Fatalf("stats changed on rejected allocate:\nbefore=%+v\nafter =%+v", before, after)
	}
}

// 拒绝次序：未定义 > 参数错误 > 悬垂引用 > 空间不足。
func TestRejectionOrder(t *testing.T) {
	h := New(Config{YoungCapacity: 100, OldCapacity: 100, PromoteThreshold: 1})

	if err := h.SetField(999, 5, 0); errKind(err) != ErrUndefined {
		t.Fatalf("got %v want undefined", err)
	}
	if _, err := h.GetField(999, 5); errKind(err) != ErrUndefined {
		t.Fatalf("got %v want undefined", err)
	}
	if err := h.AddRoot(42); errKind(err) != ErrUndefined {
		t.Fatalf("got %v want undefined", err)
	}

	alive := mustAlloc(t, h, 1)
	if err := h.SetField(alive, 7, 999); errKind(err) != ErrInvalidArgument {
		t.Fatalf("got %v want invalid argument", err)
	}

	h2 := New(Config{YoungCapacity: 1, OldCapacity: 100, PromoteThreshold: 1})
	root := mustAlloc(t, h2, 1)
	h2.AddRoot(root)
	d, _ := h2.Allocate(0) // 触发自动回收：root 晋升，d 不可达被清扫
	_, _ = h2.Allocate(0)  // 再次触发自动回收：d 不可达被清扫
	if _, ok := h2.Object(d); ok {
		t.Fatalf("d should be dead")
	}
	if err := h2.SetField(root, 0, d); errKind(err) != ErrDangling {
		t.Fatalf("got %v want dangling", err)
	}
	if _, err := h2.GetField(root, 9); errKind(err) != ErrInvalidArgument {
		t.Fatalf("got %v want invalid argument", err)
	}

	h3 := New(Config{YoungCapacity: 1, OldCapacity: 1, PromoteThreshold: 1})
	r := mustAlloc(t, h3, 1)
	h3.AddRoot(r)
	h3.CollectYoung()
	dang := mustAlloc(t, h3, 0) // 年轻区被占满
	_, err := h3.Allocate(0)    // 自动回收：r 已在年老区，dang 不可达被清扫
	if err != nil {
		t.Fatalf("sweep allocate: %v", err)
	}
	y := mustAlloc(t, h3, 0)
	h3.AddRoot(y)
	if err := h3.SetField(r, 0, dang); errKind(err) != ErrDangling {
		t.Fatalf("got %v want dangling (before space check)", err)
	}
	if _, err := h3.Allocate(0); errKind(err) != ErrOutOfSpace {
		t.Fatalf("got %v want out of space", err)
	}
}

// 根移除不立即死亡；对象可同时是根与记忆集目标。
func TestRootRemovalLazyAndRootAndRSCoexist(t *testing.T) {
	h := New(Config{YoungCapacity: 100, OldCapacity: 100, PromoteThreshold: 2})
	old := mustAlloc(t, h, 1)
	h.AddRoot(old)
	h.CollectYoung()
	h.CollectYoung() // old 第二次存活后晋升

	young := mustAlloc(t, h, 0)
	mustSet(t, h, old, 0, young)
	h.AddRoot(young)
	if !h.IsRoot(young) || !h.rs.contains(old) {
		t.Fatalf("young should be rooted and targeted by an rs member")
	}
	h.RemoveRoot(young)
	if _, ok := h.Object(young); !ok {
		t.Fatalf("root removal must not kill immediately")
	}
	h.CollectYoung()
	if _, ok := h.Object(young); !ok {
		t.Fatalf("young still reachable via rs member old")
	}
}

// 写屏障只登记“年老对象写年轻引用”；同对象多次写入只算一次。
func TestBarrierRegistrationRules(t *testing.T) {
	h := New(Config{YoungCapacity: 100, OldCapacity: 100, PromoteThreshold: 1})
	oldA := mustAlloc(t, h, 2)
	oldB := mustAlloc(t, h, 1)
	h.AddRoot(oldA)
	h.AddRoot(oldB)
	h.CollectYoung()

	youngHolder := mustAlloc(t, h, 1)
	youngTarget := mustAlloc(t, h, 0)
	h.AddRoot(youngHolder)

	before := h.Stats().BarrierEntries
	mustSet(t, h, youngHolder, 0, youngTarget) // 年轻对象写字段：不登记
	if h.Stats().BarrierEntries != before {
		t.Fatalf("young write must not register barrier")
	}
	mustSet(t, h, oldA, 0, oldB) // 年老→年老：不登记
	if h.Stats().BarrierEntries != before {
		t.Fatalf("old->old write must not register barrier")
	}
	mustSet(t, h, oldA, 0, youngTarget) // 年老→年轻：登记
	mustSet(t, h, oldA, 1, youngTarget) // 同对象再次写入：不重复计数
	if h.Stats().BarrierEntries != before+1 {
		t.Fatalf("barrier entries=%d want %d", h.Stats().BarrierEntries, before+1)
	}
	if h.Stats().RememberedSize != 1 {
		t.Fatalf("rs size=%d want 1 (object-granularity)", h.Stats().RememberedSize)
	}
}

// 并发写入与回收：结果必须等价于某个串行顺序，-race 下无数据竞争。
func TestConcurrentWritersAndCollectors(t *testing.T) {
	h := New(Config{YoungCapacity: 4096, OldCapacity: 4096, PromoteThreshold: 4})
	anchor := mustAlloc(t, h, 12)
	h.AddRoot(anchor)

	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				id, err := h.Allocate(0)
				if err != nil {
					continue
				}
				_ = h.SetField(anchor, seed*3+(i%3), id)
				_, _ = h.GetField(anchor, i%12)
			}
		}(w)
	}
	for c := 0; c < 2; c++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 150; i++ {
				h.CollectYoung()
			}
		}()
	}
	wg.Wait()

	// 串行等价不变量：anchor 恒存活，非空字段都指向存活对象。
	o, ok := h.Object(anchor)
	if !ok {
		t.Fatalf("anchor died")
	}
	for i := 0; i < o.NumFields(); i++ {
		if ref := o.Field(i); ref != 0 {
			if _, alive := h.Object(ref); !alive {
				t.Fatalf("field %d points to dead %d", i, ref)
			}
		}
	}
}
