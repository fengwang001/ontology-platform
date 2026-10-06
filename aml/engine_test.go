package aml

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"testing"
)

// 测试配置：L=100, H=1000, K=3, D=7。
func testConfig() Config { return Config{Low: 100, High: 1000, K: 3, D: 7} }

func newEngine(t *testing.T, accounts ...string) *Engine {
	t.Helper()
	e, err := NewEngine(testConfig())
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	for i, acc := range accounts {
		if err := e.AddAccount(acc, int64(i)); err != nil {
			t.Fatalf("AddAccount(%s): %v", acc, err)
		}
	}
	return e
}

func dep(t *testing.T, e *Engine, txn, acc string, amount, now int64) *Report {
	t.Helper()
	r, err := e.Deposit(txn, acc, amount, now)
	if err != nil {
		t.Fatalf("Deposit(%s,%s,%d,%d): %v", txn, acc, amount, now, err)
	}
	t.Logf("Deposit(txn=%s acc=%s amount=%d now=%d) -> report=%v", txn, acc, amount, now, r != nil)
	return r
}

func wantErr(t *testing.T, err error, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want errors.Is %v", err, want)
	}
	t.Logf("rejected with %v (as expected)", err)
}

func wantNoReport(t *testing.T, r *Report, why string) {
	t.Helper()
	if r != nil {
		t.Fatalf("unexpected report %+v (%s)", r, why)
	}
}

func wantReport(t *testing.T, r *Report, kind ReportKind, id int, total int64, txns ...string) {
	t.Helper()
	if r == nil {
		t.Fatalf("expect report kind=%s id=%d, got nil", kind, id)
	}
	if r.Kind != kind || r.ID != id || r.Total != total {
		t.Fatalf("report = %+v, want kind=%s id=%d total=%d", r, kind, id, total)
	}
	wantTxns := append([]string(nil), txns...)
	sort.Strings(wantTxns)
	if !reflect.DeepEqual(r.TxnIDs, wantTxns) {
		t.Fatalf("report txns = %v, want %v", r.TxnIDs, wantTxns)
	}
	t.Logf("report #%d %s txns=%v total=%d trigger=%s date=%d accounts=%v",
		r.ID, r.Kind, r.TxnIDs, r.Total, r.Trigger, r.Date, r.Accounts)
}

func TestConfigValidation(t *testing.T) {
	bad := []Config{
		{Low: 0, High: 1000, K: 3, D: 7},
		{Low: -1, High: 1000, K: 3, D: 7},
		{Low: 1000, High: 1000, K: 3, D: 7},
		{Low: 1001, High: 1000, K: 3, D: 7},
		{Low: 100, High: 0, K: 3, D: 7},
		{Low: 100, High: -5, K: 3, D: 7},
		{Low: 100, High: 1000, K: 1, D: 7},
		{Low: 100, High: 1000, K: 0, D: 7},
		{Low: 100, High: 1000, K: 3, D: 0},
		{Low: 100, High: 1000, K: 3, D: -1},
	}
	for _, c := range bad {
		if _, err := NewEngine(c); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("config %+v: err = %v, want ErrInvalidParam", c, err)
		}
	}
	if _, err := NewEngine(testConfig()); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	if _, err := NewEngine(Config{Low: 1, High: 2, K: 2, D: 1}); err != nil {
		t.Fatalf("minimal valid config rejected: %v", err)
	}
}

