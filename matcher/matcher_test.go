package matcher

import (
	"errors"
	"reflect"
	"testing"
)

func mustAdd(t *testing.T, m *Matcher, id, fam, start, n, h, up, d int64) {
	t.Helper()
	if err := m.AddCommit(id, fam, start, n, h, up, d); err != nil {
		t.Fatalf("AddCommit(%d,%d,%d,%d,%d,%d,%d) = %v, want nil", id, fam, start, n, h, up, d, err)
	}
}

func mustApply(t *testing.T, m *Matcher, hour int64, lines []Line) []HourReport {
	t.Helper()
	reps, err := m.Apply(hour, lines)
	if err != nil {
		t.Fatalf("Apply(%d, %v) = %v, want nil", hour, lines, err)
	}
	return reps
}

// TestSpecExample 复现需求中的完整示例。
func TestSpecExample(t *testing.T) {
	m := New()
	mustAdd(t, m, 1, 0, 0, 3, 100, 100, 3000)
	mustAdd(t, m, 2, 7, 0, 2, 50, 0, 5000)

	reps := mustApply(t, m, 0, []Line{{Fam: 7, P: 120}, {Fam: 5, P: 200}})
	if len(reps) != 1 {
		t.Fatalf("got %d reports, want 1", len(reps))
	}
	r := reps[0]
	if r.Bill != 261 {
		t.Errorf("hour0 bill = %d, want 261", r.Bill)
	}
	want := []CommitReport{
		{ID: 2, Used: 50, Unused: 0, Covered: 100},
		{ID: 1, Used: 100, Unused: 0, Covered: 142},
	}
	if !reflect.DeepEqual(r.Commits, want) {
		t.Errorf("hour0 commits = %+v, want %+v", r.Commits, want)
	}

	reps = mustApply(t, m, 3, nil)
	if len(reps) != 3 {
		t.Fatalf("got %d reports, want 3", len(reps))
	}
	bills := []int64{reps[0].Bill, reps[1].Bill, reps[2].Bill}
	if !reflect.DeepEqual(bills, []int64{183, 134, 0}) {
		t.Errorf("skipped-hour bills = %v, want [183 134 0]", bills)
	}
	// 小时 2 是承诺 1 的最后有效小时，摊尾项 34。
	if reps[1].Commits[0].ID != 1 || reps[1].Bill != 134 {
		t.Errorf("hour2 report = %+v, want bill 134 from commit 1 tail amortization", reps[1])
	}
}

// TestDiscountTieOrder 验证 d 并列时按 id 升序处理与报告。
// TestDiscountTieOrder 验证 d 并列时按 id 升序处理与报告。
func TestDiscountTieOrder(t *testing.T) {
	m := New()
	mustAdd(t, m, 9, 0, 0, 1, 100, 0, 5000)
	mustAdd(t, m, 3, 0, 0, 1, 100, 0, 5000)
	mustAdd(t, m, 7, 0, 0, 1, 100, 0, 5000)

	reps := mustApply(t, m, 0, []Line{{Fam: 1, P: 200}})
	ids := []int64{reps[0].Commits[0].ID, reps[0].Commits[1].ID, reps[0].Commits[2].ID}
	if !reflect.DeepEqual(ids, []int64{3, 7, 9}) {
		t.Errorf("commit order = %v, want [3 7 9]", ids)
	}
	// id=3 先覆盖：e=100，R=100 恰好整行覆盖。
	if reps[0].Commits[0].Used != 100 || reps[0].Commits[0].Covered != 200 {
		t.Errorf("unexpected first commit report: %+v", reps[0].Commits[0])
	}
}

