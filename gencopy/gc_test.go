package gencopy

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func mustAlloc(t *testing.T, h *Heap, refs int, payload string) Handle {
	t.Helper()
	x, err := h.Allocate(refs, []byte(payload))
	if err != nil {
		t.Fatalf("输入 Allocate(refs=%d,payload=%q) 失败: %v", refs, payload, err)
	}
	t.Logf("输入 Allocate(refs=%d,payload=%q) -> 句柄 %d", refs, payload, x)
	return x
}

func mustSet(t *testing.T, h *Heap, obj Handle, slot int, ref Handle) {
	t.Helper()
	if err := h.SetRef(obj, slot, ref); err != nil {
		t.Fatalf("输入 SetRef(obj=%d,slot=%d,ref=%d) 失败: %v", obj, slot, ref, err)
	}
	t.Logf("输入 SetRef(obj=%d,slot=%d,ref=%d)", obj, slot, ref)
}

func mustGC(t *testing.T, h *Heap, label string) Stats {
	t.Helper()
	st, err := h.ForceMinorGC()
	if err != nil {
		t.Fatalf("输入 ForceMinorGC(%s) 失败: %v", label, err)
	}
	t.Logf("输入 ForceMinorGC(%s) -> 输出 回收=%d 晋升=%d", label, st.MinorCollections, st.Promotions)
	return st
}

func mustRoot(t *testing.T, h *Heap, x Handle) {
	t.Helper()
	if err := h.AddRoot(x); err != nil {
		t.Fatalf("AddRoot(%d): %v", x, err)
	}
	t.Logf("输入 AddRoot(%d)", x)
}

func mustGet(t *testing.T, h *Heap, obj Handle, slot int) Handle {
	t.Helper()
	v, err := h.GetRef(obj, slot)
	if err != nil {
		t.Fatalf("GetRef(%d,%d): %v", obj, slot, err)
	}
	return v
}

// graphsEqualLabeled 以句柄身份做精确同构比较：复制/晋升后句柄不变，
// 因此图同构等价于存活集合、逐边与逐载荷完全一致。
func graphsEqualLabeled(a, b map[Handle]*objSnapshot) (bool, string) {
	if len(a) != len(b) {
		return false, fmt.Sprintf("存活集合大小 %d != %d", len(a), len(b))
	}
	for id, an := range a {
		bn, ok := b[id]
		if !ok {
			return false, fmt.Sprintf("对象 %d 在回收后缺失", id)
		}
		if len(an.refs) != len(bn.refs) {
			return false, fmt.Sprintf("对象 %d 字段数变化", id)
		}
		for i := range an.refs {
			if an.refs[i] != bn.refs[i] {
				return false, fmt.Sprintf("对象 %d 字段 %d: %d -> %d", id, i, an.refs[i], bn.refs[i])
			}
		}
		if !bytes.Equal(an.payload, bn.payload) {
			return false, fmt.Sprintf("对象 %d 载荷变化", id)
		}
	}
	return true, ""
}

func memSnapshot(t *testing.T, h *Heap) []byte {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	s := make([]byte, len(h.mem))
	copy(s, h.mem)
	return s
}

// 用例1：只被老年对象引用的年轻对象必须存活；跨代引用断开后必须被回收。
func TestOldOnlyReferencedYoungSurvives(t *testing.T) {
	h, err := NewHeap(Config{EdenSize: 400, SurvivorSize: 400, OldSize: 400, PromoteAge: 3})
	if err != nil {
		t.Fatal(err)
	}
	old := mustAlloc(t, h, 1, "old")
	mustRoot(t, h, old)
	mustGC(t, h, "old 年龄1")
	mustGC(t, h, "old 年龄2")
	mustGC(t, h, "old 年龄3，晋升")
	h.RemoveRoot(old)

	young := mustAlloc(t, h, 0, "young") // 不登记为根
	mustSet(t, h, old, 0, young)         // 写屏障：old 进入记忆集
	h.mu.Lock()
	inRS := len(h.rs) == 1 && h.rs[0] == old
	h.mu.Unlock()
	if !inRS {
		t.Fatalf("判定依据：写屏障后记忆集应含 %d", old)
	}
	t.Logf("判定依据：写屏障已把老年对象 %d 记入记忆集", old)

	pre := snapshotGraph(t, h, []Handle{old})
	mustGC(t, h, "年轻对象仅被老年对象引用")
	assertUsedEqualsLive(t, h)
	post := snapshotGraph(t, h, []Handle{old})
	if ok, why := graphsEqualLabeled(pre, post); !ok {
		t.Fatalf("判定依据：回收前后图应同构（%s）", why)
	}
	if got := mustGet(t, h, old, 0); got != young {
		t.Fatalf("判定依据：old.field0 仍应指向 %d，实际 %d", young, got)
	}
	t.Logf("输出：未登记根的年轻对象 %d 仅经记忆集存活，图同构", young)

	mustSet(t, h, old, 0, Nil) // 不再引用年轻代：写屏障移出记忆集
	mustGC(t, h, "断开跨代引用（年轻对象仍在幸存区，已不可达）")
	// 断开当轮：年轻代（Eden+From）全部清空，young 若在幸存区即被回收；
	// 再触发一轮确保任何残留中间年龄对象也被清理。
	mustGC(t, h, "再确认一轮")
	if _, err := h.Payload(young); !errors.Is(err, ErrHandleReclaimed) {
		t.Fatalf("判定依据：young 应被回收，得到 err=%v", err)
	}
	h.mu.Lock()
	rsEmpty := len(h.rs) == 0
	h.mu.Unlock()
	if !rsEmpty {
		t.Fatalf("判定依据：不再引用年轻代后记忆集应为空")
	}
	t.Logf("判定依据：young=%d 已回收，old=%d 已移出记忆集", young, old)
}

