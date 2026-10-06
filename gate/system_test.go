package gate

import (
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
)

func testCfg() Config {
	return Config{Buffer: 15, StayLimit: 180, Deplane: 30, Board: 45}
}

func newSys(t *testing.T, cfg Config) *System {
	t.Helper()
	s, err := NewSystem(cfg)
	if err != nil {
		t.Fatalf("NewSystem: %v", err)
	}
	return s
}

func addGate(t *testing.T, s *System, id string, lvl Level, kind GateKind, now int, adj ...string) {
	t.Helper()
	if r := s.AddGate(GateSpec{ID: id, MaxLevel: lvl, Kind: kind, Adjacent: adj}, now); !r.OK {
		t.Fatalf("AddGate %s: %s", id, r.Detail)
	}
}

func addFlight(t *testing.T, s *System, id string, lvl Level, kind FlightKind, arr, dep, lead, now int) {
	t.Helper()
	spec := FlightSpec{ID: id, Level: lvl, Kind: kind, SchedArr: arr, SchedDep: dep, BoardLead: lead}
	if r := s.AddFlight(spec, now); !r.OK {
		t.Fatalf("AddFlight %s: %s", id, r.Detail)
	}
}

func wantReject(t *testing.T, r Result, reason Reason) {
	t.Helper()
	if r.OK || r.Reason != reason {
		t.Fatalf("want reject %s, got ok=%v reason=%s detail=%s", reason, r.OK, r.Reason, r.Detail)
	}
}

func wantOK(t *testing.T, r Result) {
	t.Helper()
	if !r.OK {
		t.Fatalf("want ok, got %s: %s", r.Reason, r.Detail)
	}
}

func segGate(s *System, fid string, seg SegKind) (string, bool) {
	sf, ok := s.Snapshot()[fid]
	if !ok {
		return "", false
	}
	g, ok := sf.Gates[seg]
	return g, ok
}

// 占用区间端点相接不冲突；缓冲时长使原本不相接的区间相交。
func TestEndpointTouchAndBuffer(t *testing.T) {
	s := newSys(t, testCfg()) // Buffer=15
	addGate(t, s, "G1", 3, GateDual, 0)
	addFlight(t, s, "F1", 1, FlightDomestic, 100, 200, 30, 0) // 占用 [100,215)
	addFlight(t, s, "F2", 1, FlightDomestic, 215, 300, 30, 0) // 端点相接
	addFlight(t, s, "F3", 1, FlightDomestic, 210, 300, 30, 0) // 无缓冲则不相交
	wantOK(t, s.Assign("F1", SegWhole, "G1", 0))
	wantOK(t, s.Assign("F2", SegWhole, "G1", 0))
	r := s.Assign("F3", SegWhole, "G1", 0)
	wantReject(t, r, ReasonTimeConflict)
	if r.Conflict != "F1" {
		t.Fatalf("conflict should be F1 (earliest start), got %q", r.Conflict)
	}
	if id, _, ok := s.Occupant("G1", 214); !ok || id != "F1" {
		t.Fatalf("occupant at 214 should be F1, got %q ok=%v", id, ok)
	}
	if id, _, ok := s.Occupant("G1", 215); !ok || id != "F2" {
		t.Fatalf("occupant at 215 should be F2, got %q ok=%v", id, ok)
	}
}

// 多个冲突时报占用区间起点最早者。
func TestTimeConflictEarliestStart(t *testing.T) {
	s := newSys(t, Config{Buffer: 0, StayLimit: 500, Deplane: 30, Board: 45})
	addGate(t, s, "G1", 3, GateDual, 0)
	addFlight(t, s, "FZ", 1, FlightDomestic, 100, 150, 30, 0)
	addFlight(t, s, "FA", 1, FlightDomestic, 160, 200, 30, 0)
	addFlight(t, s, "FQ", 1, FlightDomestic, 120, 210, 30, 0)
	wantOK(t, s.Assign("FZ", SegWhole, "G1", 0))
	wantOK(t, s.Assign("FA", SegWhole, "G1", 0))
	r := s.Assign("FQ", SegWhole, "G1", 0) // 同时与 FZ、FA 冲突
	wantReject(t, r, ReasonTimeConflict)
	if r.Conflict != "FZ" {
		t.Fatalf("conflict should be FZ (earliest start), got %q", r.Conflict)
	}
}

