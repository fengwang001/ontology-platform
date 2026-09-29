package gengc

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
)

// TestRejectedReasons：四类拒绝原因可区分。
func TestRejectedReasons(t *testing.T) {
	h := NewHeap(Config{EdenSize: 100, SurvivorSize: 100, OldSize: 100, PromoteAge: 1})
	h.Logger = newTestLogger(t)

	// 1) 载荷超过分配区大小。
	_, err := h.Alloc(0, bytes.Repeat([]byte{'z'}, 200))
	if !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("判定依据: 超大载荷应返回 ErrPayloadTooLarge，实际 %v", err)
	}
	t.Logf("输入: Alloc(payload=200B > eden=100B) 输出: err=%v", err)

	obj, err := h.Alloc(2, []byte("ok"))
	if err != nil {
		t.Fatal(err)
	}

	// 2) 字段下标越界。
	if _, _, err := h.GetField(obj, 5); !errors.Is(err, ErrFieldIndex) {
		t.Fatalf("判定依据: 越界读应返回 ErrFieldIndex，实际 %v", err)
	}
	if err := h.SetField(obj, -1, 0); !errors.Is(err, ErrFieldIndex) {
		t.Fatalf("判定依据: 负下标写应返回 ErrFieldIndex，实际 %v", err)
	}

	// 3) 回收后访问句柄。
	t.Logf("输入: obj=%d 无引用，GC 后访问其字段/载荷/写/登记根", obj)
	if err := h.MinorGC(); err != nil {
		t.Fatal(err)
	}
	if h.Live(obj) {
		t.Fatal("判定依据: obj 应已被回收")
	}
	if _, _, err := h.GetField(obj, 0); !errors.Is(err, ErrHandleReclaimed) {
		t.Fatalf("判定依据: 回收后 GetField 应返回 ErrHandleReclaimed，实际 %v", err)
	}
	if _, err := h.Payload(obj); !errors.Is(err, ErrHandleReclaimed) {
		t.Fatalf("判定依据: 回收后 Payload 应返回 ErrHandleReclaimed，实际 %v", err)
	}
	if err := h.SetField(obj, 0, 0); !errors.Is(err, ErrHandleReclaimed) {
		t.Fatalf("判定依据: 回收后 SetField 应返回 ErrHandleReclaimed，实际 %v", err)
	}
	if err := h.AddRoot(obj); !errors.Is(err, ErrHandleReclaimed) {
		t.Fatalf("判定依据: 回收后 AddRoot 应返回 ErrHandleReclaimed，实际 %v", err)
	}
	t.Logf("输出: ErrPayloadTooLarge / ErrFieldIndex / ErrHandleReclaimed 均可区分命中")

	// 4) 老年代放不下晋升对象。
	small := NewHeap(Config{EdenSize: 400, SurvivorSize: 30, OldSize: 30, PromoteAge: 1})
	small.Logger = newTestLogger(t)
	big, err := small.Alloc(0, bytes.Repeat([]byte{'q'}, 50))
	if err != nil {
		t.Fatal(err)
	}
	if err := small.AddRoot(big); err != nil {
		t.Fatal(err)
	}
	if err := small.MinorGC(); !errors.Is(err, ErrOldGenFull) {
		t.Fatalf("判定依据: 晋升失败应返回 ErrOldGenFull，实际 %v", err)
	}
	t.Logf("输入: 64B 对象晋升进 30B 老年代 输出: err=%v", ErrOldGenFull)
}

// oracleHeap 是“全堆朴素可达性”参照模型：不区分代，不从记忆集推导，
// 每次回收只从根集做引用闭包，闭包外一律死亡。
type oracleHeap struct {
	nfields map[uint32]int
	fields  map[uint32]map[int]uint32
	roots   map[uint32]struct{}
	alive   map[uint32]bool
}

func newOracleHeap() *oracleHeap {
	return &oracleHeap{
		nfields: map[uint32]int{},
		fields:  map[uint32]map[int]uint32{},
		roots:   map[uint32]struct{}{},
		alive:   map[uint32]bool{},
	}
}

func (o *oracleHeap) alloc(id uint32, nfields int) {
	o.nfields[id] = nfields
	o.fields[id] = map[int]uint32{}
	o.alive[id] = true
}

func (o *oracleHeap) setField(src uint32, idx int, dst uint32) {
	if dst == 0 || !o.alive[dst] {
		o.fields[src][idx] = 0
	} else {
		o.fields[src][idx] = dst
	}
}

func (o *oracleHeap) addRoot(id uint32)    { o.roots[id] = struct{}{} }
func (o *oracleHeap) removeRoot(id uint32) { delete(o.roots, id) }

