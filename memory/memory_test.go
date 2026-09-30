package memory

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
)

func mustNode(t *testing.T, allocatable, oversell int64) *Node {
	t.Helper()
	n, err := NewNode(allocatable, oversell)
	if err != nil {
		t.Fatalf("NewNode(%d, %d) 失败: %v", allocatable, oversell, err)
	}
	return n
}

func mustAdmit(t *testing.T, n *Node, id string, request, limit int64) {
	t.Helper()
	t.Logf("输入: Admit(id=%q, r=%d, l=%d)", id, request, limit)
	if err := n.Admit(id, request, limit); err != nil {
		t.Fatalf("Admit(%q) 被拒: %v", id, err)
	}
	s := n.Snapshot()
	t.Logf("输出: 准入成功; 账目 sumRequest=%d sumLimit=%d (A=%d, O*A=%d)",
		s.SumRequest, s.SumLimit, s.Allocatable, s.Oversell*s.Allocatable)
}

func mustReport(t *testing.T, n *Node, id string, usage int64) []Eviction {
	t.Helper()
	t.Logf("输入: Report(id=%q, u=%d)", id, usage)
	evicted, err := n.Report(id, usage)
	if err != nil {
		t.Fatalf("Report(%q) 被拒: %v", id, err)
	}
	for _, e := range evicted {
		t.Logf("输出: 驱逐 %s, 次序依据: %s", e.ID, e.Reason)
	}
	if len(evicted) == 0 {
		t.Logf("输出: 无驱逐")
	}
	return evicted
}

func evictionIDs(evicted []Eviction) []string {
	ids := make([]string, 0, len(evicted))
	for _, e := range evicted {
		ids = append(ids, e.ID)
	}
	return ids
}

// 恰好装满的准入边界：sumRequest + r == A 准入，再超 1 即拒（请求不足）。
func TestAdmitExactFitBoundary(t *testing.T) {
	n := mustNode(t, 100, 2)
	mustAdmit(t, n, "c1", 60, 60)
	mustAdmit(t, n, "c2", 40, 40) // sumRequest 恰好 == A

	before := n.Snapshot()
	t.Logf("输入: Admit(id=%q, r=%d, l=%d)", "c3", 1, 1)
	err := n.Admit("c3", 1, 1)
	t.Logf("输出: err=%v; 判定依据: sumRequest 100 + 1 > A 100 -> 请求不足", err)
	if !errors.Is(err, ErrInsufficientRequest) {
		t.Fatalf("期望 ErrInsufficientRequest, 得到 %v", err)
	}
	if after := n.Snapshot(); after != before {
		t.Fatalf("被拒操作改变了账目: %+v -> %+v", before, after)
	}
}

// 上限超卖边界：sumLimit + l == O*A 恰好准入，再超即拒（超卖超限）。
func TestAdmitOversellBoundary(t *testing.T) {
	n := mustNode(t, 100, 2) // 上限预算 O*A = 200
	mustAdmit(t, n, "c1", 10, 150)
	mustAdmit(t, n, "c2", 10, 50) // sumLimit 恰好 == 200

	before := n.Snapshot()
	t.Logf("输入: Admit(id=%q, r=%d, l=%d) —— 请求有余量, 仅上限超限", "c3", 0, 1)
	err := n.Admit("c3", 0, 1)
	t.Logf("输出: err=%v; 判定依据: sumLimit 200 + 1 > O*A 200 -> 上限超卖超限", err)
	if !errors.Is(err, ErrOversellLimitExceeded) {
		t.Fatalf("期望 ErrOversellLimitExceeded, 得到 %v", err)
	}
	if after := n.Snapshot(); after != before {
		t.Fatalf("被拒操作改变了账目: %+v -> %+v", before, after)
	}
}

// 两类准入条件同时违反时，先报请求不足。
func TestAdmitBothViolatedRequestFirst(t *testing.T) {
	n := mustNode(t, 100, 3) // 上限预算 O*A = 300
	mustAdmit(t, n, "c1", 50, 200)
	mustAdmit(t, n, "c2", 50, 100) // sumRequest=100==A, sumLimit=300==O*A

	t.Logf("输入: Admit(id=%q, r=%d, l=%d) —— 请求与上限同时违反", "c3", 1, 1)
	err := n.Admit("c3", 1, 1)
	t.Logf("输出: err=%v; 判定依据: 同时成立时先报请求不足", err)
	if !errors.Is(err, ErrInsufficientRequest) {
		t.Fatalf("期望先报 ErrInsufficientRequest, 得到 %v", err)
	}
}

