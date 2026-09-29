package gengc

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"slices"
	"testing"
)

// newTestLogger 把“输入、输出与判定依据”全部打印到测试日志。
func newTestLogger(t *testing.T) *log.Logger {
	t.Helper()
	return log.New(testWriter{t}, "[gengc] ", log.LstdFlags|log.Lmicroseconds)
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(string(bytes.TrimRight(p, "\n")))
	return len(p), nil
}

func mustSet(t *testing.T, h *Heap, src uint32, idx int, dst uint32) {
	t.Helper()
	if err := h.SetField(src, idx, dst); err != nil {
		t.Fatalf("SetField(%d,%d,%d) 意外失败: %v", src, idx, dst, err)
	}
}

func fieldOf(t *testing.T, h *Heap, id uint32, idx int) uint32 {
	t.Helper()
	v, ok, err := h.GetField(id, idx)
	if err != nil {
		t.Fatalf("GetField(%d,%d) 意外失败: %v", id, idx, err)
	}
	if !ok {
		t.Fatalf("GetField(%d,%d) 为空，期望非空", id, idx)
	}
	return v
}

// sumLive 线性扫描某区域统计存活对象字节之和。
func sumLive(h *Heap, r region) uint32 {
	buf := h.regionBuf(r)
	var off uint32
	for off < h.used[r] {
		o := readObjFrom(buf, off)
		off += o.size()
	}
	return off
}

func assertUsedEqualsLive(t *testing.T, h *Heap, stage string) {
	t.Helper()
	checks := []struct {
		name string
		r    region
		got  uint32
	}{
		{"eden", regionEden, h.used[regionEden]},
		{"survivor", h.fromRegion, h.used[h.fromRegion]},
		{"old", regionOld, h.used[regionOld]},
	}
	for _, c := range checks {
		if want := sumLive(h, c.r); c.got != want {
			t.Fatalf("%s: %sUsed=%d 但存活对象字节和=%d", stage, c.name, c.got, want)
		}
	}
	t.Logf("判定依据[%s]: 各代已用字节 == 代内存活对象字节和 (eden=%d survivor=%d old=%d)",
		stage, h.used[regionEden], h.used[h.fromRegion], h.used[regionOld])
}

// TestOnlyReferencedByOldSurvives：只被老年对象引用的年轻对象必须存活。
func TestOnlyReferencedByOldSurvives(t *testing.T) {
	h := NewHeap(Config{EdenSize: 400, SurvivorSize: 400, OldSize: 400, PromoteAge: 1})
	h.Logger = newTestLogger(t)

	oldObj, err := h.Alloc(1, []byte("OLD"))
	if err != nil {
		t.Fatal(err)
	}
	garbage, err := h.Alloc(0, []byte("GARBAGE"))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("输入: oldHandle=%d garbageHandle=%d；old 为根，GC#1 使其晋升", oldObj, garbage)
	if err := h.AddRoot(oldObj); err != nil {
		t.Fatal(err)
	}
	if err := h.MinorGC(); err != nil {
		t.Fatal(err)
	}
	if h.Live(garbage) {
		t.Fatal("判定依据: garbage 不可达，应在 GC#1 被回收")
	}

	young, err := h.Alloc(0, []byte("YOUNG"))
	if err != nil {
		t.Fatal(err)
	}
	mustSet(t, h, oldObj, 0, young)
	if n := len(h.remembered); n != 1 {
		t.Fatalf("写屏障应把 old 加入记忆集，实际 size=%d", n)
	}
	t.Logf("输入: young=%d 仅被老年代 old 引用（不在根集）；GC#2", young)

	if err := h.MinorGC(); err != nil {
		t.Fatal(err)
	}
	if !h.Live(young) {
		t.Fatal("判定依据: young 虽无根路径，但被记忆集中老对象引用，必须存活")
	}
	if got := fieldOf(t, h, oldObj, 0); got != young {
		t.Fatalf("判定依据: old.field0 复制改写后仍应指向 young=%d，实际 %d", young, got)
	}
	if err := h.SetPayload(young, []byte("YOUNG")); err != nil {
		t.Fatal(err)
	}
	p, err := h.Payload(young)
	if err != nil || string(p) != "YOUNG" {
		t.Fatalf("判定依据: 存活 young 载荷应保持，got=%q err=%v", p, err)
	}
	t.Logf("输出: young=%d 存活，old.field0=%d，GC=%d 晋升=%d",
		young, fieldOf(t, h, oldObj, 0), h.MinorGCCount(), h.PromotionCount())
	assertUsedEqualsLive(t, h, "old-references-young")
}