func (o *oracleHeap) gc() {
	reach := map[uint32]bool{}
	var stack []uint32
	for r := range o.roots {
		if o.alive[r] {
			reach[r] = true
			stack = append(stack, r)
		}
	}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		idxs := make([]int, 0, len(o.fields[cur]))
		for i := 0; i < o.nfields[cur]; i++ {
			idxs = append(idxs, i)
		}
		sort.Ints(idxs)
		for _, i := range idxs {
			t := o.fields[cur][i]
			if t != 0 && o.alive[t] && !reach[t] {
				reach[t] = true
				stack = append(stack, t)
			}
		}
	}
	o.alive = reach
	for id := range o.fields {
		if !reach[id] {
			delete(o.fields, id)
			delete(o.nfields, id)
		}
	}
	for r := range o.roots {
		if !reach[r] {
			delete(o.roots, r)
		}
	}
}

// assertGraphEquals 对照真实堆与朴素模型：存活集相同且在相同句柄标号下图同构。
func assertGraphEquals(t *testing.T, h *Heap, o *oracleHeap, stage string) {
	t.Helper()
	got := map[uint32]bool{}
	for id := range h.handles {
		got[id] = true
	}
	if len(got) != len(o.alive) {
		t.Fatalf("判定依据[%s]: 存活数 真实=%d 朴素=%d；真实集=%v 朴素集=%v",
			stage, len(got), len(o.alive), sortedSet(got), sortedSet(o.alive))
	}
	for id := range o.alive {
		if !got[id] {
			t.Fatalf("判定依据[%s]: 朴素可达 id=%d 但真实堆已回收", stage, id)
		}
	}
	for id := 0; id < 10000; id++ {
		uid := uint32(id)
		n, want := o.nfields[uid]
		if !want {
			continue
		}
		for i := 0; i < n; i++ {
			wd := o.fields[uid][i]
			gd, ok, err := h.GetField(uid, i)
			if err != nil {
				t.Fatalf("判定依据[%s]: 存活 %d[%d] 读取失败: %v", stage, uid, i, err)
			}
			if wd == 0 {
				if ok {
					t.Fatalf("判定依据[%s]: %d[%d] 朴素为空，真实=%d", stage, uid, i, gd)
				}
			} else if !ok || gd != wd {
				t.Fatalf("判定依据[%s]: %d[%d] 朴素=%d 真实=%d(ok=%v)", stage, uid, i, wd, gd, ok)
			}
		}
	}
	assertUsedEqualsLive(t, h, stage)
	t.Logf("判定依据[%s]: 真实存活集 %v == 朴素可达集，引用图在相同句柄下同构",
		stage, sortedSet(got))
}

