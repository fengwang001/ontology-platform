package gc

import (
	"errors"
	"math/rand"
	"sync"
	"testing"
)

func mustAlloc(t *testing.T, rt *Runtime, fields int) ObjID {
	t.Helper()
	id, err := rt.Alloc(fields)
	if err != nil {
		t.Fatalf("Alloc(%d) failed: %v", fields, err)
	}
	return id
}

func aliveGen(rt *Runtime, id ObjID) (bool, generation) {
	o, ok := rt.objs[id]
	if !ok || !o.alive {
		return false, genYoung
	}
	return true, o.gen
}

func mustBeAlive(t *testing.T, rt *Runtime, id ObjID, wantGen generation) {
	t.Helper()
	alive, gen := aliveGen(rt, id)
	if !alive {
		t.Fatalf("object %d: want alive in %v, got collected", id, wantGen)
	}
	if gen != wantGen {
		t.Fatalf("object %d: want gen %v, got %v", id, wantGen, gen)
	}
}

func mustBeDead(t *testing.T, rt *Runtime, id ObjID) {
	t.Helper()
	if alive, _ := aliveGen(rt, id); alive {
		t.Fatalf("object %d: want collected, got alive", id)
	}
}

// 年老对象仅通过记忆集维持年轻对象存活（年轻对象不在根集合中）。
func TestRememberedSetKeepsYoungAlive(t *testing.T) {
	rt := New(8, 8, 2)
	a := mustAlloc(t, rt, 1)
	if err := rt.AddRoot(a); err != nil {
		t.Fatal(err)
	}
	rt.MinorGC()
	rt.MinorGC() // a 达到阈值晋升
	mustBeAlive(t, rt, a, genOld)

	b := mustAlloc(t, rt, 1)
	if err := rt.Write(a, 0, b); err != nil {
		t.Fatal(err)
	}
	if !rt.remset.has(a) {
		t.Fatal("old->young write must register the old object in the remembered set")
	}
	rt.MinorGC()
	mustBeAlive(t, rt, b, genYoung) // 仅靠记忆集存活
	if !rt.remset.has(a) {
		t.Fatal("remembered set must be recomputed and still contain a")
	}
}

// 年老对象引用改写后旧目标不再被保留；记忆集重算后移除不再引用年轻对象的登记项。
func TestRemsetRewriteDropsOldTarget(t *testing.T) {
	rt := New(8, 8, 2)
	a := mustAlloc(t, rt, 1)
	if err := rt.AddRoot(a); err != nil {
		t.Fatal(err)
	}
	rt.MinorGC()
	rt.MinorGC()
	mustBeAlive(t, rt, a, genOld)

	b := mustAlloc(t, rt, 1)
	if err := rt.Write(a, 0, b); err != nil {
		t.Fatal(err)
	}
	rt.MinorGC()
	mustBeAlive(t, rt, b, genYoung)

	if err := rt.Write(a, 0, NilObj); err != nil { // 清除 a -> b
		t.Fatal(err)
	}
	rt.MinorGC()
	mustBeDead(t, rt, b)
	if len(rt.remset) != 0 {
		t.Fatalf("remembered set must drop entries no longer referencing young, got %v", rt.remset)
	}
	if _, err := rt.Read(b, 0); !errors.Is(err, ErrDangling) {
		t.Fatalf("read on collected object: want ErrDangling, got %v", err)
	}
}

// 晋升阈值取等：回收次数达到阈值的那次回收中晋升。
func TestPromotionThresholdExact(t *testing.T) {
	rt := New(8, 8, 2)
	a := mustAlloc(t, rt, 0)
	if err := rt.AddRoot(a); err != nil {
		t.Fatal(err)
	}
	rt.MinorGC()
	mustBeAlive(t, rt, a, genYoung) // age=1 < 2
	if got := rt.Stats().Promotions; got != 0 {
		t.Fatalf("promotions before threshold: want 0, got %d", got)
	}
	rt.MinorGC()
	mustBeAlive(t, rt, a, genOld) // age=2 == 阈值，晋升
	if got := rt.Stats().Promotions; got != 1 {
		t.Fatalf("promotions at threshold: want 1, got %d", got)
	}
}

