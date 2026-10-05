package lot_test

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"ontology/lot"
	"ontology/plan"
)

var (
	insp = lot.Operator{ID: "insp", Roles: []lot.Role{lot.RoleInspector}}
	mgr  = lot.Operator{ID: "mgr", Roles: []lot.Role{lot.RoleManager}}
	none = lot.Operator{ID: "none"}

	keyA = lot.StreamKey{Supplier: "s1", Material: "m1"}
	keyB = lot.StreamKey{Supplier: "s2", Material: "m2"}
)

// stdTable 即题目示例方案表：[51,150] 区间 Normal (13,1,2)、
// Tightened (20,1,2)、Reduced (5,0,2)，Lr=2。
func stdTable(t *testing.T) *plan.Table {
	t.Helper()
	tb, err := plan.NewTable([]plan.Range{
		{
			Lo: 1, Hi: 50,
			Normal:    plan.Plan{N: 5, Ac: 0, Re: 1},
			Tightened: plan.Plan{N: 8, Ac: 0, Re: 1},
			Reduced:   plan.Plan{N: 3, Ac: 0, Re: 2},
		},
		{
			Lo: 51, Hi: 150,
			Normal:    plan.Plan{N: 13, Ac: 1, Re: 2},
			Tightened: plan.Plan{N: 20, Ac: 1, Re: 2},
			Reduced:   plan.Plan{N: 5, Ac: 0, Re: 2},
		},
		{
			Lo: 151, Hi: 1_000_000,
			Normal:    plan.Plan{N: 32, Ac: 2, Re: 3},
			Tightened: plan.Plan{N: 50, Ac: 1, Re: 2},
			Reduced:   plan.Plan{N: 13, Ac: 1, Re: 3},
		},
	}, 2)
	if err != nil {
		t.Fatal(err)
	}
	return tb
}

// runLot 提交并判定一个批，返回判定后的批。
func runLot(t *testing.T, m *lot.Manager, key lot.StreamKey, id string, n, d int) lot.Lot {
	t.Helper()
	if _, err := m.Submit(key, id, n); err != nil {
		t.Fatalf("Submit(%s) err = %v", id, err)
	}
	l, err := m.Record(id, d, insp)
	if err != nil {
		t.Fatalf("Record(%s, %d) err = %v", id, d, err)
	}
	return l
}

// severity 返回流的当前严格度。
func severity(t *testing.T, m *lot.Manager, key lot.StreamKey) plan.Severity {
	t.Helper()
	sev, ok := m.SeverityOf(key)
	if !ok {
		t.Fatalf("stream %+v 不存在", key)
	}
	return sev
}

// driveToTightened 用连续两批拒收把流带入 Tightened。
func driveToTightened(t *testing.T, m *lot.Manager, key lot.StreamKey, prefix string) {
	t.Helper()
	runLot(t, m, key, prefix+"-r1", 100, 2)
	runLot(t, m, key, prefix+"-r2", 100, 2)
	if sev := severity(t, m, key); sev != plan.Tightened {
		t.Fatalf("severity = %v, want Tightened", sev)
	}
}

// driveToReduced 用十批 d=0 接收把流带入 Reduced。
func driveToReduced(t *testing.T, m *lot.Manager, key lot.StreamKey, prefix string) {
	t.Helper()
	for i := 0; i < 10; i++ {
		runLot(t, m, key, fmt.Sprintf("%s-a%d", prefix, i), 100, 0)
	}
	if sev := severity(t, m, key); sev != plan.Reduced {
		t.Fatalf("severity = %v, want Reduced", sev)
	}
}

// driveToSuspended 把流带入 Suspended（Tightened 下累计 5 批拒收）。
func driveToSuspended(t *testing.T, m *lot.Manager, key lot.StreamKey, prefix string) {
	t.Helper()
	driveToTightened(t, m, key, prefix)
	for i := 0; i < 5; i++ {
		runLot(t, m, key, fmt.Sprintf("%s-t%d", prefix, i), 100, 2)
	}
	if sev := severity(t, m, key); sev != plan.Suspended {
		t.Fatalf("severity = %v, want Suspended", sev)
	}
}

