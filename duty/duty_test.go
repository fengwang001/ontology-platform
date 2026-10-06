package duty

import (
	"sync"
	"testing"
)

func testConfig() Config {
	return Config{
		DayLen: 1440, DayBoundary1: 360, DayBoundary2: 1080,
		BaseLimit: [3]int{600, 480, 720}, PerLegCut: 30, MinLimit: 300,
		MinRest: 600,
		Window7: 10080, Window28: 40320, Limit7: 1500, Limit28: 4500,
		MaxExtension: 120,
	}
}

func smallConfig() Config {
	c := testConfig()
	c.Window7 = 300
	c.Window28 = 900
	c.Limit7 = 400
	c.Limit28 = 900
	return c
}

func mustOK(t *testing.T, r Result) {
	t.Helper()
	if !r.OK() {
		t.Fatalf("want accept, got %s", r)
	}
}

func rejectIs(t *testing.T, r Result, want Reason) {
	t.Helper()
	if r.Reject != want {
		t.Fatalf("want %s, got %s", want, r)
	}
}

func setup(t *testing.T, cfg Config, pid int) *System {
	t.Helper()
	s := NewSystem(cfg)
	mustOK(t, s.AddPerson(0, pid))
	mustOK(t, s.UpdateQualification(0, pid, 1, 1<<30))
	return s
}

func regOnly(s *System, pid, st, en, legs, ac int) Result {
	r, _ := s.Register(0, pid, st, en, legs, ac)
	return r
}

func idOf(s *System, pid, start int) int {
	for _, d := range s.Duties(pid) {
		if d.Start == start {
			return d.ID
		}
	}
	return -1
}

func TestPeriodBoundary(t *testing.T) {
	c := testConfig()
	cases := []struct{ t, want int }{
		{0, 0}, {359, 0}, {360, 1}, {1079, 1}, {1080, 2}, {1439, 2},
		{1440, 0}, {1800, 1},
	}
	for _, tc := range cases {
		if got := c.Period(tc.t); got != tc.want {
			t.Fatalf("Period(%d)=%d want %d", tc.t, got, tc.want)
		}
	}
}

func TestSingleLimitEqualityAndLegs(t *testing.T) {
	s := setup(t, testConfig(), 1)
	mustOK(t, regOnly(s, 1, 0, 600, 0, 1)) // 早段 600 恰等
	// 1440 为新一天的 00:00（早段）；各段间隔 >= 最长休息所需（600）。
	mustOK(t, regOnly(s, 1, 1440, 1440+480, 4, 1))
	rejectIs(t, regOnly(s, 1, 3000, 3000+481, 4, 1), RejectSingleLimit)
	mustOK(t, regOnly(s, 1, 4500, 4500+360, 8, 1))

	c := testConfig()
	c.PerLegCut = 200
	ss := setup(t, c, 7)
	mustOK(t, regOnly(ss, 7, 1080, 1380, 8, 1)) // 夜段 720 递减触底 300
	rejectIs(t, regOnly(ss, 7, 1080+1440, 1080+1440+301, 8, 1), RejectSingleLimit)
}

func TestRestAndMiddleInsert(t *testing.T) {
	c := testConfig()
	c.Window7 = 100000
	c.Window28 = 400000
	c.Limit7 = 1000000
	c.Limit28 = 4000000
	s := setup(t, c, 1)
	mustOK(t, regOnly(s, 1, 0, 300, 0, 1))
	mustOK(t, regOnly(s, 1, 900, 1200, 0, 1))
	// 与前后均不重叠但休息都不足：前间隔100、后间隔200，均 < 最短休息600。
	rejectIs(t, regOnly(s, 1, 400, 700, 0, 1), RejectRest)
	rejectIs(t, regOnly(s, 1, 300, 600, 0, 1), RejectOverlap)

	s2 := setup(t, c, 2)
	// 前一值勤时长 700（<= 单次上限 600? 否）——用 590，则休息需 >=600。
	mustOK(t, regOnly(s2, 2, 0, 590, 0, 1))
	mustOK(t, regOnly(s2, 2, 1190, 1290, 0, 1)) // 间隔恰 600
	rejectIs(t, regOnly(s2, 2, 1189, 1250, 0, 1), RejectRest)
	// 前一值勤更长（休息按时长）：时长 650 > 600。
	c3 := c
	c3.BaseLimit[0] = 700
	s3 := setup(t, c3, 3)
	mustOK(t, regOnly(s3, 3, 0, 650, 0, 1))
	mustOK(t, regOnly(s3, 3, 1300, 1400, 0, 1)) // 间隔恰 650
	rejectIs(t, regOnly(s3, 3, 1299, 1360, 0, 1), RejectRest)
}

