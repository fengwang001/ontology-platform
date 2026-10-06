package duty

import (
	"sync"
	"testing"
)

// TestRejectOrdering 覆盖拒绝次序的每一对相邻类别（共 11 对）。
func TestRejectOrdering(t *testing.T) {
	// 1) 参数非法 > 时钟回退
	s := NewSystem(testConfig())
	mustOK(t, s.AddPerson(10, 1))
	// now 非负但人员 ID 非法，同时刻提交 -> 报参数非法而非时钟回退。
	rejectIs(t, s.AddPerson(10, -1), RejectInvalidParams)
	// 2) 时钟回退 > 人员不存在
	rejectIs(t, s.UpdateQualification(5, 99, 1, 100), RejectClockRollback)
	// 3) 人员不存在 > 值勤期不存在
	rejectIs(t, s.Cancel(10, 99, 1), RejectNoPerson)
	// 4) 值勤期不存在 > 已开始/已解除不可改
	mustOK(t, s.UpdateQualification(10, 1, 1, 1<<30))
	rejectIs(t, s.Cancel(10, 1, 12345), RejectNoDuty)
	// 5) 不可改 > 资质无效（已开始的值勤期延长，且资质已到期）
	mustOK(t, regAt(s, 1, 10, 100, 0, 1))
	mustOK(t, s.UpdateQualification(10, 1, 1, 50))
	rejectIs(t, s.Extend(100, 1, idOf(s, 1, 10), 105), RejectImmutable)
	mustOK(t, s.UpdateQualification(10, 1, 1, 1<<30))

	// 6) 资质无效 > 重叠
	s2 := NewSystem(testConfig())
	mustOK(t, s2.AddPerson(0, 1))
	mustOK(t, s2.UpdateQualification(0, 1, 1, 100))
	mustOK(t, regAt(s2, 1, 0, 50, 0, 1))
	// 资质更新为到期 60（恰等于新解除时刻，无效），且新段与既有段重叠。
	mustOK(t, s2.UpdateQualification(0, 1, 1, 60))
	rejectIs(t, regAt(s2, 1, 0, 60, 0, 1), RejectQualification)
	// 把资质改到覆盖解除时刻后，同样输入改报重叠。
	mustOK(t, s2.UpdateQualification(0, 1, 1, 1<<30))
	rejectIs(t, regAt(s2, 1, 0, 60, 0, 1), RejectOverlap)

	// 7) 重叠 > 休息不足
	s3 := setupNoRoll(t, 1)
	mustOK(t, regAt(s3, 1, 0, 100, 0, 1))
	rejectIs(t, regAt(s3, 1, 50, 120, 0, 1), RejectOverlap)

	// 8) 休息不足 > 单次超限
	s4 := setupNoRoll(t, 1)
	mustOK(t, regAt(s4, 1, 0, 590, 0, 1))
	rejectIs(t, regAt(s4, 1, 600, 600+900, 0, 1), RejectRest)

	// 9) 单次超限 > 延长规则（延长量超上限，但延长后时长甚至突破单次+宽限）
	c := noRollConfig()
	c.MaxExtension = 10
	s5 := setupCfg(t, c, 1)
	mustOK(t, regAt(s5, 1, 0, 100, 8, 1))
	rejectIs(t, s5.Extend(0, 1, idOf(s5, 1, 0), 100+401), RejectSingleLimit)

	// 10) 延长规则 > 7 日累计（同窗已有延长值勤期；即便累计会超也先报延长规则）
	c2 := noRollConfig()
	c2.Window7 = 100000
	c2.Limit7 = 200 // 各值勤期自身合规；第二段延长时因同窗已有延长值勤期而拒绝。
	s6 := setupCfg(t, c2, 1)
	mustOK(t, regAt(s6, 1, 0, 50, 0, 1))
	mustOK(t, s6.Extend(0, 1, idOf(s6, 1, 0), 60))
	mustOK(t, regAt(s6, 1, 1000, 1050, 0, 1))
	rejectIs(t, s6.Extend(1000, 1, idOf(s6, 1, 1000), 1060), RejectExtension)

	// 11) 7 日累计 > 28 日累计（让两个窗口都超）
	c3 := noRollConfig()
	c3.MinRest = 0
	c3.Window7 = 200
	c3.Window28 = 400
	c3.Limit7 = 40
	c3.Limit28 = 40
	s7 := setupCfg(t, c3, 1)
	mustOK(t, regAt(s7, 1, 0, 1, 0, 1))
	// 新段使 200 窗与 400 窗同时超限：前段在 [0,1)，新段 [2,43) 长41。
	r := regAt(s7, 1, 2, 43, 0, 1)
	rejectIs(t, r, RejectRolling7)
}

