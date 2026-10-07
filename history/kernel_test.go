package history

import (
	"fmt"
	"sync"
	"testing"
)

func mustKernel(t *testing.T, cfg Config) *Kernel {
	t.Helper()
	k, err := NewKernel(cfg)
	if err != nil {
		t.Fatalf("NewKernel: %v", err)
	}
	return k
}

func mustNavigate(t *testing.T, k *Kernel, url string, state any, sameDoc bool) Entry {
	t.Helper()
	e, err := k.Navigate(url, state, sameDoc)
	if err != nil {
		t.Fatalf("Navigate(%q): %v", url, err)
	}
	t.Logf("op=navigate url=%q state=%v sameDoc=%v -> doc=%s seq=%d", url, state, sameDoc, e.DocID, e.Seq)
	return e
}

func mustKind(t *testing.T, err error, want ErrKind, ctx string) {
	t.Helper()
	got, ok := KindOf(err)
	if !ok || got != want {
		t.Fatalf("%s: want kind %s, got %v", ctx, want, err)
	}
	t.Logf("%s -> rejected kind=%s reason=%q", ctx, want, err)
}

func checkInv(t *testing.T, k *Kernel) {
	t.Helper()
	if err := k.CheckInvariants(); err != nil {
		t.Fatalf("invariant violated: %v", err)
	}
}

func mustStatus(t *testing.T, k *Kernel, docID string, want docStatus) {
	t.Helper()
	got, ok := k.DocStatus(docID)
	if !ok {
		t.Fatalf("document %s not found", docID)
	}
	if got != want {
		t.Fatalf("document %s: want status %s, got %s", docID, want, got)
	}
	t.Logf("assert doc=%s status=%s ok", docID, want)
}

// 位移为零视为重新加载当前条目：分配新文档标识，旧文档卸载。
func TestZeroDeltaIsReload(t *testing.T) {
	k := mustKernel(t, Config{Capacity: 4})
	mustNavigate(t, k, "a", 1, false)
	e := mustNavigate(t, k, "b", 2, false)

	r := k.TraverseSync(0)
	t.Logf("op=traverse delta=0 -> action=%s doc=%s err=%v", r.Action, r.Entry.DocID, r.Err)
	if r.Err != nil {
		t.Fatalf("traverse(0): %v", r.Err)
	}
	if r.Action != ActionReloaded {
		t.Fatalf("delta=0: want action reloaded, got %s", r.Action)
	}
	if r.Entry.DocID == e.DocID {
		t.Fatalf("delta=0 reload must allocate a new doc id, still %s", r.Entry.DocID)
	}
	mustStatus(t, k, e.DocID, statusUnloaded)
	mustStatus(t, k, r.Entry.DocID, statusActive)
	checkInv(t, k)
}

// 位移越过两端按参数非法拒绝，且被拒绝的操作不改变任何状态。
func TestOutOfBoundsRejected(t *testing.T) {
	k := mustKernel(t, Config{Capacity: 4})
	mustNavigate(t, k, "a", 1, false)
	mustNavigate(t, k, "b", 2, false)
	mustNavigate(t, k, "c", 3, false)

	before := k.Entries()
	posBefore := k.Position()
	cachedBefore := k.CachedDocIDs()

	r := k.TraverseSync(-5)
	mustKind(t, r.Err, KindInvalidArgument, "traverse delta=-5")
	r = k.TraverseSync(1)
	mustKind(t, r.Err, KindInvalidArgument, "traverse delta=+1 at tail")

	if k.Position() != posBefore {
		t.Fatalf("rejected traversal moved position: %d -> %d", posBefore, k.Position())
	}
	entriesAfter := k.Entries()
	if len(entriesAfter) != len(before) {
		t.Fatalf("rejected traversal changed entry list")
	}
	for i := range before {
		if entriesAfter[i] != before[i] {
			t.Fatalf("entry %d changed after rejection: %+v -> %+v", i, before[i], entriesAfter[i])
		}
	}
	if fmt.Sprint(k.CachedDocIDs()) != fmt.Sprint(cachedBefore) {
		t.Fatalf("cache changed after rejection: %v -> %v", cachedBefore, k.CachedDocIDs())
	}
	checkInv(t, k)
}