// 相邻限制只对最高等级机型生效；区间相接不违反。
func TestAdjacencyMaxLevelOnly(t *testing.T) {
	s := newSys(t, Config{Buffer: 0, StayLimit: 500, Deplane: 30, Board: 45})
	addGate(t, s, "G1", 3, GateDual, 0)
	addGate(t, s, "G2", 3, GateDual, 0, "G1")
	addFlight(t, s, "F1", 3, FlightDomestic, 100, 200, 30, 0)
	addFlight(t, s, "F2", 3, FlightDomestic, 150, 250, 30, 0)
	addFlight(t, s, "F3", 2, FlightDomestic, 150, 200, 30, 0)
	addFlight(t, s, "F4", 3, FlightDomestic, 200, 300, 30, 0)
	wantOK(t, s.Assign("F1", SegWhole, "G1", 0))
	wantReject(t, s.Assign("F2", SegWhole, "G2", 0), ReasonAdjacency) // 最高等级相邻相交
	wantOK(t, s.Assign("F3", SegWhole, "G2", 0))                      // 低等级不受限
	wantOK(t, s.Assign("F4", SegWhole, "G2", 0))                      // 端点相接不违反
}

// 停留时长恰等于上限按整段处理，多一分钟则拆成两段。
func TestStayLimitExactVsSplit(t *testing.T) {
	s := newSys(t, Config{Buffer: 0, StayLimit: 180, Deplane: 30, Board: 45})
	addGate(t, s, "G1", 3, GateDual, 0)
	addGate(t, s, "G2", 3, GateDual, 0)
	addGate(t, s, "G3", 3, GateDual, 0)
	addFlight(t, s, "F1", 1, FlightDomestic, 10, 190, 30, 0) // 恰等于上限：整段
	addFlight(t, s, "F2", 1, FlightDomestic, 10, 191, 30, 0) // 多一分钟：拆段
	wantReject(t, s.Assign("F1", SegDeplane, "G1", 0), ReasonInvalidParam)
	wantOK(t, s.Assign("F1", SegWhole, "G1", 0))
	wantReject(t, s.Assign("F2", SegWhole, "G2", 0), ReasonInvalidParam)
	wantOK(t, s.Assign("F2", SegDeplane, "G2", 0))
	wantOK(t, s.Assign("F2", SegBoard, "G3", 0)) // 两段可在不同登机口
	if id, _, ok := s.Occupant("G2", 39); !ok || id != "F2" {
		t.Fatalf("deplane segment should occupy G2 at 39, got %q ok=%v", id, ok)
	}
	if _, _, ok := s.Occupant("G2", 40); ok {
		t.Fatal("deplane segment should end at 40")
	}
	if id, _, ok := s.Occupant("G3", 146); !ok || id != "F2" {
		t.Fatalf("board segment should occupy G3 at 146, got %q ok=%v", id, ok)
	}
	if _, _, ok := s.Occupant("G3", 145); ok {
		t.Fatal("board segment should start at 146")
	}
}

// 延误使区间长度跨过停留上限：整段自动拆成两段，
// 原登机口保留给卸客段，登机段转为待分配；反向合并时整段继承卸客段登机口。
func TestDelayCrossingStayLimit(t *testing.T) {
	s := newSys(t, testCfg()) // Buffer=15, StayLimit=180
	addGate(t, s, "G1", 3, GateDual, 0)
	addFlight(t, s, "F1", 1, FlightDomestic, 100, 200, 30, 0) // 长 115，整段
	wantOK(t, s.Assign("F1", SegWhole, "G1", 0))
	wantOK(t, s.DelayDeparture("F1", 400, 10)) // 长 315 > 180，拆段
	if g, ok := segGate(s, "F1", SegDeplane); !ok || g != "G1" {
		t.Fatalf("deplane should keep G1, got %q ok=%v", g, ok)
	}
	if g, ok := segGate(s, "F1", SegBoard); !ok || g != "" {
		t.Fatalf("board should be pending, got %q ok=%v", g, ok)
	}
	if _, ok := segGate(s, "F1", SegWhole); ok {
		t.Fatal("whole segment should be gone after split")
	}
	// 反向：推迟到达使长度回落到上限内，整段继承卸客段登机口。
	addFlight(t, s, "F2", 1, FlightDomestic, 500, 900, 30, 20) // 长 415，拆段
	wantOK(t, s.Assign("F2", SegDeplane, "G1", 20))
	wantOK(t, s.DelayArrival("F2", 780, 30)) // 长 135，回到整段
	if g, ok := segGate(s, "F2", SegWhole); !ok || g != "G1" {
		t.Fatalf("whole should inherit deplane gate G1, got %q ok=%v", g, ok)
	}
}