// TestPromotedObjectReferencesYoung：晋升对象仍引用年轻对象时必须进记忆集。
func TestPromotedObjectReferencesYoung(t *testing.T) {
	h := NewHeap(Config{EdenSize: 400, SurvivorSize: 400, OldSize: 800, PromoteAge: 2})
	h.Logger = newTestLogger(t)

	parent, err := h.Alloc(1, []byte("P"))
	if err != nil {
		t.Fatal(err)
	}
	if err := h.AddRoot(parent); err != nil {
		t.Fatal(err)
	}
	if err := h.MinorGC(); err != nil {
		t.Fatal(err)
	}
	// child 在第一次 GC 后才出生：第二次 GC 时 parent 晋升，child 只存活一次、仍年轻。
	child, err := h.Alloc(0, []byte("C"))
	if err != nil {
		t.Fatal(err)
	}
	mustSet(t, h, parent, 0, child)
	t.Logf("输入: parent=%d 幸存一次后分配 child=%d；parent 为根，GC#2 使 parent 晋升、child 留年轻代",
		parent, child)
	if err := h.MinorGC(); err != nil {
		t.Fatal(err)
	}

	if loc := h.handles[parent]; loc.region != regionOld {
		t.Fatalf("判定依据: parent 应在老年代，实际 %s", loc.region)
	}
	if loc := h.handles[child]; !isYoung(loc.region) {
		t.Fatalf("判定依据: child 应仍在年轻代，实际 %s", loc.region)
	}
	if _, ok := h.remembered[h.handles[parent].offset]; !ok {
		t.Fatalf("判定依据: 晋升的 parent 引用年轻 child，必须进入记忆集，实际=%v", h.remembered)
	}

	if err := h.MinorGC(); err != nil {
		t.Fatal(err)
	}
	if !h.Live(child) {
		t.Fatal("判定依据: 被晋升对象引用的 child 必须存活")
	}
	if got := fieldOf(t, h, parent, 0); got != child {
		t.Fatalf("判定依据: 晋升对象指针应随 child 复制而改写，got=%d want=%d", got, child)
	}
	t.Logf("输出: parent 在 %s，child 在 %s，parent.field0=%d，记忆集大小=%d",
		h.handles[parent].region, h.handles[child].region, child, len(h.remembered))
	assertUsedEqualsLive(t, h, "promoted-ref-young")
}

// TestCrossGenerationalCycle：跨代环必须整体存活且指针全部改写。
func TestCrossGenerationalCycle(t *testing.T) {
	h := NewHeap(Config{EdenSize: 400, SurvivorSize: 400, OldSize: 800, PromoteAge: 2})
	h.Logger = newTestLogger(t)

	alpha, err := h.Alloc(1, []byte("A"))
	if err != nil {
		t.Fatal(err)
	}
	if err := h.AddRoot(alpha); err != nil {
		t.Fatal(err)
	}
	if err := h.MinorGC(); err != nil {
		t.Fatal(err)
	}
	if err := h.MinorGC(); err != nil { // 两次存活，alpha 晋升
		t.Fatal(err)
	}
	beta, err := h.Alloc(1, []byte("B"))
	if err != nil {
		t.Fatal(err)
	}
	mustSet(t, h, alpha, 0, beta)
	mustSet(t, h, beta, 0, alpha)
	t.Logf("输入: 跨代环 alpha=%d(old) -> beta=%d(young) -> alpha；GC（beta 存活一次仍年轻）", alpha, beta)

	if err := h.MinorGC(); err != nil {
		t.Fatal(err)
	}
	if !h.Live(alpha) || !h.Live(beta) {
		t.Fatal("判定依据: 跨代环上所有对象都应存活")
	}
	if got := fieldOf(t, h, alpha, 0); got != beta {
		t.Fatalf("判定依据: alpha.field0 应改写到 beta，got=%d", got)
	}
	if got := fieldOf(t, h, beta, 0); got != alpha {
		t.Fatalf("判定依据: beta.field0 应指向 alpha，got=%d", got)
	}
	if _, ok := h.remembered[h.handles[alpha].offset]; !ok {
		t.Fatal("判定依据: alpha 仍引用年轻 beta，应在记忆集中")
	}
	t.Logf("输出: 环存活 alpha.field0=%d beta.field0=%d",
		fieldOf(t, h, alpha, 0), fieldOf(t, h, beta, 0))
	assertUsedEqualsLive(t, h, "cross-gen-cycle")
}

