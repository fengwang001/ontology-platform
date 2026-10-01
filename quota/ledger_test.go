package quota

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func mustLedger(t *testing.T, cfg Config) *Ledger {
	t.Helper()
	l, err := NewLedger(cfg)
	if err != nil {
		t.Fatalf("NewLedger(%+v) 意外失败: %v", cfg, err)
	}
	return l
}

func mustAdd(t *testing.T, l *Ledger, r Record) {
	t.Helper()
	if err := l.AddRecord(r); err != nil {
		t.Fatalf("AddRecord(%+v) 意外失败: %v", r, err)
	}
}

func mustQuery(t *testing.T, l *Ledger, k int64) Report {
	t.Helper()
	rep, err := l.Query(k)
	if err != nil {
		t.Fatalf("Query(%d) 意外失败: %v", k, err)
	}
	return rep
}

// 区间右端恰为周期边界时不触及下一周期（半开区间 [s,e)）。
func TestRightEdgeOnBoundary(t *testing.T) {
	l := mustLedger(t, Config{T0: 0, Period: 10, Quota: 100, MaxCarry: 50})
	r := Record{ID: "a", Start: 0, End: 10, Amount: 7}
	mustAdd(t, l, r)

	rep0 := mustQuery(t, l, 0)
	rep1 := mustQuery(t, l, 1)
	t.Logf("输入: 记录 %+v, P=10, 区间右端 e=10 恰为周期 1 起点", r)
	t.Logf("输出: 周期0=%+v, 周期1=%+v", rep0, rep1)
	t.Logf("判定依据: [0,10) 是半开区间, 只触及周期 0, 周期 1 用量必须为 0")

	if rep0.Usage != 7 {
		t.Errorf("周期 0 用量 = %d, 期望 7", rep0.Usage)
	}
	if rep1.Usage != 0 {
		t.Errorf("周期 1 用量 = %d, 期望 0（右端边界不触及下一周期）", rep1.Usage)
	}
}

// 余数全部加到区间所触及的最后一个周期。
func TestRemainderGoesToLastTouchedPeriod(t *testing.T) {
	l := mustLedger(t, Config{T0: 0, Period: 10, Quota: 100, MaxCarry: 50})
	// 区间 [5,25) 长 20, 触及周期 0/1/2, 重叠长度 5/10/5, amount=10。
	r := Record{ID: "a", Start: 5, End: 25, Amount: 10}
	mustAdd(t, l, r)

	got := []int64{mustQuery(t, l, 0).Usage, mustQuery(t, l, 1).Usage, mustQuery(t, l, 2).Usage}
	t.Logf("输入: 记录 %+v, P=10, 重叠长度 5/10/5", r)
	t.Logf("输出: 各周期摊入量 %v", got)
	t.Logf("判定依据: 整除部分 10*5/20=2, 10*10/20=5, 10*5/20=2, 余数 1 加到最后触及的周期 2")

	want := []int64{2, 5, 3}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("周期 %d 摊入量 = %d, 期望 %d", i, got[i], want[i])
		}
	}
	var sum int64
	for _, u := range got {
		sum += u
	}
	if sum != r.Amount {
		t.Errorf("摊入总量 = %d, 期望守恒于 amount=%d", sum, r.Amount)
	}
}

// 结转量封顶于 M。
func TestCarryCappedAtMax(t *testing.T) {
	l := mustLedger(t, Config{T0: 0, Period: 10, Quota: 10, MaxCarry: 3})
	mustAdd(t, l, Record{ID: "a", Start: 0, End: 10, Amount: 2})

	rep1 := mustQuery(t, l, 1)
	rep2 := mustQuery(t, l, 2)
	t.Logf("输入: Q=10, M=3, 周期 0 用量 2, 未用额度 8")
	t.Logf("输出: 周期1=%+v, 周期2=%+v", rep1, rep2)
	t.Logf("判定依据: 结转量 = min(未用 8, M=3) = 3, 有效额度 = 10+3 = 13")

	if rep1.CarryIn != 3 || rep1.Effective != 13 {
		t.Errorf("周期 1 结转/有效额度 = %d/%d, 期望 3/13", rep1.CarryIn, rep1.Effective)
	}
	if rep2.CarryIn != 3 {
		t.Errorf("周期 2 结转 = %d, 期望 3（周期 1 无用量, 未用 13 仍封顶于 M）", rep2.CarryIn)
	}
}