// TestRoundingBoundary 验证 e 向上取整、q 向下取整的取等边界：
// R 恰等于 e 为整行覆盖，R 比 e 小 1 为部分覆盖。
func TestRoundingBoundary(t *testing.T) {
	// d=5000 -> m=5000，p=120 -> e=ceil(120*5000/10000)=60。
	// R=60 恰等于 e：整行覆盖。
	m := New()
	mustAdd(t, m, 1, 0, 0, 1, 60, 0, 5000)
	reps := mustApply(t, m, 0, []Line{{Fam: 1, P: 120}})
	cr := reps[0].Commits[0]
	if cr.Used != 60 || cr.Unused != 0 || cr.Covered != 120 {
		t.Errorf("R==e: got %+v, want used=60 unused=0 covered=120", cr)
	}
	if reps[0].Bill != 60 {
		t.Errorf("R==e: bill = %d, want 60 (无剩余按需价)", reps[0].Bill)
	}

	// R=59 比 e 小 1：部分覆盖 q=floor(59*10000/5000)=118，剩余 2。
	m = New()
	mustAdd(t, m, 1, 0, 0, 1, 59, 0, 5000)
	reps = mustApply(t, m, 0, []Line{{Fam: 1, P: 120}})
	cr = reps[0].Commits[0]
	if cr.Used != 59 || cr.Unused != 0 || cr.Covered != 118 {
		t.Errorf("R==e-1: got %+v, want used=59 unused=0 covered=118", cr)
	}
	if reps[0].Bill != 59+2 {
		t.Errorf("R==e-1: bill = %d, want 61", reps[0].Bill)
	}

	// 非整除的向上取整：p=101, m=7000 -> e=ceil(70.7)=71。
	m = New()
	mustAdd(t, m, 1, 0, 0, 1, 71, 0, 3000)
	reps = mustApply(t, m, 0, []Line{{Fam: 1, P: 101}})
	cr = reps[0].Commits[0]
	if cr.Used != 71 || cr.Covered != 101 {
		t.Errorf("ceil edge: got %+v, want used=71 covered=101", cr)
	}
}

// TestPartialThenChained 验证部分覆盖后的剩余按需价仍可被后续承诺覆盖。
func TestPartialThenChained(t *testing.T) {
	m := New()
	mustAdd(t, m, 1, 0, 0, 1, 50, 0, 5000) // m=5000，先处理
	mustAdd(t, m, 2, 0, 0, 1, 100, 0, 3000)
	reps := mustApply(t, m, 0, []Line{{Fam: 1, P: 120}})
	// 承诺 1：e=60 > R=50，部分覆盖 q=100，剩余 20。
	// 承诺 2：e=ceil(20*7000/10000)=14，整行覆盖。
	want := []CommitReport{
		{ID: 1, Used: 50, Unused: 0, Covered: 100},
		{ID: 2, Used: 14, Unused: 86, Covered: 20},
	}
	if !reflect.DeepEqual(reps[0].Commits, want) {
		t.Errorf("commits = %+v, want %+v", reps[0].Commits, want)
	}
	if reps[0].Bill != 50+100 {
		t.Errorf("bill = %d, want 150 (无剩余按需价)", reps[0].Bill)
	}
}

// TestFamilyMatching 验证族不符的行不被覆盖、通用承诺（fam=0）覆盖任意族。
func TestFamilyMatching(t *testing.T) {
	m := New()
	mustAdd(t, m, 1, 7, 0, 1, 1000, 0, 5000) // 只覆盖族 7
	mustAdd(t, m, 2, 0, 0, 1, 1000, 0, 1000) // 通用
	reps := mustApply(t, m, 0, []Line{{Fam: 3, P: 100}, {Fam: 7, P: 100}})
	// 承诺 1（d 大先处理）：跳过族 3 的行，覆盖族 7 的行（e=50）。
	// 承诺 2：覆盖族 3 的行（e=90）。
	want := []CommitReport{
		{ID: 1, Used: 50, Unused: 950, Covered: 100},
		{ID: 2, Used: 90, Unused: 910, Covered: 100},
	}
	if !reflect.DeepEqual(reps[0].Commits, want) {
		t.Errorf("commits = %+v, want %+v", reps[0].Commits, want)
	}
	if reps[0].Bill != 2000 {
		t.Errorf("bill = %d, want 2000", reps[0].Bill)
	}
}

