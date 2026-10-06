package room

import (
	"errors"
	"sync"
	"testing"

	"ontology/queue"
	"ontology/triage"
)

// 各等级生命体征：L1..L4。
var vitalsOf = [5]triage.Vitals{
	{},
	{HR: 135, SBP: 120, SpO2: 96, Consciousness: 'A'}, // m=3 → 1 级
	{HR: 120, SBP: 75, SpO2: 93, Consciousness: 'A'},  // s=5 → 2 级
	{HR: 105, SBP: 95, SpO2: 92, Consciousness: 'V'},  // s=4 → 3 级
	{HR: 110, SBP: 100, SpO2: 94, Consciousness: 'A'}, // s=2 → 4 级
}

func mustSystem(t *testing.T, r1, r2, r3, r4, a int) *System {
	t.Helper()
	s, err := New(r1, r2, r3, r4, a)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func mustRegister(t *testing.T, s *System, now int, id string, lv int) {
	t.Helper()
	if err := s.Register(now, id, vitalsOf[lv]); err != nil {
		t.Fatalf("Register(%s): %v", id, err)
	}
}

func equalStrs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func mustCall(t *testing.T, s *System, now int, room string, wantCalled string, wantSkipped []string) {
	t.Helper()
	called, skipped, err := s.Call(now, room)
	if err != nil {
		t.Fatalf("Call(%s)@%d: %v", room, now, err)
	}
	t.Logf("Call(%s)@%d → called=%s skipped=%v", room, now, called, skipped)
	if called != wantCalled || !equalStrs(skipped, wantSkipped) {
		t.Fatalf("Call(%s)@%d: got called=%s skipped=%v, want called=%s skipped=%v",
			room, now, called, skipped, wantCalled, wantSkipped)
	}
}

// callExampleSetup 复现规格叫号例的公共前缀：R=[5,15,30,60]，A=5，诊室 n1。
// t=0 a 为 3 级，t=5 b 为 3 级，t=10 c 为 4 级；
// t=20 复评 c 升为 3 级（q 保留 10）；t=25 复评 a 降为 4 级（q=25）。
func callExampleSetup(t *testing.T) *System {
	t.Helper()
	s := mustSystem(t, 5, 15, 30, 60, 5)
	if err := s.AddRoom("n1", Clinic); err != nil {
		t.Fatal(err)
	}
	mustRegister(t, s, 0, "a", 3)
	mustRegister(t, s, 5, "b", 3)
	mustRegister(t, s, 10, "c", 4)
	if err := s.Reassess(20, "c", vitalsOf[3]); err != nil {
		t.Fatalf("复评 c 升级: %v", err)
	}
	if err := s.Reassess(25, "a", vitalsOf[4]); err != nil {
		t.Fatalf("复评 a 降级: %v", err)
	}
	return s
}

func TestCallExampleNotOverdue(t *testing.T) {
	s := callExampleSetup(t)
	// t=35：b 的 now-la=30 恰等 R[3]，不逾期，叫到 b。
	mustCall(t, s, 35, "n1", "b", nil)
}

func TestCallExampleSkipOverdue(t *testing.T) {
	s := callExampleSetup(t)
	// t=36：b 已逾期（31>30）被跳过，c 未逾期（16<=30）被叫到。
	mustCall(t, s, 36, "n1", "c", []string{"b"})

	// 恰等 A 仍可到诊：callAt=36，t=41 时 now-callAt=5=A。
	if err := s.Arrive(41, "c"); err != nil {
		t.Fatalf("c 在 t=41 应可到诊（恰等 A）: %v", err)
	}
}

func TestCallExampleMissRequeue(t *testing.T) {
	s := callExampleSetup(t)
	mustCall(t, s, 36, "n1", "c", []string{"b"})

	// c 未到诊；t=42 的被接受操作开头落地过号：n1 空闲，c 回候诊 q=41、miss=1。
	if err := s.Register(42, "d", vitalsOf[4]); err != nil {
		t.Fatalf("t=42 登记 d: %v", err)
	}
	p, ok := s.q.Get("c")
	if !ok {
		t.Fatal("c 应仍在系统")
	}
	t.Logf("c 过号落地: state=%v q=%d la=%d miss=%d 判定依据=q 取 callAt+A=41", p.State, p.Q, p.LA, p.Miss)
	if p.State != queue.Waiting || p.Q != 41 || p.LA != 20 || p.Miss != 1 {
		t.Errorf("got state=%v q=%d la=%d miss=%d, want Waiting/41/20/1", p.State, p.Q, p.LA, p.Miss)
	}
	// c 回队后排在 b 之后：复评 b 清除逾期后，b(q=5) 先被叫到，其次 c(q=41)。
	if err := s.Reassess(42, "b", vitalsOf[3]); err != nil {
		t.Fatal(err)
	}
	mustCall(t, s, 42, "n1", "b", nil)
	if err := s.Arrive(42, "b"); err != nil {
		t.Fatal(err)
	}
	if err := s.Finish(43, "n1"); err != nil {
		t.Fatal(err)
	}
	mustCall(t, s, 43, "n1", "c", nil)
}

// TestRoomKindLevels 验证 2 级两类房都可用、1 级不进诊室、3/4 级不占抢救位。
func TestRoomKindLevels(t *testing.T) {
	s := mustSystem(t, 5, 15, 30, 60, 5)
	for _, r := range []struct {
		id string
		k  Kind
	}{{"res1", Resuscitation}, {"cli1", Clinic}} {
		if err := s.AddRoom(r.id, r.k); err != nil {
			t.Fatal(err)
		}
	}
	mustRegister(t, s, 0, "p1", 1)
	mustRegister(t, s, 1, "p2", 2)
	mustRegister(t, s, 2, "p3", 3)

	// 诊室候选为 2/3/4 级：1 级 p1 不进诊室，叫到 2 级 p2。
	mustCall(t, s, 3, "cli1", "p2", nil)
	// 抢救位候选为 1/2 级：叫到 1 级 p1（3 级 p3 不占抢救位）。
	mustCall(t, s, 3, "res1", "p1", nil)

	// 2 级患者两类房都可用。
	s2 := mustSystem(t, 5, 15, 30, 60, 5)
	if err := s2.AddRoom("res1", Resuscitation); err != nil {
		t.Fatal(err)
	}
	mustRegister(t, s2, 0, "x2", 2)
	mustCall(t, s2, 1, "res1", "x2", nil)
}

// TestRejectionOrder 拒绝按次序只报第一个：
// 参数非法 > 时钟回退 > 不存在 > 状态不符 > 无可叫者。
func TestRejectionOrder(t *testing.T) {
	cases := []struct {
		name string
		run  func(s *System) error
		want error
	}{
		{"参数非法优先于时钟回退", func(s *System) error {
			return s.Register(1, "x", triage.Vitals{HR: 999, SBP: 120, SpO2: 98, Consciousness: 'A'})
		}, ErrInvalidParam},
		{"时钟回退优先于不存在", func(s *System) error {
			return s.Reassess(1, "ghost", vitalsOf[3])
		}, ErrClock},
		{"不存在优先于状态不符", func(s *System) error {
			return s.Finish(5, "noRoom")
		}, ErrExistence},
		{"重复登记归存在类", func(s *System) error {
			return s.Register(5, "a", vitalsOf[3])
		}, ErrExistence},
		{"重复房间归存在类", func(s *System) error {
			return s.AddRoom("n1", Clinic)
		}, ErrExistence},
		{"房间非空闲为状态不符", func(s *System) error {
			_, _, err := s.Call(5, "n1")
			return err
		}, ErrState},
		{"患者不在所需状态", func(s *System) error {
			return s.Reassess(5, "b", vitalsOf[3]) // b 已叫号，非候诊
		}, ErrState},
		{"无可叫者", func(s *System) error {
			if err := s.AddRoom("n3", Clinic); err != nil {
				return err
			}
			_, _, err := s.Call(5, "n3") // 候诊为空
			return err
		}, ErrNoCallable},
		{"now越界为参数非法", func(s *System) error {
			return s.Arrive(1_000_000_001, "a")
		}, ErrInvalidParam},
	}
	for _, c := range cases {
		s := mustSystem(t, 5, 15, 30, 60, 5)
		if err := s.AddRoom("n1", Clinic); err != nil {
			t.Fatal(err)
		}
		if err := s.AddRoom("n2", Clinic); err != nil {
			t.Fatal(err)
		}
		mustRegister(t, s, 0, "a", 3)
		// 让 n1、n2 分别被 a、b 占用（Called），候诊为空。
		if _, _, err := s.Call(2, "n1"); err != nil {
			t.Fatal(err)
		}
		if err := s.Register(3, "b", vitalsOf[4]); err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.Call(3, "n2"); err != nil {
			t.Fatal(err)
		}
		err := c.run(s)
		t.Logf("%s: err=%v", c.name, err)
		if !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, err, c.want)
		}
	}
}

