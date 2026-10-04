package issue

import (
	"errors"
	"reflect"
	"testing"

	"ontology/bloodstock"
	"ontology/typing"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSpecExample(t *testing.T) {
	// M=500, H=4320。a1 A+ exp1000, a2 A+ exp5000, n1 A- exp3000,
	// o1 O+ exp4000, z1 O- exp2000；P 已确认 A 阳。
	newM := func() *Manager {
		m := New(500, 4320)
		m.Grant("tech", RoleTech)
		m.Grant("nurse1", RoleIssue)
		m.Grant("nurse2", RoleIssue)
		must(t, m.AddBag(0, "a1", bloodstock.A, bloodstock.Positive, 1000))
		must(t, m.AddBag(0, "a2", bloodstock.A, bloodstock.Positive, 5000))
		must(t, m.AddBag(0, "n1", bloodstock.A, bloodstock.Negative, 3000))
		must(t, m.AddBag(0, "o1", bloodstock.O, bloodstock.Positive, 4000))
		must(t, m.AddBag(0, "z1", bloodstock.O, bloodstock.Negative, 2000))
		must(t, m.Type(0, "u", "P", "sp1", bloodstock.A, bloodstock.Positive))
		must(t, m.Type(0, "u", "P", "sp2", bloodstock.A, bloodstock.Positive))
		return m
	}

	m := newM()
	bags, _, err := m.Crossmatch(600, "tech", "P", 3)
	must(t, err)
	if !reflect.DeepEqual(bags, []string{"a2", "n1", "o1"}) {
		t.Fatalf("n=3 got %v", bags)
	}

	m = newM()
	bags, _, err = m.Crossmatch(600, "tech", "P", 4)
	must(t, err)
	if !reflect.DeepEqual(bags, []string{"a2", "n1", "o1", "z1"}) {
		t.Fatalf("n=4 got %v", bags)
	}

	m = newM()
	bags, _, err = m.Crossmatch(600, "tech", "P", 5)
	if !errors.Is(err, ErrStock) || bags != nil {
		t.Fatalf("n=5 应库存不足零预留 got=%v err=%v", bags, err)
	}

	m = newM()
	bags, _, err = m.Crossmatch(499, "tech", "P", 3)
	must(t, err)
	if !reflect.DeepEqual(bags, []string{"a1", "a2", "n1"}) {
		t.Fatalf("499 got %v", bags)
	}

	m = newM()
	bags, _, err = m.Crossmatch(500, "tech", "P", 3)
	must(t, err)
	if !reflect.DeepEqual(bags, []string{"a2", "n1", "o1"}) {
		t.Fatalf("500 got %v", bags)
	}

	// 承 now=600 预留三袋一直未发：n1@3000、o1@4000 报废，a2@4920 释放。
	m = newM()
	_, _, err = m.Crossmatch(600, "tech", "P", 3)
	must(t, err)
	must(t, m.AddBag(600, "tk1", bloodstock.O, bloodstock.Negative, 1_000_000_000))
	must(t, m.AddBag(600, "tk2", bloodstock.O, bloodstock.Negative, 1_000_000_000))
	must(t, m.AddBag(600, "tk3", bloodstock.O, bloodstock.Negative, 1_000_000_000))
	// 用预先加入的远效期血袋在各时点人工 Discard，仅作时钟推进与落地触发。
	must(t, m.Discard(3000, "tech", "tk1"))
	if st, _ := m.BagStatus("n1"); st != bloodstock.Discarded {
		t.Fatalf("n1@3000 应报废 got %v", st)
	}
	must(t, m.Discard(4000, "tech", "tk2"))
	if st, _ := m.BagStatus("o1"); st != bloodstock.Discarded {
		t.Fatal("o1@4000 应报废")
	}
	if st, _ := m.BagStatus("a2"); st != bloodstock.Reserved {
		t.Fatal("a2@4000 应仍预留")
	}
	must(t, m.Discard(4920, "tech", "tk3"))
	if st, _ := m.BagStatus("a2"); st != bloodstock.Available {
		t.Fatalf("a2@4920 应释放（恰等）got %v", st)
	}
}

func TestSingleTypingOnlyO(t *testing.T) {
	m := New(500, 4320)
	m.Grant("tech", RoleTech)
	must(t, m.AddBag(0, "o1", bloodstock.O, bloodstock.Positive, 4000))
	must(t, m.AddBag(0, "z1", bloodstock.O, bloodstock.Negative, 3000))
	must(t, m.AddBag(0, "a1", bloodstock.A, bloodstock.Positive, 5000))
	must(t, m.Type(0, "u", "Q", "sq1", bloodstock.A, bloodstock.Positive))
	bags, _, err := m.Crossmatch(600, "tech", "Q", 2)
	must(t, err)
	if !reflect.DeepEqual(bags, []string{"o1", "z1"}) {
		t.Fatalf("单次 A 阳只用 O：got %v", bags)
	}
	// 未知患者使用独立库存实例（上一步 z1 已被 Q 预留）。
	m2 := New(500, 4320)
	m2.Grant("tech", RoleTech)
	must(t, m2.AddBag(0, "z1", bloodstock.O, bloodstock.Negative, 3000))
	must(t, m2.AddBag(0, "a1", bloodstock.A, bloodstock.Positive, 5000))
	vbags, _, verr := m2.Crossmatch(600, "tech", "V", 1)
	must(t, verr)
	if !reflect.DeepEqual(vbags, []string{"z1"}) {
		t.Fatalf("未知只 O 阴 got %v", vbags)
	}
	_, _, verr = m2.Crossmatch(600, "tech", "W", 2)
	if !errors.Is(verr, ErrStock) {
		t.Fatalf("未知 n=2 库存不足 got %v", verr)
	}
}

func TestContradictionReleasesReservations(t *testing.T) {
	m := New(500, 4320)
	m.Grant("tech", RoleTech)
	m.Grant("sup", RoleSupervisor)
	must(t, m.AddBag(0, "z1", bloodstock.O, bloodstock.Negative, 9000))
	must(t, m.AddBag(0, "z2", bloodstock.O, bloodstock.Negative, 9000))
	must(t, m.Type(0, "u", "Q", "q1", bloodstock.A, bloodstock.Positive))
	_, _, err := m.Crossmatch(0, "tech", "Q", 2)
	must(t, err)
	if st, _ := m.BagStatus("z1"); st != bloodstock.Reserved {
		t.Fatal("应已预留 z1")
	}
	must(t, m.Type(10, "u", "Q", "q2", bloodstock.B, bloodstock.Positive))
	if m.PatientKind("Q") != typing.Disputed {
		t.Fatal("Q 应存疑")
	}
	for _, b := range []string{"z1", "z2"} {
		if st, _ := m.BagStatus(b); st != bloodstock.Available {
			t.Fatalf("%s 应被释放回可用 got %v", b, st)
		}
	}
	bags, _, err := m.Crossmatch(10, "tech", "Q", 2)
	must(t, err)
	if len(bags) != 2 {
		t.Fatalf("存疑可用 O 阴 got %v", bags)
	}
	if err := m.Resolve(20, "tech", "Q", bloodstock.B, bloodstock.Negative); !errors.Is(err, ErrForbidden) {
		t.Fatalf("非主管裁定应无资格 got %v", err)
	}
	must(t, m.Resolve(20, "sup", "Q", bloodstock.B, bloodstock.Negative))
	if m.PatientKind("Q") != typing.Confirmed {
		t.Fatal("Resolve 后应已确认")
	}
}

func TestIssueDoubleCheckAndReturnWindow(t *testing.T) {
	m := New(0, 100000)
	m.Grant("tech", RoleTech)
	m.Grant("n1", RoleIssue)
	m.Grant("n2", RoleIssue)
	must(t, m.AddBag(0, "z1", bloodstock.O, bloodstock.Negative, 5000))
	must(t, m.AddBag(0, "z2", bloodstock.O, bloodstock.Negative, 5000))
	picked, _, err := m.Crossmatch(0, "tech", "P", 2)
	must(t, err)
	if !reflect.DeepEqual(picked, []string{"z1", "z2"}) {
		t.Fatalf("picked=%v", picked)
	}
	if err := m.Issue(700, "n1", "n1", "P", "z1"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("同人发血应参数非法 got %v", err)
	}
	if err := m.Issue(700, "n1", "tech", "P", "z1"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("无资格 got %v", err)
	}
	must(t, m.Issue(700, "n1", "n2", "P", "z1"))
	if st, _ := m.BagStatus("z1"); st != bloodstock.Issued {
		t.Fatal("应已发")
	}
	must(t, m.Return(730, "n1", "z1"))
	if st, _ := m.BagStatus("z1"); st != bloodstock.Available {
		t.Fatalf("730 恰等可退 got %v", st)
	}
	if p := m.BagPatient("z1"); p != "" {
		t.Fatal("退回后原预留不恢复")
	}
	// 另一袋验证 731 超时不可退
	must(t, m.Issue(730, "n1", "n2", "P", "z2"))
	if err := m.Return(761, "n1", "z2"); !errors.Is(err, ErrReturnLate) {
		t.Fatalf("731 应超时不可退 got %v", err)
	}
	if st, _ := m.BagStatus("z2"); st != bloodstock.Issued {
		t.Fatal("超时拒绝不应改状态")
	}
}

func TestRejectionOrder(t *testing.T) {
	m := New(0, 100)
	if err := m.AddBag(10, "", bloodstock.O, bloodstock.Negative, 20); !errors.Is(err, ErrInvalid) {
		t.Fatalf("空血袋号 got %v", err)
	}
	if err := m.AddBag(10, "b", bloodstock.ABO(9), bloodstock.Negative, 20); !errors.Is(err, ErrInvalid) {
		t.Fatalf("非法 ABO got %v", err)
	}
	if err := m.AddBag(10, "b", bloodstock.O, bloodstock.Negative, 10); !errors.Is(err, ErrInvalid) {
		t.Fatalf("exp<=now 应非法 got %v", err)
	}

	m.Grant("tech", RoleTech)
	must(t, m.AddBag(100, "b", bloodstock.O, bloodstock.Negative, 200))
	if err := m.Discard(50, "x", "nobag"); !errors.Is(err, ErrClock) {
		t.Fatalf("时钟回退应先于不存在 got %v", err)
	}
	if err := m.Discard(100, "stranger", "nobag"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("不存在先于资格 got %v", err)
	}
	if err := m.Issue(100, "stranger", "n2", "P", "b"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("无发血资格 got %v", err)
	}
	m.Grant("n1", RoleIssue)
	m.Grant("n2", RoleIssue)
	if err := m.Issue(100, "n1", "n2", "P", "b"); !errors.Is(err, ErrState) {
		t.Fatalf("非预留发血应状态不符 got %v", err)
	}

	must(t, m.Type(100, "u", "P", "sp1", bloodstock.O, bloodstock.Negative))
	if err := m.Type(100, "u", "P2", "sp1", bloodstock.O, bloodstock.Negative); !errors.Is(err, ErrSampleDup) {
		t.Fatalf("标本重复 got %v", err)
	}
	if err := m.AddBag(100, "b", bloodstock.O, bloodstock.Negative, 300); !errors.Is(err, ErrBagDup) {
		t.Fatalf("血袋重复 got %v", err)
	}

	_, _, e := m.Crossmatch(100, "n1", "V", 20)
	if !errors.Is(e, ErrForbidden) {
		t.Fatalf("无配血资格先于库存不足 got %v", e)
	}
}

func TestRejectedOpDoesNotAdvanceClock(t *testing.T) {
	m := New(0, 100)
	m.Grant("tech", RoleTech)
	must(t, m.AddBag(100, "b", bloodstock.O, bloodstock.Negative, 1000))
	_, _, err := m.Crossmatch(100, "tech", "P", 5)
	if !errors.Is(err, ErrStock) {
		t.Fatal(err)
	}
	// 被拒后同一 now 可再次操作；更小 now 的操作只应在确有推进时报回退。
	bags, _, err := m.Crossmatch(100, "tech", "P", 1)
	must(t, err)
	if len(bags) != 1 {
		t.Fatalf("被拒操作不应留预留，got %v", bags)
	}
}

func TestCompatibilityOfEveryIssuedBag(t *testing.T) {
	// 未知/存疑者从不持有非 O 阴预留（随机对照在 fuzz 中强校验，此处定点）。
	m := New(0, 100000)
	m.Grant("tech", RoleTech)
	m.Grant("n1", RoleIssue)
	m.Grant("n2", RoleIssue)
	must(t, m.AddBag(0, "a+", bloodstock.A, bloodstock.Positive, 9000))
	must(t, m.AddBag(0, "o-", bloodstock.O, bloodstock.Negative, 9000))
	_, _, err := m.Crossmatch(0, "tech", "V", 1)
	must(t, err)
	if st, _ := m.BagStatus("a+"); st != bloodstock.Available {
		t.Fatal("未知患者绝不应预留 A+")
	}
	must(t, m.Issue(0, "n1", "n2", "V", "o-"))
}
