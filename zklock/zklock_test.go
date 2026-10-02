package zklock

import (
	"errors"
	"sync"
	"testing"
)

func nseqs(ns []Node) []int {
	out := make([]int, len(ns))
	for i, n := range ns {
		out[i] = n.N
	}
	return out
}

func grantSeqs(gs []Grant) []int {
	out := make([]int, len(gs))
	for i, g := range gs {
		out[i] = g.N
	}
	return out
}

func mustCreate(t *testing.T, c *Coordinator, sid int, lock string, k Kind) int {
	t.Helper()
	n, err := c.Create(sid, lock, k)
	if err != nil {
		t.Fatalf("create(%d,%s,%s): %v", sid, lock, string(k), err)
	}
	return n
}

// TestSpecExample 复现题目给出的完整示例。
func TestSpecExample(t *testing.T) {
	c := New(1000)
	s1, s2, s3, s4, s5 := c.Open(), c.Open(), c.Open(), c.Open(), c.Open()
	if s1 != 1 || s5 != 5 {
		t.Fatalf("session ids = %d..%d, want 1..5", s1, s5)
	}
	n1 := mustCreate(t, c, s1, "L", Write)
	n2 := mustCreate(t, c, s2, "L", Read)
	n3 := mustCreate(t, c, s3, "L", Read)
	n4 := mustCreate(t, c, s4, "L", Write)
	n5 := mustCreate(t, c, s5, "L", Read)
	if [5]int{n1, n2, n3, n4, n5} != [5]int{0, 1, 2, 3, 4} {
		t.Fatalf("seqs = %d %d %d %d %d", n1, n2, n3, n4, n5)
	}
	if got := c.Counters(); got.Zxid != 5 || got.WatchSeq != 4 {
		t.Fatalf("after creates zxid=%d ws=%d", got.Zxid, got.WatchSeq)
	}
	if got := nseqs(c.Holders("L")); len(got) != 1 || got[0] != 0 {
		t.Fatalf("holders = %v, want [0]", got)
	}

	ev, err := c.Release(s4, "L", Write, 3)
	if err != nil || len(ev) != 0 {
		t.Fatalf("cancel s4: ev=%v err=%v", ev, err)
	}
	if got := c.Counters(); got.Zxid != 6 || got.WatchSeq != 5 || got.Reevaluations != 1 {
		t.Fatalf("after cancel counters=%+v", got)
	}

	ev, err = c.Release(s1, "L", Write, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := grantSeqs(ev); len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != 4 {
		t.Fatalf("grants = %v, want [1 2 4]", got)
	}
	for _, g := range ev {
		if g.TriggerZxid != 7 {
			t.Fatalf("grant trigger zxid = %d, want 7", g.TriggerZxid)
		}
	}

	s6 := c.Open()
	if n, err := c.Create(s6, "L", Read); err != nil || n != 5 {
		t.Fatalf("next seq = %d, err=%v, want 5", n, err)
	}
	if got := nseqs(c.Children("L")); len(got) != 4 || got[0] != 1 || got[1] != 2 || got[2] != 4 || got[3] != 5 {
		t.Fatalf("children = %v, want [1 2 4 5]", got)
	}
	if got := nseqs(c.Holders("L")); len(got) != 4 {
		t.Fatalf("holders = %v, want 4 readers", got)
	}
}

// TestSequenceNeverReused 多轮删空后序号仍单调、不复用。
func TestSequenceNeverReused(t *testing.T) {
	c := New(4)
	s := c.Open()
	for round := 0; round < 5; round++ {
		var ns []int
		for k := 0; k < 4; k++ {
			ns = append(ns, mustCreate(t, c, s, "L", Read))
		}
		if _, err := c.Create(s, "L", Read); !errors.Is(err, ErrFull) {
			t.Fatalf("round %d: want ErrFull, got %v", round, err)
		}
		for _, n := range ns {
			if _, err := c.Release(s, "L", Read, n); err != nil {
				t.Fatal(err)
			}
		}
	}
	if got := nseqs(c.Children("L")); len(got) != 0 {
		t.Fatalf("children = %v, want empty", got)
	}
	s2 := c.Open()
	if n, err := c.Create(s2, "L", Write); err != nil || n != 20 {
		t.Fatalf("seq after 20 creates = %d, want 20", n)
	}
}

// TestQueueFairness 等待中的写者之后的读者不能越过该写者。
func TestQueueFairness(t *testing.T) {
	c := New(100)
	sw, sr1, sw2, sr2 := c.Open(), c.Open(), c.Open(), c.Open()
	mustCreate(t, c, sw, "L", Write)  // 0 held
	mustCreate(t, c, sr1, "L", Read)  // 1 watches 0
	mustCreate(t, c, sw2, "L", Write) // 2 watches 1
	mustCreate(t, c, sr2, "L", Read)  // 3 watches 2

	ev, _ := c.Release(sw, "L", Write, 0)
	if got := grantSeqs(ev); len(got) != 1 || got[0] != 1 {
		t.Fatalf("first release grants = %v, want [1]", got)
	}
	ev, _ = c.Release(sr1, "L", Read, 1)
	if got := grantSeqs(ev); len(got) != 1 || got[0] != 2 {
		t.Fatalf("second release grants = %v, want [2]", got)
	}
	if got := nseqs(c.Holders("L")); len(got) != 1 || got[0] != 2 {
		t.Fatalf("holders = %v, want only writer [2]", got)
	}
	ev, _ = c.Release(sw2, "L", Write, 2)
	if got := grantSeqs(ev); len(got) != 1 || got[0] != 3 {
		t.Fatalf("third release grants = %v, want [3]", got)
	}
}

// TestReaderWatchesNearestPrecedingWriter 写者观察紧邻前驱；读者观察最近前置写者。
func TestReaderWatchesNearestPrecedingWriter(t *testing.T) {
	c := New(100)
	s1, s2, s3, s4, s5 := c.Open(), c.Open(), c.Open(), c.Open(), c.Open()
	mk := func(sid int, k Kind) { mustCreate(t, c, sid, "Q", k) }
	mk(s1, Write) // 0 held
	mk(s2, Read)  // 1 watches 0
	mk(s3, Read)  // 2 watches 0
	mk(s4, Write) // 3 watches 2（紧邻前驱 R2，而不是最小者 W0）
	mk(s5, Read)  // 4 watches 3（最近前置 W）

	c.ResetCounters()
	ev, _ := c.Release(s3, "Q", Read, 2)
	if len(ev) != 0 {
		t.Fatalf("grants = %v, want none", ev)
	}
	if got := c.Counters().Reevaluations; got != 1 {
		t.Fatalf("reevaluations after deleting R2 = %d, want 1", got)
	}
	ev, _ = c.Release(s2, "Q", Read, 1)
	if len(ev) != 0 {
		t.Fatalf("grants = %v, want none", ev)
	}
	ev, _ = c.Release(s1, "Q", Write, 0)
	if got := grantSeqs(ev); len(got) != 1 || got[0] != 3 {
		t.Fatalf("grants = %v, want [3]（R4 观察 W3，W3 仍在）", got)
	}
	ev, _ = c.Release(s4, "Q", Write, 3)
	if got := grantSeqs(ev); len(got) != 1 || got[0] != 4 {
		t.Fatalf("grants = %v, want [4]", got)
	}
}

// TestCancelPropagatesToEarlierWriter 取消等待写者后观察者改观察更早的写者。
func TestCancelPropagatesToEarlierWriter(t *testing.T) {
	c := New(100)
	s1, s2, s3, s4 := c.Open(), c.Open(), c.Open(), c.Open()
	mk := func(sid int, k Kind) { mustCreate(t, c, sid, "L", k) }
	mk(s1, Write) // 0 held
	mk(s2, Write) // 1 watches 0
	mk(s3, Read)  // 2 watches 1
	mk(s4, Read)  // 3 watches 1

	ev, _ := c.Release(s2, "L", Write, 1)
	if len(ev) != 0 {
		t.Fatalf("grants = %v, want none", ev)
	}
	if got := c.Counters().WatchSeq; got != 5 {
		t.Fatalf("ws = %d, want 5 (initial ws1..3, re-register ws4,ws5)", got)
	}
	ev, _ = c.Release(s1, "L", Write, 0)
	if got := grantSeqs(ev); len(got) != 2 || got[0] != 2 || got[1] != 3 {
		t.Fatalf("grants = %v, want [2 3]", got)
	}
	for _, g := range ev {
		if g.TriggerZxid != 6 {
			t.Fatalf("trigger = %d, want 6", g.TriggerZxid)
		}
	}
}

// TestReRegisteredWatchOrder 重新登记领取新 ws 且排在旧登记之后，按 ws 通知。
func TestReRegisteredWatchOrder(t *testing.T) {
	c := New(100)
	s := make([]int, 5)
	for i := range s {
		s[i] = c.Open()
	}
	mk := func(idx int, k Kind) { mustCreate(t, c, s[idx], "L", k) }
	// 题目示例形态：W0、R1(ws1->0)、R2(ws2->0)、W3(ws3->2)、R4(ws4->3)。
	mk(0, Write)
	mk(1, Read)
	mk(2, Read)
	mk(3, Write)
	mk(4, Read)

	// 取消 W3：R4 改观察 W0 并重新登记 ws5，排在 ws1、ws2 之后。
	if _, err := c.Release(s[3], "L", Write, 3); err != nil {
		t.Fatal(err)
	}
	if got := c.Counters().WatchSeq; got != 5 {
		t.Fatalf("ws = %d, want 5", got)
	}
	ev, _ := c.Release(s[0], "L", Write, 0)
	want := []int{1, 2, 4} // ws1, ws2, ws5
	if got := grantSeqs(ev); len(got) != len(want) {
		t.Fatalf("grants = %v, want %v", got, want)
	} else {
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("grant order = %v, want %v", got, want)
			}
		}
	}
}

