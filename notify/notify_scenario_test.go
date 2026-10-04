package notify

import (
	"errors"
	"testing"

	"ontology/alert"
)

// 判定依据（与 DESIGN 第 4/5/7 条对应）：
// - sev = min(3,1+floor(x/step))，x 为超出量，取等即危急；
// - 升级仅在新 sev 严格大于事件 sev 时发生，deadline 只减不增；
// - 回读不符不是错误，累计 2 次退回待通知；
// - now>deadline 才逾期（恰等不），逾期粘滞，Act 记 late；
// - 拒绝次序：参数非法 > 时钟回退 > 不存在 > 无资格 > 状态不符。

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	m, err := New(60, 30, 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.AddTest("K", 25, 65, 5); err != nil {
		t.Fatal(err)
	}
	if err := m.SetWard("p1", "W1"); err != nil {
		t.Fatal(err)
	}
	if err := m.Grant("nurseA", "W1", Nurse); err != nil {
		t.Fatal(err)
	}
	if err := m.Grant("docA", "W1", Doctor); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestSpecWorkedExample(t *testing.T) {
	m := newTestManager(t)
	r0, err := m.Result(0, "p1", "K", 66)
	if err != nil || !r0.Created || r0.EventID != 1 || r0.Sev != 1 || r0.Deadline != 60 {
		t.Fatalf("r0=%+v err=%v", r0, err)
	}
	if _, err := m.Notify(5, 1, "tech", "nurseA"); err != nil {
		t.Fatalf("notify: %v", err)
	}
	r20, err := m.Result(20, "p1", "K", 70)
	if err != nil || !r20.Upgraded || r20.Sev != 2 || r20.Rep != 70 || r20.Deadline != 50 {
		t.Fatalf("r20=%+v err=%v", r20, err)
	}
	if _, err := m.ReadBack(22, 1, "nurseA", 70); !errors.Is(err, ErrState) {
		t.Fatalf("want ErrState got %v", err)
	}
	if _, err := m.Notify(25, 1, "tech", "nurseA"); err != nil {
		t.Fatal(err)
	}
	rb, err := m.ReadBack(26, 1, "nurseA", 66)
	if err != nil || !rb.Mismatch || rb.State != alert.StateReadBack {
		t.Fatalf("mismatch1 rb=%+v err=%v", rb, err)
	}
	rb, err = m.ReadBack(27, 1, "nurseA", 70)
	if err != nil || rb.Mismatch || rb.State != alert.StateAct {
		t.Fatalf("correct readback rb=%+v err=%v", rb, err)
	}
	r30, err := m.Result(30, "p1", "K", 71)
	if err != nil || r30.Upgraded || r30.Rep != 70 || r30.Deadline != 50 {
		t.Fatalf("same-sev r30=%+v err=%v", r30, err)
	}
	if e := m.board.Get(1); len(e.Results) != 3 {
		t.Fatalf("results=%d want 3", len(e.Results))
	}
	a, err := m.Act(50, 1, "docA")
	if err != nil || a.Late {
		t.Fatalf("act-on-time=%+v err=%v", a, err)
	}
	if ov := m.Overdue(); len(ov) != 0 {
		t.Fatalf("overdue=%v want empty", ov)
	}
}

func TestActLateAfterDeadline(t *testing.T) {
	m := newTestManager(t)
	r, _ := m.Result(0, "p1", "K", 66)
	m.Notify(1, r.EventID, "tech", "nurseA")
	m.ReadBack(2, r.EventID, "nurseA", 66)
	if _, err := m.Notify(60, 999, "tech", "nurseA"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound got %v", err)
	}
	if ov := m.Overdue(); len(ov) != 0 {
		t.Fatalf("rejected op must not land overdue: %v", ov)
	}
	a, err := m.Act(61, r.EventID, "docA")
	if err != nil || !a.Late || len(a.LandNow) != 1 || a.LandNow[0] != 1 {
		t.Fatalf("late act=%+v err=%v", a, err)
	}
	if ov := m.Overdue(); len(ov) != 1 || ov[0] != 1 {
		t.Fatalf("overdue list=%v", ov)
	}
	if _, err := m.Notify(62, r.EventID, "tech", "nurseA"); !errors.Is(err, ErrState) {
		t.Fatalf("closed event -> ErrState got %v", err)
	}
	if ov := m.Overdue(); len(ov) != 1 {
		t.Fatalf("overdue id must not duplicate: %v", ov)
	}
}

func TestTwoMismatchesReturnToNotify(t *testing.T) {
	m := newTestManager(t)
	r, _ := m.Result(0, "p1", "K", 66)
	m.Notify(0, r.EventID, "tech", "nurseA")
	rb1, _ := m.ReadBack(1, r.EventID, "nurseA", 1)
	rb2, _ := m.ReadBack(2, r.EventID, "nurseA", 2)
	if !rb1.Mismatch || !rb2.Mismatch || rb2.State != alert.StateNotify {
		t.Fatalf("rb1=%+v rb2=%+v", rb1, rb2)
	}
	if _, err := m.ReadBack(3, r.EventID, "nurseA", 66); !errors.Is(err, ErrState) {
		t.Fatalf("reset-to-notify before readback want ErrState got %v", err)
	}
	m.Grant("nurseB", "W1", Nurse)
	m.Notify(4, r.EventID, "tech", "nurseB")
	rb, _ := m.ReadBack(5, r.EventID, "nurseB", 66)
	if rb.State != alert.StateAct {
		t.Fatalf("state=%v", rb.State)
	}
	a, err := m.Act(6, r.EventID, "docA")
	if err != nil || a.Late {
		t.Fatalf("act=%+v err=%v", a, err)
	}
}

func TestNormalResultNeverCloses(t *testing.T) {
	m := newTestManager(t)
	r, _ := m.Result(0, "p1", "K", 75)
	nr, err := m.Result(5, "p1", "K", 40)
	if err != nil || !nr.Normal {
		t.Fatalf("normal=%+v err=%v", nr, err)
	}
	nb, err := m.Notify(11, r.EventID, "tech", "nurseA")
	if err != nil || len(nb.LandNow) != 1 || nb.LandNow[0] != 1 {
		t.Fatalf("notify after deadline=%+v err=%v", nb, err)
	}
	if e := m.board.Get(1); !e.Late || e.State != alert.StateReadBack {
		t.Fatalf("normal result must not close event: %+v", e)
	}
}

func TestLandingOrder(t *testing.T) {
	m := newTestManager(t)
	m.SetWard("p2", "W1")
	a, _ := m.Result(0, "p1", "K", 75)
	c, _ := m.Result(1, "p2", "K", 75)
	nb, err := m.Notify(11, a.EventID, "tech", "nurseA")
	if err != nil || len(nb.LandNow) != 1 || nb.LandNow[0] != a.EventID {
		t.Fatalf("land=%+v err=%v", nb, err)
	}
	nb2, err := m.Notify(12, c.EventID, "tech", "nurseA")
	if err != nil || len(nb2.LandNow) != 1 || nb2.LandNow[0] != c.EventID {
		t.Fatalf("land2=%+v err=%v", nb2, err)
	}
	if ov := m.Overdue(); len(ov) != 2 || ov[0] != a.EventID || ov[1] != c.EventID {
		t.Fatalf("order=%v", ov)
	}
}

func TestUpgradeNeverDelaysAndInvalidatesNotify(t *testing.T) {
	m := newTestManager(t)
	r, _ := m.Result(0, "p1", "K", 66)
	m.Notify(1, r.EventID, "tech", "nurseA")
	u, err := m.Result(40, "p1", "K", 75)
	if err != nil || !u.Upgraded || u.Deadline != 50 {
		t.Fatalf("u40=%+v err=%v", u, err)
	}
	if _, err := m.ReadBack(41, r.EventID, "nurseA", 75); !errors.Is(err, ErrState) {
		t.Fatalf("upgraded event must reset to notify: %v", err)
	}
}

func TestRejectionOrder(t *testing.T) {
	cases := []struct {
		name string
		call func(*Manager) error
		want error
	}{
		{"invalid-before-clockback", func(m *Manager) error { _, e := m.Result(-1, "p1", "K", 66); return e }, ErrInvalid},
		{"invalid-before-missing", func(m *Manager) error { _, e := m.Result(0, "", "K", 66); return e }, ErrInvalid},
		{"clockback", func(m *Manager) error {
			m.Result(5, "p1", "K", 66)
			_, e := m.Notify(4, 1, "t", "nurseA")
			return e
		}, ErrClockBack},
		{"missing-test", func(m *Manager) error { _, e := m.Result(2, "p1", "NOPE", 66); return e }, ErrNotFound},
		{"missing-patient", func(m *Manager) error { _, e := m.Result(2, "ghost", "K", 66); return e }, ErrNotFound},
		{"missing-event", func(m *Manager) error { _, e := m.Notify(2, 77, "t", "nurseA"); return e }, ErrNotFound},
		{"unauthorized-before-state", func(m *Manager) error {
			m.Result(0, "p1", "K", 66)
			m.Notify(1, 1, "t", "nurseA") // 进入待回读
			m.Grant("outsider", "W2", Nurse)
			_, e := m.ReadBack(2, 1, "outsider", 1)
			return e
		}, ErrUnauthorized},
		{"nurse-cannot-act", func(m *Manager) error {
			m.Result(0, "p1", "K", 66)
			m.Notify(2, 1, "t", "nurseA")
			m.ReadBack(3, 1, "nurseA", 66)
			_, e := m.Act(4, 1, "nurseA")
			return e
		}, ErrUnauthorized},
		{"state-mismatch-last", func(m *Manager) error {
			m.Result(0, "p1", "K", 66)
			_, e := m.Act(5, 1, "docA") // 有医生资格但不在待处置
			return e
		}, ErrState},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := newTestManager(t)
			if err := c.call(m); !errors.Is(err, c.want) {
				t.Fatalf("got %v want %v", err, c.want)
			}
		})
	}
	// 被拒操作不占号：所有被拒之后仍只有 1 号事件
	m := newTestManager(t)
	m.Result(0, "p1", "K", 66)
	m.Result(-1, "p1", "K", 66)
	m.Notify(2, 77, "t", "nurseA")
	if m.board.Get(2) != nil {
		t.Fatal("rejected ops must not consume event ids")
	}
}

func TestWardChangeAffectsLaterChecks(t *testing.T) {
	m := newTestManager(t)
	m.SetWard("p2", "W2")
	m.Grant("nurseW2", "W2", Nurse)
	m.Grant("docW2", "W2", Doctor)
	r, _ := m.Result(0, "p2", "K", 66)
	if _, err := m.Notify(1, r.EventID, "t", "nurseA"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("W1 nurse on W2 patient: got %v", err)
	}
	m.Notify(2, r.EventID, "t", "nurseW2")
	m.ReadBack(3, r.EventID, "nurseW2", 66)
	m.SetWard("p2", "W1")
	if _, err := m.Act(4, r.EventID, "docW2"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("W2 doctor after transfer to W1: got %v", err)
	}
	a, err := m.Act(5, r.EventID, "docA")
	if err != nil || a.Late {
		t.Fatalf("act=%+v err=%v", a, err)
	}
}

func TestNewValidation(t *testing.T) {
	if _, err := New(0, 30, 10); !errors.Is(err, ErrInvalid) {
		t.Fatalf("T1=0 got %v", err)
	}
	if _, err := New(60, 30, 10_001); !errors.Is(err, ErrInvalid) {
		t.Fatalf("T3 too big got %v", err)
	}
}