// 晋升失败（年老区满）后对象留在年轻区、回收次数钉在阈值，下次回收重试。
func TestPromotionFailureRetry(t *testing.T) {
	rt := New(8, 1, 1)
	a := mustAlloc(t, rt, 0)
	b := mustAlloc(t, rt, 0)
	if err := rt.AddRoot(a); err != nil {
		t.Fatal(err)
	}
	if err := rt.AddRoot(b); err != nil {
		t.Fatal(err)
	}
	rt.MinorGC() // a 晋升占满年老区；b 晋升失败
	mustBeAlive(t, rt, a, genOld)
	mustBeAlive(t, rt, b, genYoung)
	if age := rt.objs[b].age; age != 1 {
		t.Fatalf("failed promotion must pin age at threshold: want 1, got %d", age)
	}
	rt.MinorGC() // 重试仍失败，年龄保持阈值
	mustBeAlive(t, rt, b, genYoung)
	if age := rt.objs[b].age; age != 1 {
		t.Fatalf("age must stay pinned at threshold: want 1, got %d", age)
	}

	if err := rt.RemoveRoot(a); err != nil {
		t.Fatal(err)
	}
	rt.MajorGC() // 释放年老区空间
	mustBeDead(t, rt, a)
	rt.MinorGC() // 重试成功
	mustBeAlive(t, rt, b, genOld)
	if got := rt.Stats().Promotions; got != 2 {
		t.Fatalf("promotions: want 2, got %d", got)
	}
}

// 年老区回收清除死亡年老对象，并移除记忆集中相应登记项。
func TestMajorGCCleansRememberedSet(t *testing.T) {
	rt := New(8, 8, 1)
	a := mustAlloc(t, rt, 1)
	if err := rt.AddRoot(a); err != nil {
		t.Fatal(err)
	}
	rt.MinorGC()
	mustBeAlive(t, rt, a, genOld)

	b := mustAlloc(t, rt, 0)
	if err := rt.Write(a, 0, b); err != nil {
		t.Fatal(err)
	}
	if !rt.remset.has(a) {
		t.Fatal("expected a in remembered set")
	}
	if err := rt.RemoveRoot(a); err != nil {
		t.Fatal(err)
	}
	rt.MajorGC()
	mustBeDead(t, rt, a)
	if len(rt.remset) != 0 {
		t.Fatalf("major GC must remove entries of collected objects, got %v", rt.remset)
	}
	mustBeAlive(t, rt, b, genYoung) // 年老区回收不回收年轻对象
	rt.MinorGC()
	mustBeDead(t, rt, b) // 记忆集已清空，b 不再被保留
}

// 分配触发自动年轻区回收；回收后仍不足则报空间不足且不产生对象。
func TestAllocAutoGCAndOutOfMemory(t *testing.T) {
	rt := New(2, 0, 1)
	x := mustAlloc(t, rt, 0)
	y := mustAlloc(t, rt, 0)
	z, err := rt.Alloc(0) // 触发自动回收，x、y 无根被清除
	if err != nil {
		t.Fatalf("alloc after auto GC: %v", err)
	}
	mustBeDead(t, rt, x)
	mustBeDead(t, rt, y)
	if got := rt.Stats().MinorGCs; got != 1 {
		t.Fatalf("auto minor GC count: want 1, got %d", got)
	}
	if err := rt.AddRoot(z); err != nil {
		t.Fatal(err)
	}
	w := mustAlloc(t, rt, 0)
	if err := rt.AddRoot(w); err != nil { // 年轻区两个槽位均被根占满
		t.Fatal(err)
	}

	before := rt.Stats()
	if _, err := rt.Alloc(0); !errors.Is(err, ErrOutOfMemory) {
		t.Fatalf("want ErrOutOfMemory, got %v", err)
	}
	after := rt.Stats()
	// 失败的分配不产生对象、不改变对象数/晋升数/记忆集/写屏障统计；
	// 被真实触发的自动回收本身计入 MinorGCs。
	if after.YoungObjects != before.YoungObjects ||
		after.OldObjects != before.OldObjects ||
		after.Promotions != before.Promotions ||
		after.RememberedSize != before.RememberedSize ||
		after.BarrierRegistrations != before.BarrierRegistrations {
		t.Fatalf("rejected alloc must not change stats: before %+v after %+v", before, after)
	}
	if after.MinorGCs != before.MinorGCs+1 {
		t.Fatalf("auto GC must be counted: before %d after %d", before.MinorGCs, after.MinorGCs)
	}
}