// TestAmortizationTail 验证 up 不能被 n 整除时的摊销与尾项。
func TestAmortizationTail(t *testing.T) {
	m := New()
	mustAdd(t, m, 1, 0, 5, 3, 10, 100, 1000) // 100/3: 33, 33, 34
	reps := mustApply(t, m, 8, nil)          // 小时 0..8，承诺在 [5,8) 有效
	bills := []int64{reps[5].Bill, reps[6].Bill, reps[7].Bill}
	if !reflect.DeepEqual(bills, []int64{43, 43, 44}) {
		t.Errorf("bills = %v, want [43 43 44]", bills)
	}
	if reps[8].Bill != 0 {
		t.Errorf("hour8 bill = %d, want 0 (已过期)", reps[8].Bill)
	}
	// up=0 时摊销全为 0。
	m = New()
	mustAdd(t, m, 1, 0, 0, 2, 10, 0, 1000)
	reps = mustApply(t, m, 1, nil)
	if reps[0].Bill != 10 || reps[1].Bill != 10 {
		t.Errorf("up=0 bills = [%d %d], want [10 10]", reps[0].Bill, reps[1].Bill)
	}
}

// TestExpiryRightEdge 验证有效区间右端恰等于小时即过期（左闭右开）。
func TestExpiryRightEdge(t *testing.T) {
	m := New()
	mustAdd(t, m, 1, 0, 0, 2, 10, 0, 1000) // 有效 [0,2)
	reps := mustApply(t, m, 2, nil)
	if reps[0].Bill != 10 || reps[1].Bill != 10 || reps[2].Bill != 0 {
		t.Errorf("bills = [%d %d %d], want [10 10 0]",
			reps[0].Bill, reps[1].Bill, reps[2].Bill)
	}
	if len(reps[2].Commits) != 0 {
		t.Errorf("hour2 commits = %+v, want empty (已过期)", reps[2].Commits)
	}
}

// TestSkipHoursAcrossExpiry 验证跳过小时跨过承诺到期点的补算。
func TestSkipHoursAcrossExpiry(t *testing.T) {
	m := New()
	mustAdd(t, m, 1, 0, 0, 3, 100, 7, 1000) // 摊销 2,2,3
	mustAdd(t, m, 2, 0, 2, 2, 50, 0, 2000)  // 有效 [2,4)
	reps := mustApply(t, m, 5, nil)         // 小时 0..5
	want := []int64{102, 102, 103 + 50, 50, 0, 0}
	var got []int64
	for _, r := range reps {
		got = append(got, r.Bill)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("bills = %v, want %v", got, want)
	}
}

