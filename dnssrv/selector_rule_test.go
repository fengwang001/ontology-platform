package dnssrv

import (
	"errors"
	"testing"
)

func mustNew(t *testing.T, capacity int, coolCap, wr int64) *Selector {
	t.Helper()
	s, err := New(capacity, coolCap, wr)
	if err != nil {
		t.Fatalf("New(%d,%d,%d) unexpected error: %v", capacity, coolCap, wr, err)
	}
	return s
}

func mustAdd(t *testing.T, s *Selector, target string, port, pri, w int, ttl, now int64) {
	t.Helper()
	if err := s.Add(target, port, pri, w, ttl, now); err != nil {
		t.Fatalf("Add(%s:%d) unexpected error: %v", target, port, err)
	}
}

func pickTarget(t *testing.T, s *Selector, r uint64, now int64) string {
	t.Helper()
	tgt, _, err := s.Pick(r, now)
	if err != nil {
		t.Fatalf("Pick(r=%d,now=%d) unexpected error: %v", r, now, err)
	}
	return tgt
}

func TestConfigInvalid(t *testing.T) {
	cases := []struct {
		capacity int
		coolCap  int64
		wr       int64
	}{
		{0, 60, 100}, {-1, 60, 100},
		{2, 0, 100}, {2, 1_000_000_001, 100},
		{2, 60, 0}, {2, 60, 1_000_000_001},
	}
	for i, c := range cases {
		if _, err := New(c.capacity, c.coolCap, c.wr); !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("case %d: got %v want ErrInvalidConfig", i, err)
		}
	}
}

// TestPickGroupOrdering mirrors the worked example: R2(w0), R1(5), R3(15);
// S=20, r1 = r mod 21.
func TestPickGroupOrdering(t *testing.T) {
	s := mustNew(t, 10, 1_000_000_000, 1_000_000_000)
	mustAdd(t, s, "R1", 1, 10, 5, 1000, 0)
	mustAdd(t, s, "R2", 2, 10, 0, 1000, 0)
	mustAdd(t, s, "R3", 3, 10, 15, 1000, 0)
	mustAdd(t, s, "R4", 4, 20, 100, 1000, 0)

	want := map[uint64]string{
		0: "R2",
		1: "R1", 2: "R1", 3: "R1", 4: "R1", 5: "R1",
		6: "R3", 10: "R3", 20: "R3",
		21: "R2",
	}
	for r, name := range want {
		got := pickTarget(t, s, r, 50)
		t.Logf("判定依据: r=%d -> r1=%d, 顺序[R2:0,R1:5,R3:20] 首个累计>=r1 的是 %s, Pick=%s",
			r, r%21, name, got)
		if got != name {
			t.Errorf("Pick(r=%d)=%s want %s", r, got, name)
		}
	}
}

// TestPickAllZeroWeight: S=0, r1 always 0, lowest regID wins for every r.
func TestPickAllZeroWeight(t *testing.T) {
	s := mustNew(t, 10, 1_000_000_000, 1_000_000_000)
	mustAdd(t, s, "A", 1, 10, 0, 1000, 0)
	mustAdd(t, s, "B", 2, 10, 0, 1000, 0)
	mustAdd(t, s, "C", 3, 10, 0, 1000, 0)
	for _, r := range []uint64{0, 1, 7, 1 << 40} {
		got := pickTarget(t, s, r, 50)
		t.Logf("判定依据: 全 0 权重 S=0, r1=%d, 选登记序最小 A, Pick(%d)=%s", r, r, got)
		if got != "A" {
			t.Errorf("Pick(r=%d)=%s want A", r, got)
		}
	}
}

