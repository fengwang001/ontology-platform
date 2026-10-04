package room

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/queue"
	"ontology/triage"
)

func l3() triage.Vitals { return triage.Vitals{HR: 105, SBP: 95, SPO2: 92, LOC: triage.V} }
func l4() triage.Vitals { return triage.Vitals{HR: 80, SBP: 120, SPO2: 96, LOC: triage.A} }
func l1() triage.Vitals { return triage.Vitals{HR: 135, SBP: 120, SPO2: 96, LOC: triage.A} }
func l2() triage.Vitals { return triage.Vitals{HR: 120, SBP: 75, SPO2: 93, LOC: triage.A} }

func mustReg(t *testing.T, s *System, now int, p string, v triage.Vitals) (int, int) {
	t.Helper()
	reg, lv, err := s.Register(now, p, v)
	if err != nil {
		t.Fatalf("Register %s: %v", p, err)
	}
	return reg, lv
}

func TestSpecCallExample(t *testing.T) {
	s := NewSystem(5, 15, 30, 1, 5) // 4 级复评时限仅 1，filler 很快逾期
	if err := s.AddRoom("n1", Clinic); err != nil {
		t.Fatal(err)
	}
	mustReg(t, s, 0, "a", l3())
	mustReg(t, s, 5, "b", l3())
	mustReg(t, s, 10, "c", l4())
	if lv, err := s.Reassess(20, "c", l3()); err != nil || lv != 3 {
		t.Fatalf("c upgrade: lv=%d err=%v", lv, err)
	}
	if _, err := s.Reassess(25, "a", l4()); err != nil {
		t.Fatal(err)
	}
	r, err := s.Call(35, "n1")
	if err != nil || r.Patient != "b" || len(r.Skipped) != 0 {
		t.Fatalf("call@35=%+v err=%v", r, err)
	}
}

func TestSpecCallOverdueAndArrive(t *testing.T) {
	s := NewSystem(5, 15, 30, 60, 5)
	s.AddRoom("n1", Clinic)
	mustReg(t, s, 0, "a", l3())
	mustReg(t, s, 5, "b", l3())
	mustReg(t, s, 10, "c", l4())
	s.Reassess(20, "c", l3())
	s.Reassess(25, "a", l4())
	r, err := s.Call(36, "n1")
	if err != nil || r.Patient != "c" || fmt.Sprint(r.Skipped) != "[b]" {
		t.Fatalf("call@36=%+v err=%v want c skip [b]", r, err)
	}
	if err := s.Arrive(41, "c"); err != nil {
		t.Fatalf("arrive@41 (==callAt+A): %v", err)
	}
}

func TestMissRequeueAtDeadline(t *testing.T) {
	s := NewSystem(5, 15, 30, 60, 5)
	s.AddRoom("n1", Clinic)
	mustReg(t, s, 0, "a", l3()) // 队首
	if _, err := s.Call(30, "n1"); err != nil {
		t.Fatal(err)
	}
	// t=36 接受 Register：先落地 a（deadline=35<36），a q=35, miss=1。
	mustReg(t, s, 36, "fresh", l4())
	ea, _ := s.qq.Get("a")
	if ea.Miss != 1 || ea.Q != 35 || ea.Status != Waiting {
		t.Fatalf("a after miss: %+v", ea)
	}
	// a 刚落回 la=0 已逾期；复评后 la=36，可被叫且 q=35。
	if _, err := s.Reassess(36, "a", l3()); err != nil {
		t.Fatal(err)
	}
	r, err := s.Call(36, "n1")
	if err != nil || r.Patient != "a" || len(r.Skipped) != 0 {
		t.Fatalf("requeued a callable: %+v err=%v", r, err)
	}
}

func TestThreeMissGone(t *testing.T) {
	// R4=8：a 在每次 Call 前都复评故新鲜；filler 登记 4 分钟后即逾期。
	s := NewSystem(5, 15, 30, 8, 5)
	s.AddRoom("n1", Clinic)
	mustReg(t, s, 0, "a", l4())
	if _, err := s.Reassess(9, "a", l4()); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		base := i * 10
		cr, err := s.Call(base, "n1")
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if cr.Patient != "a" {
			t.Fatalf("call %d picked=%s want a", i, cr.Patient)
		}
		mustReg(t, s, base+6, fmt.Sprintf("x%d", i), l4())
		if i < 3 {
			if _, err := s.Reassess(base+7, "a", l4()); err != nil {
				t.Fatalf("reassess %d: %v", i, err)
			}
		}
	}
	// a 每次复评保持新鲜；filler 因 R4=1 全部逾期被跳过。第三次落地后 a=Gone。
	ea, _ := s.qq.Get("a")
	if ea.Status != 2 || ea.Miss != 3 {
		t.Fatalf("a status=%d miss=%d want Gone/3", ea.Status, ea.Miss)
	}
	if _, err := s.Reassess(40, "a", l4()); !errors.Is(err, ErrState) {
		t.Fatalf("gone reassess: %v", err)
	}
}

