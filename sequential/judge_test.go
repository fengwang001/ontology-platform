package sequential

import (
	"errors"
	"sync"
	"testing"
)

func mustJudge(t *testing.T, rA, rB, nmin, minStep, nmax int64, table []int64, tf, tau int64) *Judge {
	t.Helper()
	j, err := NewJudge(rA, rB, nmin, minStep, nmax, table, tf, tau)
	if err != nil {
		t.Fatalf("NewJudge(%d,%d,%d,%d,%d,%v,%d,%d) 报错: %v", rA, rB, nmin, minStep, nmax, table, tf, tau, err)
	}
	return j
}

func look(t *testing.T, j *Judge, nA, cA, nB, cB int64) Conclusion {
	t.Helper()
	c, err := j.Look(nA, cA, nB, cB)
	if err != nil {
		t.Fatalf("Look(%d,%d,%d,%d) 被拒绝: %v", nA, cA, nB, cB, err)
	}
	t.Logf("Look(%d,%d,%d,%d) => %s", nA, cA, nB, cB, c)
	return c
}

// 规范中的完整示例。
func TestSpecExample(t *testing.T) {
	j := mustJudge(t, 1, 1, 100, 1000, 100000, []int64{2000, 1000, 400}, 50, 5)
	if got := look(t, j, 1000, 100, 1000, 140); got != Continue {
		t.Fatalf("第 1 次检视 = %s, 期望 继续", got)
	}
	if st := j.Status(); st.Looks != 1 || st.LastCountedN != 2000 || st.Stopped {
		t.Fatalf("状态错误: %+v", st)
	}
	if got := look(t, j, 2000, 200, 2000, 300); got != Win {
		t.Fatalf("第 2 次检视 = %s, 期望 胜出", got)
	}
	st := j.Status()
	if !st.Stopped || st.Looks != 2 || st.LastCountedN != 4000 || st.LastConclusion != Win {
		t.Fatalf("状态错误: %+v", st)
	}
}

// n 恰等于 2*nmin 才开始比例检查，差 1 不检查。
func TestRatioCheckStartsAt2Nmin(t *testing.T) {
	// n = 99 = 2*nmin-1：偏差足以异常，但不应检查。
	j := mustJudge(t, 1, 1, 50, 1, 2_000_000, []int64{1_000_000}, 0, 5)
	if got := look(t, j, 60, 0, 39, 0); got != Continue {
		t.Fatalf("n=2*nmin-1 时 = %s, 期望 继续（不检查比例）", got)
	}
	if st := j.Status(); st.Looks != 0 {
		t.Fatalf("样本不足不应计数, Looks = %d", st.Looks)
	}
	// n = 100 = 2*nmin：同样的偏差触发比例异常（且先于样本不足判定）。
	j = mustJudge(t, 1, 1, 50, 1, 2_000_000, []int64{1_000_000}, 0, 5)
	if got := look(t, j, 60, 0, 40, 0); got != Imbalance {
		t.Fatalf("n=2*nmin 时 = %s, 期望 比例异常", got)
	}
	st := j.Status()
	if !st.Stopped || st.Looks != 0 || st.LastConclusion != Imbalance {
		t.Fatalf("比例异常不应计数且应停止: %+v", st)
	}
}

// 比例偏差恰等于 tau 通过，偏差乘积多 1 则异常。
func TestRatioExactTau(t *testing.T) {
	// tau=1：|nA-nB|*100 == 1*(nA+nB) -> 2*100 == 200，恰等于容差，通过。
	j := mustJudge(t, 1, 1, 50, 1, 2_000_000, []int64{1_000_000}, 0, 1)
	if got := look(t, j, 101, 0, 99, 0); got != Continue {
		t.Fatalf("偏差恰等于 tau 时 = %s, 期望 继续", got)
	}
	// |nA-nB|*100 = 300 == 1*299 + 1，多 1，异常。
	j = mustJudge(t, 1, 1, 50, 1, 2_000_000, []int64{1_000_000}, 0, 1)
	if got := look(t, j, 151, 0, 148, 0); got != Imbalance {
		t.Fatalf("偏差多 1 时 = %s, 期望 比例异常", got)
	}
}

