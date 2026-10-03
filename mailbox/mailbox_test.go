package mailbox

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

func mustEnqueue(t *testing.T, b *Mailbox, id, ck string, prio int, ttl, now int64, want Outcome) {
	t.Helper()
	got, err := b.Enqueue(id, ck, prio, ttl, now)
	if err != nil {
		t.Fatalf("Enqueue(%q) unexpected err = %v", id, err)
	}
	if got != want {
		t.Fatalf("Enqueue(%q) outcome = %v, want %v", id, got, want)
	}
}

func mustEnqueueErr(t *testing.T, b *Mailbox, id, ck string, prio int, ttl, now int64, want error) {
	t.Helper()
	if _, err := b.Enqueue(id, ck, prio, ttl, now); !errors.Is(err, want) {
		t.Fatalf("Enqueue(%q) err = %v, want %v", id, err, want)
	}
}

func mustStore(t *testing.T, b *Mailbox, id, ck string, prio int, ttl, now int64) {
	t.Helper()
	mustEnqueue(t, b, id, ck, prio, ttl, now, OutcomeStored)
}

func mustPeek(t *testing.T, b *Mailbox, now int64) []Item {
	t.Helper()
	items, err := b.Peek(now)
	if err != nil {
		t.Fatalf("Peek err = %v", err)
	}
	return items
}

func mustDrain(t *testing.T, b *Mailbox, now int64, cnt int) []Item {
	t.Helper()
	items, err := b.Drain(now, cnt)
	if err != nil {
		t.Fatalf("Drain err = %v", err)
	}
	return items
}

// describe 返回一项的紧凑描述，用于断言与日志。
func describe(it Item) string {
	if it.Marker {
		return fmt.Sprintf("marker(n=%d,seq=%d)", it.N, it.Seq)
	}
	return fmt.Sprintf("msg(%s,p%d,seq=%d,exp=%d)", it.ID, it.Prio, it.Seq, it.Exp)
}

func describeAll(items []Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = describe(it)
	}
	return out
}

