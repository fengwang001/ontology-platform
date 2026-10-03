package approval_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/approval"
	"ontology/org"
)

func mkEngine(t *testing.T, T int64) (*approval.Engine, *org.Org) {
	t.Helper()
	e, err := approval.New(org.New(), T)
	must(t, err)
	return e, e.OrgForTest()
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func chain(t *testing.T, o *org.Org, m map[string]string, lim map[string]int64) {
	t.Helper()
	for e, g := range m {
		must(t, o.SetManager(e, g))
	}
	for e, x := range lim {
		must(t, o.SetLimit(e, x))
	}
}

// 题面例子：T=10，A→B→C→D，B100 C500 D1000，提交 300。
func TestSpecExample(t *testing.T) {
	e, o := mkEngine(t, 10)
	chain(t, o, map[string]string{"A": "B", "B": "C", "C": "D"},
		map[string]int64{"B": 100, "C": 500, "D": 1000})
	must(t, e.Submit("r", "A", 300, 0))
	if st, _ := e.Status("r", 25); st.Outcome != approval.Expired || st.FinalAt != 20 {
		t.Fatalf("25=%+v want Expired@20", st)
	}
	if st, _ := e.Status("r", 5); st.Outcome != approval.Pending || st.Assignee != "C" || st.Ta != 0 {
		t.Fatalf("5=%+v want C ta0（Status 不推进时钟）", st)
	}
}

// 到期恰等升级、差 1 不升级；一次跨多级，新 ta 取到期时刻而非处理时刻。
func TestDeadlineAndMulti(t *testing.T) {
	e, o := mkEngine(t, 10)
	chain(t, o, map[string]string{"A": "C", "C": "D", "D": "E", "E": "F"},
		map[string]int64{"C": 1, "D": 1, "E": 1, "F": 1})
	must(t, e.Submit("r", "A", 1, 0))
	if st, _ := e.Status("r", 9); st.Assignee != "C" {
		t.Fatalf("9=%+v", st)
	}
	if st, _ := e.Status("r", 10); st.Assignee != "D" || st.Ta != 10 {
		t.Fatalf("10=%+v want D ta10", st)
	}
	if st, _ := e.Status("r", 35); st.Assignee != "F" || st.Ta != 30 {
		t.Fatalf("35=%+v want F ta30", st)
	}
	if st, _ := e.Status("r", 40); st.Outcome != approval.Expired || st.FinalAt != 40 {
		t.Fatalf("40=%+v want Expired@40", st)
	}
}

// 冻结链不随后加入的更近上级变化；额度恰等通过、不足者被排除。
func TestFrozenAndEqual(t *testing.T) {
	e, o := mkEngine(t, 10)
	chain(t, o, map[string]string{"A": "C", "C": "D"}, map[string]int64{"C": 100, "D": 100})
	must(t, e.Submit("r", "A", 100, 0))
	must(t, o.SetManager("A", "B"))
	must(t, o.SetManager("B", "C"))
	must(t, o.SetLimit("B", 1000))
	if st, _ := e.Status("r", 10); st.Assignee != "D" {
		t.Fatalf("frozen violated %+v", st)
	}
	e2, o2 := mkEngine(t, 10)
	chain(t, o2, map[string]string{"A": "B", "B": "C"}, map[string]int64{"B": 100, "C": 500})
	must(t, e2.Submit("s", "A", 500, 0))
	if st, _ := e2.Status("s", 0); st.Assignee != "C" {
		t.Fatalf("B 额度不足应被排除，got %q", st.Assignee)
	}
	must(t, e2.Decide("s", "C", true, 3))
	if st, _ := e2.Status("s", 3); st.Outcome != approval.Approved || st.FinalAt != 3 {
		t.Fatalf("approve=%+v", st)
	}
}

// 撤权：ErrRevoked 后保持待决 ta 不变，超时升级，撤权者再决策得 NotAssignee。
func TestRevoked(t *testing.T) {
	e, o := mkEngine(t, 10)
	chain(t, o, map[string]string{"A": "C", "C": "D"}, map[string]int64{"C": 500, "D": 1000})
	must(t, e.Submit("r", "A", 300, 0))
	must(t, o.SetLimit("C", 0))
	if err := e.Decide("r", "C", true, 5); !errors.Is(err, approval.ErrRevoked) {
		t.Fatalf("want revoked %v", err)
	}
	if st, _ := e.Status("r", 5); st.Assignee != "C" || st.Ta != 0 {
		t.Fatalf("撤权后状态被改 %+v", st)
	}
	if err := e.Decide("r", "C", true, 10); !errors.Is(err, approval.ErrNotAssignee) {
		t.Fatalf("升级后 want notassignee %v", err)
	}
	must(t, e.Decide("r", "D", true, 10))
	if st, _ := e.Status("r", 10); st.Outcome != approval.Approved || st.FinalAt != 10 {
		t.Fatalf("final=%+v", st)
	}
}

// 错误优先级；被拒操作不落实到期；无候选/终局/拒绝/构造 T 越界。
func TestErrors(t *testing.T) {
	e, o := mkEngine(t, 10)
	chain(t, o, map[string]string{"A": "C", "C": "D"}, map[string]int64{"C": 100, "D": 100})
	must(t, e.Submit("r", "A", 50, 0))
	chk := func(got, want error) {
		t.Helper()
		if !errors.Is(got, want) {
			t.Errorf("want %v got %v", want, got)
		}
	}
	chk(e.Submit("", "A", 1, 6), approval.ErrInvalid)
	chk(e.Submit("q", "A", 0, 6), approval.ErrInvalid)
	chk(e.Submit("q", "A", 1e12+1, 6), approval.ErrInvalid)
	chk(e.Submit("q", "A", 1, 1e15+1), approval.ErrInvalid)
	chk(e.Submit("q", "A", 1, -1), approval.ErrInvalid)
	chk(e.Submit("r", "A", 1, 6), approval.ErrNotFound)
	chk(e.Decide("r", "C", true, -1), approval.ErrInvalid)
	chk(e.Decide("zz", "C", true, 6), approval.ErrNotFound)
	chk(e.Decide("r", "X", true, 6), approval.ErrNotAssignee)
	chk(e.Decide("r", "C", true, 10), approval.ErrNotAssignee) // 10 时已升级给 D
	if st, _ := e.Status("r", 5); st.Assignee != "C" {         // 被拒操作未落实到期
		t.Fatalf("rejected op applied expiry %+v", st)
	}
	must(t, e.Decide("r", "D", false, 10))
	chk(e.Decide("r", "D", true, 11), approval.ErrClosed) // ErrClosed 优先
	if st, _ := e.Status("r", 99); st.Outcome != approval.Rejected || st.FinalAt != 10 {
		t.Fatalf("reject=%+v", st)
	}
	chk(e.Submit("z", "Ghost", 1, 10), approval.ErrNoApprover)
	if _, err := e.Status("z", 10); !errors.Is(err, approval.ErrNotFound) {
		t.Fatalf("被拒 Submit 不应创建申请 %v", err)
	}
	if _, err := approval.New(org.New(), 0); !errors.Is(err, approval.ErrInvalid) {
		t.Fatal("T=0 应非法")
	}
}

// 堆考察项数 = 本次升级与终局数 + 1；无关待决申请 1 与 10000 两档。
func TestExaminedCounter(t *testing.T) {
	for _, n := range []int{1, 10_000} {
		e, o := mkEngine(t, 10)
		chain(t, o, map[string]string{"A": "C0", "C0": "C1", "C1": "C2", "C2": "C3"},
			map[string]int64{"C0": 1, "C1": 1, "C2": 1, "C3": 1})
		must(t, e.Submit("r", "A", 1, 0))
		if st, _ := e.Status("r", 40); st.Outcome != approval.Expired {
			t.Fatalf("r=%+v", st)
		}
		if e.Examined() != 5 { // 4 次弹出 + 1 次停下
			t.Fatalf("n=%d examined=%d want 5", n, e.Examined())
		}
		must(t, o.SetManager("Z", "W"))
		must(t, o.SetLimit("W", 1))
		for i := 0; i < n; i++ {
			must(t, e.Submit(fmt.Sprintf("q%d", i), "Z", 1, 990)) // due=1000
		}
		if e.HeapLen() != n { // r 已随成功操作落实终局而出堆
			t.Fatalf("n=%d heap=%d want %d", n, e.HeapLen(), n)
		}
		_, _ = e.Status("r", 1000) // n 个 q 恰等到期：pop n + 堆空 1
		if e.Examined() != n+1 {
			t.Fatalf("n=%d examined=%d want %d", n, e.Examined(), n+1)
		}
	}
}

// 并发 SetLimit 与 Decide：批准或先撤权，恰为某个串行顺序。
func TestConcurrent(t *testing.T) {
	for trial := 0; trial < 80; trial++ {
		e, o := mkEngine(t, 1000)
		chain(t, o, map[string]string{"A": "C"}, map[string]int64{"C": 100})
		must(t, e.Submit("r", "A", 50, 0))
		var wg sync.WaitGroup
		var derr error
		wg.Add(2)
		go func() { defer wg.Done(); derr = e.Decide("r", "C", true, 5) }()
		go func() { defer wg.Done(); _ = o.SetLimit("C", 0) }()
		wg.Wait()
		st, _ := e.Status("r", 6)
		if !((derr == nil && st.Outcome == approval.Approved) ||
			(errors.Is(derr, approval.ErrRevoked) && st.Outcome == approval.Pending)) {
			t.Fatalf("trial=%d derr=%v st=%+v", trial, derr, st)
		}
	}
}