// TestCooldownRecoveryBoundary: unavailable at deadline-1, available exactly
// at the deadline.
func TestCooldownRecoveryBoundary(t *testing.T) {
	s := mustNew(t, 10, 1_000_000_000, 1_000_000_000)
	mustAdd(t, s, "R3", 3, 10, 15, 10000, 0)
	if err := s.Failure("R3", 3, 10, 100); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Pick(0, 109); !errors.Is(err, ErrNoAvailable) {
		t.Fatalf("at 109 want ErrNoAvailable, got %v", err)
	}
	t.Log("判定依据: Failure@100 cooldown=10 f=1 -> 截止110; now=109<110 不可用")
	if got := pickTarget(t, s, 0, 110); got != "R3" {
		t.Fatalf("at 110 want R3 recovered, got %s", got)
	}
	t.Log("判定依据: now=110>=冷却截止110 且 now<到期, 冷却恰在截止时刻恢复")
}

// TestExpiryAndPriorityFallback: cooled record shrinks the group; when the
// whole group is unavailable selection falls to the next priority; expiry at
// now == expire.
func TestExpiryAndPriorityFallback(t *testing.T) {
	s := mustNew(t, 10, 1_000_000_000, 1_000_000_000)
	mustAdd(t, s, "R1", 1, 10, 5, 1000, 0)
	mustAdd(t, s, "R2", 2, 10, 0, 1000, 0)
	mustAdd(t, s, "R3", 3, 10, 15, 10000, 0)
	mustAdd(t, s, "R4", 4, 20, 100, 10000, 0)

	if err := s.Failure("R3", 3, 10, 100); err != nil {
		t.Fatal(err)
	}
	if got := pickTarget(t, s, 5, 105); got != "R1" {
		t.Errorf("r=5 want R1, got %s", got)
	}
	if got := pickTarget(t, s, 6, 105); got != "R2" {
		t.Errorf("r=6 -> r1=0 want R2, got %s", got)
	}
	t.Log("判定依据: R3 冷却中, 组10 S=5, r=5 选 R1; r=6 r1=0 选 R2")

	// Extend R3's cooling past 1000, when R1/R2 expire: whole group down.
	if err := s.Failure("R3", 3, 900, 105); err != nil {
		t.Fatal(err)
	}
	if err := s.Failure("R3", 3, 900, 126); err != nil {
		t.Fatal(err)
	}
	if got := s.recs[id{"R3", 3}].coolUntil; got <= 1000 {
		t.Fatalf("setup: R3 coolUntil=%d should exceed 1000", got)
	}
	if got := pickTarget(t, s, 99, 2000); got != "R4" {
		t.Errorf("group 10 unavailable at 1000, want R4 got %s", got)
	}
	t.Log("判定依据: R1/R2 到期时刻1000<=now=2000, R3 冷却至3726, 整组10不可用, 降到优先级20选 R4")

	// At 9999 R3 has cooled down (deadline 3726) and R1/R2 are gone, so
	// priority 10 contains only R3 again.
	if got := pickTarget(t, s, 0, 9999); got != "R3" {
		t.Errorf("at 9999 want recovered R3, got %s", got)
	}
	if _, _, err := s.Pick(0, 10000); !errors.Is(err, ErrNoAvailable) {
		t.Errorf("at expire=10000 want ErrNoAvailable, got %v", err)
	}
	t.Log("判定依据: now==到期时刻 即不可用(可用要求 now<到期)")

	if err := s.Success("R4", 4, 10000); !errors.Is(err, ErrExpired) {
		t.Errorf("Success on expired want ErrExpired, got %v", err)
	}
	if err := s.Failure("R4", 4, 1, 10000); !errors.Is(err, ErrExpired) {
		t.Errorf("Failure on expired want ErrExpired, got %v", err)
	}
	if err := s.Failure("nope", 9, 1, 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing record want ErrNotFound, got %v", err)
	}
}

