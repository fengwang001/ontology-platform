package sla

import (
	"errors"
	"sync"
	"testing"
)

func exampleParams() Params {
	return Params{
		L: 43200, G: 5, Ex: 100,
		T1: 999000, T2: 990000, T3: 950000,
		C1: 10, C2: 25, C3: 100,
		St: 5, Sm: 4, Y: 2500,
	}
}

func mustNew(t *testing.T, p Params) *Settler {
	t.Helper()
	st, err := New(p)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return st
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func mustClose(t *testing.T, st *Settler) Result {
	t.Helper()
	res, err := st.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	return res
}

// 题目给出的三个月示例。
func TestSpecExample(t *testing.T) {
	st := mustNew(t, exampleParams())

	report := func() {
		must(t, st.Report(100, 130))
		must(t, st.Report(130, 140))
		must(t, st.Report(1000, 1060))
		must(t, st.Report(500, 503))
		must(t, st.AddExclude(110, 120))
	}

	must(t, st.NewMonth(10000))
	report()
	res := mustClose(t, st)
	want := Result{D: 90, A: 997916, Base: 10, Pct: 10, Credit: 1000}
	if res != want {
		t.Fatalf("month0 = %+v, want %+v", res, want)
	}

	must(t, st.NewMonth(10000))
	report()
	res = mustClose(t, st)
	want = Result{D: 90, A: 997916, Base: 10, Pct: 15, Credit: 1500}
	if res != want {
		t.Fatalf("month1 = %+v, want %+v", res, want)
	}
	if got := st.State().YearPaid[0]; got != 2500 {
		t.Fatalf("yearPaid = %d, want 2500", got)
	}

	must(t, st.NewMonth(10000))
	report()
	res = mustClose(t, st)
	if res.D != 90 || res.A != 997916 || res.Base != 10 || res.Pct != 20 || res.Credit != 0 {
		t.Fatalf("month2 = %+v, want D=90 A=997916 base=10 pct=20 credit=0", res)
	}
	if got := st.State().S; got != 3 {
		t.Fatalf("s = %d, want 3", got)
	}
	if got := st.State().YearPaid[0]; got != 2500 {
		t.Fatalf("yearPaid = %d, want 2500 (cap not exceeded)", got)
	}
}

// 重叠与首尾相接的故障归并。
func TestMergeOverlapAndAdjacent(t *testing.T) {
	p := exampleParams()
	p.L, p.G, p.Ex, p.Y = 100, 0, 0, 0
	st := mustNew(t, p)
	must(t, st.NewMonth(1))
	must(t, st.Report(10, 20))
	must(t, st.Report(15, 30)) // 重叠
	must(t, st.Report(30, 40)) // 首尾相接
	must(t, st.Report(50, 60))
	res := mustClose(t, st)
	if res.D != 40 { // [10,40) + [50,60)
		t.Fatalf("D = %d, want 40", res.D)
	}
}

// 排除窗口把一段故障切成两段。
func TestExcludeSplitsSegment(t *testing.T) {
	p := exampleParams()
	p.L, p.G, p.Ex, p.Y = 100, 0, 100, 0
	st := mustNew(t, p)
	must(t, st.NewMonth(1))
	must(t, st.Report(10, 90))
	must(t, st.AddExclude(40, 50))
	res := mustClose(t, st)
	if res.D != 70 { // [10,40) + [50,90)
		t.Fatalf("D = %d, want 70", res.D)
	}
}

// 先扣排除再丢弃短段：8 分钟故障扣 5 分钟排除剩 3 分钟，g=4 应被丢弃；
// 若顺序反过来（先丢弃再扣）则会错误地保留 3 分钟。
func TestSubtractBeforeJitterDrop(t *testing.T) {
	p := exampleParams()
	p.L, p.G, p.Ex, p.Y = 10, 4, 10, 0
	st := mustNew(t, p)
	must(t, st.NewMonth(1))
	must(t, st.Report(0, 8))
	must(t, st.AddExclude(0, 5))
	res := mustClose(t, st)
	if res.D != 0 || res.Base != 0 {
		t.Fatalf("D=%d base=%d, want D=0 base=0 (残段 3<g=4 被丢弃)", res.D, res.Base)
	}
	if res.A != 1_000_000 {
		t.Fatalf("A = %d, want 1000000", res.A)
	}
}

// 残段长度恰等于 g 保留，少 1 丢弃。
func TestJitterBoundary(t *testing.T) {
	p := exampleParams()
	p.L, p.G, p.Ex, p.Y = 100, 5, 0, 0

	st := mustNew(t, p)
	must(t, st.NewMonth(1))
	must(t, st.Report(0, 5)) // 恰等于 g，保留
	res := mustClose(t, st)
	if res.D != 5 {
		t.Fatalf("D = %d, want 5 (len==g 保留)", res.D)
	}

	st2 := mustNew(t, p)
	must(t, st2.NewMonth(1))
	must(t, st2.Report(0, 4)) // 少 1，丢弃
	res2 := mustClose(t, st2)
	if res2.D != 0 {
		t.Fatalf("D = %d, want 0 (len==g-1 丢弃)", res2.D)
	}
}

// A 向下取整。
func TestAvailabilityFloor(t *testing.T) {
	p := exampleParams()
	p.L, p.G, p.Ex, p.Y = 3, 0, 0, 0
	st := mustNew(t, p)
	must(t, st.NewMonth(1))
	must(t, st.Report(0, 1))
	res := mustClose(t, st)
	if res.A != 666666 { // floor(2*1e6/3)
		t.Fatalf("A = %d, want 666666", res.A)
	}
}

// A 恰等于各档位 T 时的归属。
func TestTierBoundaries(t *testing.T) {
	// L=1e6 时 A = L-D 精确成立。
	base := Params{
		L: 1_000_000, G: 0, Ex: 0,
		T1: 999000, T2: 990000, T3: 950000,
		C1: 10, C2: 25, C3: 100,
		St: 0, Sm: 0, Y: 0,
	}
	cases := []struct {
		name     string
		d        int64
		wantA    int64
		wantBase int64
	}{
		{"A==T1", 1000, 999000, 0},
		{"A==T1-1", 1001, 998999, 10},
		{"A==T2", 10000, 990000, 10},
		{"A==T2-1", 10001, 989999, 25},
		{"A==T3", 50000, 950000, 25},
		{"A==T3-1", 50001, 949999, 100},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := mustNew(t, base)
			must(t, st.NewMonth(1))
			must(t, st.Report(0, tc.d))
			res := mustClose(t, st)
			if res.A != tc.wantA || res.Base != tc.wantBase {
				t.Fatalf("A=%d base=%d, want A=%d base=%d", res.A, res.Base, tc.wantA, tc.wantBase)
			}
		})
	}
}