// TestRejectedNoLandNoClock 被拒绝的操作不落地、不推进时钟。
func TestRejectedNoLandNoClock(t *testing.T) {
	s := mustSystem(t, 5, 15, 30, 60, 5)
	if err := s.AddRoom("n1", Clinic); err != nil {
		t.Fatal(err)
	}
	mustRegister(t, s, 0, "a", 3)
	mustCall(t, s, 10, "n1", "a", nil) // callAt=10，A=5，t>15 即过号

	// t=20 的参数非法操作被拒绝：过号不落地，a 仍为 Called，n1 仍被占，时钟不推进。
	bad := triage.Vitals{HR: 999, SBP: 120, SpO2: 98, Consciousness: 'A'}
	if err := s.Register(20, "b", bad); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("参数非法: %v", err)
	}
	// t=5 的时钟回退操作同样被拒绝（maxNow 仍为 10）。
	if err := s.Register(5, "c", vitalsOf[4]); !errors.Is(err, ErrClock) {
		t.Fatalf("时钟回退: %v", err)
	}
	p, _ := s.q.Get("a")
	if p.State != queue.Called || p.Miss != 0 {
		t.Errorf("被拒操作落地了过号: state=%v miss=%d", p.State, p.Miss)
	}
	if s.rooms["n1"].Patient != "a" {
		t.Errorf("被拒操作释放了房间: %q", s.rooms["n1"].Patient)
	}
	if s.maxNow != 10 {
		t.Errorf("被拒操作推进了时钟: maxNow=%d", s.maxNow)
	}

	// t=21 的被接受操作落地过号：n1 空闲，a 回候诊 q=15、miss=1。
	if err := s.Register(21, "d", vitalsOf[4]); err != nil {
		t.Fatal(err)
	}
	p, _ = s.q.Get("a")
	if p.State != queue.Waiting || p.Q != 15 || p.Miss != 1 {
		t.Errorf("过号落地: state=%v q=%d miss=%d, want Waiting/15/1", p.State, p.Q, p.Miss)
	}
	if s.rooms["n1"].Patient != "" {
		t.Errorf("房间应空闲: %q", s.rooms["n1"].Patient)
	}
}