// 超额周期只报告超额量，不产生负结转。
func TestOverageNoNegativeCarry(t *testing.T) {
	l := mustLedger(t, Config{T0: 0, Period: 10, Quota: 10, MaxCarry: 10})
	mustAdd(t, l, Record{ID: "a", Start: 0, End: 10, Amount: 15})

	rep0 := mustQuery(t, l, 0)
	rep1 := mustQuery(t, l, 1)
	t.Logf("输入: Q=10, M=10, 周期 0 用量 15（超额 5）")
	t.Logf("输出: 周期0=%+v, 周期1=%+v", rep0, rep1)
	t.Logf("判定依据: 超额量 5 只如实报告; 未用额度不足 0 按 0, 周期 1 结转为 0 而非负数")

	if rep0.Overage != 5 {
		t.Errorf("周期 0 超额量 = %d, 期望 5", rep0.Overage)
	}
	if rep1.CarryIn != 0 || rep1.Effective != 10 {
		t.Errorf("周期 1 结转/有效额度 = %d/%d, 期望 0/10（不允许负结转）", rep1.CarryIn, rep1.Effective)
	}
}

// 晚到的记录会改变此前周期的查询结果，并顺延影响之后的结转。
func TestLateRecordChangesEarlierAndLaterPeriods(t *testing.T) {
	l := mustLedger(t, Config{T0: 0, Period: 10, Quota: 10, MaxCarry: 10})
	mustAdd(t, l, Record{ID: "a", Start: 0, End: 10, Amount: 4})

	before0 := mustQuery(t, l, 0)
	before1 := mustQuery(t, l, 1)

	// 晚到记录: 周期 0 增加用量 3。
	late := Record{ID: "b", Start: 2, End: 8, Amount: 3}
	mustAdd(t, l, late)

	after0 := mustQuery(t, l, 0)
	after1 := mustQuery(t, l, 1)
	t.Logf("输入: 已有周期 0 用量 4; 晚到记录 %+v", late)
	t.Logf("输出: 周期0 %v -> %v; 周期1 %v -> %v", before0, after0, before1, after1)
	t.Logf("判定依据: 查询按当前全部记录现算, 周期 0 用量变 7, 结转 6 -> 3 顺延到周期 1")

	if after0.Usage != 7 {
		t.Errorf("晚到后周期 0 用量 = %d, 期望 7", after0.Usage)
	}
	if after1.CarryIn != 3 || after1.Effective != 13 {
		t.Errorf("晚到后周期 1 结转/有效额度 = %d/%d, 期望 3/13", after1.CarryIn, after1.Effective)
	}
	if before0.Usage != 4 || before1.CarryIn != 6 {
		t.Errorf("晚到前的快照不符合预期: %+v / %+v", before0, before1)
	}
}

// 任意到达顺序下同一记录集的查询结果完全相同；重放亦然。
func TestOrderIndependenceAndReplay(t *testing.T) {
	cfg := Config{T0: 100, Period: 10, Quota: 20, MaxCarry: 5}
	records := []Record{
		{ID: "r1", Start: 100, End: 135, Amount: 17},
		{ID: "r2", Start: 112, End: 118, Amount: 0}, // amount 为 0 合法
		{ID: "r3", Start: 105, End: 111, Amount: 9},
		{ID: "r4", Start: 128, End: 160, Amount: 33},
		{ID: "r5", Start: 100, End: 101, Amount: 100},
	}
	orders := [][]int{
		{0, 1, 2, 3, 4},
		{4, 3, 2, 1, 0},
		{2, 0, 4, 1, 3},
		{1, 3, 0, 4, 2},
	}

	snapshot := func(l *Ledger, n int64) []Report {
		reps := make([]Report, n)
		for k := int64(0); k < n; k++ {
			reps[k] = mustQuery(t, l, k)
		}
		return reps
	}

	var reference []Report
	for i, order := range orders {
		l := mustLedger(t, cfg)
		for _, idx := range order {
			mustAdd(t, l, records[idx])
		}
		reps := snapshot(l, 8)
		t.Logf("输入: 到达顺序 %v", order)
		t.Logf("输出: %v", reps)
		if i == 0 {
			reference = reps
			continue
		}
		for k := range reps {
			if reps[k] != reference[k] {
				t.Fatalf("顺序 %v 的周期 %d 结果 %v 与基准 %v 不一致", order, k, reps[k], reference[k])
			}
		}
	}
	t.Logf("判定依据: 查询按当前全部记录现算, 与到达顺序无关; 重放同一记录集得到完全相同结果")

	// 守恒性: 每条记录摊入各周期之和恒等于其 amount。
	l := mustLedger(t, cfg)
	for _, r := range records {
		mustAdd(t, l, r)
	}
	var totalUsage int64
	for _, rep := range snapshot(l, 8) {
		totalUsage += rep.Usage
	}
	var totalAmount int64
	for _, r := range records {
		totalAmount += r.Amount
	}
	t.Logf("守恒校验: 摊入总量 %d, 记录总量 %d", totalUsage, totalAmount)
	if totalUsage != totalAmount {
		t.Errorf("摊入总量 = %d, 期望守恒于 %d", totalUsage, totalAmount)
	}
}

