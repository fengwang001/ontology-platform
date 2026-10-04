package mailbox_test

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"

	"ontology/mailbox"
)

func mustNew(t *testing.T, k, p, lmax int) *mailbox.Mailbox {
	t.Helper()
	mb, err := mailbox.New(k, p, lmax)
	if err != nil {
		t.Fatalf("New(%d,%d,%d): %v", k, p, lmax, err)
	}
	return mb
}

func mustEnq(t *testing.T, mb *mailbox.Mailbox, id, ck string, prio int, ttl, now int64) mailbox.Result {
	t.Helper()
	res, err := mb.Enqueue(id, ck, prio, ttl, now)
	if err != nil {
		t.Fatalf("Enqueue(%q,%q,%d,%d,%d): %v", id, ck, prio, ttl, now, err)
	}
	return res
}

func mustDrain(t *testing.T, mb *mailbox.Mailbox, now int64, cnt int) []mailbox.Item {
	t.Helper()
	items, err := mb.Drain(now, cnt)
	if err != nil {
		t.Fatalf("Drain(%d,%d): %v", now, cnt, err)
	}
	return items
}

func mustPeek(t *testing.T, mb *mailbox.Mailbox, now int64) []mailbox.Item {
	t.Helper()
	items, err := mb.Peek(now)
	if err != nil {
		t.Fatalf("Peek(%d): %v", now, err)
	}
	return items
}

// itemKey renders an item for order/content comparison.
func itemKey(it mailbox.Item) string {
	if it.Marker {
		return fmt.Sprintf("M(n=%d)#%d", it.N, it.Seq)
	}
	return fmt.Sprintf("%s(ck=%q,p=%d)#%d", it.ID, it.CK, it.Prio, it.Seq)
}

func keys(items []mailbox.Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = itemKey(it)
	}
	return out
}