// 延误推移触发挤占：四级优先比较（国际先、等级高先、计划到达早先、标识小先）。
func TestBumpPriority(t *testing.T) {
	cfg := Config{Buffer: 0, StayLimit: 500, Deplane: 30, Board: 45}
	// setup 中 delayed 为被延误航班（[100,200) 延误起飞到 350），
	// other 为被冲突航班（[300,400)）；返回被挤占者列表。
	setup := func(t *testing.T, delayedKind, otherKind FlightKind, delayedLvl, otherLvl Level,
		delayedID, otherID string, otherSchedArr int) (*System, Result) {
		s := newSys(t, cfg)
		addGate(t, s, "G1", 3, GateDual, 0)
		addFlight(t, s, delayedID, delayedLvl, delayedKind, 100, 200, 30, 0)
		addFlight(t, s, otherID, otherLvl, otherKind, otherSchedArr, otherSchedArr+100, 30, 0)
		wantOK(t, s.Assign(delayedID, SegWhole, "G1", 0))
		if otherSchedArr != 300 { // 把 other 推移到 [300,400)
			wantOK(t, s.DelayDeparture(otherID, 400, 0))
			wantOK(t, s.DelayArrival(otherID, 300, 0))
		}
		wantOK(t, s.Assign(otherID, SegWhole, "G1", 0))
		r := s.DelayDeparture(delayedID, 350, 0) // 与 other [300,400) 冲突
		wantOK(t, r)
		return s, r
	}

	t.Run("国际先于国内", func(t *testing.T) {
		_, r := setup(t, FlightDomestic, FlightInternational, 1, 1, "FD", "FI", 300)
		if len(r.Bumped) != 1 || r.Bumped[0] != "FD" {
			t.Fatalf("domestic should be bumped, got %v", r.Bumped)
		}
		_, r = setup(t, FlightInternational, FlightDomestic, 1, 1, "FI", "FD", 300)
		if len(r.Bumped) != 1 || r.Bumped[0] != "FD" {
			t.Fatalf("domestic should be bumped, got %v", r.Bumped)
		}
	})

	t.Run("机型等级高者先", func(t *testing.T) {
		_, r := setup(t, FlightDomestic, FlightDomestic, 1, 2, "F1", "F2", 300)
		if len(r.Bumped) != 1 || r.Bumped[0] != "F1" {
			t.Fatalf("lower level F1 should be bumped, got %v", r.Bumped)
		}
		_, r = setup(t, FlightDomestic, FlightDomestic, 3, 2, "F1", "F2", 300)
		if len(r.Bumped) != 1 || r.Bumped[0] != "F2" {
			t.Fatalf("lower level F2 should be bumped, got %v", r.Bumped)
		}
	})

	t.Run("计划到达早者先", func(t *testing.T) {
		_, r := setup(t, FlightDomestic, FlightDomestic, 1, 1, "F1", "F2", 50)
		if len(r.Bumped) != 1 || r.Bumped[0] != "F1" {
			t.Fatalf("later-scheduled F1 should be bumped, got %v", r.Bumped)
		}
		_, r = setup(t, FlightDomestic, FlightDomestic, 1, 1, "F1", "F2", 300)
		if len(r.Bumped) != 1 || r.Bumped[0] != "F2" {
			t.Fatalf("later-scheduled F2 should be bumped, got %v", r.Bumped)
		}
	})

	t.Run("标识小者先", func(t *testing.T) {
		_, r := setup(t, FlightDomestic, FlightDomestic, 1, 1, "FA", "FB", 100)
		if len(r.Bumped) != 1 || r.Bumped[0] != "FB" {
			t.Fatalf("larger id FB should be bumped, got %v", r.Bumped)
		}
		_, r = setup(t, FlightDomestic, FlightDomestic, 1, 1, "FB", "FA", 100)
		if len(r.Bumped) != 1 || r.Bumped[0] != "FB" {
			t.Fatalf("larger id FB should be bumped, got %v", r.Bumped)
		}
	})
}

