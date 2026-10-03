package approval

import (
	"errors"
	"testing"

	"ontology/org"
)

func newTestEngine(t *testing.T, T int64) (*Engine, *org.Org) {
	t.Helper()
	o := org.New()
	e, err := New(o, T)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return e, o
}

// 规格主例：T=10，A→B→C→D，额度 100/500/1000，金额 300，候选 [C,D]。
func TestSpecExample(t *testing.T) {
	e, o := newTestEngine(t, 10)
	buildChain(t, o, []string{"A", "B", "C", "D"})
	mustLimit(t, o, "B", 100)
	mustLimit(t, o, "C", 500)
	mustLimit(t, o, "D", 1000)
	if err := e.Submit("r", "A", 300, 0); err != nil {
		t.Fatal(err)
	}
	st, err := e.Status("r", 9) // 差 1：仍为 C
	check(t, st, err, Pending, "C", 0, 0)
	st, err = e.Status("r", 10) // 恰等到期：升级 D，新 ta=到期时刻 10
	check(t, st, err, Pending, "D", 10, 0)
	st, err = e.Status("r", 25) // D 于 20 到期，链尽
	if err != nil || st.Outcome != Expired || st.FinalAt != 20 {
		t.Fatalf("Status(25)=%+v,%v want Expired@20", st, err)
	}
	t.Logf("依据 ta+T<=now：C@0→D@10→Expired@20，得 %s@%d", st.Outcome, st.FinalAt)
}

func TestRevokeThenEscalate(t *testing.T) {
	e, o := newTestEngine(t, 10)
	buildChain(t, o, []string{"A", "B", "C", "D"})
	mustLimit(t, o, "B", 100)
	mustLimit(t, o, "C", 500)
	mustLimit(t, o, "D", 1000)
	if err := e.Submit("r", "A", 300, 0); err != nil {
		t.Fatal(err)
	}
	if err := o.SetLimit("C", 0); err != nil { // 撤权不触发升级
		t.Fatal(err)
	}
	if err := e.Decide("r", "C", true, 5); !errors.Is(err, ErrRevoked) {
		t.Fatalf("Decide(C,5)=%v want ErrRevoked", err)
	}
	st, _ := e.Status("r", 5) // 保持待决、ta 不变
	check(t, st, nil, Pending, "C", 0, 0)
	// 10 时 C 已升级；撤权者不是审批人，ErrNotAssignee 优先。
	if err := e.Decide("r", "C", true, 10); !errors.Is(err, ErrNotAssignee) {
		t.Fatalf("Decide(C,10)=%v want ErrNotAssignee", err)
	}
	if err := e.Decide("r", "D", true, 10); err != nil {
		t.Fatalf("Decide(D,10)=%v want nil", err)
	}
	st, _ = e.Status("r", 10)
	if st.Outcome != Approved || st.FinalAt != 10 {
		t.Fatalf("终局=%+v want Approved@10", st)
	}
	t.Log("撤权→ErrRevoked 且不动 ta；超时升级 D 后实时复核 1000>=300→Approved@10")
}

func TestFourCandidatesAndEquality(t *testing.T) {
	e, o := newTestEngine(t, 10)
	buildChain(t, o, []string{"A", "B", "C", "D", "E", "F"})
	for _, x := range []struct {
		n string
		v int64
	}{{"B", 100}, {"C", 500}, {"D", 600}, {"E", 700}, {"F", 800}} {
		mustLimit(t, o, x.n, x.v)
	}
	if err := e.Submit("r4", "A", 300, 0); err != nil {
		t.Fatal(err)
	}
	st, _ := e.Status("r4", 35)
	check(t, st, nil, Pending, "F", 30, 0)
	st, _ = e.Status("r4", 40)
	if st.Outcome != Expired || st.FinalAt != 40 {
		t.Fatalf("Status(40)=%+v want Expired@40", st)
	}
	if err := e.Submit("r5", "A", 500, 0); err != nil { // B(100) 排除
		t.Fatal(err)
	}
	st, _ = e.Status("r5", 0)
	if st.Assignee != "C" { // C 额度恰等 500，入选
		t.Fatalf("equal-limit assignee=%s want C", st.Assignee)
	}
}

func TestFrozenChain(t *testing.T) {
	e, o := newTestEngine(t, 10)
	buildChain(t, o, []string{"A", "B", "C"})
	mustLimit(t, o, "B", 100)
	mustLimit(t, o, "C", 1000)
	if err := e.Submit("r", "A", 300, 0); err != nil {
		t.Fatal(err)
	}
	if err := o.SetManager("X", "B"); err != nil {
		t.Fatal(err)
	}
	if err := o.SetManager("A", "X"); err != nil { // 提交后插入更近上级
		t.Fatal(err)
	}
	mustLimit(t, o, "X", 999)
	mustLimit(t, o, "B", 999)
	st, _ := e.Status("r", 5)
	if st.Assignee != "C" {
		t.Fatalf("frozen chain violated: assignee=%s want C", st.Assignee)
	}
}