// TestSpecExample 复现题目示例：自 Normal 起六批 d 为 0、2、1、0、0、3，
// 判定为收、拒、收、收、收、拒；第 6 批后最近 5 批含 2 批拒收，转 Tightened，
// 第 7 批按 (20,1,2) 抽样。
func TestSpecExample(t *testing.T) {
	t.Run("六批后转 Tightened", func(t *testing.T) {
		m := lot.NewManager(stdTable(t))
		ds := []int{0, 2, 1, 0, 0, 3}
		wants := []lot.Status{
			lot.Released, lot.Rejected, lot.Released,
			lot.Released, lot.Released, lot.Rejected,
		}
		for i, d := range ds {
			l := runLot(t, m, keyA, fmt.Sprintf("L%d", i+1), 100, d)
			if l.Status != wants[i] {
				t.Fatalf("lot %d: status = %v, want %v", i+1, l.Status, wants[i])
			}
		}
		if sev := severity(t, m, keyA); sev != plan.Tightened {
			t.Fatalf("severity = %v, want Tightened", sev)
		}
		l, err := m.Submit(keyA, "L7", 100)
		if err != nil {
			t.Fatal(err)
		}
		if l.Plan != (plan.Plan{N: 20, Ac: 1, Re: 2}) || l.Severity != plan.Tightened {
			t.Fatalf("lot7 plan = %+v sev = %v, want (20,1,2) Tightened", l.Plan, l.Severity)
		}
	})
	t.Run("窗口滑动后旧拒收不计入", func(t *testing.T) {
		m := lot.NewManager(stdTable(t))
		for i, d := range []int{0, 2, 1, 0, 0, 0} {
			runLot(t, m, keyA, fmt.Sprintf("L%d", i+1), 100, d)
		}
		// 第 7 批 d=2 拒收：最近 5 批（第 3..7 批）只含 1 批拒收，不转。
		l := runLot(t, m, keyA, "L7", 100, 2)
		if l.Status != lot.Rejected {
			t.Fatalf("lot7 status = %v, want Rejected", l.Status)
		}
		if sev := severity(t, m, keyA); sev != plan.Normal {
			t.Fatalf("severity = %v, want Normal", sev)
		}
	})
}

// TestBoundaryAcRe 覆盖 d 恰等 Ac 与 Re 的边界，以及 Reduced 的边缘接收。
func TestBoundaryAcRe(t *testing.T) {
	cases := []struct {
		name string
		sev  plan.Severity
		d    int
		want lot.Status
	}{
		{"Normal d=Ac 接收", plan.Normal, 1, lot.Released},
		{"Normal d=Re 拒收", plan.Normal, 2, lot.Rejected},
		{"Tightened d=Ac 接收", plan.Tightened, 1, lot.Released},
		{"Tightened d=Re 拒收", plan.Tightened, 2, lot.Rejected},
		{"Reduced d=Ac 接收", plan.Reduced, 0, lot.Released},
		{"Reduced Ac<d<Re 边缘接收", plan.Reduced, 1, lot.Released},
		{"Reduced d=Re 拒收", plan.Reduced, 2, lot.Rejected},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := lot.NewManager(stdTable(t))
			switch tc.sev {
			case plan.Tightened:
				driveToTightened(t, m, keyA, "dr")
			case plan.Reduced:
				driveToReduced(t, m, keyA, "dr")
			}
			if _, err := m.Submit(keyA, "x", 100); err != nil {
				t.Fatal(err)
			}
			l, err := m.Record("x", tc.d, insp)
			if err != nil {
				t.Fatal(err)
			}
			if l.Status != tc.want {
				t.Fatalf("status = %v, want %v", l.Status, tc.want)
			}
		})
	}
}