// 拒绝次序组合：未定义 > 参数错误 > 悬垂引用 > 空间不足；被拒绝的操作不改变状态。
func TestRejectionOrder(t *testing.T) {
	rt2 := New(2, 4, 100) // 阈值取大，避免晋升干扰空间占用
	live2 := mustAlloc(t, rt2, 1)
	if err := rt2.AddRoot(live2); err != nil {
		t.Fatal(err)
	}
	dead2, err := rt2.Alloc(1) // 未加根，下一步回收后被清除
	if err != nil {
		t.Fatal(err)
	}
	rt2.MinorGC() // dead2 被回收
	mustBeDead(t, rt2, dead2)
	extra := mustAlloc(t, rt2, 0) // 重新占满年轻区，供 OOM 用例使用
	if err := rt2.AddRoot(extra); err != nil {
		t.Fatal(err)
	}

	before := rt2.Stats()
	cases := []struct {
		name string
		op   func() error
		want error
	}{
		{"undefined src beats bad field", func() error { return rt2.Write(9999, -1, 8888) }, ErrUndefined},
		{"undefined dst", func() error { return rt2.Write(live2, 0, 8888) }, ErrUndefined},
		{"bad field beats dangling dst", func() error { return rt2.Write(live2, 5, dead2) }, ErrBadArgument},
		{"negative field", func() error { return rt2.Write(live2, -1, live2) }, ErrBadArgument},
		{"dangling src", func() error { return rt2.Write(dead2, 0, live2) }, ErrDangling},
		{"dangling dst", func() error { return rt2.Write(live2, 0, dead2) }, ErrDangling},
		{"read undefined", func() error { _, err := rt2.Read(9999, 0); return err }, ErrUndefined},
		{"read bad field beats dangling", func() error { _, err := rt2.Read(dead2, 3); return err }, ErrBadArgument},
		{"read dangling", func() error { _, err := rt2.Read(dead2, 0); return err }, ErrDangling},
		{"alloc bad arg beats oom", func() error { _, err := rt2.Alloc(-1); return err }, ErrBadArgument},
		{"add root undefined", func() error { return rt2.AddRoot(9999) }, ErrUndefined},
		{"add root dangling", func() error { return rt2.AddRoot(dead2) }, ErrDangling},
	}
	for _, c := range cases {
		if err := c.op(); !errors.Is(err, c.want) {
			t.Errorf("%s: want %v, got %v", c.name, c.want, err)
		}
	}
	after := rt2.Stats()
	if before != after {
		t.Fatalf("rejected ops must not change stats:\nbefore %+v\nafter  %+v", before, after)
	}
	if len(rt2.remset) != 0 || len(rt2.roots) != 2 {
		t.Fatalf("rejected ops must not change remset/roots: remset=%v roots=%v", rt2.remset, rt2.roots)
	}
	// 空间不足优先级最低：触发的自动回收真实发生（计入 MinorGCs），
	// 但分配本身被拒绝，不产生对象。
	if _, err := rt2.Alloc(0); !errors.Is(err, ErrOutOfMemory) {
		t.Fatalf("alloc on full young space: want ErrOutOfMemory, got %v", err)
	}
	if got := rt2.Stats().YoungObjects; got != 2 {
		t.Fatalf("rejected alloc must not create objects, young=%d", got)
	}
}

// 统计视图：两区对象数、累计晋升、两类回收次数、记忆集大小、写屏障登记次数。
func TestStatsSnapshot(t *testing.T) {
	rt := New(8, 8, 1)
	a := mustAlloc(t, rt, 1)
	if err := rt.AddRoot(a); err != nil {
		t.Fatal(err)
	}
	rt.MinorGC() // a 晋升
	b := mustAlloc(t, rt, 0)
	c := mustAlloc(t, rt, 0)
	if err := rt.Write(a, 0, b); err != nil { // 登记 1 次
		t.Fatal(err)
	}
	if err := rt.Write(a, 0, c); err != nil { // 同一对象再写仍计登记次数，记忆集不去重
		t.Fatal(err)
	}
	rt.MajorGC()
	s := rt.Stats()
	want := Stats{YoungObjects: 2, OldObjects: 1, Promotions: 1, MinorGCs: 1, MajorGCs: 1, RememberedSize: 1, BarrierRegistrations: 2, LastMinorWork: 2}
	if s != want {
		t.Fatalf("stats mismatch:\nwant %+v\ngot  %+v", want, s)
	}
}