// credit 向上取整。
func TestCreditCeil(t *testing.T) {
	p := exampleParams()
	p.L, p.G, p.Ex, p.Y = 100, 0, 0, 1_000_000_000_000
	p.T1, p.T2, p.T3 = 999999, 999990, 999900
	p.C1, p.C2, p.C3 = 10, 20, 30
	st := mustNew(t, p)
	must(t, st.NewMonth(101))
	must(t, st.Report(0, 1)) // A=990000 < T3，base=30
	res := mustClose(t, st)
	if res.Base != 30 {
		t.Fatalf("base = %d, want 30", res.Base)
	}
	// ceil(101*30/100) = ceil(30.3) = 31
	if res.Credit != 31 {
		t.Fatalf("credit = %d, want 31", res.Credit)
	}
}

// s 超过 sm 后不再升级。
func TestEscalationCapMonths(t *testing.T) {
	p := exampleParams()
	p.L, p.G, p.Ex, p.Y = 100, 0, 0, 0
	p.T1, p.T2, p.T3 = 999999, 990000, 99900
	p.C1, p.C2, p.C3 = 10, 20, 30
	p.St, p.Sm = 10, 2
	st := mustNew(t, p)
	wantPcts := []int64{10, 20, 30, 30, 30}
	for i, want := range wantPcts {
		must(t, st.NewMonth(1000))
		must(t, st.Report(0, 1)) // A=990000==T2 → base=10
		res := mustClose(t, st)
		if res.Base != 10 || res.Pct != want {
			t.Fatalf("month %d: base=%d pct=%d, want base=10 pct=%d", i, res.Base, res.Pct, want)
		}
	}
	if got := st.State().S; got != 5 {
		t.Fatalf("s = %d, want 5 (s 持续增长，仅升级幅度被封顶)", got)
	}
}