// TestReAddKeepsState: update keeps regID, coolUntil, f, lf; overwrites rest.
func TestReAddKeepsState(t *testing.T) {
	s := mustNew(t, 10, 1_000_000_000, 100)
	mustAdd(t, s, "X", 1, 10, 5, 1000, 0)
	if err := s.Failure("X", 1, 10, 100); err != nil {
		t.Fatal(err)
	}
	if err := s.Add("X", 1, 30, 7, 1000, 200); err != nil {
		t.Fatal(err)
	}
	cur := s.recs[id{"X", 1}]
	if cur.regID != 1 {
		t.Errorf("regID changed: got %d want 1", cur.regID)
	}
	if cur.coolUntil != 110 || cur.f != 1 || !cur.hasLF || cur.lf != 100 {
		t.Errorf("cooldown/f/lf not preserved: %+v", cur)
	}
	if cur.priority != 30 || cur.weight != 7 || cur.expire != 1200 {
		t.Errorf("priority/weight/expire not updated: %+v", cur)
	}
	if s.nextID != 2 {
		t.Errorf("nextID advanced on update: %d", s.nextID)
	}
	t.Logf("判定依据: 重复登记覆盖 p/w/expire=%d, 保留 regID=%d coolUntil=%d f=%d lf=%d",
		cur.expire, cur.regID, cur.coolUntil, cur.f, cur.lf)
}

// TestExponentialCooldownAndWr follows the spec's worked example, including
// Success at 150 and the Wr reset boundary (reset at ==lf+Wr, not one before).
func TestExponentialCooldownAndWr(t *testing.T) {
	s := mustNew(t, 10, 60, 100)
	mustAdd(t, s, "R3", 3, 10, 15, 100000, 0)

	check := func(now int64, wantF int, wantCool int64) {
		t.Helper()
		rec := s.recs[id{"R3", 3}]
		if rec.f != wantF || rec.coolUntil != wantCool {
			t.Fatalf("at %d: f=%d(want %d) coolUntil=%d(want %d)",
				now, rec.f, wantF, rec.coolUntil, wantCool)
		}
		t.Logf("判定依据: now=%d -> f=%d 冷却截止=%d", now, wantF, wantCool)
	}

	mustFail := func(now int64) {
		t.Helper()
		if err := s.Failure("R3", 3, 10, now); err != nil {
			t.Fatal(err)
		}
	}

	mustFail(100)
	check(100, 1, 110)
	mustFail(105) // 105 < 200: no reset
	check(105, 2, 125)
	mustFail(130)
	check(130, 3, 170)
	mustFail(140)
	check(140, 4, 200) // eff min(60,80)=60

	if err := s.Success("R3", 3, 150); err != nil {
		t.Fatal(err)
	}
	rec := s.recs[id{"R3", 3}]
	if rec.f != 0 || rec.coolUntil != 200 || rec.lf != 140 {
		t.Fatalf("success changed cool/lf: f=%d cool=%d lf=%d", rec.f, rec.coolUntil, rec.lf)
	}
	t.Log("判定依据: Success@150 仅清 f=0, coolUntil 仍 200, lf 仍 140")

	mustFail(160)      // f restarts at 1, eff 10
	check(160, 1, 200) // max(200,170)=200: never shortens

	// One before the silence window boundary does not reset.
	mustFail(259) // 259 < 160+100: f becomes 2, eff 20 -> max(200,279)=279
	check(259, 2, 279)
	// Exactly at lf+Wr: reset to f=0 then f=1, eff 10, max keeps deadline.
	mustFail(359) // lf=259, 259+100=359 -> reset, f=1, max(279,369)=369
	check(359, 1, 369)

	// Spec's 300-style reset after a gap past Wr: fresh f=1 and eff 10 may
	// extend the deadline via max.
	if err := s.Success("R3", 3, 400); err != nil {
		t.Fatal(err)
	}
	mustFail(400)
	check(400, 1, 410) // max(369,410)
	mustFail(500)      // 500 >= 400+100 -> reset, f=1, eff 10
	check(500, 1, 510)
}