func TestRoomKindEligibility(t *testing.T) {
	s := NewSystem(5, 15, 30, 60, 5)
	s.AddRoom("res", Rescue)
	s.AddRoom("cli", Clinic)
	mustReg(t, s, 0, "one", l1())
	if _, err := s.Call(1, "cli"); !errors.Is(err, ErrNoCallee) {
		t.Fatalf("level1 into clinic: %v", err)
	}
	if r, err := s.Call(1, "res"); err != nil || r.Patient != "one" {
		t.Fatalf("level1 rescue: %+v %v", r, err)
	}

	s2 := NewSystem(5, 15, 30, 60, 5)
	s2.AddRoom("cli", Clinic)
	mustReg(t, s2, 0, "two", l2())
	if _, err := s2.Call(1, "cli"); err != nil {
		t.Fatalf("level2 clinic: %v", err)
	}
	s3 := NewSystem(5, 15, 30, 60, 5)
	s3.AddRoom("res", Rescue)
	mustReg(t, s3, 0, "two", l2())
	if _, err := s3.Call(1, "res"); err != nil {
		t.Fatalf("level2 rescue: %v", err)
	}
	s4 := NewSystem(5, 15, 30, 60, 5)
	s4.AddRoom("res", Rescue)
	mustReg(t, s4, 0, "three", l3())
	if _, err := s4.Call(1, "res"); !errors.Is(err, ErrNoCallee) {
		t.Fatalf("level3 rescue: %v", err)
	}
}

func TestRejectOrder(t *testing.T) {
	s := NewSystem(5, 15, 30, 60, 5)
	s.AddRoom("n1", Clinic)
	mustReg(t, s, 10, "a", l3())
	cases := []struct {
		name string
		fn   func() error
		want error
	}{
		{"invalid time", func() error { _, _, e := s.Register(-1, "x", l3()); return e }, ErrInvalid},
		{"invalid vitals", func() error { _, _, e := s.Register(11, "x", triage.Vitals{HR: 999}); return e }, ErrInvalid},
		{"clock back", func() error { _, _, e := s.Register(5, "x", l3()); return e }, ErrClock},
		{"dup register", func() error { _, _, e := s.Register(11, "a", l3()); return e }, ErrNotFound},
		{"unknown patient", func() error { _, e := s.Reassess(11, "ghost", l3()); return e }, ErrNotFound},
		{"unknown room", func() error { _, e := s.Call(11, "zz"); return e }, ErrNotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := c.fn(); !errors.Is(err, c.want) {
				t.Fatalf("got %v want %v", err, c.want)
			}
		})
	}
	// 房间为空、a 在 t=41 已逾期：无可叫者。
	if _, err := s.Call(41, "n1"); !errors.Is(err, ErrNoCallee) {
		t.Fatalf("empty room no callee: %v", err)
	}
	mustReg(t, s, 42, "b", l4())
	if r, err := s.Call(42, "n1"); err != nil || r.Patient != "b" {
		t.Fatalf("call a: %+v %v", r, err)
	}
	if _, err := s.Call(43, "n1"); !errors.Is(err, ErrState) {
		t.Fatalf("busy room: %v", err)
	}
	mustReg(t, s, 43, "c", l4())
	if err := s.Arrive(43, "c"); !errors.Is(err, ErrState) {
		t.Fatalf("arrive waiting: %v", err)
	}
	if err := s.AddRoom("n1", Clinic); !errors.Is(err, ErrNotFound) {
		t.Fatalf("dup room: %v", err)
	}
}

func TestRejectedNoLanding(t *testing.T) {
	s := NewSystem(5, 15, 30, 60, 5)
	s.AddRoom("n1", Clinic)
	mustReg(t, s, 0, "a", l3())
	s.Call(0, "n1")
	before := s.maxNow
	if _, _, err := s.Register(-5, "x", l3()); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := s.Call(1, "n1"); !errors.Is(err, ErrState) {
		t.Fatalf("busy room reject: %v", err)
	}
	if s.maxNow != before {
		t.Fatalf("maxNow changed on reject: %d->%d", before, s.maxNow)
	}
	ea, _ := s.qq.Get("a")
	if ea.Status != 1 || ea.Miss != 0 {
		t.Fatalf("reject caused landing: %+v", ea)
	}
}