// TestMultipleReadersGrantedTogether 多个读者在同一次通知中同时授予。
func TestMultipleReadersGrantedTogether(t *testing.T) {
	c := New(100)
	sw := c.Open()
	const k = 8
	readers := make([]int, k)
	for i := range readers {
		readers[i] = c.Open()
	}
	mustCreate(t, c, sw, "L", Write)
	for i, sid := range readers {
		if n := mustCreate(t, c, sid, "L", Read); n != i+1 {
			t.Fatalf("reader %d seq = %d", i, n)
		}
		if len(c.Holders("L")) != 1 {
			t.Fatalf("reader %d must wait while writer holds", i)
		}
	}
	ev, _ := c.Release(sw, "L", Write, 0)
	if len(ev) != k {
		t.Fatalf("grants = %d, want %d in one notification", len(ev), k)
	}
	for i := 1; i < k; i++ {
		if ev[i].TriggerZxid != ev[0].TriggerZxid || ev[i].N != i+1 {
			t.Fatalf("grant[%d] = %+v, want seq %d same zxid", i, ev[i], i+1)
		}
	}
}

// TestExpireOrderAndNoSelfGrant 过期：先撤销观察，再按创建 zxid 升序删除，
// 过期会话自己的等待者一律不授予。
func TestExpireOrderAndNoSelfGrant(t *testing.T) {
	c := New(100)
	other := c.Open()
	s := c.Open()
	// other 的 W0 持有；s 依次建 W1、R2、W3（均等待），创建 zxid 2,3,4。
	mustCreate(t, c, other, "L", Write)
	mustCreate(t, c, s, "L", Write)
	mustCreate(t, c, s, "L", Read)
	mustCreate(t, c, s, "L", Write)

	ev, err := c.Expire(s)
	if err != nil || len(ev) != 0 {
		t.Fatalf("expire events = %v, err = %v; want no grants of expired session", ev, err)
	}
	// 删除三个节点各领一个 zxid（5,6,7），但没有等待者留下。
	if got := c.Counters().Zxid; got != 7 {
		t.Fatalf("zxid = %d, want 7", got)
	}
	// 只剩 other 的 W0。
	if got := nseqs(c.Children("L")); len(got) != 1 || got[0] != 0 {
		t.Fatalf("children = %v, want [0]", got)
	}
	if got := nseqs(c.Holders("L")); len(got) != 1 || got[0] != 0 {
		t.Fatalf("holders = %v, want [0]", got)
	}

	// 过期不幂等；过期会话 Create/Release 被拒。
	if _, err := c.Expire(s); !errors.Is(err, ErrExpired) {
		t.Fatalf("double expire: want ErrExpired, got %v", err)
	}
	if _, err := c.Create(s, "L", Read); !errors.Is(err, ErrExpired) {
		t.Fatalf("create after expire: got %v", err)
	}
	if _, err := c.Release(other, "L", Write, 0); err != nil {
		t.Fatal(err)
	}
	// other 的锁释放不应“补发”任何已过期节点的授予。
	if _, err := c.Expire(other); err != nil {
		t.Fatal(err)
	}
}