// TestWindowClearedOnSwitch 验证切换后窗口与计数清空：
// Tightened 下十批（收收收收拒收收收收收）后连续接收达 5 转 Normal，
// 其间拒收累计 1 批；回到 Normal 后首批即拒收，窗口只有这 1 批，不转；
// 且批数计数清零，需重新累计 10 批才可转 Reduced。
func TestWindowClearedOnSwitch(t *testing.T) {
	m := lot.NewManager(stdTable(t))
	driveToTightened(t, m, keyA, "in")
	for i, d := range []int{0, 0, 0, 0, 2, 0, 0, 0, 0, 0} {
		runLot(t, m, keyA, fmt.Sprintf("t%d", i), 100, d)
	}
	if sev := severity(t, m, keyA); sev != plan.Normal {
		t.Fatalf("severity = %v, want Normal", sev)
	}
	runLot(t, m, keyA, "n0", 100, 2) // 回到 Normal 首批即拒收
	if sev := severity(t, m, keyA); sev != plan.Normal {
		t.Fatalf("进入前的拒收被带入: severity = %v, want Normal", sev)
	}
	// 另一流验证批数清零：进入 Normal 后需满 10 批才评估 Reduced。
	for i := 0; i < 9; i++ {
		runLot(t, m, keyB, fmt.Sprintf("b%d", i), 100, 0)
	}
	if sev := severity(t, m, keyB); sev != plan.Normal {
		t.Fatalf("9 批即转: severity = %v, want Normal", sev)
	}
	runLot(t, m, keyB, "b9", 100, 0)
	if sev := severity(t, m, keyB); sev != plan.Reduced {
		t.Fatalf("severity = %v, want Reduced", sev)
	}
}

// TestReducedSlidingAndLrEquality 验证放宽的 10 批滑动窗口与限数取等：
// 连续十批接收 d 为 1,0,0,0,1,0,0,0,0,1，和为 3 大于 Lr=2，不转；
// 第 11 批 d=0，最近 10 批（第 2..11 批）之和为 2，恰等 Lr，转 Reduced。
func TestReducedSlidingAndLrEquality(t *testing.T) {
	m := lot.NewManager(stdTable(t))
	for i, d := range []int{1, 0, 0, 0, 1, 0, 0, 0, 0, 1} {
		runLot(t, m, keyA, fmt.Sprintf("L%d", i+1), 100, d)
	}
	if sev := severity(t, m, keyA); sev != plan.Normal {
		t.Fatalf("d 之和 3 > Lr 不应转: severity = %v", sev)
	}
	runLot(t, m, keyA, "L11", 100, 0)
	if sev := severity(t, m, keyA); sev != plan.Reduced {
		t.Fatalf("最近 10 批之和恰等 Lr 应转: severity = %v, want Reduced", sev)
	}
}

// TestMarginalAccept 验证 Reduced 下边缘接收与拒收都转 Normal。
func TestMarginalAccept(t *testing.T) {
	t.Run("边缘接收本批放行并转 Normal", func(t *testing.T) {
		m := lot.NewManager(stdTable(t))
		driveToReduced(t, m, keyA, "dr")
		l := runLot(t, m, keyA, "mg", 100, 1) // Reduced (5,0,2)：Ac=0<d=1<Re=2
		if l.Status != lot.Released {
			t.Fatalf("status = %v, want Released", l.Status)
		}
		if sev := severity(t, m, keyA); sev != plan.Normal {
			t.Fatalf("severity = %v, want Normal", sev)
		}
	})
	t.Run("拒收转 Normal", func(t *testing.T) {
		m := lot.NewManager(stdTable(t))
		driveToReduced(t, m, keyA, "dr")
		l := runLot(t, m, keyA, "rj", 100, 2)
		if l.Status != lot.Rejected {
			t.Fatalf("status = %v, want Rejected", l.Status)
		}
		if sev := severity(t, m, keyA); sev != plan.Normal {
			t.Fatalf("severity = %v, want Normal", sev)
		}
	})
}