// TestMissThriceLeaves 三次过号离队，不再候诊。
func TestMissThriceLeaves(t *testing.T) {
	s := mustSystem(t, 5, 15, 30, 60, 5)
	if err := s.AddRoom("n1", Clinic); err != nil {
		t.Fatal(err)
	}
	mustRegister(t, s, 0, "a", 3)
	for i := 1; i <= 3; i++ {
		mustCall(t, s, i*10, "n1", "a", nil)
		// 下一次被接受操作（i*10+6 > callAt+A）落地过号，房间空闲。
		if err := s.Reassess(i*10+6, "a", vitalsOf[3]); i < 3 && err != nil {
			t.Fatalf("第 %d 次过号后复评: %v", i, err)
		}
	}
	p, _ := s.q.Get("a")
	t.Logf("三次过号: miss=%d state=%v 判定依据=miss 达 3 离队", p.Miss, p.State)
	if p.Miss != 3 || p.State != queue.Gone {
		t.Fatalf("got miss=%d state=%v, want 3/Gone", p.Miss, p.State)
	}
	if _, _, err := s.Call(100, "n1"); !errors.Is(err, ErrNoCallable) {
		t.Errorf("离队后无可叫者: %v", err)
	}
}

// TestConcurrent 并发调用等价于某串行序：不同患者的登记全部成功且登记号唯一。
func TestConcurrent(t *testing.T) {
	s := mustSystem(t, 5, 15, 30, 60, 5)
	if err := s.AddRoom("n1", Clinic); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := string(rune('A' + i))
			if err := s.Register(0, id, vitalsOf[3]); err != nil {
				t.Errorf("Register(%s): %v", id, err)
			}
		}(i)
	}
	wg.Wait()
	seen := make(map[int]string)
	for _, p := range s.q.Snapshot() {
		if prev, dup := seen[p.Reg]; dup {
			t.Fatalf("登记号 %d 同时属于 %s 与 %s", p.Reg, prev, p.ID)
		}
		seen[p.Reg] = p.ID
	}
	if len(seen) != 50 {
		t.Fatalf("登记号数=%d, want 50", len(seen))
	}
}