// 笔数与金额合计恰等于阈值时成立，差一不成立。
func TestStructuringThresholdExact(t *testing.T) {
	t.Run("笔数与合计恰等于阈值", func(t *testing.T) {
		e := newEngine(t, "A")
		wantNoReport(t, dep(t, e, "t1", "A", 400, 1), "count=1 < K=3")
		wantNoReport(t, dep(t, e, "t2", "A", 300, 1), "count=2 < K=3")
		r := dep(t, e, "t3", "A", 300, 1) // count=3==K, sum=1000==H
		wantReport(t, r, KindStructuring, 1, 1000, "t1", "t2", "t3")
		if r.Trigger != "deposit(t3)" || r.Date != 1 {
			t.Fatalf("trigger/date = %s/%d", r.Trigger, r.Date)
		}
		if !reflect.DeepEqual(r.Accounts, []string{"A"}) {
			t.Fatalf("accounts = %v", r.Accounts)
		}
	})
	t.Run("合计差一不成立", func(t *testing.T) {
		e := newEngine(t, "A")
		wantNoReport(t, dep(t, e, "t1", "A", 333, 1), "sum=333 < H")
		wantNoReport(t, dep(t, e, "t2", "A", 333, 1), "sum=666 < H")
		wantNoReport(t, dep(t, e, "t3", "A", 333, 1), "count=3==K 但 sum=999==H-1")
	})
	t.Run("笔数差一不成立", func(t *testing.T) {
		e := newEngine(t, "A")
		wantNoReport(t, dep(t, e, "t1", "A", 500, 1), "count=1")
		wantNoReport(t, dep(t, e, "t2", "A", 500, 1), "count=2==K-1 虽 sum=1000==H")
	})
}

// 日期恰在窗口边界：now-date < D 计入，now-date == D 排除。
func TestWindowBoundary(t *testing.T) {
	e := newEngine(t, "A")
	wantNoReport(t, dep(t, e, "old", "A", 400, 10), "count=1")
	wantNoReport(t, dep(t, e, "t2", "A", 400, 16), "old 仍在窗口(16-10=6<D)")
	// now=17 时 old 的差为 7==D，滑出窗口；集合只剩 t2、t3，不成立。
	wantNoReport(t, dep(t, e, "t3", "A", 400, 17), "old 已滑出窗口(17-10=7==D)")
	r := dep(t, e, "t4", "A", 400, 17) // 集合 {t2,t3,t4}
	wantReport(t, r, KindStructuring, 1, 1200, "t2", "t3", "t4")
}

// 金额恰等于 L 计入判定；恰等于 H 立即大额报告且不进集合；小于 L 只记录。
func TestAmountBounds(t *testing.T) {
	t.Run("金额等于L参与判定", func(t *testing.T) {
		e := newEngine(t, "A")
		var r *Report
		for i := 1; i <= 10; i++ {
			r = dep(t, e, fmt.Sprintf("t%d", i), "A", 100, 1) // 恰为 L
		}
		wantReport(t, r, KindStructuring, 1, 1000,
			"t1", "t2", "t3", "t4", "t5", "t6", "t7", "t8", "t9", "t10")
	})
	t.Run("金额等于H立即大额报告", func(t *testing.T) {
		e := newEngine(t, "A")
		r := dep(t, e, "big", "A", 1000, 1) // 恰为 H
		wantReport(t, r, KindLarge, 1, 1000, "big")
		count, sum, err := e.CurrentSet("A", 1)
		if err != nil || count != 0 || sum != 0 {
			t.Fatalf("CurrentSet = (%d,%d,%v), 大额不得进入判定集合", count, sum, err)
		}
	})
	t.Run("小于L只记录", func(t *testing.T) {
		e := newEngine(t, "A")
		wantNoReport(t, dep(t, e, "tiny", "A", 99, 1), "99 < L 不参与判定")
		count, sum, err := e.CurrentSet("A", 1)
		if err != nil || count != 0 || sum != 0 {
			t.Fatalf("CurrentSet = (%d,%d,%v), 微小存款不进集合", count, sum, err)
		}
		if err := e.Reverse("tiny", 2); err != nil {
			t.Fatalf("微小存款应可冲正: %v", err)
		}
	})
}