func wantOrder(t *testing.T, items []Item, want ...string) {
	t.Helper()
	got := describeAll(items)
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func newBox(t *testing.T, k, p, lmax int) *Mailbox {
	t.Helper()
	b, err := NewMailbox(k, p, lmax)
	if err != nil {
		t.Fatalf("NewMailbox err = %v", err)
	}
	return b
}

// 折叠替换使消息取新序号并排到后面。
func TestCollapseReplaceNewSeq(t *testing.T) {
	b := newBox(t, 2, 3, 100)
	mustStore(t, b, "a", "x", 0, 100, 0)
	mustStore(t, b, "b", "y", 0, 100, 1)
	mustEnqueue(t, b, "c", "x", 0, 100, 2, OutcomeCollapsed)
	// a 被移除，c 取新序号 3，排在 b(2) 之后。
	wantOrder(t, mustPeek(t, b, 2),
		"msg(b,p0,seq=2,exp=101)", "msg(c,p0,seq=3,exp=102)")
}

// 种类数恰为 K 时：同 ck 仍替换，新 ck 才溢出；新消息计入 c。
func TestKindsAtKReplaceVsOverflow(t *testing.T) {
	b := newBox(t, 2, 3, 100)
	mustStore(t, b, "a", "x", 0, 100, 0)
	mustStore(t, b, "b", "y", 0, 100, 1)
	mustEnqueue(t, b, "c", "x", 0, 100, 2, OutcomeCollapsed) // 种类数不变
	mustEnqueue(t, b, "d", "z", 0, 100, 3, OutcomeOverflow)  // c = 2 + 1 = 3
	// b、c、d 被丢弃，标记 n=3 序号 4。
	wantOrder(t, mustPeek(t, b, 3), "marker(n=3,seq=4)")
}

// 不可折叠恰为 P 时再入一条才溢出，新消息计入 c。
func TestNonCollapsibleOverflowAtP(t *testing.T) {
	b := newBox(t, 2, 3, 100)
	mustStore(t, b, "e", "", 0, 100, 0)
	mustStore(t, b, "f", "", 0, 100, 1)
	mustStore(t, b, "g", "", 0, 100, 2)
	mustEnqueue(t, b, "h", "", 0, 100, 3, OutcomeOverflow) // c = 3 + 1 = 4
	wantOrder(t, mustPeek(t, b, 3), "marker(n=4,seq=4)")
}

// 两类溢出互不波及，高优先级消息同样在各自类别溢出中被丢弃。
func TestOverflowKindsIndependentAndHighPrioDropped(t *testing.T) {
	b := newBox(t, 1, 2, 100)
	mustStore(t, b, "n1", "", 0, 100, 0)  // 不可折叠普通
	mustStore(t, b, "h1", "x", 1, 100, 1) // 可折叠高优先级
	// 折叠溢出：h1 与新消息 h2 被丢弃，n1 不受影响。
	mustEnqueue(t, b, "h2", "y", 1, 100, 2, OutcomeOverflow) // c = 1 + 1 = 2
	wantOrder(t, mustPeek(t, b, 2),
		"marker(n=2,seq=3)", "msg(n1,p0,seq=1,exp=100)")
	// 不可折叠溢出：n1、n2 与新消息 n3 被丢弃，可折叠消息不受影响。
	mustStore(t, b, "c1", "x", 0, 100, 3)
	mustStore(t, b, "n2", "", 1, 100, 4)                    // 不可折叠高优先级
	mustEnqueue(t, b, "n3", "", 0, 100, 5, OutcomeOverflow) // c = 2 + 1 = 3
	// 标记累加：n = 2 + 3 = 5，取新序号。
	wantOrder(t, mustPeek(t, b, 5),
		"marker(n=5,seq=6)", "msg(c1,p0,seq=4,exp=103)")
}

// 标记与高优先级消息按序号交错出队。
func TestMarkerInterleavesWithHighPrio(t *testing.T) {
	b := newBox(t, 1, 10, 100)
	mustStore(t, b, "h1", "", 1, 100, 0) // seq 1
	mustStore(t, b, "x", "k", 0, 100, 1) // seq 2
	// 折叠溢出：x 与新消息 y 被丢弃，c=2，标记 seq 3。
	mustEnqueue(t, b, "y", "j", 0, 100, 2, OutcomeOverflow)
	mustStore(t, b, "h2", "", 1, 100, 3) // seq 4
	mustStore(t, b, "n", "", 0, 100, 4)  // seq 5
	// 高类 {h1(1), marker(3), h2(4)} 按序号升序，普通类 {n(5)} 在后。
	wantOrder(t, mustDrain(t, b, 4, 10),
		"msg(h1,p1,seq=1,exp=100)", "marker(n=2,seq=3)",
		"msg(h2,p1,seq=4,exp=103)", "msg(n,p0,seq=5,exp=104)")
}

// exp 恰等于 now 即过期；过期清除先于容量判定使溢出不发生，且不计入标记。
func TestExpiryExactNowAndPurgeBeforeOverflow(t *testing.T) {
	b := newBox(t, 1, 10, 100)
	mustStore(t, b, "a", "x", 0, 5, 0) // exp = 5
	// now = 5 时 a 已过期，被清除后种类数为 0，新 ck 不再溢出。
	mustStore(t, b, "c", "y", 0, 100, 5)
	wantOrder(t, mustPeek(t, b, 5), "msg(c,p0,seq=2,exp=105)")
}

// 过期清除先于 Lmax 判定，使淘汰不发生。
func TestPurgeBeforeLmax(t *testing.T) {
	b := newBox(t, 10, 10, 1)
	mustStore(t, b, "a", "", 0, 5, 0) // exp = 5
	// now = 5 时 a 过期被清除，b 存入后总数为 1，不触发淘汰。
	mustStore(t, b, "b", "", 0, 100, 5)
	wantOrder(t, mustPeek(t, b, 5), "msg(b,p0,seq=2,exp=105)")
}

// ttl 为 0 的 Dropped：不存放、不取序号、不改变箱内状态。
func TestTTLZeroDropped(t *testing.T) {
	b := newBox(t, 2, 2, 10)
	mustStore(t, b, "a", "", 0, 100, 0) // seq 1
	mustEnqueue(t, b, "z", "", 0, 0, 1, OutcomeDropped)
	mustStore(t, b, "c", "", 0, 100, 2) // seq 2：Dropped 不取序号
	wantOrder(t, mustPeek(t, b, 2),
		"msg(a,p0,seq=1,exp=100)", "msg(c,p0,seq=2,exp=102)")
}

// 规格示例一：K=2、P=3 的完整序列。
func TestSpecExampleCollapseAndOverflow(t *testing.T) {
	b := newBox(t, 2, 3, 100)
	mustStore(t, b, "a", "x", 0, 100, 0)                     // seq 1
	mustStore(t, b, "b", "y", 0, 100, 1)                     // seq 2
	mustEnqueue(t, b, "c", "x", 0, 100, 2, OutcomeCollapsed) // seq 3
	mustEnqueue(t, b, "d", "z", 0, 100, 3, OutcomeOverflow)  // c=3，标记 n=3 seq 4
	mustStore(t, b, "e", "", 0, 100, 4)                      // seq 5
	mustStore(t, b, "f", "", 0, 100, 5)                      // seq 6
	mustStore(t, b, "g", "", 0, 100, 6)                      // seq 7
	mustEnqueue(t, b, "h", "", 0, 100, 7, OutcomeOverflow)   // c=4，标记 n=7 seq 8
	mustStore(t, b, "i", "", 1, 100, 8)                      // seq 9
	mustStore(t, b, "j", "", 0, 100, 9)                      // seq 10
	wantOrder(t, mustDrain(t, b, 9, 10),
		"marker(n=7,seq=8)", "msg(i,p1,seq=9,exp=108)", "msg(j,p0,seq=10,exp=109)")
}

// 规格示例二：Lmax=2 的淘汰序列，含新消息自身被淘汰。
func TestSpecExampleLmaxEviction(t *testing.T) {
	b := newBox(t, 10, 10, 2)
	mustStore(t, b, "p1", "", 1, 100, 0) // seq 1
	mustStore(t, b, "q1", "", 0, 100, 1) // seq 2
	// 存入 p2 后总数为 3，淘汰普通类序号最小的 q1，标记 n=1 seq 4。
	mustStore(t, b, "p2", "", 1, 100, 2)
	// 普通类仅 q2，q2 自己被淘汰，标记 n=2 seq 6。
	mustEnqueue(t, b, "q2", "", 0, 100, 3, OutcomeEvicted)
	wantOrder(t, mustPeek(t, b, 3),
		"msg(p1,p1,seq=1,exp=100)", "msg(p2,p1,seq=3,exp=102)", "marker(n=2,seq=6)")
}

// 总数恰为 Lmax 时不淘汰，再入一条才淘汰；普通类先于高类，同类取序号最小。
func TestEvictionOrderNormalBeforeHigh(t *testing.T) {
	b := newBox(t, 10, 10, 3)
	mustStore(t, b, "h1", "", 1, 100, 0) // seq 1
	mustStore(t, b, "n1", "", 0, 100, 1) // seq 2
	mustStore(t, b, "n2", "", 0, 100, 2) // seq 3，总数恰为 Lmax，不淘汰
	mustStore(t, b, "h2", "", 1, 100, 3) // seq 4，淘汰普通类序号最小的 n1
	wantOrder(t, mustPeek(t, b, 3),
		"msg(h1,p1,seq=1,exp=100)", "msg(h2,p1,seq=4,exp=103)",
		"marker(n=1,seq=5)", "msg(n2,p0,seq=3,exp=102)")
	// 普通类仍有 n2，继续淘汰普通类。
	mustStore(t, b, "h3", "", 1, 100, 4) // seq 6，淘汰 n2，标记 n=2 seq 7
	// 普通类为空后，淘汰高优先级类中序号最小的 h1。
	mustStore(t, b, "h4", "", 1, 100, 5) // seq 8，淘汰 h1，标记 n=3 seq 9
	wantOrder(t, mustPeek(t, b, 5),
		"msg(h2,p1,seq=4,exp=103)", "msg(h3,p1,seq=6,exp=104)",
		"msg(h4,p1,seq=8,exp=105)", "marker(n=3,seq=9)")
}

// 淘汰使折叠键种类减少，从而后续新 ck 不再溢出。
func TestEvictionReducesKinds(t *testing.T) {
	b := newBox(t, 2, 10, 1)
	mustStore(t, b, "a", "x", 0, 100, 0) // seq 1
	// 存入 b 后总数 2 > 1，淘汰序号最小的 a，种类数降为 1。
	mustStore(t, b, "b", "y", 0, 100, 1)
	// 种类数 1 < K=2，新 ck z 存入而非溢出；随后淘汰 b。
	mustStore(t, b, "c", "z", 0, 100, 2)
	wantOrder(t, mustPeek(t, b, 2),
		"marker(n=2,seq=5)", "msg(c,p0,seq=4,exp=102)")
}

// 标记被取走后可再创建，n 重新累计。
func TestMarkerRecreateAfterDrain(t *testing.T) {
	b := newBox(t, 1, 10, 100)
	mustStore(t, b, "a", "x", 0, 100, 0)
	mustEnqueue(t, b, "b", "y", 0, 100, 1, OutcomeOverflow) // c=2，标记 n=2 seq 2
	wantOrder(t, mustDrain(t, b, 1, 10), "marker(n=2,seq=2)")
	mustStore(t, b, "c", "x", 0, 100, 2)                    // seq 3
	mustEnqueue(t, b, "d", "y", 0, 100, 3, OutcomeOverflow) // c=2，新标记 n=2 seq 4
	wantOrder(t, mustPeek(t, b, 3), "marker(n=2,seq=4)")
}

// 重复检查只看存活消息：存活同 id 拒绝，已过期（未清除）同 id 不拒绝。
func TestDuplicateOnlyLive(t *testing.T) {
	b := newBox(t, 4, 4, 10)
	mustStore(t, b, "a", "", 0, 5, 0) // exp = 5
	mustEnqueueErr(t, b, "a", "", 0, 5, 3, ErrDuplicateID)
	// now = 5 时旧 a 已过期，同 id 可再入箱。
	mustStore(t, b, "a", "", 0, 100, 5)
}

// 拒绝原因按序只报第一个：参数非法 > 时钟回退 > 重复。
func TestRejectionOrder(t *testing.T) {
	b := newBox(t, 4, 4, 10)
	mustStore(t, b, "a", "", 0, 100, 5)
	// 参数非法且时钟回退：报参数非法。
	mustEnqueueErr(t, b, "x", "", 2, 100, 4, ErrInvalidParam)
	// 时钟回退且 id 重复：报时钟回退。
	mustEnqueueErr(t, b, "a", "", 0, 100, 4, ErrClockRegression)
	// 仅 id 重复：报重复。
	mustEnqueueErr(t, b, "a", "", 0, 100, 5, ErrDuplicateID)
}

// 各类参数非法。
func TestInvalidParams(t *testing.T) {
	if _, err := NewMailbox(0, 1, 1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("NewMailbox k=0 err = %v", err)
	}
	if _, err := NewMailbox(65, 1, 1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("NewMailbox k=65 err = %v", err)
	}
	if _, err := NewMailbox(1, 0, 1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("NewMailbox p=0 err = %v", err)
	}
	if _, err := NewMailbox(1, 1, 0); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("NewMailbox lmax=0 err = %v", err)
	}
	b := newBox(t, 1, 1, 1)
	mustEnqueueErr(t, b, "", "", 0, 1, 0, ErrInvalidParam)                        // id 为空
	mustEnqueueErr(t, b, "a", string(make([]byte, 65)), 0, 1, 0, ErrInvalidParam) // ck 超长
	mustEnqueueErr(t, b, "a", "", 3, 1, 0, ErrInvalidParam)                       // prio 非法
	mustEnqueueErr(t, b, "a", "", 0, maxTTL+1, 0, ErrInvalidParam)                // ttl 越界
	mustEnqueueErr(t, b, "a", "", 0, 1, maxNow+1, ErrInvalidParam)                // now 越界
	if _, err := b.Drain(0, 0); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("Drain cnt=0 err = %v", err)
	}
	if _, err := b.Drain(0, maxCnt+1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("Drain cnt 越界 err = %v", err)
	}
}