func assertOrder(t *testing.T, items []mailbox.Item, want ...string) {
	t.Helper()
	got := keys(items)
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

// 折叠替换使消息取新序号并排到后面。
func TestCollapseReplaceGetsNewSeq(t *testing.T) {
	mb := mustNew(t, 4, 4, 16)
	if r := mustEnq(t, mb, "a", "x", 0, 100, 0); r != mailbox.Stored {
		t.Fatalf("a: %v", r)
	}
	if r := mustEnq(t, mb, "b", "y", 0, 100, 1); r != mailbox.Stored {
		t.Fatalf("b: %v", r)
	}
	if r := mustEnq(t, mb, "c", "x", 0, 100, 2); r != mailbox.Collapsed {
		t.Fatalf("c: %v", r)
	}
	// a 被替换移除，c 取新序号 3 排在 b(序号2) 之后。
	assertOrder(t, mustDrain(t, mb, 3, 10), `b(ck="y",p=0)#2`, `c(ck="x",p=0)#3`)
}

// 种类数恰为 K 时同 ck 仍替换而新 ck 溢出。
func TestCollapseAtKindLimit(t *testing.T) {
	mb := mustNew(t, 2, 4, 16)
	mustEnq(t, mb, "a", "x", 0, 100, 0)
	mustEnq(t, mb, "b", "y", 0, 100, 1)
	// 同 ck 替换，不发生溢出。
	if r := mustEnq(t, mb, "c", "x", 0, 100, 2); r != mailbox.Collapsed {
		t.Fatalf("c: %v", r)
	}
	// 新 ck 触发溢出：丢弃 b、c 与新消息 d，c=3。
	if r := mustEnq(t, mb, "d", "z", 0, 100, 3); r != mailbox.Overflow {
		t.Fatalf("d: %v", r)
	}
	assertOrder(t, mustDrain(t, mb, 4, 10), "M(n=3)#4")
}

// 不可折叠恰为 P 时再入一条才溢出；溢出时新消息计入 c。
func TestPlainOverflowAtLimit(t *testing.T) {
	mb := mustNew(t, 4, 2, 16)
	mustEnq(t, mb, "e", "", 0, 100, 0)
	mustEnq(t, mb, "f", "", 0, 100, 1)
	// 恰为 P=2 时不溢出。
	assertOrder(t, mustPeek(t, mb, 2), `e(ck="",p=0)#1`, `f(ck="",p=0)#2`)
	// 再入一条溢出，c = P+1 = 3（含新消息 g）。
	if r := mustEnq(t, mb, "g", "", 0, 100, 2); r != mailbox.Overflow {
		t.Fatalf("g: %v", r)
	}
	assertOrder(t, mustDrain(t, mb, 3, 10), "M(n=3)#3")
}

// 溢出时新消息计入 c：箱内 1 条可折叠 + 新消息，c=2。
func TestOverflowCountsNewMessage(t *testing.T) {
	mb := mustNew(t, 1, 1, 16)
	mustEnq(t, mb, "a", "x", 0, 100, 0)
	if r := mustEnq(t, mb, "b", "y", 0, 100, 1); r != mailbox.Overflow {
		t.Fatalf("b: %v", r)
	}
	assertOrder(t, mustDrain(t, mb, 2, 10), "M(n=2)#2")

	mb2 := mustNew(t, 1, 1, 16)
	mustEnq(t, mb2, "p", "", 0, 100, 0)
	if r := mustEnq(t, mb2, "q", "", 0, 100, 1); r != mailbox.Overflow {
		t.Fatalf("q: %v", r)
	}
	assertOrder(t, mustDrain(t, mb2, 2, 10), "M(n=2)#2")
}

// 标记已存在时 n 累加且取新序号。
func TestMarkerAccumulates(t *testing.T) {
	mb := mustNew(t, 1, 1, 16)
	mustEnq(t, mb, "a", "x", 0, 100, 0) // seq1
	mustEnq(t, mb, "b", "y", 0, 100, 1) // 溢出，标记 n=2 seq2
	mustEnq(t, mb, "h", "", 1, 100, 2)  // 高优先级 seq3
	mustEnq(t, mb, "c", "z", 0, 100, 3) // seq4
	mustEnq(t, mb, "d", "w", 0, 100, 4) // 溢出，标记 n=4 取新序号 seq5
	mustEnq(t, mb, "e", "", 0, 100, 5)  // P=1，不可折叠溢出，丢弃 h、e，标记 n=6 seq6
	mustEnq(t, mb, "f", "", 0, 100, 6)  // seq7
	// 标记 n 三次累加（2→4→6）且每次取新序号。
	assertOrder(t, mustDrain(t, mb, 7, 10), "M(n=6)#6", `f(ck="",p=0)#7`)
}

// 两类溢出互不波及。
func TestOverflowKindsIndependent(t *testing.T) {
	mb := mustNew(t, 1, 1, 16)
	mustEnq(t, mb, "a", "x", 0, 100, 0) // seq1
	mustEnq(t, mb, "p", "", 0, 100, 1)  // seq2
	// 折叠溢出只丢弃可折叠消息 a 与新消息 b，p 保留。
	if r := mustEnq(t, mb, "b", "y", 0, 100, 2); r != mailbox.Overflow {
		t.Fatalf("b: %v", r)
	}
	// 不可折叠溢出只丢弃 p 与新消息 q。
	if r := mustEnq(t, mb, "q", "", 0, 100, 3); r != mailbox.Overflow {
		t.Fatalf("q: %v", r)
	}
	assertOrder(t, mustDrain(t, mb, 4, 10), "M(n=4)#4")
}

// 高优先级消息在溢出中同样丢弃。
func TestOverflowDropsHighPrio(t *testing.T) {
	mb := mustNew(t, 1, 1, 16)
	mustEnq(t, mb, "a", "x", 1, 100, 0) // 高优先级可折叠
	mustEnq(t, mb, "p", "", 1, 100, 1)  // 高优先级不可折叠
	if r := mustEnq(t, mb, "b", "y", 0, 100, 2); r != mailbox.Overflow {
		t.Fatalf("b: %v", r)
	}
	if r := mustEnq(t, mb, "q", "", 0, 100, 3); r != mailbox.Overflow {
		t.Fatalf("q: %v", r)
	}
	// a、p 均被各自类别溢出丢弃，c 各为 2。
	assertOrder(t, mustDrain(t, mb, 4, 10), "M(n=4)#4")
}

// exp 恰等于 now 即过期，且过期清除不计入标记。
func TestExpiryBoundary(t *testing.T) {
	mb := mustNew(t, 4, 4, 16)
	mustEnq(t, mb, "a", "", 0, 5, 0) // exp = 5
	// now = 5 时 a 已过期（exp 不大于 now）。
	assertOrder(t, mustPeek(t, mb, 5))
	items := mustDrain(t, mb, 5, 10)
	assertOrder(t, items)
	// 箱内无标记，过期清除静默。
	assertOrder(t, mustPeek(t, mb, 6))
}

// 过期清除先于容量判定使溢出不发生。
func TestPurgeBeforeOverflowCheck(t *testing.T) {
	mb := mustNew(t, 1, 4, 16)
	mustEnq(t, mb, "a", "x", 0, 5, 0) // exp = 5
	// now = 5 时 a 过期被清除，种类数归零，b 存入而非溢出。
	if r := mustEnq(t, mb, "b", "y", 0, 100, 5); r != mailbox.Stored {
		t.Fatalf("b: %v", r)
	}
	assertOrder(t, mustDrain(t, mb, 6, 10), `b(ck="y",p=0)#2`)
}

// ttl 为 0 的 Dropped：不存放、不取序号。
func TestTTLZeroDropped(t *testing.T) {
	mb := mustNew(t, 4, 4, 16)
	if r := mustEnq(t, mb, "a", "", 0, 0, 0); r != mailbox.Dropped {
		t.Fatalf("a: %v", r)
	}
	assertOrder(t, mustPeek(t, mb, 0))
	// 下一条消息取序号 1，说明 Dropped 未消耗序号。
	mustEnq(t, mb, "b", "", 0, 100, 1)
	assertOrder(t, mustDrain(t, mb, 2, 10), `b(ck="",p=0)#1`)
}

// 总数恰为 Lmax 时再入一条才淘汰。
func TestEvictOnlyBeyondLmax(t *testing.T) {
	mb := mustNew(t, 4, 4, 2)
	mustEnq(t, mb, "a", "", 0, 100, 0)
	mustEnq(t, mb, "b", "", 0, 100, 1)
	// 恰为 Lmax=2，无淘汰、无标记。
	assertOrder(t, mustPeek(t, mb, 2), `a(ck="",p=0)#1`, `b(ck="",p=0)#2`)
	// 第三条触发淘汰：普通类序号最小者 a，标记 n=1。
	if r := mustEnq(t, mb, "c", "", 0, 100, 2); r != mailbox.Stored {
		t.Fatalf("c: %v", r)
	}
	assertOrder(t, mustDrain(t, mb, 3, 10), "M(n=1)#4", `b(ck="",p=0)#2`, `c(ck="",p=0)#3`)
}

// 普通类先于高类被淘汰，同类取序号最小。
func TestEvictOrder(t *testing.T) {
	mb := mustNew(t, 4, 4, 2)
	mustEnq(t, mb, "p1", "", 1, 100, 0) // seq1 高
	mustEnq(t, mb, "q1", "", 0, 100, 1) // seq2 普通
	mustEnq(t, mb, "q2", "", 0, 100, 2) // seq3，淘汰普通类序号最小的 q1
	assertOrder(t, mustPeek(t, mb, 3), `p1(ck="",p=1)#1`, "M(n=1)#4", `q2(ck="",p=0)#3`)
	mustEnq(t, mb, "p2", "", 1, 100, 4) // seq5，普通类仅 q2，淘汰 q2
	// 箱内 p1、p2 皆高，再入一条只能淘汰高类中序号最小的 p1。
	mustEnq(t, mb, "p3", "", 1, 100, 5) // seq7，淘汰 p1，标记 n=3
	assertOrder(t, mustDrain(t, mb, 6, 10),
		`p2(ck="",p=1)#5`, `p3(ck="",p=1)#7`, "M(n=3)#8")
}

// 新消息自身被淘汰，结果 Evicted。
func TestEvictSelf(t *testing.T) {
	mb := mustNew(t, 4, 4, 1)
	mustEnq(t, mb, "h", "", 1, 100, 0) // seq1 高
	// 普通 q 存入后总数 2 > 1，普通类仅 q 自己，被淘汰。
	if r := mustEnq(t, mb, "q", "", 0, 100, 1); r != mailbox.Evicted {
		t.Fatalf("q: %v", r)
	}
	assertOrder(t, mustDrain(t, mb, 2, 10), `h(ck="",p=1)#1`, "M(n=1)#3")
}

// 淘汰使折叠键种类减少。
func TestEvictFreesCollapseKind(t *testing.T) {
	mb := mustNew(t, 2, 4, 1)
	mustEnq(t, mb, "a", "x", 0, 100, 0) // seq1
	mustEnq(t, mb, "b", "y", 0, 100, 1) // seq2，淘汰 a，种类数回到 1
	// 若种类数未减少，新 ck "z" 将溢出；实际应存入。
	if r := mustEnq(t, mb, "c", "z", 0, 100, 2); r != mailbox.Stored {
		t.Fatalf("c: %v", r)
	}
	// c 存入后总数 2 > 1，淘汰普通类序号最小的 b。
	assertOrder(t, mustDrain(t, mb, 3, 10), "M(n=2)#5", `c(ck="z",p=0)#4`)
}

// 过期清除先于 Lmax 判定。
func TestPurgeBeforeLmaxCheck(t *testing.T) {
	mb := mustNew(t, 4, 4, 1)
	mustEnq(t, mb, "a", "", 0, 5, 0) // exp = 5
	// now = 5 时 a 过期被清除，b 存入后总数为 1，不触发淘汰。
	if r := mustEnq(t, mb, "b", "", 0, 100, 5); r != mailbox.Stored {
		t.Fatalf("b: %v", r)
	}
	assertOrder(t, mustDrain(t, mb, 6, 10), `b(ck="",p=0)#2`)
}

// 标记被取走后可再创建，n 重新从本次丢弃数计起。
func TestMarkerRecreateAfterDrain(t *testing.T) {
	mb := mustNew(t, 1, 4, 16)
	mustEnq(t, mb, "a", "x", 0, 100, 0)
	mustEnq(t, mb, "b", "y", 0, 100, 1) // 溢出，标记 n=2 seq2
	assertOrder(t, mustDrain(t, mb, 2, 10), "M(n=2)#2")
	mustEnq(t, mb, "c", "z", 0, 100, 3) // seq3
	mustEnq(t, mb, "d", "w", 0, 100, 4) // 溢出，新建标记 n=2 seq4
	assertOrder(t, mustDrain(t, mb, 5, 10), "M(n=2)#4")
}

// 构造参数越界拒绝。
func TestNewInvalidParams(t *testing.T) {
	for _, args := range [][3]int{
		{0, 1, 1}, {65, 1, 1}, {-1, 1, 1},
		{1, 0, 1}, {1, 10001, 1},
		{1, 1, 0}, {1, 1, 10001},
	} {
		if _, err := mailbox.New(args[0], args[1], args[2]); !errors.Is(err, mailbox.ErrInvalidParam) {
			t.Fatalf("New%v: %v", args, err)
		}
	}
	if _, err := mailbox.New(1, 1, 1); err != nil {
		t.Fatalf("New(1,1,1): %v", err)
	}
	if _, err := mailbox.New(64, 10000, 10000); err != nil {
		t.Fatalf("New(64,10000,10000): %v", err)
	}
}

// 操作参数非法拒绝，且边界值合法。
func TestInvalidParams(t *testing.T) {
	mb := mustNew(t, 4, 4, 16)
	ck65 := strings.Repeat("c", 65)
	ck64 := strings.Repeat("c", 64)
	cases := []struct {
		id, ck   string
		prio     int
		ttl, now int64
	}{
		{"", "", 0, 1, 0},                  // id 为空
		{"a", ck65, 0, 1, 0},               // ck 超长
		{"a", "", 2, 1, 0},                 // prio 非法
		{"a", "", -1, 1, 0},                // prio 非法
		{"a", "", 0, -1, 0},                // ttl 越界
		{"a", "", 0, 1_000_000_001, 0},     // ttl 越界
		{"a", "", 0, 1, -1},                // now 越界
		{"a", "", 0, 1, 1_000_000_000_001}, // now 越界
	}
	for _, c := range cases {
		if _, err := mb.Enqueue(c.id, c.ck, c.prio, c.ttl, c.now); !errors.Is(err, mailbox.ErrInvalidParam) {
			t.Fatalf("Enqueue%+v: %v", c, err)
		}
	}
	// 边界值合法。
	if _, err := mb.Enqueue("a", ck64, 1, 1_000_000_000, 1_000_000_000_000); err != nil {
		t.Fatalf("boundary enqueue: %v", err)
	}
	// cnt 越界。
	for _, cnt := range []int{0, -1, 10001} {
		if _, err := mb.Drain(0, cnt); !errors.Is(err, mailbox.ErrInvalidParam) {
			t.Fatalf("Drain cnt=%d: %v", cnt, err)
		}
	}
}

// 时钟回退拒绝；Peek 不提升最大 now。
func TestClockRollback(t *testing.T) {
	mb := mustNew(t, 4, 4, 16)
	mustEnq(t, mb, "a", "", 0, 100, 10)
	if _, err := mb.Enqueue("b", "", 0, 100, 9); !errors.Is(err, mailbox.ErrClockRollback) {
		t.Fatalf("enqueue rollback: %v", err)
	}
	if _, err := mb.Drain(9, 1); !errors.Is(err, mailbox.ErrClockRollback) {
		t.Fatalf("drain rollback: %v", err)
	}
	if _, err := mb.Peek(9); !errors.Is(err, mailbox.ErrClockRollback) {
		t.Fatalf("peek rollback: %v", err)
	}
	// Peek 不推进最大 now：Peek(20) 后 now=15 的 Enqueue 仍被接受。
	mb2 := mustNew(t, 4, 4, 16)
	mustEnq(t, mb2, "a", "", 0, 100, 10)
	if _, err := mb2.Peek(20); err != nil {
		t.Fatalf("peek: %v", err)
	}
	if _, err := mb2.Enqueue("b", "", 0, 100, 15); err != nil {
		t.Fatalf("enqueue after peek: %v", err)
	}
}

// 重复检查只看存活消息。
func TestDuplicateOnlyLive(t *testing.T) {
	mb := mustNew(t, 4, 4, 16)
	mustEnq(t, mb, "a", "", 0, 5, 0) // exp = 5
	// now=3 时 a 存活，重复拒绝。
	if _, err := mb.Enqueue("a", "", 0, 100, 3); !errors.Is(err, mailbox.ErrDuplicateID) {
		t.Fatalf("dup live: %v", err)
	}
	// now=5 时 a 已过期（exp 恰等于 now），可再入。
	if r := mustEnq(t, mb, "a", "", 0, 100, 5); r != mailbox.Stored {
		t.Fatalf("re-enqueue after expiry: %v", r)
	}
	// 被折叠替换移除的旧 id 不再构成重复。
	mustEnq(t, mb, "x", "k", 0, 100, 6)
	mustEnq(t, mb, "y", "k", 0, 100, 7) // 替换 x
	if r := mustEnq(t, mb, "x", "", 0, 100, 8); r != mailbox.Stored {
		t.Fatalf("re-enqueue replaced id: %v", r)
	}
}

// 被拒操作不改变消息、标记、seq 与最大 now，也不执行清除。
func TestRejectedKeepsState(t *testing.T) {
	mb := mustNew(t, 4, 4, 16)
	mustEnq(t, mb, "a", "", 0, 100, 0) // seq1
	mustEnq(t, mb, "b", "", 0, 100, 1) // seq2
	before := keys(mustPeek(t, mb, 2))

	// 参数非法。
	if _, err := mb.Enqueue("", "", 0, 100, 2); !errors.Is(err, mailbox.ErrInvalidParam) {
		t.Fatalf("invalid: %v", err)
	}
	// 时钟回退。
	mustEnq(t, mb, "c", "", 0, 100, 10) // seq3，maxNow=10
	if _, err := mb.Enqueue("d", "", 0, 100, 5); !errors.Is(err, mailbox.ErrClockRollback) {
		t.Fatalf("rollback: %v", err)
	}
	// 重复（now=20 的重复请求不得推进 maxNow）。
	if _, err := mb.Enqueue("a", "", 0, 100, 20); !errors.Is(err, mailbox.ErrDuplicateID) {
		t.Fatalf("dup: %v", err)
	}
	// maxNow 未被被拒操作推进：now=15 仍可接受。
	if r := mustEnq(t, mb, "e", "", 0, 100, 15); r != mailbox.Stored {
		t.Fatalf("enqueue at 15: %v", r)
	}
	// 被拒操作未消耗序号：c#3、e#4。
	assertOrder(t, mustDrain(t, mb, 16, 10),
		`a(ck="",p=0)#1`, `b(ck="",p=0)#2`, `c(ck="",p=0)#3`, `e(ck="",p=0)#4`)
	_ = before
}

// 被拒的 Drain 不改变状态。
func TestRejectedDrainKeepsState(t *testing.T) {
	mb := mustNew(t, 4, 4, 16)
	mustEnq(t, mb, "a", "", 0, 5, 0) // exp = 5
	// cnt 非法被拒：不得执行清除，a 仍在箱内（未过期）。
	if _, err := mb.Drain(3, 0); !errors.Is(err, mailbox.ErrInvalidParam) {
		t.Fatalf("drain: %v", err)
	}
	if _, err := mb.Enqueue("a", "", 0, 100, 3); !errors.Is(err, mailbox.ErrDuplicateID) {
		t.Fatalf("a should still be in box: %v", err)
	}
}

// 题目示例一：K=2、P=3 完整走查。
func TestSpecExampleCollapseAndOverflow(t *testing.T) {
	mb := mustNew(t, 2, 3, 100)
	if r := mustEnq(t, mb, "a", "x", 0, 100, 0); r != mailbox.Stored {
		t.Fatalf("a: %v", r)
	}
	if r := mustEnq(t, mb, "b", "y", 0, 100, 1); r != mailbox.Stored {
		t.Fatalf("b: %v", r)
	}
	if r := mustEnq(t, mb, "c", "x", 0, 100, 2); r != mailbox.Collapsed {
		t.Fatalf("c: %v", r)
	}
	if r := mustEnq(t, mb, "d", "z", 0, 100, 3); r != mailbox.Overflow {
		t.Fatalf("d: %v", r)
	}
	mustEnq(t, mb, "e", "", 0, 100, 4)
	mustEnq(t, mb, "f", "", 0, 100, 5)
	mustEnq(t, mb, "g", "", 0, 100, 6)
	if r := mustEnq(t, mb, "h", "", 0, 100, 7); r != mailbox.Overflow {
		t.Fatalf("h: %v", r)
	}
	mustEnq(t, mb, "i", "", 1, 100, 8)
	mustEnq(t, mb, "j", "", 0, 100, 9)
	// Drain：高类为 i#9 与标记#8（序号交错，标记在前），普通类 j#10。
	assertOrder(t, mustDrain(t, mb, 10, 10),
		"M(n=7)#8", `i(ck="",p=1)#9`, `j(ck="",p=0)#10`)
}

// 题目示例二：Lmax=2 淘汰走查。
func TestSpecExampleEviction(t *testing.T) {
	mb := mustNew(t, 4, 4, 2)
	mustEnq(t, mb, "p1", "", 1, 100, 0) // seq1
	mustEnq(t, mb, "q1", "", 0, 100, 1) // seq2
	// p2 存入后总数 3 > 2，淘汰普通类序号最小的 q1。
	if r := mustEnq(t, mb, "p2", "", 1, 100, 2); r != mailbox.Stored {
		t.Fatalf("p2: %v", r)
	}
	// q2 存入后普通类仅 q2，自己被淘汰。
	if r := mustEnq(t, mb, "q2", "", 0, 100, 3); r != mailbox.Evicted {
		t.Fatalf("q2: %v", r)
	}
	// 箱内留 p1#1、p2#3 与标记 n=2#6，高类按序号升序。
	assertOrder(t, mustDrain(t, mb, 4, 10),
		`p1(ck="",p=1)#1`, `p2(ck="",p=1)#3`, "M(n=2)#6")
}

// 标记与高优先级按序号交错出队。
func TestDrainInterleavesMarkerAndHigh(t *testing.T) {
	mb := mustNew(t, 1, 4, 16)
	mustEnq(t, mb, "h1", "", 1, 100, 0) // seq1
	mustEnq(t, mb, "a", "x", 0, 100, 1) // seq2
	mustEnq(t, mb, "b", "y", 0, 100, 2) // 溢出，标记 n=2 seq3
	mustEnq(t, mb, "h2", "", 1, 100, 3) // seq4
	mustEnq(t, mb, "n1", "", 0, 100, 4) // seq5
	assertOrder(t, mustDrain(t, mb, 5, 10),
		`h1(ck="",p=1)#1`, "M(n=2)#3", `h2(ck="",p=1)#4`, `n1(ck="",p=0)#5`)
}

// Drain 的 cnt 截断：取走前 cnt 项，其余保留。
func TestDrainPartial(t *testing.T) {
	mb := mustNew(t, 4, 4, 16)
	mustEnq(t, mb, "h", "", 1, 100, 0)
	mustEnq(t, mb, "a", "", 0, 100, 1)
	mustEnq(t, mb, "b", "", 0, 100, 2)
	assertOrder(t, mustDrain(t, mb, 3, 2), `h(ck="",p=1)#1`, `a(ck="",p=0)#2`)
	assertOrder(t, mustDrain(t, mb, 4, 10), `b(ck="",p=0)#3`)
}

// Peek 只读且与随后的 Drain 顺序一致；Peek 不清除过期消息。
func TestPeekConsistentWithDrain(t *testing.T) {
	mb := mustNew(t, 1, 4, 16)
	mustEnq(t, mb, "h", "", 1, 100, 0)
	mustEnq(t, mb, "a", "x", 0, 100, 1)
	mustEnq(t, mb, "b", "y", 0, 100, 2) // 溢出，标记 n=2
	mustEnq(t, mb, "c", "", 0, 3, 3)    // exp = 6
	peek1 := keys(mustPeek(t, mb, 4))
	peek2 := keys(mustPeek(t, mb, 4))
	if len(peek1) == 0 || len(peek1) != len(peek2) {
		t.Fatalf("peek not repeatable: %v vs %v", peek1, peek2)
	}
	for i := range peek1 {
		if peek1[i] != peek2[i] {
			t.Fatalf("peek not repeatable: %v vs %v", peek1, peek2)
		}
	}
	// Peek 未清除任何消息：c 在 now=6 过期，Drain 时静默清除，顺序与存活项一致。
	drained := keys(mustDrain(t, mb, 6, 10))
	alive := make([]string, 0, len(peek1))
	for _, k := range peek1 {
		if k != `c(ck="",p=0)#4` {
			alive = append(alive, k)
		}
	}
	if len(drained) != len(alive) {
		t.Fatalf("drain %v, want %v", drained, alive)
	}
	for i := range alive {
		if drained[i] != alive[i] {
			t.Fatalf("drain %v, want %v", drained, alive)
		}
	}
}

// 并发调用安全（配合 -race 验证），结果等价于某个串行顺序。
func TestConcurrent(t *testing.T) {
	mb := mustNew(t, 4, 8, 16)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(g)))
			for i := 0; i < 200; i++ {
				switch rng.Intn(3) {
				case 0:
					ck := ""
					if rng.Intn(2) == 0 {
						ck = fmt.Sprintf("k%d", rng.Intn(6))
					}
					// now 恒为 0，避免并发下时钟回退拒绝。
					_, _ = mb.Enqueue(fmt.Sprintf("g%d-i%d", g, i), ck, rng.Intn(2), 1000, 0)
				case 1:
					_, _ = mb.Drain(0, 1+rng.Intn(4))
				default:
					_, _ = mb.Peek(0)
				}
			}
		}(g)
	}
	wg.Wait()
	// 收尾验证不变量：总量不超过 Lmax，同 ck 至多一条。
	items := mustPeek(t, mb, 0)
	msgs := 0
	seenCK := map[string]bool{}
	plain := 0
	for _, it := range items {
		if it.Marker {
			continue
		}
		msgs++
		if it.CK == "" {
			plain++
		} else if seenCK[it.CK] {
			t.Fatalf("duplicate ck %q", it.CK)
		} else {
			seenCK[it.CK] = true
		}
	}
	if msgs > 16 {
		t.Fatalf("total %d > Lmax", msgs)
	}
	if plain > 8 {
		t.Fatalf("plain %d > P", plain)
	}
	if len(seenCK) > 4 {
		t.Fatalf("ck kinds %d > K", len(seenCK))
	}
}