// 关联合并两组后集合取并集，触发报告的是关联操作。
func TestLinkTriggers(t *testing.T) {
	e := newEngine(t, "A", "B")
	dep(t, e, "a1", "A", 400, 1)
	dep(t, e, "a2", "A", 400, 1)
	dep(t, e, "b1", "B", 400, 1)
	dep(t, e, "b2", "B", 400, 1)
	r, err := e.Link("A", "B", 2)
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	wantReport(t, r, KindStructuring, 1, 1600, "a1", "a2", "b1", "b2")
	if r.Trigger != "link(A,B)" || r.Date != 2 {
		t.Fatalf("trigger/date = %s/%d，触发操作应为关联", r.Trigger, r.Date)
	}
	if !reflect.DeepEqual(r.Accounts, []string{"A", "B"}) {
		t.Fatalf("accounts = %v", r.Accounts)
	}
	// 已在同一组（含传递、对称）报已关联。
	wantErr(t, err2("A", "B", 3, e), ErrAlreadyLinked)
	wantErr(t, err2("B", "A", 3, e), ErrAlreadyLinked)
	wantErr(t, err2("A", "A", 3, e), ErrAlreadyLinked)
}

func err2(a, b string, now int64, e *Engine) error {
	_, err := e.Link(a, b, now)
	return err
}

// 冲正使集合不再成立，新存款使其重新成立并产生新报告。
func TestReverseBreaksAndRequalifies(t *testing.T) {
	e := newEngine(t, "A")
	dep(t, e, "t1", "A", 400, 1)
	dep(t, e, "t2", "A", 400, 1)
	r1 := dep(t, e, "t3", "A", 400, 1)
	wantReport(t, r1, KindStructuring, 1, 1200, "t1", "t2", "t3")

	if err := e.Reverse("t2", 2); err != nil {
		t.Fatalf("Reverse: %v", err)
	}
	count, sum, _ := e.CurrentSet("A", 2)
	if count != 2 || sum != 800 {
		t.Fatalf("冲正后集合 = (%d,%d), want (2,800)", count, sum)
	}
	// 冲正不触发评估，已发报告不撤回。
	if got := len(e.Reports()); got != 1 {
		t.Fatalf("冲正后报告数 = %d, want 1", got)
	}
	// 新存款使集合重新成立，且新存款未被覆盖 -> 新报告。
	r2 := dep(t, e, "t4", "A", 400, 3)
	wantReport(t, r2, KindStructuring, 2, 1200, "t1", "t3", "t4")
}

// 同一批存款不重复报告：集合全部被覆盖后，无新未覆盖存款则不再报告。
func TestNoDuplicateReport(t *testing.T) {
	e := newEngine(t, "A", "B", "C", "D")
	dep(t, e, "a1", "A", 400, 4)
	dep(t, e, "a2", "A", 400, 4)
	dep(t, e, "a3", "A", 400, 4) // -> 报告 #1，A 组全部覆盖
	wantNoReport(t, dep(t, e, "tiny", "A", 50, 5), "微小存款不改变集合，不得重复报告")
	wantReport(t, dep(t, e, "huge", "A", 5000, 5), KindLarge, 2, 5000, "huge")
	r, err := e.Link("A", "B", 6) // B 组为空，合并后仍全部被覆盖
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	wantNoReport(t, r, "合并后无未覆盖存款，不得重复报告")

	dep(t, e, "c1", "C", 400, 6)
	dep(t, e, "c2", "C", 400, 6)
	dep(t, e, "c3", "C", 400, 6) // -> 报告 #3，C 组全部覆盖
	r, err = e.Link("A", "C", 7) // 两组均已覆盖
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	wantNoReport(t, r, "两组均已覆盖，合并不得产生新报告")

	reports := e.Reports()
	if len(reports) != 3 { // #1 结构化、#2 大额、#3 结构化
		t.Fatalf("报告数 = %d, want 3", len(reports))
	}
	for i, rep := range reports {
		if rep.ID != i+1 {
			t.Fatalf("报告编号不连续: %+v", reports)
		}
	}
}