// 年轻区回收开销与年老区对象数无关（只随根数、记忆集大小、年轻对象数变化）。
func TestMinorGCWorkIndependentOfOldSize(t *testing.T) {
	build := func(deadOld int) (*Runtime, int) {
		rt := New(64, 8192, 2)
		for i := 0; i < deadOld; i++ { // 制造死亡年老对象（年老区回收前一直存在）
			g := mustAlloc(t, rt, 0)
			if err := rt.AddRoot(g); err != nil {
				t.Fatal(err)
			}
			rt.MinorGC()
			rt.MinorGC()
			if err := rt.RemoveRoot(g); err != nil {
				t.Fatal(err)
			}
		}
		r := mustAlloc(t, rt, 1)
		y1 := mustAlloc(t, rt, 1)
		y2 := mustAlloc(t, rt, 0)
		if err := rt.AddRoot(r); err != nil {
			t.Fatal(err)
		}
		if err := rt.Write(r, 0, y1); err != nil {
			t.Fatal(err)
		}
		if err := rt.Write(y1, 0, y2); err != nil {
			t.Fatal(err)
		}
		rt.MinorGC()
		return rt, rt.Stats().LastMinorWork
	}
	rtSmall, workSmall := build(5)
	rtBig, workBig := build(200)
	if workSmall != workBig {
		t.Fatalf("minor GC work must not grow with old-space size: small=%d big=%d", workSmall, workBig)
	}
	if rtBig.Stats().OldObjects <= rtSmall.Stats().OldObjects {
		t.Fatal("test setup broken: big runtime must have more old objects")
	}
}

// 并发分配、写引用、读取与回收：单互斥锁保证等价于某个串行顺序，
// 写屏障登记与引用写入不可分。结束后校验记忆集不变式。
func TestConcurrentWritesAndGC(t *testing.T) {
	rt := New(512, 512, 3)
	var producers sync.WaitGroup
	for g := 0; g < 8; g++ {
		producers.Add(1)
		go func(seed int64) {
			defer producers.Done()
			rng := rand.New(rand.NewSource(seed))
			var ids []ObjID
			for i := 0; i < 2000; i++ {
				switch rng.Intn(5) {
				case 0:
					if id, err := rt.Alloc(2); err == nil {
						ids = append(ids, id)
						if len(ids) > 64 {
							ids = ids[1:]
						}
					}
				case 1:
					if len(ids) > 0 {
						_ = rt.Write(ids[rng.Intn(len(ids))], rng.Intn(2), ids[rng.Intn(len(ids))])
					}
				case 2:
					if len(ids) > 0 {
						_, _ = rt.Read(ids[rng.Intn(len(ids))], rng.Intn(2))
					}
				case 3:
					if len(ids) > 0 {
						_ = rt.AddRoot(ids[rng.Intn(len(ids))])
					}
				case 4:
					if len(ids) > 0 {
						_ = rt.RemoveRoot(ids[rng.Intn(len(ids))])
					}
				}
			}
		}(int64(g + 1))
	}
	stop := make(chan struct{})
	var collectors sync.WaitGroup
	collectors.Add(2)
	go func() {
		defer collectors.Done()
		for {
			select {
			case <-stop:
				return
			default:
				rt.MinorGC()
			}
		}
	}()
	go func() {
		defer collectors.Done()
		for i := 0; i < 50; i++ {
			rt.MajorGC()
		}
	}()
	producers.Wait()
	close(stop)
	collectors.Wait()

	rt.mu.Lock()
	defer rt.mu.Unlock()
	for _, o := range rt.objs {
		if o.alive && o.gen == genOld && rt.referencesYoung(o) && !rt.remset.has(o.id) {
			t.Errorf("lost registration: old object %d references young but is not in remembered set", o.id)
		}
	}
	for id := range rt.remset {
		o, ok := rt.objs[id]
		if !ok || !o.alive || o.gen != genOld {
			t.Errorf("stale remembered-set entry: %d", id)
		}
	}
}