// u 恰等于 r 不算超出：驱逐时 u==r 者排在 u>r 者之后，本轮不被驱逐。
func TestUsageEqualRequestNotOver(t *testing.T) {
	n := mustNode(t, 100, 3)
	mustAdmit(t, n, "c1", 50, 100)
	mustAdmit(t, n, "c2", 50, 100)

	mustReport(t, n, "c1", 60)            // u > r, 超出
	evicted := mustReport(t, n, "c2", 50) // u == r, 不算超出; sumUsage=110 > A=100
	t.Logf("判定依据: c1 u=60>r=50 为第一键超出组, c2 u=50==r=50 不超出; 驱逐 c1 后 sumUsage=50 <= 100 即停")
	if got := evictionIDs(evicted); !reflect.DeepEqual(got, []string{"c1"}) {
		t.Fatalf("期望驱逐 [c1], 得到 %v", got)
	}
	if s := n.Snapshot(); s.Containers != 1 || s.SumUsage != 50 {
		t.Fatalf("期望 c2 留存且 sumUsage=50, 得到 %+v", s)
	}
}

// 保证型最后被驱逐：压力足够大时尽力型、突发型依次先走，保证型留存。
func TestGuaranteedEvictedLast(t *testing.T) {
	n := mustNode(t, 100, 2)
	mustAdmit(t, n, "be", 0, 80)  // 尽力型
	mustAdmit(t, n, "bu", 20, 60) // 突发型
	mustAdmit(t, n, "gu", 50, 50) // 保证型

	mustReport(t, n, "gu", 50)            // sumUsage=50
	mustReport(t, n, "be", 50)            // sumUsage=100, 恰好达标不驱逐
	evicted := mustReport(t, n, "bu", 60) // sumUsage=160 > 100
	t.Logf("判定依据: be/bu 均 u>r, 第二键尽力型先于突发型; 驱逐 be 后 110>100, 再驱逐 bu 后 50<=100 即停; 保证型 gu 最后, 本轮留存")
	if got := evictionIDs(evicted); !reflect.DeepEqual(got, []string{"be", "bu"}) {
		t.Fatalf("期望驱逐 [be bu], 得到 %v", got)
	}
	if s := n.Snapshot(); s.Containers != 1 || s.SumRequest != 50 {
		t.Fatalf("期望仅保证型留存, 得到 %+v", s)
	}
}

// 并列按标识升序：四个键全部相同时标识小者先被驱逐。
func TestTieBreakByID(t *testing.T) {
	n := mustNode(t, 100, 2)
	mustAdmit(t, n, "b", 0, 60)
	mustAdmit(t, n, "a", 0, 60)

	mustReport(t, n, "b", 60)
	evicted := mustReport(t, n, "a", 60) // sumUsage=120 > 100
	t.Logf("判定依据: a/b 同为 u>r、尽力型、u-r=60, 第四键标识升序 -> a 先; 驱逐 a 后 60<=100 即停")
	if got := evictionIDs(evicted); !reflect.DeepEqual(got, []string{"a"}) {
		t.Fatalf("期望驱逐 [a], 得到 %v", got)
	}
}

// 恰驱逐到达标即停：不多驱逐。
func TestStopAsSoonAsMet(t *testing.T) {
	n := mustNode(t, 100, 3)
	mustAdmit(t, n, "x1", 0, 50)
	mustAdmit(t, n, "x2", 0, 50)
	mustAdmit(t, n, "x3", 0, 50)

	mustReport(t, n, "x1", 40)
	mustReport(t, n, "x2", 40)
	evicted := mustReport(t, n, "x3", 40) // sumUsage=120 > 100
	t.Logf("判定依据: 三者完全并列按标识升序; 驱逐 x1 后 sumUsage=80<=100 达标即停, x2/x3 不多驱逐")
	if got := evictionIDs(evicted); !reflect.DeepEqual(got, []string{"x1"}) {
		t.Fatalf("期望驱逐 [x1], 得到 %v", got)
	}
	if s := n.Snapshot(); s.SumUsage != 80 || s.Containers != 2 {
		t.Fatalf("期望 sumUsage=80 且留存 2 个, 得到 %+v", s)
	}
}

// 驱逐后被驱逐者的请求与上限即刻退出账目，腾出准入空间。
func TestEvictionFreesAdmission(t *testing.T) {
	n := mustNode(t, 100, 2)
	mustAdmit(t, n, "b1", 60, 100)
	mustAdmit(t, n, "b2", 40, 100) // sumRequest=100==A, sumLimit=200==O*A

	t.Logf("输入: Admit(id=%q, r=%d, l=%d) —— 驱逐前无空间", "c3", 1, 1)
	if err := n.Admit("c3", 1, 1); !errors.Is(err, ErrInsufficientRequest) {
		t.Fatalf("驱逐前期望 ErrInsufficientRequest, 得到 %v", err)
	}
	t.Logf("输出: 按预期拒绝（请求不足）")

	mustReport(t, n, "b2", 40)
	evicted := mustReport(t, n, "b1", 90) // sumUsage=130 > 100, 驱逐 b1 后 40<=100
	if got := evictionIDs(evicted); !reflect.DeepEqual(got, []string{"b1"}) {
		t.Fatalf("期望驱逐 [b1], 得到 %v", got)
	}
	s := n.Snapshot()
	t.Logf("驱逐后账目: sumRequest=%d sumLimit=%d", s.SumRequest, s.SumLimit)
	mustAdmit(t, n, "c3", 50, 60) // 40+50<=100, 100+60<=200
}