// nA 恰等于 nmin 参与判定，差 1 则样本不足。
func TestNminBoundary(t *testing.T) {
	j := mustJudge(t, 1, 1, 100, 1, 2_000_000, []int64{1_000_000}, 0, 100)
	if got := look(t, j, 100, 0, 100, 0); got != Continue {
		t.Fatalf("nA=nmin 时 = %s, 期望 继续", got)
	}
	if st := j.Status(); st.Looks != 1 || st.LastCountedN != 200 {
		t.Fatalf("nA=nmin 应计数: %+v", st)
	}
	j = mustJudge(t, 1, 1, 100, 1, 2_000_000, []int64{1_000_000}, 0, 100)
	if got := look(t, j, 99, 0, 100, 0); got != Continue {
		t.Fatalf("nA=nmin-1 时 = %s, 期望 继续", got)
	}
	if st := j.Status(); st.Looks != 0 || st.LastCountedN != 0 {
		t.Fatalf("nA=nmin-1 不应计数: %+v", st)
	}
}

// n 增量恰等于 minStep 计数，差 1 仅观察；仅观察不推进边界序号与上次计数 n。
func TestMinStepBoundary(t *testing.T) {
	j := mustJudge(t, 1, 1, 1, 1000, 2_000_000, []int64{1_000_000}, 0, 100)
	look(t, j, 500, 0, 500, 0) // n=1000，第 1 次计数
	if st := j.Status(); st.Looks != 1 || st.LastCountedN != 1000 {
		t.Fatalf("首次检视应计数: %+v", st)
	}
	look(t, j, 999, 0, 1000, 0) // n=1999，增量 999 < 1000，仅观察
	if st := j.Status(); st.Looks != 1 || st.LastCountedN != 1000 {
		t.Fatalf("增量差 1 应仅观察: %+v", st)
	}
	look(t, j, 1000, 0, 1000, 0) // n=2000，增量恰 1000，计数
	if st := j.Status(); st.Looks != 2 || st.LastCountedN != 2000 {
		t.Fatalf("增量恰等于 minStep 应计数: %+v", st)
	}
}

// 仅观察的检视不推进边界序号：第 2 次计数检视必须使用 T_2 而非 T_3。
func TestObserveOnlyDoesNotAdvanceBoundary(t *testing.T) {
	// T = [1e6, 1000, 1e6]；统计量约 1095（100*z^2），在 T_2=1000 下显著、在 T_3=1e6 下不显著。
	j := mustJudge(t, 1, 1, 1, 100, 2_000_000, []int64{1_000_000, 1000, 1_000_000}, 0, 100)
	look(t, j, 100, 5, 100, 5) // n=200，第 1 次计数（T_1）
	look(t, j, 150, 8, 149, 8) // n=299，增量 99 < 100，仅观察
	if st := j.Status(); st.Looks != 1 {
		t.Fatalf("仅观察不应推进边界序号: %+v", st)
	}
	// n=499，增量 299 >= 100，第 2 次计数，应使用 T_2=1000 -> 显著且 D>0。
	if got := look(t, j, 250, 10, 249, 30); got != Win {
		t.Fatalf("第 2 次计数检视 = %s, 期望 胜出（使用 T_2）", got)
	}
}

