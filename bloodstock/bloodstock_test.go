package bloodstock

import (
	"reflect"
	"testing"
)

func mkStore(M int64) *Store { return NewStore(M) }

func TestReserveOrderingAndExpiry(t *testing.T) {
	// 题面例：M=500。a1(A+)exp=1000, a2(A+)exp=5000, n1(A-)exp=3000,
	// o1(O+)exp=4000, z1(O-)exp=2000。
	setup := func() *Store {
		s := mkStore(500)
		s.Add("a1", A, Positive, 1000)
		s.Add("a2", A, Positive, 5000)
		s.Add("n1", A, Negative, 3000)
		s.Add("o1", O, Positive, 4000)
		s.Add("z1", O, Negative, 2000)
		return s
	}
	// A 阳受者次序：A+ A- O+ O-
	orderAPos := []int{Slot(A, Positive), Slot(A, Negative), Slot(O, Positive), Slot(O, Negative)}

	t.Run("now600_n3", func(t *testing.T) {
		s := setup()
		picked, examined, ok := s.Reserve(600, 4320, orderAPos, "P", 3)
		if !ok || !reflect.DeepEqual(picked, []string{"a2", "n1", "o1"}) {
			t.Fatalf("picked=%v ok=%v", picked, ok)
		}
		// a1 因效期被排除计入 examined：3 中选 + 1 排除 = 4 <= 3+1
		if examined != 4 {
			t.Fatalf("examined=%d want 4", examined)
		}
	})
	t.Run("now600_n4", func(t *testing.T) {
		s := setup()
		picked, _, ok := s.Reserve(600, 4320, orderAPos, "P", 4)
		if !ok || !reflect.DeepEqual(picked, []string{"a2", "n1", "o1", "z1"}) {
			t.Fatalf("picked=%v ok=%v", picked, ok)
		}
	})
	t.Run("now600_n5_all_or_nothing", func(t *testing.T) {
		s := setup()
		picked, _, ok := s.Reserve(600, 4320, orderAPos, "P", 5)
		if ok || picked != nil {
			t.Fatalf("应库存不足且零预留，got %v", picked)
		}
		for _, id := range []string{"a2", "n1", "o1", "z1"} {
			if b, _ := s.Get(id); b.Status != Available {
				t.Fatalf("%s 应仍可用", id)
			}
		}
	})
	t.Run("now499_boundary_inclusive", func(t *testing.T) {
		s := setup()
		// exp=1000 > 499+500=999，a1 入选；exp 恰等 now+M 才排除（now=500）。
		picked, _, ok := s.Reserve(499, 4320, orderAPos, "P", 3)
		if !ok || !reflect.DeepEqual(picked, []string{"a1", "a2", "n1"}) {
			t.Fatalf("picked=%v ok=%v", picked, ok)
		}
	})
	t.Run("now500_exact_exclusion", func(t *testing.T) {
		s := setup()
		picked, _, ok := s.Reserve(500, 4320, orderAPos, "P", 3)
		if !ok || !reflect.DeepEqual(picked, []string{"a2", "n1", "o1"}) {
			t.Fatalf("picked=%v ok=%v", picked, ok)
		}
	})
}

func TestLandExpireBeforeRelease(t *testing.T) {
	// 承 now=600 预留三袋 a2,n1,o1（H=4320）：
	// n1 exp=3000、o1 exp=4000 报废；a2 在 600+4320=4920 释放。
	s := mkStore(500)
	s.Add("a2", A, Positive, 5000)
	s.Add("n1", A, Negative, 3000)
	s.Add("o1", O, Positive, 4000)
	order := []int{Slot(A, Positive), Slot(A, Negative), Slot(O, Positive)}
	_, _, ok := s.Reserve(600, 4320, order, "P", 3)
	if !ok {
		t.Fatal("reserve failed")
	}
	discarded := map[string]bool{}
	s.Land(3000, func(bag string) { discarded[bag] = true })
	if b, _ := s.Get("n1"); b.Status != Discarded || !discarded["n1"] {
		t.Fatalf("n1 应于 3000 报废, status=%v", b.Status)
	}
	s.Land(4000, func(bag string) { discarded[bag] = true })
	if b, _ := s.Get("o1"); b.Status != Discarded {
		t.Fatal("o1 应于 4000 报废")
	}
	if b, _ := s.Get("a2"); b.Status != Reserved {
		t.Fatal("a2 在 4000 时应仍预留")
	}
	s.Land(4920, nil)
	if b, _ := s.Get("a2"); b.Status != Available {
		t.Fatalf("a2 应于 4920 释放（恰等），got %v", b.Status)
	}
	if s.TotalPopped > s.TotalApplied+1 {
		t.Fatalf("取出 %d 应 <= 落地 %d+1", s.TotalPopped, s.TotalApplied)
	}
}