// TestSuspendResume 验证 Tightened 下累计 5 批拒收（不要求连续）暂停、
// 暂停期间 Submit 拒绝、Resume 权限与恢复后计数清零。
func TestSuspendResume(t *testing.T) {
	m := lot.NewManager(stdTable(t))
	driveToTightened(t, m, keyA, "in")
	// 累计 5 批拒收，穿插接收（接收不清累计拒收数）。
	for i, d := range []int{2, 0, 2, 2, 0, 2, 2} {
		runLot(t, m, keyA, fmt.Sprintf("t%d", i), 100, d)
	}
	if sev := severity(t, m, keyA); sev != plan.Suspended {
		t.Fatalf("severity = %v, want Suspended", sev)
	}
	if _, err := m.Submit(keyA, "z", 100); !errors.Is(err, lot.ErrSuspended) {
		t.Fatalf("Submit err = %v, want ErrSuspended", err)
	}
	if err := m.Resume(keyA, insp); !errors.Is(err, lot.ErrPermission) {
		t.Fatalf("Resume(Inspector) err = %v, want ErrPermission", err)
	}
	if err := m.Resume(keyA, mgr); err != nil {
		t.Fatalf("Resume(Manager) err = %v", err)
	}
	if sev := severity(t, m, keyA); sev != plan.Tightened {
		t.Fatalf("severity = %v, want Tightened", sev)
	}
	// 计数已清零：4 批拒收仍 Tightened，第 5 批才再次暂停。
	for i := 0; i < 4; i++ {
		runLot(t, m, keyA, fmt.Sprintf("r%d", i), 100, 2)
	}
	if sev := severity(t, m, keyA); sev != plan.Tightened {
		t.Fatalf("恢复后计数未清零: severity = %v, want Tightened", sev)
	}
	runLot(t, m, keyA, "r4", 100, 2)
	if sev := severity(t, m, keyA); sev != plan.Suspended {
		t.Fatalf("severity = %v, want Suspended", sev)
	}
}

// TestRetest 验证复检：方案固定 Tightened 档、接收为 Released、拒收为
// Scrapped、不进入任何窗口与计数、不可重复复检。
func TestRetest(t *testing.T) {
	m := lot.NewManager(stdTable(t))
	// N=60 的拒收批，初检方案 (13,1,2)。
	l := runLot(t, m, keyA, "r1", 60, 3)
	if l.Status != lot.Rejected || l.Plan != (plan.Plan{N: 13, Ac: 1, Re: 2}) {
		t.Fatalf("lot = %+v, want Rejected (13,1,2)", l)
	}
	// 复检按 Tightened 档 (20,1,2)。
	rl, err := m.Resubmit("r1")
	if err != nil {
		t.Fatal(err)
	}
	if rl.Plan != (plan.Plan{N: 20, Ac: 1, Re: 2}) || rl.Severity != plan.Tightened ||
		rl.Status != lot.Pending || !rl.Retest {
		t.Fatalf("retest lot = %+v", rl)
	}
	// d=1 则 Released。
	rl, err = m.Record("r1", 1, insp)
	if err != nil {
		t.Fatal(err)
	}
	if rl.Status != lot.Released {
		t.Fatalf("retest status = %v, want Released", rl.Status)
	}
	// 复检不改变流的任何计数：流仍 Normal，窗口无记录。
	if sev := severity(t, m, keyA); sev != plan.Normal {
		t.Fatalf("severity = %v, want Normal", sev)
	}
	// 已复检过的批不能再复检。
	if _, err := m.Resubmit("r1"); !errors.Is(err, lot.ErrState) {
		t.Fatalf("Resubmit err = %v, want ErrState", err)
	}
	// 另一流：初检 1 批拒收 + 复检拒收（Scrapped），若复检计入窗口，
	// 则窗口已有 2 批拒收会转 Tightened；期望复检不计数，流仍 Normal。
	runLot(t, m, keyB, "b1", 100, 2)
	if _, err := m.Resubmit("b1"); err != nil {
		t.Fatal(err)
	}
	bl, err := m.Record("b1", 5, insp)
	if err != nil {
		t.Fatal(err)
	}
	if bl.Status != lot.Scrapped {
		t.Fatalf("retest status = %v, want Scrapped", bl.Status)
	}
	for i := 0; i < 4; i++ {
		runLot(t, m, keyB, fmt.Sprintf("b%d", i+2), 100, 0)
	}
	if sev := severity(t, m, keyB); sev != plan.Normal {
		t.Fatalf("复检批被计入窗口: severity = %v, want Normal", sev)
	}
	// 流有未判定批时不能复检。
	runLot(t, m, keyA, "r2", 100, 3)
	runLot(t, m, keyA, "r3", 100, 3) // 转入 Tightened
	if _, err := m.Submit(keyA, "p1", 100); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Resubmit("r2"); !errors.Is(err, lot.ErrState) {
		t.Fatalf("Resubmit(有未判定批) err = %v, want ErrState", err)
	}
}