// 相同操作序列重放得到完全相同的结果与顺序。
func TestReplayDeterministic(t *testing.T) {
	run := func() ([]mailbox.Result, []string) {
		mb := mustNew(t, 2, 3, 4)
		rng := rand.New(rand.NewSource(42))
		var results []mailbox.Result
		var drained []string
		var clock int64
		for i := 0; i < 100; i++ {
			clock += int64(rng.Intn(3))
			if rng.Intn(4) == 0 {
				items, _ := mb.Drain(clock, 1+rng.Intn(3))
				drained = append(drained, keys(items)...)
				continue
			}
			ck := ""
			if rng.Intn(2) == 0 {
				ck = fmt.Sprintf("k%d", rng.Intn(3))
			}
			res, err := mb.Enqueue(fmt.Sprintf("id%d", rng.Intn(10)), ck, rng.Intn(2),
				int64(rng.Intn(20)), clock)
			if err != nil {
				res = -1
			}
			results = append(results, res)
		}
		items, _ := mb.Drain(clock, 10000)
		drained = append(drained, keys(items)...)
		return results, drained
	}
	r1, d1 := run()
	r2, d2 := run()
	if len(r1) != len(r2) || len(d1) != len(d2) {
		t.Fatalf("replay mismatch")
	}
	for i := range r1 {
		if r1[i] != r2[i] {
			t.Fatalf("replay mismatch at %d: %v vs %v", i, r1[i], r2[i])
		}
	}
	for i := range d1 {
		if d1[i] != d2[i] {
			t.Fatalf("replay drain mismatch at %d: %v vs %v", i, d1[i], d2[i])
		}
	}
}