// TestRejections 验证各类拒绝及校验顺序（只报第一个错误）。
func TestRejections(t *testing.T) {
	m := New()
	mustAdd(t, m, 1, 0, 0, 2, 10, 0, 1000)
	mustApply(t, m, 1, nil) // lastHour=1

	// AddCommit 参数越界。
	badAdds := [][7]int64{
		{-1, 0, 5, 1, 1, 0, 1},       // id < 0
		{1e6 + 1, 0, 5, 1, 1, 0, 1},  // id > 1e6
		{2, -1, 5, 1, 1, 0, 1},       // fam < 0
		{2, 100, 5, 1, 1, 0, 1},      // fam > 99
		{2, 0, -1, 1, 1, 0, 1},       // start < 0
		{2, 0, 5, 0, 1, 0, 1},        // n < 1
		{2, 0, 5, 1e5 + 1, 1, 0, 1},  // n > 1e5
		{2, 0, 5, 1, 0, 0, 1},        // h < 1
		{2, 0, 5, 1, 1e9 + 1, 0, 1},  // h > 1e9
		{2, 0, 5, 1, 1, -1, 1},       // up < 0
		{2, 0, 5, 1, 1, 1e12 + 1, 1}, // up > 1e12
		{2, 0, 5, 1, 1, 0, 0},        // d < 1
		{2, 0, 5, 1, 1, 0, 10000},    // d > 9999
		{1, 100, 0, 1, 1, 0, 1},      // 参数非法优先于 id 重复与起始已过
	}
	for _, a := range badAdds {
		if err := m.AddCommit(a[0], a[1], a[2], a[3], a[4], a[5], a[6]); !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("AddCommit%v = %v, want ErrInvalidArgument", a, err)
		}
	}
	// id 重复优先于起始已过。
	if err := m.AddCommit(1, 0, 0, 1, 1, 0, 1); !errors.Is(err, ErrDuplicateID) {
		t.Errorf("dup id err = %v, want ErrDuplicateID", err)
	}
	// 起始已过：start <= lastHour。
	if err := m.AddCommit(2, 0, 1, 1, 1, 0, 1); !errors.Is(err, ErrStartPassed) {
		t.Errorf("start==lastHour err = %v, want ErrStartPassed", err)
	}
	if err := m.AddCommit(2, 0, 0, 1, 1, 0, 1); !errors.Is(err, ErrStartPassed) {
		t.Errorf("start<lastHour err = %v, want ErrStartPassed", err)
	}

	// Apply 参数越界。
	if _, err := m.Apply(-1, nil); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("Apply(-1) = %v, want ErrInvalidArgument", err)
	}
	if _, err := m.Apply(m.lastHour+10001, nil); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("Apply(lastHour+10001) = %v, want ErrInvalidArgument", err)
	}
	if _, err := m.Apply(2, make([]Line, 1001)); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("Apply(1001 lines) = %v, want ErrInvalidArgument", err)
	}
	for _, ln := range []Line{{0, 1}, {100, 1}, {1, 0}, {1, 1e9 + 1}} {
		if _, err := m.Apply(2, []Line{ln}); !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("Apply(line %+v) = %v, want ErrInvalidArgument", ln, err)
		}
	}
	// 参数非法优先于时间回退。
	if _, err := m.Apply(0, []Line{{0, 1}}); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("Apply(invalid+regression) = %v, want ErrInvalidArgument", err)
	}
	// 时间回退。
	if _, err := m.Apply(1, nil); !errors.Is(err, ErrTimeRegression) {
		t.Errorf("Apply(1) = %v, want ErrTimeRegression", err)
	}
	if _, err := m.Apply(0, nil); !errors.Is(err, ErrTimeRegression) {
		t.Errorf("Apply(0) = %v, want ErrTimeRegression", err)
	}
}

// TestRejectionKeepsState 验证被拒绝的操作不改变承诺表与 lastHour。
func TestRejectionKeepsState(t *testing.T) {
	m := New()
	mustAdd(t, m, 1, 0, 0, 3, 10, 0, 1000)
	mustApply(t, m, 1, nil) // lastHour=1

	// 一批被拒绝的操作。
	_ = m.AddCommit(2, 0, 0, 1, 1, 0, 1)      // 起始已过
	_ = m.AddCommit(1, 0, 5, 1, 1, 0, 1)      // id 重复
	_ = m.AddCommit(3, 0, 5, 0, 1, 0, 1)      // 参数非法
	_, _ = m.Apply(1, nil)                    // 时间回退
	_, _ = m.Apply(2, []Line{{Fam: 0, P: 1}}) // 参数非法

	// lastHour 仍为 1，承诺表仍只有 id=1。
	reps := mustApply(t, m, 2, nil)
	if len(reps) != 1 || reps[0].Hour != 2 {
		t.Fatalf("reports = %+v, want single report for hour 2", reps)
	}
	if reps[0].Bill != 10 || len(reps[0].Commits) != 1 || reps[0].Commits[0].ID != 1 {
		t.Errorf("hour2 report = %+v, want bill 10 from commit 1 only", reps[0])
	}
	// id=2、3 未被登记。
	mustAdd(t, m, 2, 0, 3, 1, 1, 0, 1)
	mustAdd(t, m, 3, 0, 3, 1, 1, 0, 1)
}