// TestRetestWhileSuspended 验证暂停期间可复检，且复检不改变暂停状态。
func TestRetestWhileSuspended(t *testing.T) {
	m := lot.NewManager(stdTable(t))
	driveToSuspended(t, m, keyA, "ds") // 拒收批 ds-t0 .. ds-t4
	if _, err := m.Submit(keyA, "z", 100); !errors.Is(err, lot.ErrSuspended) {
		t.Fatalf("Submit err = %v, want ErrSuspended", err)
	}
	rl, err := m.Resubmit("ds-t0")
	if err != nil {
		t.Fatalf("Suspended 期间应可复检: err = %v", err)
	}
	if rl.Plan != (plan.Plan{N: 20, Ac: 1, Re: 2}) {
		t.Fatalf("retest plan = %+v, want (20,1,2)", rl.Plan)
	}
	rl, err = m.Record("ds-t0", 1, insp)
	if err != nil {
		t.Fatal(err)
	}
	if rl.Status != lot.Released {
		t.Fatalf("retest status = %v, want Released", rl.Status)
	}
	if sev := severity(t, m, keyA); sev != plan.Suspended {
		t.Fatalf("severity = %v, want Suspended", sev)
	}
	if err := m.Resume(keyA, mgr); err != nil {
		t.Fatal(err)
	}
	if sev := severity(t, m, keyA); sev != plan.Tightened {
		t.Fatalf("severity = %v, want Tightened", sev)
	}
}

// TestCappedSampleSize 验证 n 大于 N 时取 n=N 而 Ac、Re 不变。
func TestCappedSampleSize(t *testing.T) {
	m := lot.NewManager(stdTable(t))
	l, err := m.Submit(keyA, "c1", 3) // [1,50] Normal (5,0,1)，n 截断为 3
	if err != nil {
		t.Fatal(err)
	}
	if l.Plan != (plan.Plan{N: 3, Ac: 0, Re: 1}) {
		t.Fatalf("plan = %+v, want (3,0,1)", l.Plan)
	}
	if _, err := m.Record("c1", 4, insp); !errors.Is(err, lot.ErrOutOfRange) {
		t.Fatalf("Record(d=4>n=3) err = %v, want ErrOutOfRange", err)
	}
	l, err = m.Record("c1", 1, insp) // d=1 >= Re=1 拒收
	if err != nil {
		t.Fatal(err)
	}
	if l.Status != lot.Rejected {
		t.Fatalf("status = %v, want Rejected", l.Status)
	}
	// 复检方案 Tightened (8,0,1) 同样截断为 n=3。
	rl, err := m.Resubmit("c1")
	if err != nil {
		t.Fatal(err)
	}
	if rl.Plan != (plan.Plan{N: 3, Ac: 0, Re: 1}) || rl.Severity != plan.Tightened {
		t.Fatalf("retest plan = %+v sev = %v", rl.Plan, rl.Severity)
	}
}