func TestRollingOverlap(t *testing.T) {
	// 非重叠值勤期在窗内按重叠长度计入。关闭最短休息以得到紧邻两段。
	c := smallConfig()
	c.Window7 = 100
	c.Limit7 = 90
	c.MinRest = 0
	s := setup(t, c, 1)
	mustOK(t, regOnly(s, 1, 0, 1, 0, 1))
	// 新段 [2,91)（前一值勤仅 1 分钟，休息需 1，间隙 1 恰够）：
	// 窗 [0,100) 重叠 1+89=90 恰等于上限 -> 接受。
	mustOK(t, regOnly(s, 1, 2, 91, 0, 1))

	s2 := setup(t, c, 2)
	mustOK(t, regOnly(s2, 2, 0, 1, 0, 1))
	// 新段 [2,92) 长 90：窗 [0,100)=1+90=91 > 90 -> 拒绝，最小窗起点 0。
	r := regOnly(s2, 2, 2, 92, 0, 1)
	rejectIs(t, r, RejectRolling7)
	if r.WindowStart != 0 {
		t.Fatalf("window start=%d want 0", r.WindowStart)
	}
}

func TestQualification(t *testing.T) {
	s := NewSystem(testConfig())
	mustOK(t, s.AddPerson(0, 1))
	mustOK(t, s.UpdateQualification(0, 1, 1, 600))
	rejectIs(t, regOnly(s, 1, 0, 600, 0, 1), RejectQualification)
	mustOK(t, regOnly(s, 1, 0, 599, 0, 1))
	mustOK(t, s.RevokeQualification(100, 1, 1))
	if len(s.Duties(1)) != 1 {
		t.Fatalf("duties after revoke = %d want 1", len(s.Duties(1)))
	}
}

func TestExtension(t *testing.T) {
	s := setup(t, testConfig(), 1)
	mustOK(t, regOnly(s, 1, 0, 100, 0, 1))
	rejectIs(t, s.Extend(0, 1, idOf(s, 1, 0), 221), RejectExtension)
	mustOK(t, s.Extend(0, 1, idOf(s, 1, 0), 200))
	rejectIs(t, s.Extend(0, 1, idOf(s, 1, 0), 210), RejectExtension)

	s2 := setup(t, testConfig(), 2)
	mustOK(t, regOnly(s2, 2, 0, 600, 0, 1))
	mustOK(t, s2.Extend(100, 2, idOf(s2, 2, 0), 650))
	rejectIs(t, s2.Extend(650, 2, idOf(s2, 2, 0), 700), RejectImmutable)

	c := smallConfig()
	c.Window7 = 1000
	c.Limit7 = 230
	s3 := setup(t, c, 3)
	mustOK(t, regOnly(s3, 3, 0, 100, 0, 1))
	mustOK(t, regOnly(s3, 3, 700, 800, 0, 1))
	r := s3.Extend(0, 3, idOf(s3, 3, 700), 900)
	rejectIs(t, r, RejectRolling7)
	if r.WindowStart != 0 {
		t.Fatalf("window=%d want 0", r.WindowStart)
	}

	s4 := setup(t, smallConfig(), 4)
	mustOK(t, regOnly(s4, 4, 0, 100, 0, 1))
	mustOK(t, s4.Extend(0, 4, idOf(s4, 4, 0), 150))
	mustOK(t, regOnly(s4, 4, 1000, 1100, 0, 1))
	rejectIs(t, s4.Extend(0, 4, idOf(s4, 4, 1000), 1150), RejectExtension)
}

func TestCancel(t *testing.T) {
	s := setup(t, testConfig(), 1)
	mustOK(t, regOnly(s, 1, 1000, 1300, 0, 1))
	id := idOf(s, 1, 1000)
	rejectIs(t, regOnly(s, 1, 1000, 1200, 0, 1), RejectOverlap)
	mustOK(t, s.Cancel(0, 1, id))
	mustOK(t, regOnly(s, 1, 1000, 1200, 0, 1))
	mustOK(t, regOnly(s, 1, 5000, 5300, 0, 1))
	rejectIs(t, s.Cancel(5000, 1, idOf(s, 1, 5000)), RejectImmutable)
}

func TestClock(t *testing.T) {
	s := NewSystem(testConfig())
	mustOK(t, s.AddPerson(10, 1))
	rejectIs(t, s.AddPerson(9, 2), RejectClockRollback)
	mustOK(t, s.AddPerson(10, 2))
}

var _ = sync.Mutex{}