// 同文档遍历：目标与当前共享文档标识，当前文档不进入缓存。
func TestSameDocTraverseDoesNotCache(t *testing.T) {
	k := mustKernel(t, Config{Capacity: 4})
	e0 := mustNavigate(t, k, "a", 1, false)
	e1 := mustNavigate(t, k, "a#frag", 2, true) // 仅片段变化，共享文档标识
	if e1.DocID != e0.DocID {
		t.Fatalf("same-doc navigation must share doc id: %s vs %s", e0.DocID, e1.DocID)
	}

	r := k.TraverseSync(-1)
	t.Logf("op=traverse delta=-1 -> action=%s doc=%s", r.Action, r.Entry.DocID)
	if r.Err != nil || r.Action != ActionSameDocument {
		t.Fatalf("want same-document traversal, got action=%s err=%v", r.Action, r.Err)
	}
	if k.IsCached(e0.DocID) {
		t.Fatalf("same-document traversal must not cache the current document")
	}
	mustStatus(t, k, e0.DocID, statusActive)

	r = k.TraverseSync(1)
	if r.Err != nil || r.Action != ActionSameDocument {
		t.Fatalf("want same-document traversal, got action=%s err=%v", r.Action, r.Err)
	}
	if len(k.CachedDocIDs()) != 0 {
		t.Fatalf("cache must stay empty, got %v", k.CachedDocIDs())
	}
	checkInv(t, k)
}