// TestExpireDeletionOrderGrantsOther 过期会话按创建 zxid 升序删除时，
// 其他存活会话的等待者能在中间步骤被授予。
func TestExpireDeletionOrderGrantsOther(t *testing.T) {
	c := New(100)
	s := c.Open() // 持有队首写者
	dead := c.Open()
	r1 := c.Open()
	r2 := c.Open()
	mustCreate(t, c, s, "L", Write)    // 0 held by s
	mustCreate(t, c, dead, "L", Write) // 1 waits, watched by r1
	mustCreate(t, c, r1, "L", Read)    // 2 watches 1
	mustCreate(t, c, dead, "L", Write) // 3 watches 2, watched by r2? r2 below
	mustCreate(t, c, r2, "L", Read)    // 4 watches 3

	// 先释放 W0：dead 的 W1 被授予。
	if ev, _ := c.Release(s, "L", Write, 0); len(ev) != 1 || ev[0].N != 1 {
		t.Fatalf("release W0 grants = %v, want [1]", ev)
	}
	// 此时 dead 持有 W1，并仍有等待的 W3；过期 dead：
	// 先撤销 W1/W3 上 dead 自己没有观察；删除顺序按创建 zxid：W1(zxid2) 先，
	// R2 观察的是 W1 -> 重新评估：前置 W 只有（已不存在的 0 与将删的）；授予 R2(seq2)。
	// 再删 W3(zxid4)：R4 观察 W3，前面已无 W，授予 seq4。
	ev, err := c.Expire(dead)
	if err != nil {
		t.Fatal(err)
	}
	if got := grantSeqs(ev); len(got) != 2 || got[0] != 2 || got[1] != 4 {
		t.Fatalf("expire grants = %v, want [2 4] in create-zxid order", got)
	}
	if ev[0].TriggerZxid >= ev[1].TriggerZxid {
		t.Fatalf("trigger zxids not increasing: %d >= %d", ev[0].TriggerZxid, ev[1].TriggerZxid)
	}
}