// 各类非法输入整体拒绝，且被拒操作不改变任何账目。
func TestRejections(t *testing.T) {
	if _, err := NewNode(0, 1); !errors.Is(err, ErrInvalidAllocatable) {
		t.Fatalf("A=0 期望 ErrInvalidAllocatable, 得到 %v", err)
	}
	if _, err := NewNode(-5, 1); !errors.Is(err, ErrInvalidAllocatable) {
		t.Fatalf("A<0 期望 ErrInvalidAllocatable, 得到 %v", err)
	}
	if _, err := NewNode(100, 0); !errors.Is(err, ErrInvalidOversell) {
		t.Fatalf("O=0 期望 ErrInvalidOversell, 得到 %v", err)
	}

	n := mustNode(t, 100, 2)
	mustAdmit(t, n, "ok", 10, 20)

	cases := []struct {
		name string
		op   func() error
		want error
	}{
		{"r 为负", func() error { return n.Admit("n1", -1, 10) }, ErrNegativeRequest},
		{"l 为零", func() error { return n.Admit("n2", 0, 0) }, ErrInvalidLimit},
		{"l 小于 r", func() error { return n.Admit("n3", 10, 5) }, ErrInvalidLimit},
		{"标识重复", func() error { return n.Admit("ok", 1, 1) }, ErrDuplicateID},
		{"上报不存在", func() error { _, err := n.Report("ghost", 1); return err }, ErrNotFound},
		{"删除不存在", func() error { return n.Delete("ghost") }, ErrNotFound},
		{"u 为负", func() error { _, err := n.Report("ok", -1); return err }, ErrInvalidUsage},
		{"u 超过 l", func() error { _, err := n.Report("ok", 21); return err }, ErrInvalidUsage},
	}
	for _, tc := range cases {
		before := n.Snapshot()
		err := tc.op()
		t.Logf("输入: %s; 输出: err=%v", tc.name, err)
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s: 期望 %v, 得到 %v", tc.name, tc.want, err)
		}
		if after := n.Snapshot(); after != before {
			t.Fatalf("%s: 被拒操作改变了账目: %+v -> %+v", tc.name, before, after)
		}
	}
}

// 相同的操作序列重放得到完全相同的驱逐序列。
func TestDeterministicReplay(t *testing.T) {
	run := func() []Eviction {
		n, err := NewNode(100, 2)
		if err != nil {
			t.Fatalf("NewNode: %v", err)
		}
		var all []Eviction
		admit := func(id string, r, l int64) {
			if err := n.Admit(id, r, l); err != nil {
				t.Fatalf("Admit(%q): %v", id, err)
			}
		}
		report := func(id string, u int64) {
			evicted, err := n.Report(id, u)
			if err != nil {
				t.Fatalf("Report(%q): %v", id, err)
			}
			all = append(all, evicted...)
		}
		admit("a", 0, 60)
		admit("b", 20, 60)
		admit("g", 50, 50)
		report("a", 50)
		report("b", 50)
		report("g", 50) // sum=150 -> 驱逐 a
		admit("c", 30, 30)
		report("b", 55) // sum=135 -> 驱逐 b
		if err := n.Delete("g"); err != nil {
			t.Fatalf("Delete(g): %v", err)
		}
		report("c", 30)
		return all
	}
	first, second := run(), run()
	t.Logf("第一次重放驱逐序列: %v", evictionIDs(first))
	t.Logf("第二次重放驱逐序列: %v", evictionIDs(second))
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("重放不一致: %v != %v", first, second)
	}
	if got := evictionIDs(first); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("期望驱逐序列 [a b], 得到 %v", got)
	}
}

// 并发调用准入、上报、删除与查询；结束后账目不变量必须成立。
func TestConcurrent(t *testing.T) {
	n := mustNode(t, 1000, 4)
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				id := fmt.Sprintf("w%d-c%d", worker, i)
				_ = n.Admit(id, 10, 40)
				_, _ = n.Report(id, int64((i*7)%41))
				_ = n.Snapshot()
				if i%3 == 0 {
					_ = n.Delete(id)
				}
			}
		}(worker)
	}
	wg.Wait()
	s := n.Snapshot()
	t.Logf("并发结束后账目: %+v", s)
	if s.SumRequest > s.Allocatable {
		t.Fatalf("不变量违反: sumRequest %d > A %d", s.SumRequest, s.Allocatable)
	}
	if s.SumLimit > s.Oversell*s.Allocatable {
		t.Fatalf("不变量违反: sumLimit %d > O*A %d", s.SumLimit, s.Oversell*s.Allocatable)
	}
	if s.SumUsage > s.Allocatable {
		t.Fatalf("不变量违反: sumUsage %d > A %d", s.SumUsage, s.Allocatable)
	}
}