// 非法参数与非法记录整体拒绝，原因可区分，且不改变账目。
func TestValidation(t *testing.T) {
	cfg := Config{T0: 10, Period: 10, Quota: 10, MaxCarry: 5}

	configCases := []struct {
		name string
		cfg  Config
		want error
	}{
		{"Q 不为正", Config{T0: 0, Period: 10, Quota: 0, MaxCarry: 5}, ErrNonPositiveQuota},
		{"P 不为正", Config{T0: 0, Period: -1, Quota: 10, MaxCarry: 5}, ErrNonPositivePeriod},
		{"M 为负", Config{T0: 0, Period: 10, Quota: 10, MaxCarry: -1}, ErrNegativeMaxCarry},
	}
	for _, c := range configCases {
		_, err := NewLedger(c.cfg)
		t.Logf("输入: %+v -> 输出: %v (判定依据: %s)", c.cfg, err, c.name)
		if !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, 期望 %v", c.name, err, c.want)
		}
	}

	l := mustLedger(t, cfg)
	mustAdd(t, l, Record{ID: "ok", Start: 10, End: 15, Amount: 3})
	mustAdd(t, l, Record{ID: "zero", Start: 10, End: 20, Amount: 0}) // amount 为 0 合法

	recordCases := []struct {
		name string
		rec  Record
		want error
	}{
		{"s 早于 t0", Record{ID: "x1", Start: 9, End: 12, Amount: 1}, ErrStartBeforeT0},
		{"区间非法", Record{ID: "x2", Start: 15, End: 15, Amount: 1}, ErrInvalidInterval},
		{"amount 为负", Record{ID: "x3", Start: 10, End: 12, Amount: -1}, ErrNegativeAmount},
		{"标识重复", Record{ID: "ok", Start: 10, End: 12, Amount: 1}, ErrDuplicateID},
		// 多因同时成立时按固定顺序只报第一个。
		{"多因: 早于t0+区间非法+负量+重复", Record{ID: "ok", Start: 5, End: 5, Amount: -2}, ErrStartBeforeT0},
		{"多因: 区间非法+负量+重复", Record{ID: "ok", Start: 12, End: 12, Amount: -2}, ErrInvalidInterval},
		{"多因: 负量+重复", Record{ID: "ok", Start: 10, End: 12, Amount: -2}, ErrNegativeAmount},
	}
	for _, c := range recordCases {
		err := l.AddRecord(c.rec)
		t.Logf("输入: %+v -> 输出: %v (判定依据: %s)", c.rec, err, c.name)
		if !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, 期望 %v", c.name, err, c.want)
		}
	}

	if _, err := l.Query(-1); !errors.Is(err, ErrNegativePeriodIndex) {
		t.Errorf("Query(-1): err = %v, 期望 %v", err, ErrNegativePeriodIndex)
	}
	t.Logf("输入: Query(-1) -> 输出: %v (判定依据: 查询周期不能为负)", ErrNegativePeriodIndex)

	// 被拒绝的操作不得改变任何账目。
	rep := mustQuery(t, l, 0)
	if rep.Usage != 3 {
		t.Errorf("拒绝非法操作后周期 0 用量 = %d, 期望仍为 3", rep.Usage)
	}
	t.Logf("输出: 全部拒绝后周期0=%+v (判定依据: 被拒绝的操作不改变账目)", rep)
}

// 记录与查询可被并发调用（配合 -race 运行）。
func TestConcurrentAccess(t *testing.T) {
	l := mustLedger(t, Config{T0: 0, Period: 10, Quota: 100, MaxCarry: 50})

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				id := fmt.Sprintf("w%d-r%d", i, j)
				if err := l.AddRecord(Record{ID: id, Start: int64(j), End: int64(j + 20), Amount: int64(j)}); err != nil {
					t.Errorf("AddRecord(%s) 失败: %v", id, err)
					return
				}
				if _, err := l.Query(int64(j % 5)); err != nil {
					t.Errorf("Query 失败: %v", err)
					return
				}
			}
		}(i)
	}
	wg.Wait()

	// 并发写入完成后，结果与顺序无关的确定性快照一致。
	var total int64
	for k := int64(0); k < 10; k++ {
		total += mustQuery(t, l, k).Usage
	}
	var want int64
	for j := 0; j < 50; j++ {
		want += int64(j) * 8
	}
	t.Logf("输入: 8 协程各写 50 条记录并穿插查询")
	t.Logf("输出: 前 10 周期总用量 %d, 记录总量 %d", total, want)
	t.Logf("判定依据: 并发安全(-race)且摊入总量守恒")
	if total != want {
		t.Errorf("并发后摊入总量 = %d, 期望 %d", total, want)
	}
}