// 关联时两组各自已有的覆盖标记保持不变；
// 合并集合中仍有未覆盖存款时才可能发新报告。
func TestCoveragePreservedOnLink(t *testing.T) {
	e := newEngine(t, "A", "B")
	dep(t, e, "a1", "A", 400, 1)
	dep(t, e, "a2", "A", 400, 1)
	dep(t, e, "a3", "A", 400, 1) // -> 报告 #1，A 组全部覆盖
	dep(t, e, "b1", "B", 400, 1) // B 组 2 笔，未成立、未覆盖
	dep(t, e, "b2", "B", 400, 1)
	r, err := e.Link("A", "B", 2)
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	// 合并集合 5 笔、合计 2000，b1/b2 未覆盖 -> 新报告覆盖全部 5 笔。
	wantReport(t, r, KindStructuring, 2, 2000, "a1", "a2", "a3", "b1", "b2")
}

// 错误可区分且按优先级只报第一个：
// 参数非法 > 时钟回退 > 账户不存在 > 交易号重复/不存在/已冲正 > 已关联。
func TestErrorPriority(t *testing.T) {
	e := newEngine(t, "A", "B") // lastNow = 1
	if err := e.AddAccount("C", 5); err != nil {
		t.Fatalf("AddAccount: %v", err)
	}
	// 参数非法 优先于 时钟回退。
	wantErr(t, e.AddAccount("", 0), ErrInvalidParam)
	wantErr(t, e.AddAccount("A", 0), ErrClockRollback) // 时钟回退 优先于 账户已存在
	_, err := e.Deposit("", "ghost", -1, 0)
	wantErr(t, err, ErrInvalidParam)
	// 时钟回退 优先于 账户不存在。
	_, err = e.Deposit("t", "ghost", 100, 4)
	wantErr(t, err, ErrClockRollback)
	// 账户不存在 优先于 交易号重复。
	dep(t, e, "d", "A", 100, 6)
	_, err = e.Deposit("d", "ghost", 100, 7)
	wantErr(t, err, ErrAccountNotFound)
	_, err = e.Deposit("d", "A", 100, 7)
	wantErr(t, err, ErrDuplicateTxn)
	// 关联：参数 > 时钟 > 账户不存在 > 已关联。
	wantErr(t, err2("", "A", 0, e), ErrInvalidParam)
	wantErr(t, err2("ghost", "A", 4, e), ErrClockRollback)
	wantErr(t, err2("ghost", "A", 6, e), ErrAccountNotFound)
	wantErr(t, err2("A", "A", 6, e), ErrAlreadyLinked)
	// 冲正：参数 > 时钟 > 交易号不存在 > 已冲正。
	wantErr(t, e.Reverse("", 0), ErrInvalidParam)
	wantErr(t, e.Reverse("x", 4), ErrClockRollback)
	wantErr(t, e.Reverse("x", 6), ErrTxnNotFound)
	if err := e.Reverse("d", 7); err != nil {
		t.Fatalf("Reverse: %v", err)
	}
	wantErr(t, e.Reverse("d", 8), ErrAlreadyReversed)
	// 交易号一经使用（即使已冲正）不得再用于另一笔存款。
	_, err = e.Deposit("d", "A", 100, 9)
	wantErr(t, err, ErrDuplicateTxn)
	// 重复注册账户。
	wantErr(t, e.AddAccount("A", 9), ErrAccountExists)
}