func TestArriveEqualityAndFinish(t *testing.T) {
	s := NewSystem(5, 15, 30, 60, 5)
	s.AddRoom("n1", Clinic)
	mustReg(t, s, 0, "a", l3())
	s.Call(0, "n1")
	if err := s.Arrive(5, "a"); err != nil {
		t.Fatalf("arrive exactly at A: %v", err)
	}
	if p, err := s.Finish(6, "n1"); err != nil || p != "a" {
		t.Fatalf("finish: p=%s err=%v", p, err)
	}
	if _, err := s.Finish(7, "n1"); !errors.Is(err, ErrState) {
		t.Fatalf("double finish: %v", err)
	}
	ea, _ := s.qq.Get("a")
	if ea.Status != 3 {
		t.Fatalf("finished status=%d", ea.Status)
	}
}

func TestExaminedScales(t *testing.T) {
	for _, n := range []int{100, 10000} {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			s := NewSystem(5, 15, 30, 60, 5)
			s.AddRoom("n1", Clinic)
			for i := 0; i < n; i++ {
				mustReg(t, s, 0, fmt.Sprintf("p%05d", i), l3())
			}
			last := fmt.Sprintf("p%05d", n-1)
			if _, err := s.Reassess(31, last, l3()); err != nil {
				t.Fatal(err)
			}
			s.qq.ResetExamined()
			r, err := s.Call(31, "n1")
			if err != nil {
				t.Fatal(err)
			}
			if r.Patient != last || len(r.Skipped) != n-1 {
				t.Fatalf("picked=%s skipped=%d", r.Patient, len(r.Skipped))
			}
			if ex := s.qq.Examined(); ex > len(r.Skipped)+1 {
				t.Fatalf("examined=%d > skipped+1=%d", ex, len(r.Skipped)+1)
			}
			t.Logf("判定依据: n=%d 候选仅遍历3级treap, examined=%d, 与候诊总数无关", n, s.qq.Examined())
		})
	}
}

func TestLandExaminedBound(t *testing.T) {
	const n = 50
	s := NewSystem(5, 15, 30, 60, 5)
	for i := 0; i < n; i++ {
		rm := fmt.Sprintf("r%02d", i)
		s.AddRoom(RoomID(rm), Clinic)
		mustReg(t, s, 0, fmt.Sprintf("p%02d", i), l3())
		if _, err := s.Call(0, RoomID(rm)); err != nil {
			t.Fatal(err)
		}
	}
	s.landExamined, s.landedMiss = 0, 0
	mustReg(t, s, 6, "z", l3()) // 触发全部 deadline=5 的过号落地
	if s.landedMiss != n {
		t.Fatalf("landed=%d want %d", s.landedMiss, n)
	}
	if s.landExamined > s.landedMiss+1 {
		t.Fatalf("landExamined=%d > landed+1=%d", s.landExamined, s.landedMiss+1)
	}
	t.Logf("判定依据: 落地逐个pop已叫号treap, 取出=%d 实际过号=%d", s.landExamined, s.landedMiss)
}

func TestConcurrentSerializable(t *testing.T) {
	run := func() string {
		s := NewSystem(5, 15, 30, 60, 5)
		for i := 0; i < 20; i++ {
			s.AddRoom(RoomID(fmt.Sprintf("r%02d", i)), Clinic)
		}
		phases := []func(now, g int) error{
			func(now, g int) error {
				_, _, err := s.Register(now, fmt.Sprintf("p%02d", g), l3())
				return err
			},
			func(now, g int) error {
				_, err := s.Call(now, RoomID(fmt.Sprintf("r%02d", g)))
				return err
			},
			func(now, g int) error {
				return s.Arrive(now, fmt.Sprintf("p%02d", g))
			},
			func(now, g int) error {
				_, err := s.Finish(now, RoomID(fmt.Sprintf("r%02d", g)))
				return err
			},
		}
		for ph, fn := range phases {
			var wg sync.WaitGroup
			for g := 0; g < 20; g++ {
				wg.Add(1)
				go func(g int) {
					defer wg.Done()
					if err := fn(100+ph, g); err != nil {
						t.Errorf("phase %d g %d: %v", ph, g, err)
					}
				}(g)
			}
			wg.Wait()
		}
		if n := s.called.Len(); n != 0 {
			t.Fatalf("after arrive phase called treap len=%d", n)
		}
		for i := 0; i < 20; i++ {
			e, ok := s.qq.Get(queue.ID(fmt.Sprintf("p%02d", i)))
			if !ok || e.Status != 3 {
				t.Fatalf("p%02d status ok=%v status=%d arrived=%v callAt=%d", i, ok, e.Status, e.Arrived, e.CallAt)
			}
			rm := s.rooms[queue.ID(fmt.Sprintf("r%02d", i))]
			if rm.occupant != nil {
				t.Fatalf("room r%02d not free", i)
			}
		}
		return "invariants-hold"
	}
	a, b := run(), run()
	if a != b {
		t.Fatalf("重放结果不一致: %s vs %s", a, b)
	}
}