// 升级后 pct 封顶 100。
func TestPctCap100(t *testing.T) {
	p := exampleParams()
	p.L, p.G, p.Ex, p.Y = 100, 0, 0, 1_000_000_000_000
	p.T1, p.T2, p.T3 = 999999, 990000, 99900
	p.C1, p.C2, p.C3 = 10, 20, 90
	p.St, p.Sm = 20, 12
	st := mustNew(t, p)
	must(t, st.NewMonth(1000))
	must(t, st.Report(0, 95)) // A=50000 < T3=99900 → base=90
	res := mustClose(t, st)
	if res.Pct != 90 {
		t.Fatalf("pct = %d, want 90", res.Pct)
	}
	must(t, st.NewMonth(1000))
	must(t, st.Report(0, 95))
	res = mustClose(t, st)
	if res.Pct != 100 { // 90+20=110 → 封顶 100
		t.Fatalf("pct = %d, want 100 (capped)", res.Pct)
	}
	if res.Credit != 1000 {
		t.Fatalf("credit = %d, want 1000", res.Credit)
	}
}

// 年度额度截断发生在升级之后。
func TestAnnualCapAfterEscalation(t *testing.T) {
	p := exampleParams()
	p.L, p.G, p.Ex, p.Y = 100, 0, 0, 250
	p.T1, p.T2, p.T3 = 999999, 990000, 99900
	p.C1, p.C2, p.C3 = 10, 20, 30
	p.St, p.Sm = 10, 12
	st := mustNew(t, p)

	must(t, st.NewMonth(1000))
	must(t, st.Report(0, 1)) // base=10, s=0 → pct=10, credit=100
	res := mustClose(t, st)
	if res.Pct != 10 || res.Credit != 100 {
		t.Fatalf("month0 = %+v, want pct=10 credit=100", res)
	}

	must(t, st.NewMonth(1000))
	must(t, st.Report(0, 1)) // s=1 → pct=20, 升级后 200，被年度剩余 150 截断
	res = mustClose(t, st)
	if res.Pct != 20 || res.Credit != 150 {
		t.Fatalf("month1 = %+v, want pct=20 credit=150 (截断发生在升级之后)", res)
	}
	if got := st.State().YearPaid[0]; got != 250 {
		t.Fatalf("yearPaid = %d, want 250", got)
	}
}