// 被拒绝的操作不得改变任何状态、报告与时钟。
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	e := newEngine(t, "A") // lastNow = 0
	if err := e.AddAccount("B", 5); err != nil {
		t.Fatalf("AddAccount: %v", err)
	}
	before := len(e.Reports())
	// 账户不存在的存款被拒绝，交易号不被占用，时钟不推进。
	_, err := e.Deposit("x", "ghost", 400, 10)
	wantErr(t, err, ErrAccountNotFound)
	// 时钟回退的存款被拒绝，时钟保持 5。
	_, err = e.Deposit("y", "A", 400, 3)
	wantErr(t, err, ErrClockRollback)
	// 时钟仍为 5：now=6 被接受；x 未被占用可再用。
	dep(t, e, "x", "A", 400, 6)
	// 重复交易号被拒绝，不覆盖原存款。
	_, err = e.Deposit("x", "A", 999, 7)
	wantErr(t, err, ErrDuplicateTxn)
	// 已关联被拒绝，不产生报告。
	if _, err := e.Link("A", "B", 8); err != nil {
		t.Fatalf("Link: %v", err)
	}
	wantErr(t, err2("A", "B", 9, e), ErrAlreadyLinked)
	// 冲正不存在的交易号被拒绝。
	wantErr(t, e.Reverse("nope", 10), ErrTxnNotFound)
	// 报告序列只含合法操作产生的报告。
	reports := e.Reports()
	if len(reports) != before {
		t.Fatalf("被拒绝操作留下了报告痕迹: %v", reports)
	}
	count, sum, _ := e.CurrentSet("A", 10)
	if count != 1 || sum != 400 {
		t.Fatalf("集合 = (%d,%d), want (1,400)：被拒绝操作改变了状态", count, sum)
	}
}

// 大额报告与结构化报告互相独立。
func TestLargeReportIndependence(t *testing.T) {
	e := newEngine(t, "A")
	dep(t, e, "s1", "A", 400, 1)
	dep(t, e, "s2", "A", 400, 1)
	r := dep(t, e, "big", "A", 1000, 2)
	wantReport(t, r, KindLarge, 1, 1000, "big")
	// 大额存款不进入结构化判定集合，也不改变已有集合。
	count, sum, _ := e.CurrentSet("A", 2)
	if count != 2 || sum != 800 {
		t.Fatalf("集合 = (%d,%d), want (2,800)", count, sum)
	}
	// 已有集合不因大额存款自身而变化；第三笔小额触发结构化报告。
	r2 := dep(t, e, "s3", "A", 400, 3)
	wantReport(t, r2, KindStructuring, 2, 1200, "s1", "s2", "s3")
}

// 查询只取决于已接受操作与查询 now，不修改状态、不受查询次数影响。
func TestQueries(t *testing.T) {
	e := newEngine(t, "A", "B")
	if _, err := e.GroupAccounts("ghost"); !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("GroupAccounts(ghost) = %v", err)
	}
	if _, _, err := e.CurrentSet("ghost", 0); !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("CurrentSet(ghost) = %v", err)
	}
	if _, err := e.Link("A", "B", 5); err != nil {
		t.Fatalf("Link: %v", err)
	}
	accounts, err := e.GroupAccounts("A")
	if err != nil || !reflect.DeepEqual(accounts, []string{"A", "B"}) {
		t.Fatalf("GroupAccounts = %v, %v", accounts, err)
	}
	dep(t, e, "s1", "A", 400, 5)
	dep(t, e, "s2", "B", 400, 6)
	// 按查询所用 now 计算窗口：now=12 时 s1 差 7==D 滑出。
	if c, s, _ := e.CurrentSet("A", 6); c != 2 || s != 800 {
		t.Fatalf("CurrentSet(now=6) = (%d,%d), want (2,800)", c, s)
	}
	if c, s, _ := e.CurrentSet("A", 12); c != 1 || s != 400 {
		t.Fatalf("CurrentSet(now=12) = (%d,%d), want (1,400)", c, s)
	}
	if c, s, _ := e.CurrentSet("A", 13); c != 0 || s != 0 {
		t.Fatalf("CurrentSet(now=13) = (%d,%d), want (0,0)", c, s)
	}
	// 查询幂等、不改状态：重复查询结果一致，时钟不受影响。
	for i := 0; i < 3; i++ {
		if c, s, _ := e.CurrentSet("A", 12); c != 1 || s != 400 {
			t.Fatalf("第 %d 次查询结果不同", i)
		}
	}
	if _, err := e.GroupAccounts("A"); err != nil {
		t.Fatal(err)
	}
	if got := len(e.Reports()); got != 0 {
		t.Fatalf("查询产生了报告: %d", got)
	}
	// 查询不推进时钟：以当前 now=6 查询后，后续 now=6 的操作仍被接受。
	if c, s, _ := e.CurrentSet("A", 6); c != 2 || s != 800 {
		t.Fatalf("CurrentSet(now=6) = (%d,%d), want (2,800)", c, s)
	}
	dep(t, e, "s3", "A", 400, 6)
}