func noRollConfig() Config {
	c := testConfig()
	c.Window7 = 100000
	c.Window28 = 400000
	c.Limit7 = 1000000
	c.Limit28 = 4000000
	return c
}

func setupNoRoll(t *testing.T, pid int) *System {
	t.Helper()
	return setupCfg(t, noRollConfig(), pid)
}

func setupCfg(t *testing.T, cfg Config, pid int) *System {
	t.Helper()
	s := NewSystem(cfg)
	mustOK(t, s.AddPerson(0, pid))
	mustOK(t, s.UpdateQualification(0, pid, 1, 1<<30))
	return s
}

func regAt(s *System, pid, st, en, legs, ac int) Result {
	r, _ := s.Register(st, pid, st, en, legs, ac)
	return r
}

func regN(s *System, now, pid, st, en, legs, ac int) Result {
	r, _ := s.Register(now, pid, st, en, legs, ac)
	return r
}

// 被拒绝操作不推进时钟、不改记录。
func TestRejectedOpNoEffect(t *testing.T) {
	s := NewSystem(testConfig())
	mustOK(t, s.AddPerson(10, 1))
	rejectIs(t, s.Cancel(11, 1, 999), RejectNoDuty)
	mustOK(t, s.AddPerson(11, 2)) // 11 未被回退
	if len(s.Duties(1)) != 0 {
		t.Fatal("rejected op mutated state")
	}
}

// 并发：同一操作集合从两批 goroutine 提交；结论只取决于时刻串行顺序。
// 这里验证可重复且最终序列合规、无竞态（go test -race）。
func TestConcurrent(t *testing.T) {
	run := func() {
		s := NewSystem(smallConfig())
		var wg sync.WaitGroup
		for p := 0; p < 4; p++ {
			pid := p
			wg.Add(1)
			go func() {
				defer wg.Done()
				_ = s.AddPerson(0, pid)
				_ = s.UpdateQualification(0, pid, 1, 1<<30)
				now := 0
				st := 0
				for k := 0; k < 20; k++ {
					en := st + 50
					r, _ := s.Register(now, pid, st, en, 0, 1)
					if r.OK() {
						st = en + 600
						now = st
					} else {
						st += 700
						now = st
					}
				}
			}()
		}
		wg.Wait()
		for p := 0; p < 4; p++ {
			ds := s.Duties(p)
			for i := 1; i < len(ds); i++ {
				if ds[i].Start <= ds[i-1].End {
					t.Fatalf("person %d duties overlap", p)
				}
			}
		}
	}
	for i := 0; i < 5; i++ {
		run()
	}
}

// 最早可报到时刻：基本精确性，由朴素逐分钟扫描在差分测试中全面校验。
func TestEarliestReportBasic(t *testing.T) {
	s := setup(t, testConfig(), 1)
	got, ok := s.EarliestReport(1, 0, 0, 1)
	if !ok || got != 0 {
		t.Fatalf("earliest = %d,%v want 0,true", got, ok)
	}
	mustOK(t, regAt(s, 1, 0, 600, 0, 1))
	got, ok = s.EarliestReport(1, 0, 0, 1)
	if !ok {
		t.Fatal("person vanished")
	}
	// 最早须在第一段结束并满足最短休息（600）之后：>=1200。
	if got < 1200 {
		t.Fatalf("earliest = %d want >=1200", got)
	}
	if r, _ := s.Register(0, 1, got, got+600, 0, 1); !r.OK() {
		t.Fatalf("earliest candidate rejected: %s", r)
	}
	// 人员不存在。
	if _, ok := s.EarliestReport(99, 0, 0, 1); ok {
		t.Fatal("missing person should report false")
	}
}