// 跨年边界：年度额度重置而 s 不重置。
func TestYearBoundary(t *testing.T) {
	p := exampleParams()
	p.L, p.G, p.Ex, p.Y = 100, 0, 0, 300
	p.T1, p.T2, p.T3 = 999999, 990000, 99900
	p.C1, p.C2, p.C3 = 10, 20, 30
	p.St, p.Sm = 10, 12
	st := mustNew(t, p)

	// 第 0 年连续 12 个违约月，每年额度 300 在第 3 个月耗尽。
	for i := 0; i < 12; i++ {
		must(t, st.NewMonth(1000))
		must(t, st.Report(0, 1))
		mustClose(t, st)
	}
	if got := st.State().YearPaid[0]; got != 300 {
		t.Fatalf("year0 paid = %d, want 300", got)
	}
	if got := st.State().S; got != 12 {
		t.Fatalf("s = %d, want 12", got)
	}

	// 第 1 年第 1 个月：额度重置，s 继续（不重置），pct=10+10*12=130→100。
	must(t, st.NewMonth(1000))
	must(t, st.Report(0, 1))
	res := mustClose(t, st)
	if res.Pct != 100 || res.Credit != 300 { // 升级后 1000，被新一年额度 300 截断
		t.Fatalf("year1 month0 = %+v, want pct=100 credit=300", res)
	}
	if got := st.State().YearPaid[1]; got != 300 {
		t.Fatalf("year1 paid = %d, want 300", got)
	}
	if got := st.State().S; got != 13 {
		t.Fatalf("s = %d, want 13 (跨年不重置)", got)
	}
}

// 无违约月使 s 归零。
func TestCleanMonthResetsS(t *testing.T) {
	p := exampleParams()
	p.L, p.G, p.Ex, p.Y = 100, 0, 0, 0
	p.T1, p.T2, p.T3 = 999999, 990000, 99900
	p.C1, p.C2, p.C3 = 10, 20, 30
	p.St, p.Sm = 10, 12
	st := mustNew(t, p)

	for i := 0; i < 2; i++ {
		must(t, st.NewMonth(1000))
		must(t, st.Report(0, 1))
		mustClose(t, st)
	}
	if got := st.State().S; got != 2 {
		t.Fatalf("s = %d, want 2", got)
	}

	must(t, st.NewMonth(1000)) // 无故障，base=0
	res := mustClose(t, st)
	if res.Base != 0 || res.Credit != 0 {
		t.Fatalf("clean month = %+v, want base=0 credit=0", res)
	}
	if got := st.State().S; got != 0 {
		t.Fatalf("s = %d, want 0 (无违约月归零)", got)
	}

	must(t, st.NewMonth(1000))
	must(t, st.Report(0, 1))
	res = mustClose(t, st)
	if res.Pct != 10 { // s 已归零，回到基础档
		t.Fatalf("pct = %d, want 10 (s 归零后重新起算)", res.Pct)
	}
}

// Revoke 只删一份完全相同的区间。
func TestRevokeRemovesOneCopy(t *testing.T) {
	p := exampleParams()
	p.L, p.G, p.Ex, p.Y = 100, 0, 0, 0
	st := mustNew(t, p)
	must(t, st.NewMonth(1))
	must(t, st.Report(0, 10))
	must(t, st.Report(0, 10))
	must(t, st.Revoke(0, 10))
	res := mustClose(t, st)
	if res.D != 10 {
		t.Fatalf("D = %d, want 10 (只删一份)", res.D)
	}
	if err := st.NewMonth(1); err != nil {
		t.Fatal(err)
	}
	if err := st.Revoke(0, 10); !errors.Is(err, ErrIntervalNotFound) {
		t.Fatalf("revoke missing = %v, want ErrIntervalNotFound", err)
	}
}

// 排除并集恰等于 ex 通过、多 1 拒绝、重叠窗口不重复计长。
func TestExclusionUnionCap(t *testing.T) {
	p := exampleParams() // L=43200, ex=100
	st := mustNew(t, p)
	must(t, st.NewMonth(1))
	must(t, st.AddExclude(110, 120))
	must(t, st.AddExclude(110, 120)) // 完全相同的窗口不重复计长
	must(t, st.AddExclude(115, 205)) // 并集 [110,205) 长 95，通过
	if err := st.AddExclude(300, 306); !errors.Is(err, ErrExclusionLimit) {
		t.Fatalf("add (300,306) = %v, want ErrExclusionLimit (并集 101>100)", err)
	}
	must(t, st.AddExclude(300, 305)) // 并集恰为 100，通过
	if err := st.AddExclude(400, 401); !errors.Is(err, ErrExclusionLimit) {
		t.Fatalf("add (400,401) = %v, want ErrExclusionLimit (并集 101>100)", err)
	}
}