// 边界序号超过 k 后沿用 T_k。
func TestBoundaryIndexBeyondK(t *testing.T) {
	// T = [1000, 1e6]；第 3 次计数检视统计量约 1948，在 T_1 下显著、在 T_k=T_2 下不显著。
	j := mustJudge(t, 1, 1, 1, 100, 2_000_000, []int64{1000, 1_000_000}, 0, 100)
	look(t, j, 100, 10, 100, 10) // 第 1 次计数
	look(t, j, 250, 10, 249, 30) // 第 2 次计数（T_2=1e6，不显著）
	if got := look(t, j, 350, 10, 349, 40); got != Continue {
		t.Fatalf("第 3 次计数检视 = %s, 期望 继续（沿用 T_k=T_2）", got)
	}
	if st := j.Status(); st.Looks != 3 {
		t.Fatalf("Looks = %d, 期望 3", st.Looks)
	}
}

// z^2 恰等于边界时显著（>= 判定）；同时覆盖胜出与变差的方向。
func TestExactBoundarySignificant(t *testing.T) {
	// nA=nB=100, cA=10, cB=30：D=2000，100*z^2 恰为 1250。
	j := mustJudge(t, 1, 1, 1, 1, 2_000_000, []int64{1250}, 0, 100)
	if got := look(t, j, 100, 10, 100, 30); got != Win {
		t.Fatalf("z^2 恰等于边界且 D>0 = %s, 期望 胜出", got)
	}
	// 镜像：D=-2000，恰等于边界，变差。
	j = mustJudge(t, 1, 1, 1, 1, 2_000_000, []int64{1250}, 0, 100)
	if got := look(t, j, 100, 30, 100, 10); got != Lose {
		t.Fatalf("z^2 恰等于边界且 D<0 = %s, 期望 变差", got)
	}
	// 边界高 1：不显著。
	j = mustJudge(t, 1, 1, 1, 1, 2_000_000, []int64{1251}, 0, 100)
	if got := look(t, j, 100, 10, 100, 30); got != Continue {
		t.Fatalf("边界高 1 时 = %s, 期望 继续", got)
	}
}

// c 为 0 或 c 等于 n 时统计量未定义：显著必为否，无效“小于”当且仅当 Tf>0。
func TestUndefinedStatistic(t *testing.T) {
	// c=0，Tf=0，2n >= nmax：无效“小于”不成立，继续。
	j := mustJudge(t, 1, 1, 1, 1, 2000, []int64{1_000_000}, 0, 100)
	if got := look(t, j, 500, 0, 500, 0); got != Continue {
		t.Fatalf("c=0 且 Tf=0 = %s, 期望 继续", got)
	}
	// c=0，Tf>0，2n == nmax：无效。
	j = mustJudge(t, 1, 1, 1, 1, 2000, []int64{1_000_000}, 1, 100)
	if got := look(t, j, 500, 0, 500, 0); got != Futile {
		t.Fatalf("c=0 且 Tf>0 且 2n>=nmax = %s, 期望 无效", got)
	}
	// c=n，Tf>0，2n >= nmax：无效。
	j = mustJudge(t, 1, 1, 1, 1, 2000, []int64{1_000_000}, 1, 100)
	if got := look(t, j, 500, 500, 500, 500); got != Futile {
		t.Fatalf("c=n 且 Tf>0 且 2n>=nmax = %s, 期望 无效", got)
	}
	// c=0，Tf>0，但 2n < nmax：继续。
	j = mustJudge(t, 1, 1, 1, 1, 2002, []int64{1_000_000}, 1, 100)
	if got := look(t, j, 500, 0, 500, 0); got != Continue {
		t.Fatalf("c=0 且 Tf>0 但 2n<nmax = %s, 期望 继续", got)
	}
	// c=0，n >= nmax：走样本上限分支，无效。
	j = mustJudge(t, 1, 1, 1, 1, 1000, []int64{1_000_000}, 0, 100)
	if got := look(t, j, 500, 0, 500, 0); got != Futile {
		t.Fatalf("c=0 且 n>=nmax = %s, 期望 无效", got)
	}
}