func TestLandReleaseExpireSameTime(t *testing.T) {
	// 预留释放与效期同刻：报废优先。
	s := mkStore(0)
	s.Add("b1", O, Negative, 100)
	// now=0, H=100 -> 释放时刻=100，与 exp=100 同刻。
	picked, _, ok := s.Reserve(0, 100, []int{Slot(O, Negative)}, "P", 1)
	if !ok || picked[0] != "b1" {
		t.Fatal("reserve failed")
	}
	s.Land(100, nil)
	if b, _ := s.Get("b1"); b.Status != Discarded {
		t.Fatalf("同刻应报废优先，got %v", b.Status)
	}
	if s.LandPopped != 1 || s.LandApplied != 1 {
		t.Fatalf("同刻两事件应折叠为取出1/落地1, got popped=%d applied=%d", s.LandPopped, s.LandApplied)
	}
}

func TestIssueAndReturnLifecycle(t *testing.T) {
	s := mkStore(0)
	s.Add("b1", O, Negative, 1000)
	picked, _, ok := s.Reserve(0, 10000, []int{Slot(O, Negative)}, "P", 1)
	if !ok {
		t.Fatal("reserve")
	}
	if !s.MarkIssued(700, "P", picked[0]) {
		t.Fatal("issue")
	}
	// 已发不落地：1000 时刻 Land 不影响。
	s.Land(1000, nil)
	if b, _ := s.Get("b1"); b.Status != Issued {
		t.Fatal("已发血袋不参与落地")
	}
	s.Return(730, "b1")
	if b, _ := s.Get("b1"); b.Status != Available {
		t.Fatalf("730 恰等可退，got %v", b.Status)
	}
	// 退回时已过效期 -> 直接报废
	s2 := mkStore(0)
	s2.Add("b2", O, Negative, 720)
	s2.Reserve(0, 10000, []int{Slot(O, Negative)}, "P", 1)
	s2.MarkIssued(700, "P", "b2")
	existed, expired := s2.Return(730, "b2")
	if !existed || !expired {
		t.Fatal("退回时 now>=exp 应直接报废")
	}
	if b, _ := s2.Get("b2"); b.Status != Discarded {
		t.Fatal("应报废")
	}
}

func TestExaminedBoundIndependence(t *testing.T) {
	// 空槽最多 peek 8 次：无库存时 examined=0，库存不足时不超过 n+排除+8。
	s := mkStore(0)
	for i := 0; i < 100; i++ {
		// 全部放到 O-
		s.Add(string(rune('a'+i%26))+itoa(i), O, Negative, 10)
	}
	orderAll := []int{
		Slot(A, Positive), Slot(A, Negative),
		Slot(B, Positive), Slot(B, Negative),
		Slot(AB, Positive), Slot(AB, Negative),
		Slot(O, Positive), Slot(O, Negative),
	}
	// now=10, M=0 -> exp<=10 全部被排除；求 5 袋失败。
	_, examined, ok := s.Reserve(10, 100, orderAll, "P", 5)
	if ok {
		t.Fatal("应库存不足")
	}
	// examined=100 全排除，n=5：100 <= 5+100+8 恒成立（关键是不扫两遍）。
	if examined > 5+100+8 {
		t.Fatalf("examined=%d 越界", examined)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func TestReserveReleasePatient(t *testing.T) {
	s := mkStore(0)
	s.Add("x1", O, Negative, 100)
	s.Add("x2", O, Negative, 100)
	s.Reserve(0, 50, []int{Slot(O, Negative)}, "P", 2)
	n := s.ReleasePatient("P")
	if n != 2 {
		t.Fatalf("释放 %d 袋", n)
	}
	for _, id := range []string{"x1", "x2"} {
		if b, _ := s.Get(id); b.Status != Available || b.Patient != "" {
			t.Fatalf("%s 应可用且无患者", id)
		}
	}
	// 释放后不应在 50 时刻又触发释放事件（已物理删除）。
	s.Land(50, nil)
	if s.LandPopped != 0 {
		t.Fatalf("释放事件应已删除, popped=%d", s.LandPopped)
	}
}

func TestABOSlotOrder(t *testing.T) {
	cases := []struct {
		name string
		abo  ABO
		rh   Rh
		want []int
	}{
		{"A+", A, Positive, []int{0, 1, 6, 7}},
		{"A-", A, Negative, []int{1, 7}},
		{"B+", B, Positive, []int{2, 3, 6, 7}},
		{"B-", B, Negative, []int{3, 7}},
		{"AB+", AB, Positive, []int{4, 5, 0, 1, 2, 3, 6, 7}},
		{"AB-", AB, Negative, []int{5, 1, 3, 7}},
		{"O+", O, Positive, []int{6, 7}},
		{"O-", O, Negative, []int{7}},
	}
	// 直接验证次序的槽位序列（与 typing.Order 同规则，此处验证 Slot 编码）。
	for _, c := range cases {
		got := []int{}
		var abos []ABO
		switch c.abo {
		case A:
			abos = []ABO{A, O}
		case B:
			abos = []ABO{B, O}
		case AB:
			abos = []ABO{AB, A, B, O}
		case O:
			abos = []ABO{O}
		}
		if c.rh == Negative {
			for _, a := range abos {
				got = append(got, Slot(a, Negative))
			}
		} else {
			for _, a := range abos {
				got = append(got, Slot(a, Positive), Slot(a, Negative))
			}
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Fatalf("%s got=%v want=%v", c.name, got, c.want)
		}
	}
}