// 用例2：晋升对象仍引用年轻对象时必须加入记忆集。
func TestPromotedObjectRememberedForYoungRef(t *testing.T) {
	// a 大小 24+8+40=72；幸存区 44 只放得下 24 字节的 b。
	h, err := NewHeap(Config{EdenSize: 400, SurvivorSize: 44, OldSize: 400, PromoteAge: 10})
	if err != nil {
		t.Fatal(err)
	}
	a := mustAlloc(t, h, 1, string(make([]byte, 40))) // 句柄1，大对象
	b := mustAlloc(t, h, 0, "")                       // 句柄2，24 字节
	mustSet(t, h, a, 0, b)
	mustRoot(t, h, a)

	st := mustGC(t, h, "a 幸存区溢出提前晋升，b 留在幸存区")
	if st.Promotions != 1 {
		t.Fatalf("判定依据：应提前晋升 1 个对象，实际 %d", st.Promotions)
	}
	h.mu.Lock()
	aOld := h.objs[a].region == regionOld
	bYoung := h.objs[b] != nil && h.objs[b].region != regionOld
	hasA := len(h.rs) == 1 && h.rs[0] == a
	h.mu.Unlock()
	if !(aOld && bYoung && hasA) {
		t.Fatalf("判定依据：a 在老年代、b 在年轻代、记忆集含 a")
	}
	t.Logf("判定依据：a=%d 已提前晋升且因引用年轻 b=%d 加入记忆集", a, b)

	// b 不再直接是根，仅靠晋升对象 a 的记忆集条目存活。
	h.RemoveRoot(a)
	pre := snapshotGraph(t, h, []Handle{a})
	mustGC(t, h, "b 仅被晋升对象 a 引用")
	assertUsedEqualsLive(t, h)
	post := snapshotGraph(t, h, []Handle{a})
	if ok, why := graphsEqualLabeled(pre, post); !ok {
		t.Fatalf("判定依据：图同构（%s）", why)
	}
	if got := mustGet(t, h, a, 0); got != b {
		t.Fatalf("判定依据：晋升对象 a 仍应引用 b，实际 %d", got)
	}
	t.Logf("输出：仅被晋升对象引用的 b=%d 存活，边 a->b 完整", b)
}

// 用例3：跨代环 老O <-> 年轻Y。
func TestCrossGenerationalCycle(t *testing.T) {
	h, err := NewHeap(Config{EdenSize: 400, SurvivorSize: 400, OldSize: 400, PromoteAge: 1})
	if err != nil {
		t.Fatal(err)
	}
	o := mustAlloc(t, h, 1, "O")
	mustRoot(t, h, o)
	mustGC(t, h, "晋升 O")

	y := mustAlloc(t, h, 1, "Y")
	mustSet(t, h, o, 0, y)
	mustSet(t, h, y, 0, o)
	h.RemoveRoot(o) // 无任何根；o->y 靠写屏障入记忆集，环整体存活

	pre := snapshotGraph(t, h, []Handle{o, y})
	mustGC(t, h, "跨代环")
	assertUsedEqualsLive(t, h)
	post := snapshotGraph(t, h, []Handle{o, y})
	if ok, why := graphsEqualLabeled(pre, post); !ok {
		t.Fatalf("判定依据：跨代环回收前后应同构（%s）", why)
	}
	if mustGet(t, h, o, 0) != y || mustGet(t, h, y, 0) != o {
		t.Fatalf("判定依据：环边 o->y、y->o 必须保持")
	}
	t.Logf("输出：跨代环 O=%d <-> Y=%d 完整存活", o, y)
}