// D 为 0：不显著；Tf>0 且 2n>=nmax 时 0 < Tf*rhs 成立，无效。
func TestDZero(t *testing.T) {
	j := mustJudge(t, 1, 1, 1, 1, 2_000_000, []int64{1}, 0, 100)
	if got := look(t, j, 100, 10, 100, 10); got != Continue {
		t.Fatalf("D=0 = %s, 期望 继续", got)
	}
	j = mustJudge(t, 1, 1, 1, 1, 400, []int64{1}, 1, 100)
	if got := look(t, j, 100, 10, 100, 10); got != Futile {
		t.Fatalf("D=0 且 2n>=nmax 且 Tf>0 = %s, 期望 无效", got)
	}
}

// Nmax 与无效边界的先后：显著最优先；n>=nmax 先于无效边界。
func TestNmaxAndFutilityOrder(t *testing.T) {
	// n >= nmax 且显著：显著优先，胜出。
	j := mustJudge(t, 1, 1, 1, 1, 200, []int64{1250}, 0, 100)
	if got := look(t, j, 100, 10, 100, 30); got != Win {
		t.Fatalf("n>=nmax 且显著 = %s, 期望 胜出", got)
	}
	// n >= nmax，不显著，Tf=0（无效边界不可能成立）：仍无效（样本上限分支）。
	j = mustJudge(t, 1, 1, 1, 1, 200, []int64{1_000_000}, 0, 100)
	if got := look(t, j, 100, 10, 100, 10); got != Futile {
		t.Fatalf("n>=nmax 且 Tf=0 = %s, 期望 无效", got)
	}
	// n < nmax 但 2n >= nmax 且穿越无效边界：无效。
	j = mustJudge(t, 1, 1, 1, 1, 400, []int64{1_000_000}, 1_000_000, 100)
	if got := look(t, j, 100, 10, 100, 10); got != Futile {
		t.Fatalf("2n>=nmax 且穿越无效边界 = %s, 期望 无效", got)
	}
}

// 2n 恰等于 nmax 才检查无效边界，差 2 不检查。
func TestFutilityCheckedAt2NEqualsNmax(t *testing.T) {
	// 2n = 400 == nmax：检查无效边界，D=0 < Tf*rhs，无效。
	j := mustJudge(t, 1, 1, 1, 1, 400, []int64{1_000_000}, 1, 100)
	if got := look(t, j, 100, 10, 100, 10); got != Futile {
		t.Fatalf("2n==nmax = %s, 期望 无效", got)
	}
	// 2n = 398 < nmax：不检查，继续。
	j = mustJudge(t, 1, 1, 1, 1, 400, []int64{1_000_000}, 1, 100)
	if got := look(t, j, 100, 10, 99, 10); got != Continue {
		t.Fatalf("2n==nmax-2 = %s, 期望 继续", got)
	}
}

// 停止后再 Look 被拒；参数非法优先于已停止。
func TestLookAfterStop(t *testing.T) {
	j := mustJudge(t, 1, 1, 1, 1, 2_000_000, []int64{1250}, 0, 100)
	if got := look(t, j, 100, 10, 100, 30); got != Win {
		t.Fatalf("首次检视 = %s, 期望 胜出", got)
	}
	before := j.Status()
	if _, err := j.Look(200, 20, 200, 40); !errors.Is(err, ErrStopped) {
		t.Fatalf("停止后 Look err = %v, 期望 ErrStopped", err)
	}
	// 参数非法优先于已停止。
	if _, err := j.Look(-1, 0, 0, 0); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("停止后非法 Look err = %v, 期望 ErrInvalidParam", err)
	}
	if after := j.Status(); after != before {
		t.Fatalf("被拒绝的 Look 改变了状态: %+v -> %+v", before, after)
	}
}