// 被拒绝的操作不改变消息、标记、seq 与最大 now，也不执行清除。
func TestRejectedNoStateChange(t *testing.T) {
	b := newBox(t, 2, 2, 10)
	mustStore(t, b, "a", "", 0, 100, 5) // seq 1
	before := describeAll(mustPeek(t, b, 5))

	mustEnqueueErr(t, b, "", "", 0, 1, 6, ErrInvalidParam)
	mustEnqueueErr(t, b, "b", "", 0, 1, 4, ErrClockRegression)
	mustEnqueueErr(t, b, "a", "", 0, 1, 6, ErrDuplicateID)
	if _, err := b.Drain(4, 1); !errors.Is(err, ErrClockRegression) {
		t.Fatalf("Drain 回退 err = %v", err)
	}
	if _, err := b.Peek(4); !errors.Is(err, ErrClockRegression) {
		t.Fatalf("Peek 回退 err = %v", err)
	}

	after := describeAll(mustPeek(t, b, 6))
	if fmt.Sprint(before) != fmt.Sprint(after) {
		t.Fatalf("状态被改变：before %v, after %v", before, after)
	}
	// seq 未被消耗：下一条消息取序号 2。
	mustStore(t, b, "c", "", 0, 100, 6)
	wantOrder(t, mustPeek(t, b, 6),
		"msg(a,p0,seq=1,exp=105)", "msg(c,p0,seq=2,exp=106)")
	// 最大 now 未推进：now=5 仍被接受（不小于已接受最大值 6？否，6 已接受）。
	mustEnqueueErr(t, b, "d", "", 0, 1, 5, ErrClockRegression)
}