// 双方都已到达/已登机时不可挤占，延误更新被拒绝且不留任何改动。
func TestBothProtectedNotBumpable(t *testing.T) {
	s := newSys(t, Config{Buffer: 0, StayLimit: 500, Deplane: 30, Board: 45})
	addGate(t, s, "G1", 3, GateDual, 0)
	addFlight(t, s, "FA", 1, FlightDomestic, 100, 200, 60, 0)
	addFlight(t, s, "FB", 1, FlightDomestic, 300, 400, 60, 0)
	wantOK(t, s.Assign("FA", SegWhole, "G1", 0))
	wantOK(t, s.Assign("FB", SegWhole, "G1", 0))
	// now=350：FA 已到达（>=100），FB 已开始登机（>=400-60=340）。
	wantReject(t, s.DelayDeparture("FA", 350, 350), ReasonNotBumpable)
	if d := s.Snapshot()["FA"].CurDep; d != 200 {
		t.Fatalf("rejected update must not change departure, got %d", d)
	}
	if g, _ := segGate(s, "FB", SegWhole); g != "G1" {
		t.Fatalf("rejected update must not change assignments, FB at %q", g)
	}
	// 仅一方受保护：受保护者留下，另一方被挤占。
	wantOK(t, s.DelayDeparture("FA", 350, 150)) // FA 已到达受保护，FB 被挤占
	if g, _ := segGate(s, "FB", SegWhole); g != "" {
		t.Fatalf("FB should be bumped to pending, got %q", g)
	}
}

// 改派被拒绝后原指派保持不变。
func TestReassignRejectedKeepsGate(t *testing.T) {
	s := newSys(t, Config{Buffer: 0, StayLimit: 500, Deplane: 30, Board: 45})
	addGate(t, s, "G1", 3, GateDual, 0)
	addGate(t, s, "G2", 3, GateDual, 0)
	addFlight(t, s, "FA", 1, FlightDomestic, 100, 200, 30, 0)
	addFlight(t, s, "FB", 1, FlightDomestic, 100, 200, 30, 0)
	wantOK(t, s.Assign("FA", SegWhole, "G1", 0))
	wantOK(t, s.Assign("FB", SegWhole, "G2", 0))
	r := s.Assign("FA", SegWhole, "G2", 0)
	wantReject(t, r, ReasonTimeConflict)
	if r.Conflict != "FB" {
		t.Fatalf("conflict should be FB, got %q", r.Conflict)
	}
	if g, _ := segGate(s, "FA", SegWhole); g != "G1" {
		t.Fatalf("rejected reassign must keep FA at G1, got %q", g)
	}
}

// 被挤占者转为待分配后不自动重新指派。
func TestBumpedStaysPending(t *testing.T) {
	s := newSys(t, Config{Buffer: 0, StayLimit: 500, Deplane: 30, Board: 45})
	addGate(t, s, "G1", 3, GateDual, 0)
	addGate(t, s, "G2", 3, GateDual, 0)
	addFlight(t, s, "FA", 1, FlightInternational, 100, 200, 30, 0)
	addFlight(t, s, "FB", 1, FlightDomestic, 300, 400, 30, 0)
	wantOK(t, s.Assign("FA", SegWhole, "G1", 0))
	wantOK(t, s.Assign("FB", SegWhole, "G1", 0))
	r := s.DelayDeparture("FA", 350, 0)
	wantOK(t, r)
	if len(r.Bumped) != 1 || r.Bumped[0] != "FB" {
		t.Fatalf("FB should be bumped, got %v", r.Bumped)
	}
	if g, _ := segGate(s, "FB", SegWhole); g != "" {
		t.Fatalf("FB should be pending, got %q", g)
	}
	wantOK(t, s.DelayDeparture("FA", 360, 0)) // 后续操作不触发自动重指派
	if g, _ := segGate(s, "FB", SegWhole); g != "" {
		t.Fatalf("FB must stay pending, got %q", g)
	}
}