// 冲正大额与微小存款：报告不撤回，集合不受影响。
func TestReverseLargeAndTiny(t *testing.T) {
	e := newEngine(t, "A")
	wantReport(t, dep(t, e, "big", "A", 2000, 1), KindLarge, 1, 2000, "big")
	dep(t, e, "tiny", "A", 10, 1)
	if err := e.Reverse("big", 2); err != nil {
		t.Fatalf("Reverse(big): %v", err)
	}
	if err := e.Reverse("tiny", 2); err != nil {
		t.Fatalf("Reverse(tiny): %v", err)
	}
	if got := len(e.Reports()); got != 1 {
		t.Fatalf("已发出的大额报告不得撤回, reports=%d", got)
	}
	if c, s, _ := e.CurrentSet("A", 2); c != 0 || s != 0 {
		t.Fatalf("集合 = (%d,%d), want (0,0)", c, s)
	}
}

// 相同操作序列重放得到完全相同的报告序列。
func TestReplayDeterminism(t *testing.T) {
	script := func(e *Engine) {
		for i, acc := range []string{"A", "B", "C"} {
			if err := e.AddAccount(acc, int64(i)); err != nil {
				t.Fatal(err)
			}
		}
		ops := []func(){
			func() { _, _ = e.Deposit("t1", "A", 400, 5) },
			func() { _, _ = e.Deposit("t2", "B", 400, 5) },
			func() { _, _ = e.Deposit("t3", "C", 400, 6) },
			func() { _, _ = e.Link("A", "B", 7) },
			func() { _, _ = e.Deposit("t4", "A", 400, 8) },
			func() { _, _ = e.Link("A", "C", 9) },
			func() { _ = e.Reverse("t1", 10) },
			func() { _, _ = e.Deposit("t5", "C", 400, 11) },
			func() { _, _ = e.Deposit("t6", "A", 5000, 12) },
			func() { _, _ = e.Deposit("t5", "A", 400, 13) }, // 重复，被拒绝
			func() { _, _ = e.Link("B", "C", 14) },          // 已关联，被拒绝
		}
		for _, op := range ops {
			op()
		}
	}
	e1, _ := NewEngine(testConfig())
	e2, _ := NewEngine(testConfig())
	script(e1)
	script(e2)
	r1, r2 := e1.Reports(), e2.Reports()
	if !reflect.DeepEqual(r1, r2) {
		t.Fatalf("重放报告序列不一致:\n%v\n%v", r1, r2)
	}
	if len(r1) == 0 {
		t.Fatal("脚本应产生至少一份报告")
	}
	t.Logf("重放报告序列: %+v", r1)
}

// 并发调用等价于某个串行顺序：报告编号连续、无丢失、无数据竞争。
func TestConcurrency(t *testing.T) {
	e, err := NewEngine(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	const nAcc = 4
	for i := 0; i < nAcc; i++ {
		if err := e.AddAccount(fmt.Sprintf("acc%d", i), 0); err != nil {
			t.Fatal(err)
		}
	}
	amounts := []int64{400, 300, 1000, 50, 100, 999}
	var wg sync.WaitGroup
	var reported int64
	var mu sync.Mutex
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				txn := fmt.Sprintf("g%d-t%d", g, i)
				acc := fmt.Sprintf("acc%d", (g+i)%nAcc)
				r, err := e.Deposit(txn, acc, amounts[(g+i)%len(amounts)], 1)
				if err != nil {
					t.Errorf("Deposit: %v", err)
					return
				}
				if r != nil {
					mu.Lock()
					reported++
					mu.Unlock()
				}
				if i%17 == 0 { // 交错查询
					_, _, _ = e.CurrentSet(acc, 1)
					_, _ = e.GroupAccounts(acc)
					_ = e.Reports()
				}
			}
		}(g)
	}
	wg.Wait()
	reports := e.Reports()
	if int64(len(reports)) != reported {
		t.Fatalf("报告数 = %d, 各操作返回的报告数 = %d", len(reports), reported)
	}
	for i, r := range reports {
		if r.ID != i+1 {
			t.Fatalf("报告编号不连续: 第 %d 份 ID=%d", i, r.ID)
		}
	}
	t.Logf("并发完成: %d 笔存款, %d 份报告", 8*200, len(reports))
}