// 用例4：幸存区放不下时提前晋升（年龄未到阈值）。
func TestSurvivorOverflowEarlyPromotion(t *testing.T) {
	// 最小对象 24 字节，幸存区 30 只放得下一个。
	h, err := NewHeap(Config{EdenSize: 400, SurvivorSize: 30, OldSize: 400, PromoteAge: 10})
	if err != nil {
		t.Fatal(err)
	}
	a := mustAlloc(t, h, 0, "")
	b := mustAlloc(t, h, 0, "")
	mustRoot(t, h, a)
	mustRoot(t, h, b)

	// 根按句柄升序处理：a 占满幸存区，b 提前晋升。
	st := mustGC(t, h, "幸存区溢出")
	if st.Promotions != 1 {
		t.Fatalf("判定依据：仅 b 提前晋升，晋升数=%d", st.Promotions)
	}
	h.mu.Lock()
	aYoung := h.objs[a].region != regionOld
	bOld := h.objs[b].region == regionOld
	ageA := int(binaryU32(h.mem[h.abs(h.objs[a])+hdrAge:]))
	h.mu.Unlock()
	if !(aYoung && bOld) || ageA != 1 {
		t.Fatalf("判定依据：a 年轻且年龄=1，b 提前晋升")
	}
	t.Logf("输出：a=%d 幸存（年龄1），b=%d 未到年龄但因幸存区溢出提前晋升", a, b)
	assertUsedEqualsLive(t, h)
}

// 用例5：晋升失败时本次回收整体撤回，堆与回收前逐字节相同。
func TestPromotionFailureRollsBack(t *testing.T) {
	h, err := NewHeap(Config{EdenSize: 300, SurvivorSize: 300, OldSize: 200, PromoteAge: 1})
	if err != nil {
		t.Fatal(err)
	}
	// 老年代 200：8 个 24 字节对象占 192，第 9 个（40 字节）放不下。
	var rooted []Handle
	for i := 0; i < 8; i++ {
		x := mustAlloc(t, h, 0, "") // 24 字节，8 个恰好 192/200
		mustRoot(t, h, x)
		rooted = append(rooted, x)
		mustGC(t, h, fmt.Sprintf("填充老年代第 %d 个", i+1))
	}
	for _, x := range rooted {
		h.RemoveRoot(x)
	}

	big := mustAlloc(t, h, 0, "payload-16-bytes!!") // 24+16=40
	mustRoot(t, h, big)
	before := memSnapshot(t, h)
	statsBefore := h.Stats()

	_, gerr := h.ForceMinorGC()
	if !errors.Is(gerr, ErrOldFull) {
		t.Fatalf("判定依据：应返回 ErrOldFull，实际 %v", gerr)
	}
	after := memSnapshot(t, h)
	if !bytes.Equal(before, after) {
		t.Fatalf("判定依据：撤回后堆内存必须与回收前逐字节相同")
	}
	if h.Stats() != statsBefore {
		t.Fatalf("判定依据：撤回后计数不变")
	}
	pl, perr := h.Payload(big)
	if perr != nil || string(pl) != "payload-16-bytes!!" {
		t.Fatalf("判定依据：撤回后对象仍可在原位置访问，pl=%q err=%v", pl, perr)
	}
	youngUsed, oldUsed := h.UsedBytes()
	if oldUsed != 192 || youngUsed == 0 {
		t.Fatalf("判定依据：游标撤回，young=%d old=%d", youngUsed, oldUsed)
	}
	t.Logf("输出：晋升失败 -> ErrOldFull；内存 %d 字节逐字节相同；对象 %d 仍在 Eden 可访问", len(before), big)
}