// 数据回退被拒且不改状态；仅观察的检视也会更新“上一次被接受”的基准值。
func TestRegressionRejected(t *testing.T) {
	j := mustJudge(t, 1, 1, 1, 1000, 2_000_000, []int64{1_000_000}, 0, 100)
	look(t, j, 500, 0, 500, 0) // 计数检视，n=1000
	look(t, j, 600, 0, 600, 0) // 仅观察（增量 200 < 1000），但被接受，更新基准值
	before := j.Status()
	if _, err := j.Look(550, 0, 550, 0); !errors.Is(err, ErrRegression) {
		t.Fatalf("相对仅观察检视回退 err = %v, 期望 ErrRegression", err)
	}
	if _, err := j.Look(600, 0, 600, 0); err != nil {
		t.Fatalf("与基准值持平应被接受, err = %v", err)
	}
	// 回退拒绝不改变任何状态；持平检视被接受但仅观察。
	if st := j.Status(); st.Looks != 1 || st.LastCountedN != 1000 || st.Stopped {
		t.Fatalf("状态错误: %+v (拒绝前 %+v)", st, before)
	}
	// 非法参数优先于回退。
	if _, err := j.Look(0, 1, 0, 0); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("非法且回退 err = %v, 期望 ErrInvalidParam", err)
	}
	// 后续有效检视不受影响。
	look(t, j, 1000, 0, 1000, 0)
	if st := j.Status(); st.Looks != 2 || st.LastCountedN != 2000 {
		t.Fatalf("回退拒绝后状态错误: %+v", st)
	}
}

// 乘积超过 int64（约 2e32）的用例，必须由大整数正确比较。
func TestBigIntegerProducts(t *testing.T) {
	// D=1e12，lhs = 1e24 * 2e6 * 100 = 2e32，rhs = 1 * 1e24：显著，胜出。
	j := mustJudge(t, 1, 1, 1, 1, 2_000_000, []int64{1}, 0, 100)
	if got := look(t, j, 1_000_000, 0, 1_000_000, 1_000_000); got != Win {
		t.Fatalf("大整数显著 = %s, 期望 胜出", got)
	}
	// lhs = 8e20 与 rhs = 1e30 均超过 int64 的比较路径（rhs 一侧），不显著后命中样本上限。
	j = mustJudge(t, 1, 1, 1, 1, 2_000_000, []int64{1_000_000}, 0, 100)
	if got := look(t, j, 1_000_000, 499_999, 1_000_000, 500_001); got != Futile {
		t.Fatalf("大整数不显著 = %s, 期望 无效（n>=nmax）", got)
	}
}

// 构造参数越界一律报参数非法。
func TestConstructorValidation(t *testing.T) {
	valid := func() []any {
		return []any{int64(1), int64(1), int64(100), int64(1000), int64(100000), []int64{2000, 1000}, int64(50), int64(5)}
	}
	if _, err := NewJudge(1, 1, 100, 1000, 100000, []int64{2000, 1000}, 50, 5); err != nil {
		t.Fatalf("合法参数报错: %v", err)
	}
	type mutation func(a []any)
	cases := map[string]mutation{
		"rA=0":          func(a []any) { a[0] = int64(0) },
		"rA=101":        func(a []any) { a[0] = int64(101) },
		"rB=0":          func(a []any) { a[1] = int64(0) },
		"rB=101":        func(a []any) { a[1] = int64(101) },
		"nmin=0":        func(a []any) { a[2] = int64(0) },
		"nmin=1e6+1":    func(a []any) { a[2] = int64(1_000_001) },
		"minStep=0":     func(a []any) { a[3] = int64(0) },
		"minStep=1e6+1": func(a []any) { a[3] = int64(1_000_001) },
		"nmax=1":        func(a []any) { a[4] = int64(1) },
		"nmax=2e6+1":    func(a []any) { a[4] = int64(2_000_001) },
		"table空":        func(a []any) { a[5] = []int64{} },
		"table过长":       func(a []any) { a[5] = []int64{1, 1, 1, 1, 1, 1, 1, 1, 1} },
		"T=0":           func(a []any) { a[5] = []int64{2000, 0} },
		"T=1e6+1":       func(a []any) { a[5] = []int64{1_000_001} },
		"Tf=-1":         func(a []any) { a[6] = int64(-1) },
		"Tf=1e6+1":      func(a []any) { a[6] = int64(1_000_001) },
		"tau=-1":        func(a []any) { a[7] = int64(-1) },
		"tau=101":       func(a []any) { a[7] = int64(101) },
	}
	for name, mutate := range cases {
		a := valid()
		mutate(a)
		_, err := NewJudge(a[0].(int64), a[1].(int64), a[2].(int64), a[3].(int64), a[4].(int64),
			a[5].([]int64), a[6].(int64), a[7].(int64))
		if !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("%s: err = %v, 期望 ErrInvalidParam", name, err)
		}
	}
	// 边界值合法。
	if _, err := NewJudge(100, 100, 1_000_000, 1_000_000, 2, []int64{1, 1_000_000}, 0, 0); err != nil {
		t.Fatalf("边界合法参数报错: %v", err)
	}
	if _, err := NewJudge(1, 1, 1, 1, 2_000_000, []int64{1}, 1_000_000, 100); err != nil {
		t.Fatalf("边界合法参数报错: %v", err)
	}
}