// 复杂度证明：评估与关联的开销不随全部账户数、全部历史存款数、
// 以及被合并较大一组窗口之外的历史存款数增长。通过 Visits 计数器验证。
func TestVisitsBound(t *testing.T) {
	cfg := Config{Low: 100, High: 1000000, K: 3, D: 7}
	t.Run("历史存款不进入开销", func(t *testing.T) {
		e, _ := NewEngine(cfg)
		if err := e.AddAccount("big", 0); err != nil {
			t.Fatal(err)
		}
		// 5000 笔微小存款：不进窗口，评估开销为 0。
		for i := 0; i < 5000; i++ {
			if _, err := e.Deposit(fmt.Sprintf("tiny%d", i), "big", 1, 1); err != nil {
				t.Fatal(err)
			}
		}
		if v := e.Visits(); v != 0 {
			t.Fatalf("微小存款产生窗口访问 %d 次, want 0", v)
		}
		// 200 笔窗口内小额存款：每笔评估至多访问常数个窗口条目。
		for i := 0; i < 200; i++ {
			if _, err := e.Deposit(fmt.Sprintf("s%d", i), "big", 100, 1); err != nil {
				t.Fatal(err)
			}
		}
		if v := e.Visits(); v > 2*200 {
			t.Fatalf("200 笔窗口内存款访问 %d 次, 应 <= 400", v)
		}
		// 时钟前进，200 笔滑出窗口：一次性剔除，均摊每笔 1 次。
		if _, err := e.Deposit("tick", "big", 1, 100); err != nil {
			t.Fatal(err)
		}
		if v := e.Visits(); v > 2*200+2*200 {
			t.Fatalf("剔除过期条目后访问 %d 次, 应 <= 600", v)
		}
		// 关联：较大一组有 5200 笔历史（窗口外），关联开销须与其无关。
		if err := e.AddAccount("small", 100); err != nil {
			t.Fatal(err)
		}
		if _, err := e.Deposit("x1", "small", 100, 100); err != nil {
			t.Fatal(err)
		}
		if _, err := e.Deposit("x2", "small", 100, 100); err != nil {
			t.Fatal(err)
		}
		v0 := e.Visits()
		if _, err := e.Link("big", "small", 100); err != nil {
			t.Fatal(err)
		}
		if dv := e.Visits() - v0; dv > 10 {
			t.Fatalf("关联访问窗口条目 %d 次, 应 <= 10（与较大组窗口外历史无关）", dv)
		}
	})
	t.Run("评估开销与窗口规模线性", func(t *testing.T) {
		for _, n := range []int{1000, 2000, 4000} {
			e, _ := NewEngine(cfg)
			if err := e.AddAccount("a", 0); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < n; i++ {
				if _, err := e.Deposit(fmt.Sprintf("t%d", i), "a", 100, 1); err != nil {
					t.Fatal(err)
				}
			}
			if v := e.Visits(); v > 3*int64(n) {
				t.Fatalf("n=%d 时访问 %d 次, 超过线性界 3n", n, v)
			}
			t.Logf("n=%d visits=%d (<=3n 成立)", n, e.Visits())
		}
	})
}