// Peek 返回的顺序与随后的 Drain 一致。
func TestPeekConsistentWithDrain(t *testing.T) {
	b := newBox(t, 1, 10, 100)
	mustStore(t, b, "h1", "", 1, 100, 0)
	mustStore(t, b, "x", "k", 0, 100, 1)
	mustEnqueue(t, b, "y", "j", 0, 100, 2, OutcomeOverflow)
	mustStore(t, b, "n1", "", 0, 100, 3)
	mustStore(t, b, "h2", "", 1, 100, 4)
	peeked := describeAll(mustPeek(t, b, 5))
	drained := describeAll(mustDrain(t, b, 5, 100))
	if fmt.Sprint(peeked) != fmt.Sprint(drained) {
		t.Fatalf("Peek %v != Drain %v", peeked, drained)
	}
	// Peek 不清除：Drain 后箱已空，再次 Drain 为空。
	if got := mustDrain(t, b, 5, 100); len(got) != 0 {
		t.Fatalf("Drain 后应为空，got %v", describeAll(got))
	}
}

// 并发调用等价于某个串行顺序：无数据竞争，且满足守恒
// stored == remaining + drained + collapsed + (markerN - overflows)。
func TestConcurrentConservation(t *testing.T) {
	b := newBox(t, 4, 8, 64)
	var stored, collapsed, overflows, drained, drainedMarkerN int64
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				id := fmt.Sprintf("g%d-%d", g, i)
				ck := fmt.Sprintf("k%d", i%6)
				if i%3 == 0 {
					ck = ""
				}
				out, err := b.Enqueue(id, ck, i%2, maxTTL, 0)
				if err != nil {
					t.Errorf("Enqueue err = %v", err)
					return
				}
				switch out {
				case OutcomeStored, OutcomeEvicted:
					atomic.AddInt64(&stored, 1)
				case OutcomeCollapsed:
					atomic.AddInt64(&stored, 1)
					atomic.AddInt64(&collapsed, 1)
				case OutcomeOverflow:
					atomic.AddInt64(&overflows, 1)
				}
				if i%7 == 0 {
					if _, err := b.Peek(0); err != nil {
						t.Errorf("Peek err = %v", err)
						return
					}
				}
				if i%11 == 0 {
					items, err := b.Drain(0, 3)
					if err != nil {
						t.Errorf("Drain err = %v", err)
						return
					}
					for _, it := range items {
						if it.Marker {
							atomic.AddInt64(&drainedMarkerN, int64(it.N))
						} else {
							atomic.AddInt64(&drained, 1)
						}
					}
				}
			}
		}(g)
	}
	wg.Wait()

	var remaining, markerN int64
	for _, it := range mustDrain(t, b, 0, maxCnt) {
		if it.Marker {
			markerN += int64(it.N)
		} else {
			remaining++
		}
	}
	markerN += atomic.LoadInt64(&drainedMarkerN)
	got := atomic.LoadInt64(&stored)
	want := remaining + atomic.LoadInt64(&drained) + atomic.LoadInt64(&collapsed) +
		(markerN - atomic.LoadInt64(&overflows))
	if got != want {
		t.Fatalf("守恒破坏：stored=%d remaining=%d drained=%d collapsed=%d markerN=%d overflows=%d",
			got, remaining, drained, collapsed, markerN, overflows)
	}
}