// 用例6：根外句柄被回收后访问，以及各类可区分的拒绝原因。
func TestReclaimedAndDistinguishableErrors(t *testing.T) {
	h, err := NewHeap(Config{EdenSize: 200, SurvivorSize: 200, OldSize: 200, PromoteAge: 1})
	if err != nil {
		t.Fatal(err)
	}
	keep := mustAlloc(t, h, 2, "keep")
	dead := mustAlloc(t, h, 1, "dead")
	mustRoot(t, h, keep)

	// 字段下标越界（对象存活）。
	if _, e := h.GetRef(keep, 2); !errors.Is(e, ErrSlotOutOfRange) {
		t.Fatalf("判定依据：读越界应 ErrSlotOutOfRange，实际 %v", e)
	}
	if e := h.SetRef(keep, -1, Nil); !errors.Is(e, ErrSlotOutOfRange) {
		t.Fatalf("判定依据：负下标应 ErrSlotOutOfRange，实际 %v", e)
	}
	t.Logf("判定依据：下标越界 -> ErrSlotOutOfRange")

	// 从未颁发的句柄。
	if _, e := h.GetRef(Handle(9999), 0); !errors.Is(e, ErrInvalidHandle) {
		t.Fatalf("判定依据：未知句柄应 ErrInvalidHandle，实际 %v", e)
	}
	t.Logf("判定依据：未知句柄 -> ErrInvalidHandle")

	// dead 不是根、无跨代引用：回收后立墓碑。
	mustGC(t, h, "回收 dead")
	if _, e := h.Payload(dead); !errors.Is(e, ErrHandleReclaimed) {
		t.Fatalf("判定依据：应 ErrHandleReclaimed，实际 %v", e)
	}
	if _, e := h.GetRef(dead, 0); !errors.Is(e, ErrHandleReclaimed) {
		t.Fatalf("判定依据：读已回收句柄应 ErrHandleReclaimed，实际 %v", e)
	}
	if e := h.SetRef(dead, 0, Nil); !errors.Is(e, ErrHandleReclaimed) {
		t.Fatalf("判定依据：写已回收句柄应 ErrHandleReclaimed，实际 %v", e)
	}
	if e := h.AddRoot(dead); !errors.Is(e, ErrHandleReclaimed) {
		t.Fatalf("判定依据：登记已回收句柄应 ErrHandleReclaimed，实际 %v", e)
	}
	t.Logf("输出：根外句柄 %d 回收后任何访问均 ErrHandleReclaimed，与 ErrInvalidHandle 可区分", dead)

	// 载荷超过 Eden。
	bigPayload := make([]byte, 201)
	if _, e := h.Allocate(0, bigPayload); !errors.Is(e, ErrPayloadTooLarge) {
		t.Fatalf("判定依据：应 ErrPayloadTooLarge，实际 %v", e)
	}
	t.Logf("判定依据：载荷 201 > Eden 200 -> ErrPayloadTooLarge")

	// 载荷不超 Eden，但加对象头后超 Eden。
	if _, e := h.Allocate(20, make([]byte, 50)); !errors.Is(e, ErrObjectTooLarge) {
		t.Fatalf("判定依据：应 ErrObjectTooLarge，实际 %v", e)
	}
	t.Logf("判定依据：载荷 50<=200 但整对象 234>200 -> ErrObjectTooLarge")
}

// 用例7：与全堆朴素可达性 oracle 对照——多种图结构 + 多次回收。
func TestMatchesNaiveHeapReachability(t *testing.T) {
	h, err := NewHeap(Config{EdenSize: 260, SurvivorSize: 260, OldSize: 600, PromoteAge: 2})
	if err != nil {
		t.Fatal(err)
	}
	// 构造混合图：根可达链、环、孤岛、仅老年代引用的年轻对象。
	r := mustAlloc(t, h, 2, "root")
	a := mustAlloc(t, h, 1, "a")
	b := mustAlloc(t, h, 1, "b")
	c := mustAlloc(t, h, 0, "c")
	gar1 := mustAlloc(t, h, 1, "garbage-1")
	gar2 := mustAlloc(t, h, 0, "garbage-2")
	mustSet(t, h, r, 0, a)
	mustSet(t, h, r, 1, c)
	mustSet(t, h, a, 0, b)
	mustSet(t, h, b, 0, r) // 环 r->a->b->r
	mustSet(t, h, gar1, 0, gar2)
	mustRoot(t, h, r)

	for round := 1; round <= 4; round++ {
		// 每轮先做“全堆朴素可达性”判定，再跑分代回收。
		pre := snapshotGraph(t, h, allHandles(h))
		rootsPlusRS := effectiveRoots(h)
		oracle := naiveReachable(pre, rootsPlusRS)

		st := mustGC(t, h, fmt.Sprintf("第 %d 轮", round))
		assertUsedEqualsLive(t, h)

		alive := make(map[Handle]struct{})
		for _, id := range allHandles(h) {
			alive[id] = struct{}{}
		}
		if len(alive) != len(oracle) {
			t.Fatalf("第 %d 轮：分代存活 %d 个，朴素 oracle %d 个", round, len(alive), len(oracle))
		}
		for id := range oracle {
			if _, ok := alive[id]; !ok {
				t.Fatalf("第 %d 轮：朴素可达对象 %d 被分代回收误删", round, id)
			}
		}
		for id := range alive {
			if _, ok := oracle[id]; !ok {
				t.Fatalf("第 %d 轮：朴素不可达对象 %d 被分代回收错误保留", round, id)
			}
		}
		// 可达图同构（逐边逐载荷）。
		post := snapshotGraph(t, h, allHandles(h))
		for id := range oracle {
			an, bn := pre[id], post[id]
			if len(an.refs) != len(bn.refs) || !bytes.Equal(an.payload, bn.payload) {
				t.Fatalf("第 %d 轮：对象 %d 形态变化", round, id)
			}
			for i, e := range an.refs {
				_, eAlive := oracle[e]
				if e == Nil {
					if bn.refs[i] != Nil {
						t.Fatalf("第 %d 轮：对象 %d 空引用被改写", round, id)
					}
				} else if eAlive && bn.refs[i] != e {
					t.Fatalf("第 %d 轮：对象 %d 字段 %d 边被破坏 %d!=%d", round, id, i, bn.refs[i], e)
				}
			}
		}
		t.Logf("第 %d 轮判定依据：存活集合与朴素可达集完全一致（%d 个），图同构；回收=%d 晋升=%d",
			round, len(oracle), st.MinorCollections, st.Promotions)

		// 制造分配压力以反复触发回收，并随机替换一些引用（固定序列）。
		n := mustAlloc(t, h, 0, fmt.Sprintf("n-%d", round))
		mustSet(t, h, r, 1, n) // c 变为不可达（下一轮 oracle 应剔除）
	}
}