// TestRejectionsDoNotChangeState 各类拒绝都不得消耗序号、zxid、ws 或改变观察。
func TestRejectionsDoNotChangeState(t *testing.T) {
	c := New(2)
	s := c.Open()
	mustCreate(t, c, s, "L", Write)
	mustCreate(t, c, s, "L2", Read)

	before := c.Counters()
	check := func(name string, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("%s: want error", name)
		}
		if got := c.Counters(); got != before {
			t.Fatalf("%s changed counters: before=%+v after=%+v", name, before, got)
		}
	}
	// Create 拒绝顺序：非法参数、会话不存在、会话过期、已满。
	if _, err := c.Create(s, "", Read); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty lock: %v", err)
	}
	if _, err := c.Create(s, "L", Kind('X')); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad kind: %v", err)
	}
	if _, err := c.Create(9999, "L", Read); !errors.Is(err, ErrNoSession) {
		t.Fatalf("no session: %v", err)
	}
	dead := c.Open()
	if _, err := c.Expire(dead); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Create(dead, "L", Read); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired create: %v", err)
	}
	// 已满：L 现存 1，再造 1 到上限，第 3 个拒绝且不消耗序号/zxid。
	s2 := c.Open()
	mustCreate(t, c, s2, "L", Read)
	fullBefore := c.Counters()
	if _, err := c.Create(s2, "L", Read); !errors.Is(err, ErrFull) {
		t.Fatalf("full: %v", err)
	}
	if got := c.Counters(); got != fullBefore {
		t.Fatalf("full create changed counters: %+v vs %+v", got, fullBefore)
	}

	// Release 拒绝顺序：非法参数、会话不存在、过期、节点不存在、非所有者。
	check("release bad lock", errRelease(c, s, "", Write, 0))
	check("release bad kind", errRelease(c, s, "L", Kind('X'), 0))
	check("release neg n", errRelease(c, s, "L", Write, -1))
	if _, err := c.Release(9999, "L", Write, 0); !errors.Is(err, ErrNoSession) {
		t.Fatalf("release no session: %v", err)
	}
	if _, err := c.Release(dead, "L", Write, 0); !errors.Is(err, ErrExpired) {
		t.Fatalf("release expired: %v", err)
	}
	if _, err := c.Release(s, "L", Read, 0); !errors.Is(err, ErrNoNode) {
		t.Fatalf("kind mismatch want ErrNoNode, got %v", err)
	}
	if _, err := c.Release(s, "L", Write, 999); !errors.Is(err, ErrNoNode) {
		t.Fatalf("missing seq want ErrNoNode, got %v", err)
	}
	if _, err := c.Release(s, "nolock", Write, 0); !errors.Is(err, ErrNoNode) {
		t.Fatalf("missing lock want ErrNoNode, got %v", err)
	}
	if _, err := c.Release(s2, "L", Write, 0); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("not owner want ErrNotOwner, got %v", err)
	}
	// Expire 拒绝。
	if _, err := c.Expire(9999); !errors.Is(err, ErrNoSession) {
		t.Fatalf("expire no session: %v", err)
	}
	if _, err := c.Expire(dead); !errors.Is(err, ErrExpired) {
		t.Fatalf("expire non-idempotent: %v", err)
	}
	// 状态未受拒绝影响：L 仍是 W0+R1。
	if got := nseqs(c.Children("L")); len(got) != 2 || got[0] != 0 || got[1] != 1 {
		t.Fatalf("children after rejections = %v, want [0 1]", got)
	}
}