// TestExponentShiftCap: failures beyond a shift of 30 keep the multiplier at
// 2^30 (capped by CoolCap before that when applicable).
func TestExponentShiftCap(t *testing.T) {
	// Small CoolCap (2): f>=2 always clips, proving the ceiling path while
	// the failure counter keeps growing.
	s := mustNew(t, 10, 2, 1_000_000_000)
	mustAdd(t, s, "R", 1, 10, 1, 1000000, 0)
	// f=1 -> eff 1, deadline 1.
	if err := s.Failure("R", 1, 1, 0); err != nil {
		t.Fatal(err)
	}
	if got := s.recs[id{"R", 1}].coolUntil; got != 1 {
		t.Fatalf("f=1 cool=%d want 1", got)
	}
	// f=2..32: raw multiplier 2^(f-1) but ceiling clips eff to 2.
	for n := int64(0); n < 30; n++ {
		if err := s.Failure("R", 1, 1, n+10); err != nil {
			t.Fatal(err)
		}
	}
	rec := s.recs[id{"R", 1}]
	if rec.f != 31 || rec.coolUntil != 39+2 {
		t.Fatalf("f=31 state: f=%d cool=%d", rec.f, rec.coolUntil)
	}
	if err := s.Failure("R", 1, 1, 100); err != nil {
		t.Fatal(err)
	}
	rec = s.recs[id{"R", 1}]
	if rec.f != 32 || rec.coolUntil != 102 {
		t.Fatalf("f=32 state: f=%d cool=%d want 102", rec.f, rec.coolUntil)
	}
	// f=33: shift still min(f-1,30)=30; ceiling unchanged.
	if err := s.Failure("R", 1, 1, 200); err != nil {
		t.Fatal(err)
	}
	rec = s.recs[id{"R", 1}]
	if rec.f != 33 || rec.coolUntil != 202 {
		t.Fatalf("f=33 state: f=%d cool=%d want 202", rec.f, rec.coolUntil)
	}
	t.Log("判定依据: f 持续增长时位移封顶 min(f-1,30)=30, 有效冷却再受 CoolCap=2 封顶")
}

// TestExponentShift30Observable: with cooldown=1 the raw multiplier 2^(f-1)
// is visible up to f=30 (2^29 <= CoolCap), and f=31 (raw 2^30) is clipped by
// CoolCap while f keeps increasing.
func TestExponentShift30Observable(t *testing.T) {
	s := mustNew(t, 10, 1_000_000_000, 1_000_000_000)
	mustAdd(t, s, "R", 1, 10, 1, 100000000, 0)
	for n := int64(0); n < 30; n++ {
		if err := s.Failure("R", 1, 1, n); err != nil {
			t.Fatal(err)
		}
	}
	rec := s.recs[id{"R", 1}]
	if rec.f != 30 || rec.coolUntil != 29+(1<<29) {
		t.Fatalf("f=30: f=%d cool=%d want %d", rec.f, rec.coolUntil, 29+(1<<29))
	}
	if err := s.Failure("R", 1, 1, 30); err != nil {
		t.Fatal(err)
	}
	rec = s.recs[id{"R", 1}]
	if rec.f != 31 || rec.coolUntil != 30+1_000_000_000 {
		t.Fatalf("f=31: f=%d cool=%d want %d (2^30 clipped by CoolCap)",
			rec.f, rec.coolUntil, 30+1_000_000_000)
	}
	t.Logf("判定依据: f=30 有效冷却 2^29=%d; f=31 位移仍为30 且 2^30 被 CoolCap 截为1e9",
		int64(1)<<29)
}

// TestCoolCapCeiling: a huge base cooldown is clipped to CoolCap.
func TestCoolCapCeiling(t *testing.T) {
	s := mustNew(t, 10, 60, 1_000_000_000)
	mustAdd(t, s, "R", 1, 10, 1, 1000000, 0)
	if err := s.Failure("R", 1, 1_000_000_000, 0); err != nil {
		t.Fatal(err)
	}
	if got := s.recs[id{"R", 1}].coolUntil; got != 60 {
		t.Fatalf("effective cooldown clipped: coolUntil=%d want 60", got)
	}
	t.Log("判定依据: 有效冷却 min(CoolCap=60, 1e9*1)=60, 截止60")
}