// TestSurvivorOverflowPromotesEarly：幸存区放不下时提前晋升。
func TestSurvivorOverflowPromotesEarly(t *testing.T) {
	// 每个对象 26B；幸存区 30B 仅容一个，其余两个必须提前晋升。
	h := NewHeap(Config{EdenSize: 400, SurvivorSize: 30, OldSize: 800, PromoteAge: 99})
	h.Logger = newTestLogger(t)

	ids := make([]uint32, 0, 3)
	for i := 0; i < 3; i++ {
		id, err := h.Alloc(1, []byte(fmt.Sprintf("OBJ%d", i)))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
		if err := h.AddRoot(id); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("输入: 三个根对象 %v，幸存区仅 30B（容不下全部），晋升阈值=99，GC", ids)
	before := h.PromotionCount()
	if err := h.MinorGC(); err != nil {
		t.Fatal(err)
	}
	if got := h.PromotionCount() - before; got < 2 {
		t.Fatalf("判定依据: 幸存区溢出应触发提前晋升，期望>=2 实际=%d", got)
	}
	for _, id := range ids {
		if !h.Live(id) {
			t.Fatalf("判定依据: 提前晋升不应丢失对象 %d", id)
		}
	}
	t.Logf("输出: 本次晋升=%d（年龄未达阈值，仅因幸存区溢出），全部存活 %v",
		h.PromotionCount()-before, ids)
	assertUsedEqualsLive(t, h, "survivor-overflow")
}

// TestPromotionFailureRollsBack：晋升失败时回收整体撤回，堆逐字节不变。
func TestPromotionFailureRollsBack(t *testing.T) {
	h := NewHeap(Config{EdenSize: 400, SurvivorSize: 60, OldSize: 200, PromoteAge: 1})
	h.Logger = newTestLogger(t)

	for i := 0; i < 3; i++ {
		id, err := h.Alloc(0, bytes.Repeat([]byte{'X'}, 50))
		if err != nil {
			t.Fatal(err)
		}
		if err := h.AddRoot(id); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.MinorGC(); err != nil {
		t.Fatal(err)
	}
	if h.Stats().OldUsed < 192 {
		t.Fatalf("测试前置: 老年代应接近占满，实际 oldUsed=%d", h.Stats().OldUsed)
	}

	victim, err := h.Alloc(0, bytes.Repeat([]byte{'Y'}, 50))
	if err != nil {
		t.Fatal(err)
	}
	if err := h.AddRoot(victim); err != nil {
		t.Fatal(err)
	}

	edenSnap := slices.Clone(h.eden)
	oldSnap := slices.Clone(h.old)
	fromSnap := slices.Clone(h.regionBuf(h.fromRegion))
	handlesSnap := mapsCloneLoc(h.handles)
	gcs, promos := h.MinorGCCount(), h.PromotionCount()

	t.Logf("输入: oldUsed=%d，victim=%d(64B) 存活必须晋升但老年代无空间，GC 应失败",
		h.used[regionOld], victim)
	err = h.MinorGC()
	if !errors.Is(err, ErrOldGenFull) {
		t.Fatalf("判定依据: 应返回 ErrOldGenFull，实际 %v", err)
	}

	if !bytes.Equal(edenSnap, h.eden) || !bytes.Equal(oldSnap, h.old) ||
		!bytes.Equal(fromSnap, h.regionBuf(h.fromRegion)) {
		t.Fatal("判定依据: 撤回后 Eden/From/Old 必须与回收前逐字节相同")
	}
	if len(h.handles) != len(handlesSnap) {
		t.Fatal("判定依据: 撤回后句柄表必须一致")
	}
	if h.MinorGCCount() != gcs || h.PromotionCount() != promos {
		t.Fatal("判定依据: 撤回后回收/晋升计数不得增加")
	}
	for id := range handlesSnap {
		if _, err := h.Payload(id); err != nil {
			t.Fatalf("判定依据: 撤回后句柄 %d 应可正常访问: %v", id, err)
		}
	}
	t.Logf("输出: err=%v；堆字节、句柄表、计数(gc=%d,promote=%d)全部保持回收前状态",
		err, gcs, promos)
	assertUsedEqualsLive(t, h, "rollback")
}