// 拒绝次序：参数非法 > 时钟回退 > 不存在 > 不可改 > 机型 > 属性 > 相邻 > 时间冲突 > 不可挤占。
// 每对相邻类别构造同时满足两者的操作，只报更靠前的一类。
func TestRejectionOrderPairs(t *testing.T) {
	cfg := Config{Buffer: 0, StayLimit: 2000, Deplane: 30, Board: 45}

	// 基础场景：FARR 已到达；FMAX 在 GOCC 长占用（最高等级）；FOCC 在 GADJ 长占用；
	// GOCC 与 GADJ 相邻；GTINY 等级 1 国际；时钟推进到 100。
	setup := func(t *testing.T) *System {
		s := newSys(t, cfg)
		addGate(t, s, "GTINY", 1, GateInternational, 0)
		addGate(t, s, "GOCC", 3, GateDual, 0)
		addGate(t, s, "GADJ", 3, GateDual, 0, "GOCC")
		addGate(t, s, "GSPARE", 3, GateDual, 0)
		addFlight(t, s, "FARR", 2, FlightDomestic, 50, 500, 30, 0)   // now>=50 后已到达
		addFlight(t, s, "FMAX", 3, FlightDomestic, 10, 1000, 30, 0)  // 最高等级长占用
		addFlight(t, s, "FOCC", 1, FlightDomestic, 10, 1000, 30, 0)  // 时间冲突占用
		addFlight(t, s, "FCAND", 3, FlightDomestic, 200, 300, 30, 0) // [200,300)
		wantOK(t, s.Assign("FMAX", SegWhole, "GOCC", 0))
		wantOK(t, s.Assign("FOCC", SegWhole, "GADJ", 0))
		wantOK(t, s.Assign("FARR", SegWhole, "GSPARE", 0))
		addFlight(t, s, "DUMMY", 1, FlightDomestic, 500, 600, 30, 100) // 推进时钟到 100
		return s
	}

	t.Run("参数非法>时钟回退", func(t *testing.T) {
		s := setup(t)
		wantReject(t, s.Assign("FCAND", SegKind(99), "GOCC", 50), ReasonInvalidParam)
	})
	t.Run("时钟回退>不存在", func(t *testing.T) {
		s := setup(t)
		wantReject(t, s.Assign("NOPE", SegWhole, "GOCC", 50), ReasonClockSkew)
	})
	t.Run("不存在>不可改", func(t *testing.T) {
		s := setup(t)
		wantReject(t, s.Assign("FARR", SegWhole, "NOPE", 150), ReasonNotFound)
	})
	t.Run("不可改>机型不兼容", func(t *testing.T) {
		s := setup(t)
		// FARR 已到达（150>=50）且等级 2 超过 GTINY 上限 1。
		wantReject(t, s.Assign("FARR", SegWhole, "GTINY", 150), ReasonImmutable)
	})
	t.Run("机型不兼容>属性不符", func(t *testing.T) {
		s := setup(t)
		// FCAND 等级 3 超过 GTINY 上限 1，且国内航班与国际登机口属性不符。
		wantReject(t, s.Assign("FCAND", SegWhole, "GTINY", 100), ReasonLevelIncompatible)
	})
	t.Run("属性不符>相邻限制", func(t *testing.T) {
		s := setup(t)
		addGate(t, s, "GINT2", 3, GateInternational, 100, "GOCC") // 邻接 FMAX 最高等级占用
		// FCAND 为国内航班（属性不符），且其等级 3 在 GINT2 触发相邻限制。
		wantReject(t, s.Assign("FCAND", SegWhole, "GINT2", 100), ReasonKindMismatch)
	})
	t.Run("相邻限制>时间冲突", func(t *testing.T) {
		s := setup(t)
		// FCAND 等级 3 指派到 GADJ：既触发相邻限制（GOCC 的 FMAX）又与 FOCC 时间冲突。
		wantReject(t, s.Assign("FCAND", SegWhole, "GADJ", 100), ReasonAdjacency)
	})
	t.Run("时间冲突>不可挤占", func(t *testing.T) {
		// 时间冲突只在指派中产生，不可挤占只在延误中产生，二者无法共存于
		// 单个操作；分别验证两种操作各报其类、互不越界。
		s := setup(t)
		addFlight(t, s, "FLOW", 1, FlightDomestic, 200, 300, 30, 100)
		wantReject(t, s.Assign("FLOW", SegWhole, "GADJ", 100), ReasonTimeConflict)
		addGate(t, s, "GP", 3, GateDual, 100)
		addFlight(t, s, "FP1", 1, FlightDomestic, 200, 300, 90, 100)
		addFlight(t, s, "FP2", 1, FlightDomestic, 400, 600, 90, 100)
		wantOK(t, s.Assign("FP1", SegWhole, "GP", 100))
		wantOK(t, s.Assign("FP2", SegWhole, "GP", 100))
		// now=500：双方均已到达且已登机，延误触发冲突但不可挤占。
		wantReject(t, s.DelayDeparture("FP1", 550, 500), ReasonNotBumpable)
	})
}

