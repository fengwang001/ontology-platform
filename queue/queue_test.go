package queue

import (
	"testing"

	"ontology/triage"
)

// lv(n) 构造恰好定级为 n 的生命体征（避免 m=3 干扰）。
func lv(n int) triage.Vitals {
	switch n {
	case 1:
		return triage.Vitals{HR: 120, SBP: 69, SPO2: 92, LOC: triage.V} // 2+3+1+1=7
	case 2:
		return triage.Vitals{HR: 120, SBP: 75, SPO2: 93, LOC: triage.A} // s=5
	case 3:
		return triage.Vitals{HR: 105, SBP: 95, SPO2: 92, LOC: triage.V} // s=4
	default:
		return triage.Vitals{HR: 80, SBP: 120, SPO2: 96, LOC: triage.A} // s=0
	}
}

func TestRegisterOrder(t *testing.T) {
	q := New(5, 15, 30, 60, 5)
	e1 := q.Register(0, "a", lv(3))
	e2 := q.Register(5, "b", lv(3))
	if e1.RegNo != 1 || e2.RegNo != 2 || e1.Q != 0 || e1.LA != 0 {
		t.Fatalf("reg fields wrong: %+v %+v", e1, e2)
	}
	var got []string
	q.FirstEligible(5, []int{3, 4}, func(e *Entry) bool {
		got = append(got, string(e.Patient))
		return false
	})
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("order=%v want [a b]", got)
	}
}

func TestReassessQAsymmetry(t *testing.T) {
	q := New(5, 15, 30, 60, 5)
	q.Register(0, "a", lv(4))
	q.Register(5, "b", lv(3))
	// a 在 t=10 升级 4->3：q 保留 0，应排到 b 前。
	ea, old := q.Reassess(10, "a", lv(3))
	if old != 4 || ea.Q != 0 || ea.LA != 10 {
		t.Fatalf("upgrade q kept: %+v old=%d", ea, old)
	}
	var got []string
	q.FirstEligible(10, []int{3}, func(e *Entry) bool {
		got = append(got, string(e.Patient))
		return false
	})
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("after upgrade order=%v want [a b]", got)
	}
	// b 在 t=20 降级 3->4：q 重置为 20。
	eb, old := q.Reassess(20, "b", lv(4))
	if old != 3 || eb.Q != 20 || eb.LA != 20 {
		t.Fatalf("downgrade q reset: %+v old=%d", eb, old)
	}
	// 同级重评：q 不变，仅 la 变。
	ec, _ := q.Reassess(25, "a", lv(3))
	if ec.Q != 0 || ec.LA != 25 {
		t.Fatalf("same level q unchanged: %+v", ec)
	}
}

func TestOverdueEquality(t *testing.T) {
	q := New(5, 15, 30, 60, 5)
	e := q.Register(0, "a", lv(3)) // R3=30
	if q.Overdue(30, e) {
		t.Fatal("now-la==R must not be overdue")
	}
	if !q.Overdue(31, e) {
		t.Fatal("now-la>R must be overdue")
	}
}

func TestFirstEligibleExamined(t *testing.T) {
	q := New(5, 15, 30, 60, 5)
	// 3 级放 a,b；均在 t=0 登记。t=31 全部逾期。
	q.Register(0, "a", lv(3))
	q.Register(0, "b", lv(3))
	q.Register(2, "c", lv(3))
	// 只复评 c：la=31。
	q.Reassess(31, "c", lv(3))
	q.ResetExamined()
	var skipped []string
	picked := q.FirstEligible(31, []int{2, 3, 4}, func(e *Entry) bool {
		if q.Overdue(31, e) {
			skipped = append(skipped, string(e.Patient))
			return false
		}
		return true
	})
	if picked == nil || picked.Patient != "c" {
		t.Fatalf("picked=%v want c", picked)
	}
	if len(skipped) != 2 {
		t.Fatalf("skipped=%v want a,b", skipped)
	}
	if q.Examined() > len(skipped)+1 {
		t.Fatalf("examined=%d > skipped+1=%d", q.Examined(), len(skipped)+1)
	}
}

func TestReturnMissAndGone(t *testing.T) {
	q := New(5, 15, 30, 60, 5)
	b := q.Register(5, "b", lv(3))
	a := q.Register(0, "a", lv(3))
	q.MarkCalled(a)
	q.MarkCalled(b)
	// a 过号回队，q=callAt+A=41，应排在 b（仍被叫）不参与；先只测 a。
	q.Return(a, 41)
	if a.Status != Waiting || a.Miss != 1 || a.Q != 41 {
		t.Fatalf("return: %+v", a)
	}
	q.MarkCalled(a)
	q.Return(a, 50)
	if a.Miss != 2 || a.Q != 50 {
		t.Fatalf("second return: %+v", a)
	}
	q.MarkCalled(a)
	gone := q.Return(a, 60)
	if !gone || a.Status != Gone || a.Miss != 3 {
		t.Fatalf("third must be gone: %+v gone=%v", a, gone)
	}
}

func TestTreapOrder(t *testing.T) {
	tr := NewTreap[int]()
	keys := []uint64{5, 2, 8, 1, 4, 7, 3}
	for i, k := range keys {
		tr.Insert(k, i)
	}
	var out []uint64
	for {
		k, _, ok := tr.PopFirst()
		if !ok {
			break
		}
		out = append(out, k)
	}
	want := []uint64{1, 2, 3, 4, 5, 7, 8}
	if len(out) != len(want) {
		t.Fatalf("pop=%v want %v", out, want)
	}
	for i := range want {
		if out[i] != want[i] {
			t.Fatalf("pop=%v want %v", out, want)
		}
	}
}