// 被拒绝的操作不得改变任何状态。
func TestRejectedOpsKeepState(t *testing.T) {
	p := exampleParams() // L=43200, g=5, ex=100
	st := mustNew(t, p)
	must(t, st.NewMonth(10000))
	must(t, st.Report(0, 100))
	must(t, st.AddExclude(0, 100)) // 并集 100 == ex，通过

	// 各类拒绝。
	if err := st.AddExclude(200, 201); !errors.Is(err, ErrExclusionLimit) {
		t.Fatalf("add exclude = %v, want ErrExclusionLimit", err)
	}
	if err := st.Revoke(5, 9); !errors.Is(err, ErrIntervalNotFound) {
		t.Fatalf("revoke = %v, want ErrIntervalNotFound", err)
	}
	if err := st.Report(50, 50); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("report empty = %v, want ErrInvalidParam", err)
	}
	if err := st.Report(-1, 5); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("report negative = %v, want ErrInvalidParam", err)
	}
	if err := st.Report(0, 43201); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("report overflow = %v, want ErrInvalidParam", err)
	}
	if err := st.NewMonth(100); !errors.Is(err, ErrMonthOpen) {
		t.Fatalf("new month = %v, want ErrMonthOpen", err)
	}

	// 状态未被改变：故障 [0,100) 被排除 [0,100) 全扣，D=0。
	res := mustClose(t, st)
	if res.D != 0 || res.Base != 0 {
		t.Fatalf("after rejections: %+v, want D=0 base=0", res)
	}
	if st.State().Open {
		t.Fatal("month should be closed")
	}
	if _, err := st.Close(); !errors.Is(err, ErrNoOpenMonth) {
		t.Fatalf("close again = %v, want ErrNoOpenMonth", err)
	}
}

// 错误优先级：参数非法 > 状态类 > 排除超限 > 不存在。
func TestErrorPrecedence(t *testing.T) {
	p := exampleParams()
	st := mustNew(t, p)

	// 无开放月 + 非法区间 → 参数非法优先。
	if err := st.Report(5, 5); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("report = %v, want ErrInvalidParam", err)
	}
	if err := st.AddExclude(5, 5); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("addExclude = %v, want ErrInvalidParam", err)
	}
	if err := st.Revoke(5, 5); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("revoke = %v, want ErrInvalidParam", err)
	}
	// 无开放月 + 合法区间 → 无开放月。
	if err := st.Report(0, 1); !errors.Is(err, ErrNoOpenMonth) {
		t.Fatalf("report = %v, want ErrNoOpenMonth", err)
	}
	if err := st.Revoke(0, 1); !errors.Is(err, ErrNoOpenMonth) {
		t.Fatalf("revoke = %v, want ErrNoOpenMonth", err)
	}
	// 开放月 + 非法 fee → 参数非法优先于月已开放。
	must(t, st.NewMonth(100))
	if err := st.NewMonth(0); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("newMonth(0) = %v, want ErrInvalidParam", err)
	}
	if err := st.NewMonth(1_000_000_000_001); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("newMonth(too big) = %v, want ErrInvalidParam", err)
	}
	if err := st.NewMonth(50); !errors.Is(err, ErrMonthOpen) {
		t.Fatalf("newMonth = %v, want ErrMonthOpen", err)
	}
	// Revoke 合法但不存在 → 不存在。
	if err := st.Revoke(0, 1); !errors.Is(err, ErrIntervalNotFound) {
		t.Fatalf("revoke = %v, want ErrIntervalNotFound", err)
	}
	mustClose(t, st)
}