// TestRejectionOrder 验证拒绝按次序只报第一个：
// 参数非法 > 无权限 > 不存在 > 流已暂停 > 状态不符 > 冲突 > 数量越界。
func TestRejectionOrder(t *testing.T) {
	gapTable := func(t *testing.T) *plan.Table {
		t.Helper()
		tb, err := plan.NewTable([]plan.Range{
			{
				Lo: 1, Hi: 50,
				Normal:    plan.Plan{N: 5, Ac: 0, Re: 1},
				Tightened: plan.Plan{N: 8, Ac: 0, Re: 1},
				Reduced:   plan.Plan{N: 3, Ac: 0, Re: 2},
			},
			{
				Lo: 151, Hi: 1_000_000,
				Normal:    plan.Plan{N: 32, Ac: 2, Re: 3},
				Tightened: plan.Plan{N: 50, Ac: 1, Re: 2},
				Reduced:   plan.Plan{N: 13, Ac: 1, Re: 3},
			},
		}, 2)
		if err != nil {
			t.Fatal(err)
		}
		return tb
	}
	cases := []struct {
		name  string
		table func(*testing.T) *plan.Table // nil 用 stdTable
		setup func(t *testing.T, m *lot.Manager)
		run   func(m *lot.Manager) error
		want  error
	}{
		{
			name: "Submit 标识为空",
			run: func(m *lot.Manager) error {
				_, err := m.Submit(lot.StreamKey{Supplier: "", Material: "m"}, "x", 100)
				return err
			},
			want: lot.ErrInvalidParam,
		},
		{
			name: "Submit 批号为空",
			run: func(m *lot.Manager) error {
				_, err := m.Submit(keyA, "", 100)
				return err
			},
			want: lot.ErrInvalidParam,
		},
		{
			name: "Submit N 越界",
			run: func(m *lot.Manager) error {
				_, err := m.Submit(keyA, "x", 1_000_001)
				return err
			},
			want: lot.ErrInvalidParam,
		},
		{
			name:  "Submit N 无区间",
			table: gapTable,
			run: func(m *lot.Manager) error {
				_, err := m.Submit(keyA, "x", 100)
				return err
			},
			want: lot.ErrInvalidParam,
		},
		{
			name: "Record d 为负优先于无权限与不存在",
			run: func(m *lot.Manager) error {
				_, err := m.Record("ghost", -1, none)
				return err
			},
			want: lot.ErrInvalidParam,
		},
		{
			name: "Record 无权限优先于不存在",
			run: func(m *lot.Manager) error {
				_, err := m.Record("ghost", 1, none)
				return err
			},
			want: lot.ErrPermission,
		},
		{
			name: "Record 批不存在",
			run: func(m *lot.Manager) error {
				_, err := m.Record("ghost", 1, insp)
				return err
			},
			want: lot.ErrNotFound,
		},
		{
			name: "Submit 流已暂停优先于冲突",
			setup: func(t *testing.T, m *lot.Manager) {
				driveToSuspended(t, m, keyA, "ds") // 已存在批 ds-t0 等
			},
			run: func(m *lot.Manager) error {
				_, err := m.Submit(keyA, "ds-t0", 100) // 批号重复，但先报暂停
				return err
			},
			want: lot.ErrSuspended,
		},
		{
			name: "Submit 未判定批优先于冲突",
			setup: func(t *testing.T, m *lot.Manager) {
				if _, err := m.Submit(keyA, "p1", 100); err != nil {
					t.Fatal(err)
				}
			},
			run: func(m *lot.Manager) error {
				_, err := m.Submit(keyA, "p1", 100) // 批号重复，但先报状态不符
				return err
			},
			want: lot.ErrState,
		},
		{
			name: "Submit 批号重复冲突",
			setup: func(t *testing.T, m *lot.Manager) {
				runLot(t, m, keyA, "d1", 100, 0)
			},
			run: func(m *lot.Manager) error {
				_, err := m.Submit(keyA, "d1", 100)
				return err
			},
			want: lot.ErrConflict,
		},
		{
			name: "Record 批不在待判定优先于数量越界",
			setup: func(t *testing.T, m *lot.Manager) {
				runLot(t, m, keyA, "d1", 100, 0)
			},
			run: func(m *lot.Manager) error {
				_, err := m.Record("d1", 999, insp)
				return err
			},
			want: lot.ErrState,
		},
		{
			name: "Record d 大于 n 数量越界",
			setup: func(t *testing.T, m *lot.Manager) {
				if _, err := m.Submit(keyA, "p1", 100); err != nil {
					t.Fatal(err)
				}
			},
			run: func(m *lot.Manager) error {
				_, err := m.Record("p1", 14, insp) // n=13
				return err
			},
			want: lot.ErrOutOfRange,
		},
		{
			name: "Resubmit 批不存在",
			run: func(m *lot.Manager) error {
				_, err := m.Resubmit("ghost")
				return err
			},
			want: lot.ErrNotFound,
		},
		{
			name: "Resubmit 批不是 Rejected",
			setup: func(t *testing.T, m *lot.Manager) {
				runLot(t, m, keyA, "d1", 100, 0) // Released
			},
			run: func(m *lot.Manager) error {
				_, err := m.Resubmit("d1")
				return err
			},
			want: lot.ErrState,
		},
		{
			name: "Resubmit 批已复检过",
			setup: func(t *testing.T, m *lot.Manager) {
				runLot(t, m, keyA, "d1", 100, 3)
				if _, err := m.Resubmit("d1"); err != nil {
					t.Fatal(err)
				}
				if _, err := m.Record("d1", 5, insp); err != nil {
					t.Fatal(err)
				}
			},
			run: func(m *lot.Manager) error {
				_, err := m.Resubmit("d1")
				return err
			},
			want: lot.ErrState,
		},
		{
			name: "Resume 无权限优先于不存在",
			run: func(m *lot.Manager) error {
				return m.Resume(lot.StreamKey{Supplier: "g", Material: "g"}, insp)
			},
			want: lot.ErrPermission,
		},
		{
			name: "Resume 流不存在",
			run: func(m *lot.Manager) error {
				return m.Resume(lot.StreamKey{Supplier: "g", Material: "g"}, mgr)
			},
			want: lot.ErrNotFound,
		},
		{
			name: "Resume 流未暂停",
			setup: func(t *testing.T, m *lot.Manager) {
				runLot(t, m, keyA, "d1", 100, 0)
			},
			run: func(m *lot.Manager) error {
				return m.Resume(keyA, mgr)
			},
			want: lot.ErrState,
		},
		{
			name: "Resume 标识为空",
			run: func(m *lot.Manager) error {
				return m.Resume(lot.StreamKey{}, mgr)
			},
			want: lot.ErrInvalidParam,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			newTable := stdTable
			if tc.table != nil {
				newTable = tc.table
			}
			m := lot.NewManager(newTable(t))
			if tc.setup != nil {
				tc.setup(t, m)
			}
			if err := tc.run(m); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

// TestConcurrentSmoke 并发调用 Submit/Record/Resume，依赖 -race 检测数据竞争；
// 每个 goroutine 使用唯一批号，成功提交后立即判定。
func TestConcurrentSmoke(t *testing.T) {
	m := lot.NewManager(stdTable(t))
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(g + 1)))
			key := lot.StreamKey{Supplier: fmt.Sprintf("s%d", g%4), Material: "m"}
			for i := 0; i < 100; i++ {
				id := fmt.Sprintf("g%d-%d", g, i)
				if _, err := m.Submit(key, id, 100); err == nil {
					if _, err := m.Record(id, rng.Intn(4), insp); err != nil {
						t.Errorf("Record(%s) err = %v", id, err)
					}
				}
				if rng.Intn(10) == 0 {
					_ = m.Resume(key, mgr)
				}
			}
		}(g)
	}
	wg.Wait()
}