func errRelease(c *Coordinator, sid int, lock string, k Kind, n int) error {
	_, err := c.Release(sid, lock, k, n)
	return err
}

// TestHolderInvariant 任意时刻同锁要么全读者，要么恰一个写者；
// 观察总数等于等待者总数，每个等待者指向现存节点。
func TestHolderInvariant(t *testing.T) {
	c := New(1000)
	var wg sync.WaitGroup
	sessions := make([]int, 12)
	for i := range sessions {
		sessions[i] = c.Open()
	}
	// 混合创建/释放，逐步检查不变量。
	kinds := []Kind{Write, Read, Read, Write, Read, Write, Read, Read, Write, Read, Read, Write}
	for i, sid := range sessions {
		mustCreate(t, c, sid, "L", kinds[i])
		checkInvariants(t, c, "L")
	}
	order := []int{0, 3, 1, 5, 2, 8, 4, 11, 6, 7, 9, 10}
	for _, i := range order {
		if _, err := c.Release(sessions[i], "L", kinds[i], i); err != nil {
			t.Fatalf("release %d: %v", i, err)
		}
		checkInvariants(t, c, "L")
	}
	wg.Wait()
}

func checkInvariants(t *testing.T, c *Coordinator, lock string) {
	t.Helper()
	ci := c.inner
	ci.mu.Lock()
	defer ci.mu.Unlock()
	lk := ci.locks[lock]
	if lk == nil {
		return
	}
	var heldW, heldR, waiters int
	for _, ch := range avlInorder(lk.all, nil) {
		if ch.held {
			if ch.node.Kind == Write {
				heldW++
			} else {
				heldR++
			}
		} else {
			waiters++
			if ch.watchEntry == nil {
				t.Fatalf("waiter seq %d has no watch", ch.node.N)
			}
			if avlGet(lk.all, ch.watchEntry.target) == nil {
				t.Fatalf("waiter seq %d watches missing node %d", ch.node.N, ch.watchEntry.target)
			}
			if ch.watchEntry.target >= ch.node.N {
				t.Fatalf("waiter seq %d watches non-preceding %d", ch.node.N, ch.watchEntry.target)
			}
		}
	}
	if heldW > 1 || (heldW == 1 && heldR > 0) {
		t.Fatalf("holders: %d writers %d readers", heldW, heldR)
	}
	totalWatches := 0
	for _, s := range lk.watches {
		for w := s.next; w != s; w = w.next {
			totalWatches++
		}
	}
	if totalWatches != waiters {
		t.Fatalf("watches = %d, waiters = %d", totalWatches, waiters)
	}
}