// 构造参数非法整体拒绝。
func TestInvalidParams(t *testing.T) {
	good := exampleParams()
	bads := []Params{}
	mut := func(f func(*Params)) {
		p := good
		f(&p)
		bads = append(bads, p)
	}
	mut(func(p *Params) { p.L = 0 })
	mut(func(p *Params) { p.L = 1_000_001 })
	mut(func(p *Params) { p.G = -1 })
	mut(func(p *Params) { p.G = p.L + 1 })
	mut(func(p *Params) { p.T1 = p.T2 })
	mut(func(p *Params) { p.T2 = p.T3 })
	mut(func(p *Params) { p.T3 = 0 })
	mut(func(p *Params) { p.T1 = 1_000_001 })
	mut(func(p *Params) { p.C1 = p.C2 })
	mut(func(p *Params) { p.C2 = p.C3 })
	mut(func(p *Params) { p.C1 = 0 })
	mut(func(p *Params) { p.C3 = 101 })
	mut(func(p *Params) { p.St = -1 })
	mut(func(p *Params) { p.St = 101 })
	mut(func(p *Params) { p.Sm = -1 })
	mut(func(p *Params) { p.Sm = 13 })
	mut(func(p *Params) { p.Y = -1 })
	mut(func(p *Params) { p.Y = 1_000_000_000_001 })
	mut(func(p *Params) { p.Ex = -1 })
	mut(func(p *Params) { p.Ex = p.L + 1 })
	for i, p := range bads {
		if _, err := New(p); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("case %d: err = %v, want ErrInvalidParam", i, err)
		}
	}
	if _, err := New(good); err != nil {
		t.Fatalf("good params rejected: %v", err)
	}
}

// 并发调用：结果等价于某个串行顺序，且不变量始终成立。
func TestConcurrent(t *testing.T) {
	p := exampleParams()
	p.Y = 1_000_000_000
	st := mustNew(t, p)

	const workers = 8
	const months = 60
	var wg sync.WaitGroup
	errs := make(chan error, workers*months*8)

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < months; i++ {
				if err := st.NewMonth(10000); err != nil && !errors.Is(err, ErrMonthOpen) {
					errs <- err
				}
				a := int64((w*7 + i*13) % 40000)
				if err := st.Report(a, a+100); err != nil && !errors.Is(err, ErrNoOpenMonth) {
					errs <- err
				}
				if err := st.AddExclude(a, a+10); err != nil &&
					!errors.Is(err, ErrNoOpenMonth) && !errors.Is(err, ErrExclusionLimit) {
					errs <- err
				}
				if err := st.Revoke(a, a+100); err != nil &&
					!errors.Is(err, ErrNoOpenMonth) && !errors.Is(err, ErrIntervalNotFound) {
					errs <- err
				}
				if res, err := st.Close(); err == nil {
					if res.D < 0 || res.D > p.L {
						errs <- errors.New("D out of range")
					}
					if res.Credit < 0 || res.Credit > 10000 {
						errs <- errors.New("credit out of range")
					}
				} else if !errors.Is(err, ErrNoOpenMonth) {
					errs <- err
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent op: %v", err)
	}
	for y, paid := range st.State().YearPaid {
		if paid > p.Y {
			t.Fatalf("year %d paid %d > Y=%d", y, paid, p.Y)
		}
	}
}

// 相同操作序列重放得到完全相同的结果。
func TestReplayDeterminism(t *testing.T) {
	run := func() []Result {
		st := mustNew(t, exampleParams())
		var out []Result
		for m := 0; m < 15; m++ {
			must(t, st.NewMonth(10000))
			must(t, st.Report(int64(100+m*7), int64(200+m*7)))
			must(t, st.Report(1000, 1500))
			must(t, st.AddExclude(1200, 1250))
			if m%3 == 0 {
				must(t, st.Revoke(1000, 1500))
			}
			res := mustClose(t, st)
			out = append(out, res)
		}
		return out
	}
	first, second := run(), run()
	if len(first) != len(second) {
		t.Fatal("length mismatch")
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("month %d: %+v != %+v", i, first[i], second[i])
		}
	}
}