// 缓存资格四条件逐一缺失：缺一即卸载；全部满足才入缓存。
func TestEligibilityFourConditions(t *testing.T) {
	setters := []struct {
		name string
		set  func(k *Kernel, docID string)
	}{
		{"network-pending", func(k *Kernel, id string) {
			if err := k.SetNetworkPending(0, id, true); err != nil {
				t.Fatal(err)
			}
		}},
		{"unload-blocker", func(k *Kernel, id string) {
			if err := k.SetUnloadBlocker(0, id, true); err != nil {
				t.Fatal(err)
			}
		}},
		{"exclusive-resource", func(k *Kernel, id string) {
			if err := k.SetExclusiveResource(0, id, true); err != nil {
				t.Fatal(err)
			}
		}},
		{"marked-uncacheable", func(k *Kernel, id string) {
			if err := k.SetUncacheable(0, id, true); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range setters {
		t.Run(tc.name, func(t *testing.T) {
			k := mustKernel(t, Config{Capacity: 4})
			e := mustNavigate(t, k, "a", 1, false)
			tc.set(k, e.DocID)
			mustNavigate(t, k, "b", 2, false) // a 的文档离开
			mustStatus(t, k, e.DocID, statusUnloaded)
			checkInv(t, k)
		})
	}
	t.Run("all-clear", func(t *testing.T) {
		k := mustKernel(t, Config{Capacity: 4})
		e := mustNavigate(t, k, "a", 1, false)
		mustNavigate(t, k, "b", 2, false)
		mustStatus(t, k, e.DocID, statusCached)
		checkInv(t, k)
	})
}

// 缓存期间任一条件失效（资源被撤销、被远程标记等）必须立即驱逐。
func TestCachedConditionInvalidatedEvictsImmediately(t *testing.T) {
	setters := []struct {
		name string
		set  func(k *Kernel, docID string) error
	}{
		{"network-pending", func(k *Kernel, id string) error { return k.SetNetworkPending(0, id, true) }},
		{"unload-blocker", func(k *Kernel, id string) error { return k.SetUnloadBlocker(0, id, true) }},
		{"exclusive-resource", func(k *Kernel, id string) error { return k.SetExclusiveResource(0, id, true) }},
		{"marked-uncacheable", func(k *Kernel, id string) error { return k.SetUncacheable(0, id, true) }},
	}
	for _, tc := range setters {
		t.Run(tc.name, func(t *testing.T) {
			k := mustKernel(t, Config{Capacity: 4})
			e := mustNavigate(t, k, "a", 1, false)
			mustNavigate(t, k, "b", 2, false)
			mustStatus(t, k, e.DocID, statusCached)

			if err := tc.set(k, e.DocID); err != nil {
				t.Fatalf("set condition: %v", err)
			}
			t.Logf("op=set-condition name=%s doc=%s -> evicted", tc.name, e.DocID)
			mustStatus(t, k, e.DocID, statusUnloaded)
			if k.IsCached(e.DocID) {
				t.Fatalf("document %s must be evicted immediately", e.DocID)
			}
			checkInv(t, k)
		})
	}
}

// 存活时长恰好等于时视为已超过。
func TestTTLExactBoundary(t *testing.T) {
	k := mustKernel(t, Config{Capacity: 4, TTL: 10})
	e := mustNavigate(t, k, "a", 1, false) // t=0 时离开进入缓存
	mustNavigate(t, k, "b", 2, false)
	mustStatus(t, k, e.DocID, statusCached)

	if err := k.AdvanceClock(9); err != nil {
		t.Fatal(err)
	}
	t.Logf("op=advance-clock delta=9 -> now=%d, doc=%s still cached (9 < ttl 10)", k.Now(), e.DocID)
	mustStatus(t, k, e.DocID, statusCached)

	if err := k.AdvanceClock(1); err != nil {
		t.Fatal(err)
	}
	t.Logf("op=advance-clock delta=1 -> now=%d, age=10 == ttl 10, evicted", k.Now())
	mustStatus(t, k, e.DocID, statusUnloaded)
	checkInv(t, k)
}

// 容量恰好打满与超出：超出时按进入缓存时刻最早者驱逐。
func TestCapacityExactAndOverflow(t *testing.T) {
	k := mustKernel(t, Config{Capacity: 2})
	e1 := mustNavigate(t, k, "a", 1, false)
	e2 := mustNavigate(t, k, "b", 2, false)
	e3 := mustNavigate(t, k, "c", 3, false)

	// 恰好打满：D1、D2 在缓存中。
	if got := k.CachedDocIDs(); fmt.Sprint(got) != fmt.Sprint([]string{e1.DocID, e2.DocID}) {
		t.Fatalf("cache must be exactly full with [%s %s], got %v", e1.DocID, e2.DocID, got)
	}
	t.Logf("assert cache full: %v (capacity=2)", k.CachedDocIDs())

	// 超出：进入最早的 D1 被驱逐。
	e4 := mustNavigate(t, k, "d", 4, false)
	mustStatus(t, k, e1.DocID, statusUnloaded)
	mustStatus(t, k, e2.DocID, statusCached)
	mustStatus(t, k, e3.DocID, statusCached)
	mustStatus(t, k, e4.DocID, statusActive)
	t.Logf("assert overflow: evicted earliest %s, cache=%v", e1.DocID, k.CachedDocIDs())
	checkInv(t, k)
}

// 容量为 1 时遍历往返：恢复目标先于当前文档入缓存，不发生自我挤兑。
func TestCapacityOneTraverseRoundTrip(t *testing.T) {
	k := mustKernel(t, Config{Capacity: 1})
	e1 := mustNavigate(t, k, "a", 1, false)
	e2 := mustNavigate(t, k, "b", 2, false)

	r := k.TraverseSync(-1)
	if r.Err != nil || r.Action != ActionRestored {
		t.Fatalf("want restored, got action=%s err=%v", r.Action, r.Err)
	}
	mustStatus(t, k, e1.DocID, statusActive)
	mustStatus(t, k, e2.DocID, statusCached)

	r = k.TraverseSync(1)
	if r.Err != nil || r.Action != ActionRestored {
		t.Fatalf("want restored, got action=%s err=%v", r.Action, r.Err)
	}
	mustStatus(t, k, e1.DocID, statusCached)
	mustStatus(t, k, e2.DocID, statusActive)
	checkInv(t, k)
}

// 截断：被截断条目对应的缓存文档，若不再被引用则立即驱逐，若仍被引用则保留。
func TestTruncationReferencedVsUnreferenced(t *testing.T) {
	k := mustKernel(t, Config{Capacity: 8})
	e0 := mustNavigate(t, k, "a", 1, false)
	e1 := mustNavigate(t, k, "a#f", 2, true) // e0、e1 共享 D1
	e2 := mustNavigate(t, k, "b", 3, false)  // D2；D1 进入缓存
	if k.CachedDocIDs()[0] != e0.DocID {
		t.Fatalf("D1 must be cached")
	}

	// 回到 e0：D1 恢复为活动，D2 进入缓存。
	r := k.TraverseSync(-2)
	if r.Err != nil || r.Action != ActionRestored {
		t.Fatalf("want restored, got %s err=%v", r.Action, r.Err)
	}
	mustStatus(t, k, e2.DocID, statusCached)

	// 在 e0 处推入 c：截断 e1（D1 仍被 e0 引用）与 e2（D2 不再被任何条目引用）。
	mustNavigate(t, k, "c", 4, false)
	mustStatus(t, k, e1.DocID, statusCached)   // 仍被 e0 引用：保留（e0 离开后再次入缓存）
	mustStatus(t, k, e2.DocID, statusUnloaded) // 不再被引用：立即驱逐
	t.Logf("assert truncation: referenced doc=%s kept cached, unreferenced doc=%s evicted",
		e1.DocID, e2.DocID)
	checkInv(t, k)
}

// 替换导航改了状态对象后，恢复时必须看到改后的值。
func TestRestoreSeesReplacedState(t *testing.T) {
	k := mustKernel(t, Config{Capacity: 8})
	e0 := mustNavigate(t, k, "a", "v1", false)
	if _, err := k.Replace("a", "v2"); err != nil {
		t.Fatal(err)
	}
	t.Logf("op=replace url=a state=v2 -> entry seq=%d state updated", e0.Seq)
	mustNavigate(t, k, "a#f", "v3", true)
	mustNavigate(t, k, "b", "v4", false) // D1 进入缓存

	// 恢复到 e0：状态对象必须是替换后的 v2，而不是进入缓存时的旧值。
	r := k.TraverseSync(-2)
	if r.Err != nil || r.Action != ActionRestored {
		t.Fatalf("want restored, got %s err=%v", r.Action, r.Err)
	}
	if r.Entry.State != "v2" {
		t.Fatalf("restored entry must see replaced state v2, got %v", r.Entry.State)
	}
	t.Logf("assert restore doc=%s state=%v (replaced value)", r.Entry.DocID, r.Entry.State)

	// 继续：移动到同文档的 e1（同文档遍历），替换其状态，离开后再恢复，同样要看到新值。
	r = k.TraverseSync(1)
	if r.Err != nil || r.Action != ActionSameDocument {
		t.Fatalf("want same-document, got %s err=%v", r.Action, r.Err)
	}
	if _, err := k.Replace("a#f", "v3-new"); err != nil {
		t.Fatal(err)
	}
	mustNavigate(t, k, "c", "v5", false) // D1 再次进入缓存
	r = k.TraverseSync(-1)
	if r.Err != nil || r.Action != ActionRestored {
		t.Fatalf("want restored, got %s err=%v", r.Action, r.Err)
	}
	if r.Entry.State != "v3-new" {
		t.Fatalf("restored entry must see replaced state v3-new, got %v", r.Entry.State)
	}
	t.Logf("assert restore doc=%s state=%v (replaced while same-doc entry was current)", r.Entry.DocID, r.Entry.State)
	checkInv(t, k)
}

// 重新加载后，同一文档标识的所有历史条目一并更新为新标识。
func TestReloadUpdatesAllEntriesOfDocument(t *testing.T) {
	k := mustKernel(t, Config{Capacity: 8})
	e0 := mustNavigate(t, k, "a", 1, false)
	mustNavigate(t, k, "a#f", 2, true) // e0、e1 共享 D1
	// 让 D1 离开时不可缓存（持有独占资源），从而被卸载。
	if err := k.SetExclusiveResource(0, e0.DocID, true); err != nil {
		t.Fatal(err)
	}
	e2 := mustNavigate(t, k, "b", 3, false)
	mustStatus(t, k, e0.DocID, statusUnloaded)

	r := k.TraverseSync(-2)
	t.Logf("op=traverse delta=-2 -> action=%s newDoc=%s (old %s unloaded)", r.Action, r.Entry.DocID, e0.DocID)
	if r.Err != nil || r.Action != ActionReloaded {
		t.Fatalf("want reloaded, got %s err=%v", r.Action, r.Err)
	}
	newID := r.Entry.DocID
	if newID == e0.DocID {
		t.Fatalf("reload must allocate a new doc id")
	}
	entries := k.Entries()
	if entries[0].DocID != newID || entries[1].DocID != newID {
		t.Fatalf("all entries of the reloaded document must be updated: got %s, %s",
			entries[0].DocID, entries[1].DocID)
	}
	if entries[2].DocID != e2.DocID {
		t.Fatalf("unrelated entry doc id must stay %s, got %s", e2.DocID, entries[2].DocID)
	}
	t.Logf("assert entries[0..1] doc=%s, entries[2] doc=%s unchanged", newID, e2.DocID)
	checkInv(t, k)
}

// 连续到达的多次遍历请求只执行最后一次，之前的按被取代报告且不改状态。
func TestSupersededTraversals(t *testing.T) {
	k := mustKernel(t, Config{Capacity: 8})
	mustNavigate(t, k, "a", 1, false)
	mustNavigate(t, k, "b", 2, false)
	mustNavigate(t, k, "c", 3, false)
	mustNavigate(t, k, "d", 4, false) // 当前在 e3

	before := k.Entries()
	cachedBefore := k.CachedDocIDs()

	f1 := k.Traverse(-3)
	f2 := k.Traverse(-1)
	f3 := k.Traverse(-2)
	k.RunPending()

	r1, r2, r3 := f1.Result(), f2.Result(), f3.Result()
	mustKind(t, r1.Err, KindSuperseded, "traverse delta=-3")
	mustKind(t, r2.Err, KindSuperseded, "traverse delta=-1")
	if r3.Err != nil {
		t.Fatalf("last traversal must execute: %v", r3.Err)
	}
	if k.Position() != 1 {
		t.Fatalf("only the last traversal (delta=-2) may execute: pos=%d", k.Position())
	}
	t.Logf("op=traverse-batch [-3,-1,-2] -> superseded,superseded,executed pos=%d", k.Position())

	// 被取代的请求不得改变任何状态：等价于只做了一次 delta=-2。
	after := k.Entries()
	if len(after) != len(before) {
		t.Fatalf("entry list changed by superseded requests")
	}
	for i := range before {
		if after[i].Seq != before[i].Seq {
			t.Fatalf("entry %d changed by superseded requests", i)
		}
	}
	// 缓存变化恰好是一次 delta=-2 遍历的结果：
	// 目标 D2 被恢复出缓存，当前 D4 按资格进入缓存，D1、D3 不动。
	want := map[string]bool{
		before[0].DocID: true,
		before[2].DocID: true,
		before[3].DocID: true,
	}
	got := map[string]bool{}
	for _, id := range k.CachedDocIDs() {
		got[id] = true
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("cache must reflect exactly one traversal: want %v, got %v", want, got)
	}
	t.Logf("assert cache=%v equals single-traversal outcome (cachedBefore=%v)", k.CachedDocIDs(), cachedBefore)
	checkInv(t, k)
}

// 拒绝次序：参数非法先于时钟回退先于文档不存在先于状态不允许。
func TestRejectionOrdering(t *testing.T) {
	k := mustKernel(t, Config{Capacity: 4})
	e := mustNavigate(t, k, "a", 1, false)
	mustNavigate(t, k, "b", 2, false)
	if err := k.AdvanceClock(10); err != nil {
		t.Fatal(err)
	}

	// 空文档标识 + 过期时刻：参数非法优先。
	mustKind(t, k.SetNetworkPending(5, "", true), KindInvalidArgument, "empty docID + stale clock")
	// 过期时刻 + 不存在的文档：时钟回退优先。
	mustKind(t, k.SetNetworkPending(5, "D404", true), KindClockRollback, "stale clock + unknown doc")
	// 不存在的文档：文档不存在。
	mustKind(t, k.SetNetworkPending(10, "D404", true), KindDocumentNotFound, "unknown doc")
	// 已卸载文档做缓存期操作：状态不允许。
	mustStatus(t, k, e.DocID, statusCached)
	if err := k.SetExclusiveResource(10, e.DocID, true); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, k, e.DocID, statusUnloaded)
	mustKind(t, k.SetUnloadBlocker(10, e.DocID, true), KindInvalidState, "op on unloaded doc")
	// 时钟回退本身。
	mustKind(t, k.AdvanceClock(-1), KindClockRollback, "negative clock delta")
	// 负上限。
	if _, err := NewKernel(Config{Capacity: -1}); err == nil {
		t.Fatal("negative capacity must be rejected")
	} else {
		mustKind(t, err, KindInvalidArgument, "negative capacity")
	}
	// 空地址。
	if _, err := k.Navigate("", 1, false); err == nil {
		t.Fatal("empty url must be rejected")
	} else {
		mustKind(t, err, KindInvalidArgument, "empty url")
	}
	if _, err := k.Replace("", 1); err == nil {
		t.Fatal("empty url must be rejected")
	} else {
		mustKind(t, err, KindInvalidArgument, "empty url replace")
	}
	// 被拒绝的操作不改变时钟。
	if k.Now() != 10 {
		t.Fatalf("rejected ops must not change clock: now=%d", k.Now())
	}
	checkInv(t, k)
}

// 并发调用等价于某个串行顺序：不变量在任意交错后都成立。
func TestConcurrentSerialization(t *testing.T) {
	k := mustKernel(t, Config{Capacity: 3, TTL: 50})
	mustNavigate(t, k, "seed", 0, false)

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				switch (g + i) % 5 {
				case 0:
					_, _ = k.Navigate(fmt.Sprintf("u-%d-%d", g, i), i, i%3 == 0)
				case 1:
					k.TraverseSync((i % 5) - 2)
				case 2:
					_ = k.AdvanceClock(int64(i % 7))
				case 3:
					entries := k.Entries()
					if len(entries) > 0 {
						id := entries[i%len(entries)].DocID
						_ = k.SetNetworkPending(k.Now(), id, i%2 == 0)
					}
				case 4:
					_, _ = k.Replace(fmt.Sprintf("r-%d-%d", g, i), i)
				}
			}
		}(g)
	}
	wg.Wait()
	if err := k.CheckInvariants(); err != nil {
		t.Fatalf("invariants broken after concurrent run: %v", err)
	}
	t.Logf("concurrent run done: entries=%d pos=%d cached=%v now=%d",
		len(k.Entries()), k.Position(), k.CachedDocIDs(), k.Now())
}
