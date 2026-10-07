package aggview

import "testing"

const (
	tDept    = ID("Dept")
	tEmp     = ID("Emp")
	lBelong  = ID("belong")
	pSalary  = ID("salary")
	vPayroll = ID("payroll")
)

func registerPayroll(t *testing.T, e *Engine, policy Policy) {
	t.Helper()
	err := e.RegisterView(ViewDef{
		Name:      vPayroll,
		GroupType: tDept,
		Sources: []Source{{
			ObjectType: tEmp,
			LinkType:   lBelong,
			PropType:   pSalary,
			Policy:     policy,
		}},
	})
	if err != nil {
		t.Fatalf("register view: %v", err)
	}
}

// 单实例多分组归属：Full 策略下每个分组各得完整值。
func TestMultiGroupFullPolicy(t *testing.T) {
	e, st, _ := newTestEngine(t)
	registerPayroll(t, e, PolicyFull)
	mustCreate(t, st, "d1", tDept)
	mustCreate(t, st, "d2", tDept)
	mustCreate(t, st, "emp", tEmp)

	if _, err := e.SetProperty("emp", pSalary, FromInt(100)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.AddLink("emp", lBelong, "d1"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.AddLink("emp", lBelong, "d2"); err != nil {
		t.Fatal(err)
	}
	assertAgg(t, e, vPayroll, "d1", "100", 1)
	assertAgg(t, e, vPayroll, "d2", "100", 1)

	// 改值：仅触及 d1、d2，与分组内实例总数无关。
	chg, err := e.SetProperty("emp", pSalary, FromInt(130))
	if err != nil {
		t.Fatal(err)
	}
	assertAgg(t, e, vPayroll, "d1", "130", 1)
	assertAgg(t, e, vPayroll, "d2", "130", 1)
	if got := sortedGroups(chg.Touched()); got != "payroll/d1,payroll/d2" {
		t.Fatalf("touched = %s", got)
	}
}

// 单实例多分组归属：EvenShare 策略下各组各得 v/k。
func TestMultiGroupEvenSharePolicy(t *testing.T) {
	e, st, _ := newTestEngine(t)
	registerPayroll(t, e, PolicyEvenShare)
	mustCreate(t, st, "d1", tDept)
	mustCreate(t, st, "d2", tDept)
	mustCreate(t, st, "emp", tEmp)

	if _, err := e.SetProperty("emp", pSalary, FromInt(100)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.AddLink("emp", lBelong, "d1"); err != nil {
		t.Fatal(err)
	}
	assertAgg(t, e, vPayroll, "d1", "100", 1)
	chg, err := e.AddLink("emp", lBelong, "d2")
	if err != nil {
		t.Fatal(err)
	}
	// d1 由 100 调整为 50，d2 新增 50：触及恰为 2 份。
	assertAgg(t, e, vPayroll, "d1", "50", 1)
	assertAgg(t, e, vPayroll, "d2", "50", 1)
	if n := chg.TouchedGroups(); n != 2 {
		t.Fatalf("AddLink touched = %d, want 2", n)
	}

	chg, err = e.RemoveLink("emp", lBelong, "d2")
	if err != nil {
		t.Fatal(err)
	}
	assertAgg(t, e, vPayroll, "d1", "100", 1)
	assertAgg(t, e, vPayroll, "d2", "0", 0)
	if n := chg.TouchedGroups(); n != 2 {
		t.Fatalf("RemoveLink touched = %d, want 2", n)
	}
}

// 不存在状态与零值必须区分。
func TestAbsentVsZero(t *testing.T) {
	e, st, _ := newTestEngine(t)
	registerPayroll(t, e, PolicyFull)
	mustCreate(t, st, "d1", tDept)
	mustCreate(t, st, "a", tEmp)
	mustCreate(t, st, "b", tEmp)

	_, _ = e.AddLink("a", lBelong, "d1")
	_, _ = e.AddLink("b", lBelong, "d1")
	// 属性尚未写入（不存在）：和为 0，计数为 0。
	assertAgg(t, e, vPayroll, "d1", "0", 0)

	if _, err := e.SetProperty("a", pSalary, FromInt(0)); err != nil {
		t.Fatal(err)
	}
	assertAgg(t, e, vPayroll, "d1", "0", 1) // 零值参与计数

	if _, err := e.SetProperty("b", pSalary, FromInt(7)); err != nil {
		t.Fatal(err)
	}
	assertAgg(t, e, vPayroll, "d1", "7", 2)

	// b 被清除为不存在：计数与和同时减少。
	if _, err := e.SetProperty("b", pSalary, AbsentValue()); err != nil {
		t.Fatal(err)
	}
	assertAgg(t, e, vPayroll, "d1", "0", 1)

	// a 从零改成不存在：计数再减。
	if _, err := e.SetProperty("a", pSalary, AbsentValue()); err != nil {
		t.Fatal(err)
	}
	assertAgg(t, e, vPayroll, "d1", "0", 0)
}

// 分组实例删除：成员解除归属但自身存活；成员删除：扣除最后已知贡献。
func TestDeleteCascade(t *testing.T) {
	e, st, _ := newTestEngine(t)
	registerPayroll(t, e, PolicyFull)
	mustCreate(t, st, "d1", tDept)
	mustCreate(t, st, "d2", tDept)
	mustCreate(t, st, "a", tEmp)
	mustCreate(t, st, "b", tEmp)
	_, _ = e.SetProperty("a", pSalary, FromInt(10))
	_, _ = e.SetProperty("b", pSalary, FromInt(20))
	_, _ = e.AddLink("a", lBelong, "d1")
	_, _ = e.AddLink("b", lBelong, "d1")

	if _, err := e.DeleteObject("b"); err != nil {
		t.Fatal(err)
	}
	assertAgg(t, e, vPayroll, "d1", "10", 1)

	// a 同时归属 d1、d2，删除 d1 后 a 仍归属 d2 且不受影响。
	_, _ = e.AddLink("a", lBelong, "d2")
	assertAgg(t, e, vPayroll, "d2", "10", 1)
	if _, err := e.DeleteObject("d1"); err != nil {
		t.Fatal(err)
	}
	if !st.Exists("a") {
		t.Fatal("aggregated member must survive group deletion")
	}
	assertAgg(t, e, vPayroll, "d1", "0", 0)
	assertAgg(t, e, vPayroll, "d2", "10", 1)
}

// 错误分类与固定优先级：分组不存在 > 类型未声明 > 并发冲突 > 更新失败。
func TestErrorPriority(t *testing.T) {
	e, st, _ := newTestEngine(t)
	registerPayroll(t, e, PolicyFull)
	mustCreate(t, st, "d1", tDept)
	mustCreate(t, st, "emp", tEmp)
	mustCreate(t, st, "other", ID("OtherType"))

	// 分组不存在（且类型也未声明参与该视图）：必须报 GroupNotFound。
	_, err := e.MoveToGroup(vPayroll, "other", "ghost", 0)
	assertKind(t, err, KindGroupNotFound)

	// 分组存在但对象类型未声明。
	_, err = e.MoveToGroup(vPayroll, "other", "d1", 0)
	assertKind(t, err, KindTypeNotDeclared)

	// 合法归属。
	if _, err := e.MoveToGroup(vPayroll, "emp", "d1", 0); err != nil {
		t.Fatal(err)
	}
	v1 := e.MembershipVersion(vPayroll, "emp")
	mustCreate(t, st, "d2", tDept)
	if _, err := e.MoveToGroup(vPayroll, "emp", "d2", v1); err != nil {
		t.Fatal(err)
	}
	// 旧版本号必须冲突被拒绝，且不改变任何聚合结果。
	_, err = e.MoveToGroup(vPayroll, "emp", "d1", v1)
	assertKind(t, err, KindConflict)
	assertAgg(t, e, vPayroll, "d1", "0", 0)
	assertAgg(t, e, vPayroll, "d2", "0", 0)

	// 处理单元内更新失败整体回滚。
	e.SetFailHook(func(op string) error {
		if op == "SetProperty" {
			return &plainError{"injected storage fault"}
		}
		return nil
	})
	_, _ = e.SetProperty("emp", pSalary, FromInt(99))
	e.SetFailHook(nil)
	if got := st.GetProperty("emp", pSalary); got.Present {
		t.Fatalf("rollback failed, property leaked: %s", got.Rat.RatString())
	}
	assertAgg(t, e, vPayroll, "d2", "0", 0)
}

// 视图声明必须显式给出多分组贡献策略。
func TestRejectImplicitPolicy(t *testing.T) {
	e, _, _ := newTestEngine(t)
	err := e.RegisterView(ViewDef{
		Name:      "v",
		GroupType: tDept,
		Sources:   []Source{{ObjectType: tEmp, LinkType: lBelong, PropType: pSalary}},
	})
	if err == nil {
		t.Fatal("expected declaration error for missing contribution policy")
	}
}

// MoveToGroup 处理单元失败时：原分组扣减与新分组增加必须同时撤销，
// 链接归属也保持处理单元前状态。
func TestMoveToGroupRollback(t *testing.T) {
	e, st, _ := newTestEngine(t)
	registerPayroll(t, e, PolicyFull)
	mustCreate(t, st, "d1", tDept)
	mustCreate(t, st, "d2", tDept)
	mustCreate(t, st, "emp", tEmp)
	_, _ = e.SetProperty("emp", pSalary, FromInt(10))
	_, _ = e.AddLink("emp", lBelong, "d1")

	e.SetFailHook(func(op string) error {
		if op == "MoveToGroup" {
			return &plainError{"injected commit fault"}
		}
		return nil
	})
	ver := e.MembershipVersion(vPayroll, "emp")
	_, err := e.MoveToGroup(vPayroll, "emp", "d2", ver)
	assertKind(t, err, KindUpdateFailed)
	e.SetFailHook(nil)

	assertAgg(t, e, vPayroll, "d1", "10", 1)
	assertAgg(t, e, vPayroll, "d2", "0", 0)
	if !st.HasLink("emp", lBelong, "d1") || st.HasLink("emp", lBelong, "d2") {
		t.Fatal("membership links must restore on rollback")
	}
	if e.MembershipVersion(vPayroll, "emp") != ver {
		t.Fatal("membership version must restore on rollback")
	}
}