// 被拒绝的操作不推进时钟、不改任何指派与时刻。
func TestRejectedOpKeepsClockAndState(t *testing.T) {
	s := newSys(t, Config{Buffer: 0, StayLimit: 500, Deplane: 30, Board: 45})
	addGate(t, s, "G1", 3, GateDual, 0)
	addFlight(t, s, "F1", 1, FlightDomestic, 100, 200, 30, 0)
	addFlight(t, s, "F2", 1, FlightDomestic, 300, 400, 30, 0)
	wantOK(t, s.Assign("F1", SegWhole, "G1", 50))
	wantReject(t, s.Assign("F2", SegWhole, "NOPE", 150), ReasonNotFound) // 拒绝于 now=150
	wantOK(t, s.Assign("F2", SegWhole, "G1", 120))                       // 时钟仍停在 50
	wantReject(t, s.Assign("F1", SegWhole, "G1", 90), ReasonClockSkew)
	if g, _ := segGate(s, "F2", SegWhole); g != "G1" {
		t.Fatalf("F2 should be at G1, got %q", g)
	}
	if d := s.Snapshot()["F1"].CurDep; d != 200 {
		t.Fatalf("rejected ops must not change times, got %d", d)
	}
}

// Treap 与朴素切片模型随机对照。
func TestTreapAgainstSliceModel(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	var tr itree
	type iv struct {
		key segKey
		end int
	}
	var live []iv
	segs := map[segKey]*segment{}
	var visits int64
	for step := 0; step < 3000; step++ {
		if len(live) > 0 && r.Intn(3) == 0 {
			i := r.Intn(len(live))
			tr.remove(live[i].key)
			live = append(live[:i], live[i+1:]...)
			continue
		}
		k := segKey{start: r.Intn(1000), fid: fmt.Sprintf("F%d", r.Intn(50)), seg: SegKind(r.Intn(3))}
		if _, dup := segs[k]; dup {
			continue
		}
		end := k.start + 1 + r.Intn(100)
		sg := &segment{kind: k.seg, start: k.start, end: end}
		segs[k] = sg
		tr.insert(k, end, sg)
		live = append(live, iv{key: k, end: end})
		qs, qe := r.Intn(1000), 0
		qe = qs + 1 + r.Intn(200)
		got := tr.overlap(qs, qe, &visits)
		want := 0
		for _, iv := range live {
			if iv.key.start < qe && iv.end > qs {
				want++
			}
		}
		if len(got) != want {
			t.Fatalf("step %d: overlap count=%d want %d", step, len(got), want)
		}
		if tr.size != len(live) {
			t.Fatalf("step %d: size=%d want %d", step, tr.size, len(live))
		}
	}
}

// 性能可验证性：判定与查询开销不随航班总数/历史占用总数增长。
// 通过 visits 计数器直接度量 Treap 节点访问量。
func TestCostIndependentOfFleetAndHistory(t *testing.T) {
	cfg := Config{Buffer: 0, StayLimit: 500, Deplane: 30, Board: 45}
	s := newSys(t, cfg)
	addGate(t, s, "G1", 3, GateDual, 0)
	const n = 5000
	for i := 0; i < n; i++ {
		fid := fmt.Sprintf("F%d", i)
		now := i * 100
		addFlight(t, s, fid, 1, FlightDomestic, i*100+10, i*100+50, 30, now)
		wantOK(t, s.Assign(fid, SegWhole, "G1", now))
	}
	last := n * 100
	addFlight(t, s, "FTAIL", 1, FlightDomestic, last+2000, last+2100, 30, last) // 推进时钟清尾
	if size := s.gates["G1"].tree.size; size != 0 {
		t.Fatalf("expired intervals should be physically removed, size=%d", size)
	}
	// 历史 5000 次占用之后，单次查询访问量应有常数上界。
	before := atomic.LoadInt64(&s.visits)
	s.Occupant("G1", last)
	if used := atomic.LoadInt64(&s.visits) - before; used > 64 {
		t.Fatalf("occupant query visited %d nodes after %d historical ops", used, n)
	}
	// 单次指派判定（含相邻检查与过期清理）同样不随总量增长。
	addFlight(t, s, "FNEW", 1, FlightDomestic, last+1000, last+1100, 30, last)
	before = atomic.LoadInt64(&s.visits)
	wantOK(t, s.Assign("FNEW", SegWhole, "G1", last))
	if used := atomic.LoadInt64(&s.visits) - before; used > 128 {
		t.Fatalf("assign check visited %d nodes after %d historical ops", used, n)
	}
}