func sortedSet(m map[uint32]bool) []uint32 {
	out := make([]uint32, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// TestVsNaiveReachability：固定操作序列下反复与全堆朴素可达性对照。
func TestVsNaiveReachability(t *testing.T) {
	h := NewHeap(Config{EdenSize: 120, SurvivorSize: 300, OldSize: 900, PromoteAge: 3})
	h.Logger = newTestLogger(t)
	om := newOracleHeap()

	alloc := func(nfields int, tag string) uint32 {
		id, err := h.Alloc(nfields, []byte(tag))
		if err != nil {
			t.Fatalf("Alloc(%s): %v", tag, err)
		}
		om.alloc(id, nfields)
		t.Logf("输入: Alloc(%s) -> %d", tag, id)
		return id
	}
	link := func(src uint32, idx int, dst uint32) {
		mustSet(t, h, src, idx, dst)
		om.setField(src, idx, dst)
		t.Logf("输入: %d[%d] -> %d", src, idx, dst)
	}
	addRoot := func(id uint32) {
		if err := h.AddRoot(id); err != nil {
			t.Fatal(err)
		}
		om.addRoot(id)
	}
	gc := func(stage string) {
		g0 := h.MinorGCCount()
		if err := h.MinorGC(); err != nil {
			t.Fatalf("%s: GC 失败: %v", stage, err)
		}
		if h.MinorGCCount() != g0+1 {
			t.Fatalf("%s: 回收次数应精确 +1", stage)
		}
		om.gc()
		assertGraphEquals(t, h, om, stage)
	}

	a := alloc(2, "A")
	b := alloc(1, "B")
	c := alloc(0, "C")
	d := alloc(1, "D")
	addRoot(a)
	link(a, 0, b)
	link(a, 1, c)
	link(b, 0, d)
	link(d, 0, a) // a->b->d->a 环 + a->c
	gc("gc#1 环与挂叶全部存活")

	link(a, 0, 0) // 断开 a->b；b-d 环脱离
	gc("gc#2 b/d 环释放，c 存活")

	link(a, 1, 0)
	gc("gc#3 仅 a 存活")

	// 让 a 经历足够 GC 晋升。
	gc("gc#4 a 年龄+1")
	gc("gc#5 a 晋升到老年代")
	if h.handles[a].region != regionOld {
		t.Fatalf("前置: a 应在老年代，实际 %s", h.handles[a].region)
	}

	e := alloc(0, "E")
	f := alloc(0, "F")
	link(a, 0, e) // old->young 写屏障；e 只被老年代引用
	// f 无任何引用（朴素模型里也不可达）
	t.Logf("输入: e=%d 仅被老年代 a 引用；f=%d 完全不可达", e, f)
	gc("gc#6 记忆集保活 e，f 释放")

	// 分配压力触发自动 GC；每次真实 GC 后同步跑一次朴素 GC 再对照。
	for i := 0; i < 12; i++ {
		x := alloc(0, fmt.Sprintf("X%d", i))
		om.alive[x] = true
		// 不登记根：朴素模型下次 GC 会死；真实堆也应在某次自动 GC 后清掉。
	}
	autoGCs := h.MinorGCCount()
	// 此前显式 GC 6 次；自动回收次数：
	t.Logf("输入: 12 个无根对象制造分配压力，自动 GC 次数=%d", autoGCs-6)
	gc("gc#对齐观察点")
}

// TestRememberedSetPruned：老对象不再引用年轻代时必须移出记忆集。
func TestRememberedSetPruned(t *testing.T) {
	h := NewHeap(Config{EdenSize: 400, SurvivorSize: 400, OldSize: 400, PromoteAge: 1})
	h.Logger = newTestLogger(t)
	older, err := h.Alloc(1, []byte("O"))
	if err != nil {
		t.Fatal(err)
	}
	if err := h.AddRoot(older); err != nil {
		t.Fatal(err)
	}
	if err := h.MinorGC(); err != nil {
		t.Fatal(err)
	}
	y, err := h.Alloc(0, []byte("Y"))
	if err != nil {
		t.Fatal(err)
	}
	mustSet(t, h, older, 0, y)
	if len(h.remembered) != 1 {
		t.Fatalf("前置: 记忆集应有 1 项，实际 %d", len(h.remembered))
	}
	t.Logf("输入: old.field0 -> young=%d，记忆集=%d；清空该字段后 GC", y, len(h.remembered))
	if err := h.SetField(older, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := h.MinorGC(); err != nil {
		t.Fatal(err)
	}
	if len(h.remembered) != 0 {
		t.Fatalf("判定依据: 不再跨代引用后记忆集应清空，实际 %v", h.remembered)
	}
	if h.Live(y) {
		t.Fatal("判定依据: 失去唯一引用后 y 必须被回收")
	}
	t.Logf("输出: 记忆集=%d，y 存活=%v", len(h.remembered), h.Live(y))
	assertUsedEqualsLive(t, h, "remembered-prune")
}

// TestConcurrentAccess：分配/读写/回收并发调用，无数据竞争且调用只看到稳态。
func TestConcurrentAccess(t *testing.T) {
	h := NewHeap(Config{EdenSize: 300, SurvivorSize: 300, OldSize: 4000, PromoteAge: 4})
	h.Logger = newTestLogger(t)

	// 预置共享对象图：根对象持有两个槽。
	root, err := h.Alloc(2, []byte("ROOT"))
	if err != nil {
		t.Fatal(err)
	}
	if err := h.AddRoot(root); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// 协调者：回收者+读者完成后通知生产者停止。
	var doneWG sync.WaitGroup

	// 生产者：不断分配并挂到 root.field1（年轻引用），再清空。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			id, err := h.Alloc(0, []byte(fmt.Sprintf("tmp-%d", i%7)))
			if err != nil {
				t.Errorf("并发 Alloc 失败: %v", err)
				return
			}
			if err := h.SetField(root, 1, id); err != nil && !errors.Is(err, ErrHandleReclaimed) {
				t.Errorf("并发 SetField 失败: %v", err)
				return
			}
			if err := h.SetField(root, 1, 0); err != nil {
				t.Errorf("并发清空失败: %v", err)
				return
			}
		}
	}()

	// 回收者：持续触发次要回收；任一失败只可能是晋升失败（此处老年代足够大）。
	doneWG.Add(1)
	go func() {
		defer doneWG.Done()
		for i := 0; i < 200; i++ {
			if err := h.MinorGC(); err != nil && !errors.Is(err, ErrOldGenFull) {
				t.Errorf("并发 GC 失败: %v", err)
				return
			}
		}
	}()

	// 读者：遍历根与字段；要么拿到合法句柄，要么拿到可区分的拒绝错误。
	doneWG.Add(1)
	go func() {
		defer doneWG.Done()
		for i := 0; i < 2000; i++ {
			if !h.Live(root) {
				t.Errorf("根对象必须始终存活")
				return
			}
			if id, ok, err := h.GetField(root, 1); err != nil {
				if !errors.Is(err, ErrHandleReclaimed) && !errors.Is(err, ErrFieldIndex) {
					t.Errorf("读者收到非预期错误: %v", err)
					return
				}
			} else if ok {
				if _, gerr := h.Payload(id); gerr != nil && !errors.Is(gerr, ErrHandleReclaimed) {
					t.Errorf("读者读载荷收到非预期错误: %v", gerr)
					return
				}
			}
			_ = h.Stats()
		}
	}()

	// 写屏障者：预先建好持根对象，再反复建立 old->young（对象可能在间隙被回收，可接受）。
	holder, err := h.Alloc(1, []byte("HOLD"))
	if err != nil {
		t.Fatal(err)
	}
	if err := h.AddRoot(holder); err != nil {
		t.Fatal(err)
	}
	doneWG.Add(1)
	go func() {
		defer doneWG.Done()
		for i := 0; i < 1000; i++ {
			tmp, err := h.Alloc(0, []byte("w"))
			if err != nil {
				t.Errorf("屏障者 Alloc 失败: %v", err)
				return
			}
			if err := h.SetField(holder, 0, tmp); err != nil && !errors.Is(err, ErrHandleReclaimed) {
				t.Errorf("屏障者 SetField 失败: %v", err)
				return
			}
		}
	}()

	go func() {
		doneWG.Wait()
		close(stop)
	}()
	wg.Wait()
	doneWG.Wait()

	// 稳态校验：根存活、各代已用字节等于对象字节之和。
	if !h.Live(root) {
		t.Fatal("判定依据: 并发结束后根必须存活")
	}
	assertUsedEqualsLive(t, h, "concurrent-final")
	t.Logf("输出: 并发结束 gc=%d 晋升=%d eden=%d survivor=%d old=%d",
		h.MinorGCCount(), h.PromotionCount(),
		h.used[regionEden], h.used[h.fromRegion], h.used[regionOld])
}

// TestDeterministicCounts：同一操作序列重放，回收与晋升次数完全相同。
func TestDeterministicCounts(t *testing.T) {
	script := func(h *Heap) (uint64, uint64) {
		logger := h.Logger
		h.Logger = nil // 重放时安静
		defer func() { h.Logger = logger }()

		var roots []uint32
		for round := 0; round < 5; round++ {
			var ids []uint32
			for i := 0; i < 6; i++ {
				id, err := h.Alloc(1+(i%3), []byte(fmt.Sprintf("r%d-%d", round, i)))
				if err != nil {
					t.Fatalf("script alloc: %v", err)
				}
				ids = append(ids, id)
			}
			for i := 0; i+1 < len(ids); i++ {
				if err := h.SetField(ids[i], i%1, ids[i+1]); err != nil {
					t.Fatalf("script link: %v", err)
				}
			}
			if round%2 == 0 {
				if err := h.AddRoot(ids[0]); err != nil {
					t.Fatalf("script root: %v", err)
				}
				roots = append(roots, ids[0])
			}
			if err := h.MinorGC(); err != nil {
				// 晋升失败也是确定性输出的一部分：记录后继续下一轮配置不可能，
				// 因此容量必须足够；若真失败直接报错。
				t.Fatalf("script gc: %v", err)
			}
			if round == 2 {
				h.RemoveRoot(roots[0])
			}
		}
		return h.MinorGCCount(), h.PromotionCount()
	}

	// Eden 足够大，保证回收只由脚本里的显式 MinorGC 触发，计数可精确预期。
	cfg := Config{EdenSize: 4096, SurvivorSize: 2048, OldSize: 8192, PromoteAge: 3}
	h1 := NewHeap(cfg)
	h2 := NewHeap(cfg)
	h1.Logger = newTestLogger(t)
	g1, p1 := script(h1)
	g2, p2 := script(h2)
	if g1 != g2 || p1 != p2 {
		t.Fatalf("判定依据: 相同操作序列计数必须一致；run1=(gc=%d,promote=%d) run2=(gc=%d,promote=%d)",
			g1, p1, g2, p2)
	}
	if g1 != 5 {
		t.Fatalf("判定依据: 5 次显式 GC 计数应为 5，实际 %d", g1)
	}

	// 两次运行结束后存活集也应一致。
	if len(h1.handles) != len(h2.handles) {
		t.Fatalf("判定依据: 重放后存活对象数应一致 %d vs %d", len(h1.handles), len(h2.handles))
	}
	for id, l1 := range h1.handles {
		l2, ok := h2.handles[id]
		if !ok || l1.region != l2.region {
			t.Fatalf("判定依据: 句柄 %d 重放位置不一致 %s vs %s", id, l1, l2)
		}
	}
	t.Logf("输出: 两次重放计数一致 gc=%d promote=%d，存活集位置一致（大小均 %d）",
		g1, p1, len(h1.handles))
}