// effectiveRoots 返回次要回收的等价根：显式根 + 记忆集里的老年对象。
func effectiveRoots(h *Heap) []Handle {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := append([]Handle(nil), h.roots...)
	out = append(out, h.rs...)
	return out
}

// 用例8：同一操作序列多次执行得到相同的回收与晋升次数（确定性）。
func TestDeterministicCounts(t *testing.T) {
	run := func() Stats {
		h, err := NewHeap(Config{EdenSize: 120, SurvivorSize: 120, OldSize: 600, PromoteAge: 3})
		if err != nil {
			t.Fatal(err)
		}
		var root Handle
		for i := 0; i < 20; i++ {
			x := mustAlloc(t, h, 1, fmt.Sprintf("%d", i))
			if i == 0 {
				root = x
				if err := h.AddRoot(root); err != nil {
					t.Fatal(err)
				}
			} else {
				prev := mustGet(t, h, root, 0)
				if prev == Nil {
					mustSet(t, h, root, 0, x)
				}
			}
		}
		for i := 0; i < 3; i++ {
			if _, err := h.ForceMinorGC(); err != nil {
				t.Fatal(err)
			}
		}
		return h.Stats()
	}
	s1 := run()
	s2 := run()
	if s1 != s2 {
		t.Fatalf("判定依据：相同序列结果应相同，%+v != %+v", s1, s2)
	}
	t.Logf("输出：两次执行统计相同 -> 回收=%d 晋升=%d", s1.MinorCollections, s1.Promotions)
}

// 用例9：并发调用分配/读写/回收不应出现数据竞争或中间态可见。
func TestConcurrentAccess(t *testing.T) {
	h, err := NewHeap(Config{EdenSize: 64, SurvivorSize: 256, OldSize: 4096, PromoteAge: 2})
	if err != nil {
		t.Fatal(err)
	}
	base := mustAlloc(t, h, 4, "base")
	if err := h.AddRoot(base); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			i := 0
			for {
				select {
				case <-stop:
					return
				default:
				}
				x, aerr := h.Allocate(2, []byte{byte(id), byte(i)})
				if aerr != nil {
					continue // 晋升失败/堆满等可预期的容量错误
				}
				// x 尚未发布到任何存活引用；若在此瞬间被另一轮回收，
				// 它按未发布对象处理是正确语义，跳过即可。
				if gerr := h.SetRef(base, id%4, x); gerr != nil {
					if errors.Is(gerr, ErrHandleReclaimed) {
						continue
					}
					t.Errorf("SetRef(base,...): %v", gerr)
					return
				}
				if _, gerr := h.GetRef(base, id%4); gerr != nil {
					t.Errorf("GetRef(base,...): %v", gerr)
					return
				}
				i++
			}
		}(g)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			_, _ = h.ForceMinorGC()
		}
	}()
	// 运行一小段时间后停止。
	done := make(chan struct{})
	go func() {
		for i := 0; i < 200; i++ {
			h.UsedBytes()
			h.Stats()
		}
		close(done)
	}()
	<-done
	close(stop)
	wg.Wait()
	assertUsedEqualsLive(t, h)
	t.Logf("输出：并发结束后已用字节与存活对象一致，无数据竞争（配合 -race 验证）")
}