// 相同操作序列重放得到相同的指派与相同的挤占结果。
func TestDeterministicReplay(t *testing.T) {
	run := func() ([]Result, map[string]SnapFlight) {
		s := newSys(t, testCfg())
		var results []Result
		rec := func(r Result) { results = append(results, r) }
		rec(s.AddGate(GateSpec{ID: "G1", MaxLevel: 3, Kind: GateDual}, 0))
		rec(s.AddGate(GateSpec{ID: "G2", MaxLevel: 3, Kind: GateDual, Adjacent: []string{"G1"}}, 0))
		rec(s.AddGate(GateSpec{ID: "G3", MaxLevel: 2, Kind: GateDomestic}, 0))
		for i, id := range []string{"A", "B", "C", "D", "E"} {
			rec(s.AddFlight(FlightSpec{
				ID: id, Level: Level(1 + i%3), Kind: FlightKind(i % 2),
				SchedArr: 100 + i*120, SchedDep: 200 + i*120, BoardLead: 45,
			}, 0))
		}
		rec(s.Assign("A", SegWhole, "G1", 0))
		rec(s.Assign("B", SegWhole, "G1", 0))
		rec(s.Assign("C", SegWhole, "G2", 0))
		rec(s.DelayDeparture("A", 320, 10))
		rec(s.DelayArrival("D", 500, 20))
		rec(s.Assign("D", SegWhole, "G1", 30))
		rec(s.DelayDeparture("E", 1000, 40))
		rec(s.Assign("E", SegWhole, "G2", 50))
		return results, s.Snapshot()
	}
	r1, snap1 := run()
	r2, snap2 := run()
	if fmt.Sprintf("%v", r1) != fmt.Sprintf("%v", r2) {
		t.Fatal("replay produced different results")
	}
	if fmt.Sprintf("%v", snap1) != fmt.Sprintf("%v", snap2) {
		t.Fatal("replay produced different snapshots")
	}
}

// 并发调用等价于某串行顺序：竞态烟测 + 终态一致性校验。
func TestConcurrentOps(t *testing.T) {
	s := newSys(t, testCfg())
	for i := 0; i < 4; i++ {
		addGate(t, s, fmt.Sprintf("G%d", i), 3, GateDual, 0)
	}
	for i := 0; i < 20; i++ {
		addFlight(t, s, fmt.Sprintf("F%d", i), Level(1+i%3), FlightKind(i%2),
			1000+i*50, 1100+i*50, 45, 0)
	}
	var clock atomic.Int64
	clock.Store(100)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			r := rand.New(rand.NewSource(seed))
			for i := 0; i < 200; i++ {
				now := int(clock.Add(int64(r.Intn(5))))
				fid := fmt.Sprintf("F%d", r.Intn(20))
				switch r.Intn(4) {
				case 0:
					s.Assign(fid, SegKind(r.Intn(3)), fmt.Sprintf("G%d", r.Intn(4)), now)
				case 1:
					s.DelayDeparture(fid, now+100+r.Intn(500), now)
				case 2:
					s.DelayArrival(fid, now+r.Intn(100), now)
				default:
					s.Occupant(fmt.Sprintf("G%d", r.Intn(4)), now)
				}
			}
		}(int64(w))
	}
	wg.Wait()
	if err := s.CheckConsistency(); err != nil {
		t.Fatalf("inconsistent state after concurrent ops: %v", err)
	}
}