// Look 数值不满足 0 <= c <= n <= 1e6 时报参数非法。
func TestLookParamValidation(t *testing.T) {
	j := mustJudge(t, 1, 1, 1, 1, 2_000_000, []int64{1}, 0, 100)
	bad := [][4]int64{
		{-1, 0, 0, 0}, {0, -1, 0, 0}, {0, 0, -1, 0}, {0, 0, 0, -1},
		{10, 11, 0, 0}, {0, 0, 10, 11},
		{1_000_001, 0, 0, 0}, {0, 0, 1_000_001, 0},
	}
	for _, b := range bad {
		if _, err := j.Look(b[0], b[1], b[2], b[3]); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("Look%v err = %v, 期望 ErrInvalidParam", b, err)
		}
	}
	if st := j.Status(); st != (Status{LastConclusion: Continue}) {
		t.Fatalf("非法 Look 改变了状态: %+v", st)
	}
}

// 相同检视序列重放得到完全相同的结论序列。
func TestReplayDeterminism(t *testing.T) {
	seq := [][4]int64{
		{1000, 100, 1000, 140},
		{1500, 150, 1500, 200},
		{2000, 200, 2000, 300},
	}
	var first, second []Conclusion
	for run := 0; run < 2; run++ {
		j := mustJudge(t, 1, 1, 100, 1000, 100000, []int64{2000, 1000, 400}, 50, 5)
		var got []Conclusion
		for _, s := range seq {
			c, err := j.Look(s[0], s[1], s[2], s[3])
			if err != nil {
				t.Fatalf("Look%v 被拒绝: %v", s, err)
			}
			got = append(got, c)
		}
		if run == 0 {
			first = got
		} else {
			second = got
		}
	}
	if len(first) != len(second) {
		t.Fatalf("重放长度不一致")
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("第 %d 次结论不一致: %s vs %s", i, first[i], second[i])
		}
	}
}

// 并发调用 Look 与 Status：无数据竞争，停止后结论不可改变。
func TestConcurrentAccess(t *testing.T) {
	j := mustJudge(t, 1, 1, 1, 1, 2_000_000, []int64{1250}, 0, 100)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			base := int64(g * 100)
			for i := int64(0); i < 50; i++ {
				nA := base + i
				_, _ = j.Look(nA, i/2, nA, i/2)
				_ = j.Status()
			}
		}(g)
	}
	wg.Wait()
	st := j.Status()
	if st.Stopped {
		c := st.LastConclusion
		for i := 0; i < 10; i++ {
			j.Look(1_000_000, 0, 1_000_000, 0)
			if j.Status().LastConclusion != c {
				t.Fatalf("停止后结论被改变")
			}
		}
	}
}
