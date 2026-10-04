package gate

import (
	"errors"
	"testing"
)

func addExample(t *testing.T, g *Gate, sym string, prev, limit int64, x, hmax int, c int64) {
	t.Helper()
	if err := g.AddSymbol(sym, prev, limit, 500, 200, 800, 10, 60, x, hmax, 0, 10000, c); err != nil {
		t.Fatalf("AddSymbol: %v", err)
	}
}

func mustFill(t *testing.T, g *Gate, now int64, sym string, price int64, note string) {
	t.Helper()
	if hr, err := g.Trade(now, sym, price); err != nil || hr != nil {
		t.Fatalf("%s: hr=%v err=%v, want fill", note, hr, err)
	}
}

func TestWorkedExample(t *testing.T) {
	g := New()
	addExample(t, g, "S", 1000, 1000, 1, 3, 300)
	s := g.symbols["S"]
	if s.band.Up != 1100 || s.band.Dn != 900 {
		t.Fatalf("limit = [%d,%d], want [900,1100]", s.band.Dn, s.band.Up)
	}
	mustFill(t, g, 1, "S", 1010, "t1")
	mustFill(t, g, 5, "S", 1020, "t5 equality fills")
	hr, err := g.Trade(11, "S", 1031)
	if err != nil || hr == nil || hr.End != 71 || hr.Reason != ReasonDynamic {
		t.Fatalf("t11 dynamic halt: hr=%+v err=%v", hr, err)
	}
	if _, err := g.Trade(80, "S", 1020); !errors.Is(err, ErrHalted) {
		t.Fatalf("trade during halt even past he must be ErrHalted, got %v", err)
	}
	er, err := g.Resume(71, "S", 1081)
	if err != nil || er == nil || er.End != 131 {
		t.Fatalf("resume extend: er=%v err=%v", er, err)
	}
	er, err = g.Resume(131, "S", 1081)
	if err != nil || er != nil {
		t.Fatalf("resume recover: er=%v err=%v", er, err)
	}
	if got := s.hist.Rs(); got != 1081 {
		t.Fatalf("Rs after resume = %d, want 1081", got)
	}
	mustFill(t, g, 135, "S", 1100, "after resume")
	if _, err := g.Trade(141, "S", 1104); !errors.Is(err, ErrLimit) {
		t.Fatalf("1104 beyond up=1100 must be ErrLimit, got %v", err)
	}
	if s.ph.HaltCount() != 1 {
		t.Fatalf("halt count = %d, want 1", s.ph.HaltCount())
	}
}

func TestRoundingAndEndpoints(t *testing.T) {
	g := New()
	addExample(t, g, "S", 1005, 1000, 1, 3, 300)
	b := g.symbols["S"].band
	if b.Up != 1105 || b.Dn != 905 {
		t.Fatalf("inward rounding = [%d,%d], want [905,1105]", b.Dn, b.Up)
	}
	checks := []struct {
		price int64
		want  error
	}{
		{1105, nil}, {905, nil}, {1106, ErrLimit}, {904, ErrLimit},
	}
	for i, c := range checks {
		err := g.CheckOrder(int64(50+i), "S", c.price)
		if !errors.Is(err, c.want) {
			t.Fatalf("price=%d err=%v want %v", c.price, err, c.want)
		}
	}
}

func TestBandBoundariesAndStaticPriority(t *testing.T) {
	// 独立符号保证 Rd 回落 Rs：静态取等成交、多 1 静态超带；
	// 双超带报静态；只超动态报动态。
	g := New()
	if err := g.AddSymbol("Z", 1000, 1000, 500, 200, 800, 1000, 60, 1, 3, 0, 10000, 300); err != nil {
		t.Fatal(err)
	}
	// 1020：静态 200000<=500000，动态 200000==200000 取等，成交。
	mustFill(t, g, 1, "Z", 1020, "dynamic equality fills")
	// 1050：静态取等（500000==500000）不超，动态超带 -> 动态中断。
	hr0, err0 := g.Trade(2000, "Z", 1050)
	if err0 != nil || hr0 == nil || hr0.Reason != ReasonDynamic {
		t.Fatalf("static equality not over, dynamic over: %+v %v", hr0, err0)
	}
	if err := g.AddSymbol("Y", 1000, 1000, 500, 200, 800, 1000, 60, 1, 3, 0, 10000, 300); err != nil {
		t.Fatal(err)
	}
	hr, err := g.Trade(2100, "Y", 1052)
	if err != nil || hr == nil || hr.Reason != ReasonStatic {
		t.Fatalf("both bands over must report static: hr=%+v err=%v", hr, err)
	}
	if err := g.AddSymbol("X", 1000, 1000, 500, 200, 800, 1000, 60, 1, 3, 0, 10000, 300); err != nil {
		t.Fatal(err)
	}
	// 只超动态：1021 静态 210000<=500000 不超，动态 210000>200000 超 1bp。
	hr, err = g.Trade(2200, "X", 1021)
	if err != nil || hr == nil || hr.Reason != ReasonDynamic {
		t.Fatalf("dynamic one-more halt: hr=%+v err=%v", hr, err)
	}
	// 动态取等：1020 在 Rd=Rs=1000 下 200000==200000，成交。
	if err := g.AddSymbol("W", 1000, 1000, 500, 200, 800, 1000, 60, 1, 3, 0, 10000, 300); err != nil {
		t.Fatal(err)
	}
	mustFill(t, g, 2300, "W", 1020, "dynamic equality")
}

func TestTailAndHaltMax(t *testing.T) {
	g := New()
	addExample(t, g, "S", 1000, 1000, 1, 3, 300)
	hr, err := g.Trade(9660, "S", 1031)
	if err != nil || hr == nil || hr.End != 9700 {
		t.Fatalf("halt clamped to lastCall: hr=%+v err=%v", hr, err)
	}
	if _, err := g.Resume(9700, "S", 1000); err != nil {
		t.Fatalf("resume at lastCall: %v", err)
	}
	if _, err := g.Trade(9700, "S", 1031); !errors.Is(err, ErrDynamicBand) {
		t.Fatalf("tail over-band rejected without halt: %v", err)
	}
	if got := g.symbols["S"].ph.HaltCount(); got != 1 {
		t.Fatalf("tail rejection must not count halt, got %d", got)
	}
	g2 := New()
	if err := g2.AddSymbol("N", 1000, 1000, 500, 200, 800, 10, 60, 1, 0, 0, 10000, 300); err != nil {
		t.Fatal(err)
	}
	if _, err := g2.Trade(11, "N", 1031); !errors.Is(err, ErrDynamicBand) {
		t.Fatalf("Hmax exhausted reject without halt: %v", err)
	}
}

func TestCheckOrderDuringHaltAndOutsideSession(t *testing.T) {
	g := New()
	addExample(t, g, "S", 1000, 1000, 1, 3, 300)
	if _, err := g.Trade(11, "S", 1031); err != nil {
		t.Fatal(err)
	}
	if err := g.CheckOrder(80, "S", 1100); err != nil {
		t.Fatalf("order during halt accepted: %v", err)
	}
	if err := g.CheckOrder(50000, "S", 1000); err != nil {
		t.Fatalf("order outside session accepted: %v", err)
	}
	if err := g.CheckOrder(50001, "S", 1101); !errors.Is(err, ErrLimit) {
		t.Fatalf("illegal price still ErrLimit: %v", err)
	}
}

func TestRejectionOrdering(t *testing.T) {
	g := New()
	addExample(t, g, "S", 1000, 1000, 1, 3, 300)
	if _, err := g.Trade(50, "S", 1000); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Trade(-1, "S", 1000); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("invalid before clockback: %v", err)
	}
	if _, err := g.Trade(10, "NOPE", 1000); !errors.Is(err, ErrClockBack) {
		t.Fatalf("clockback before unknown: %v", err)
	}
	if _, err := g.Trade(51, "NOPE", 1000); !errors.Is(err, ErrUnknownSymbol) {
		t.Fatalf("unknown before phase: %v", err)
	}
	if _, err := g.Trade(60, "S", 1031); err != nil { // he=120
		t.Fatal(err)
	}
	if _, err := g.Trade(20000, "S", 1000); !errors.Is(err, ErrOutOfSession) {
		t.Fatalf("session before halted: %v", err)
	}
	if _, err := g.Trade(121, "S", 5000); !errors.Is(err, ErrHalted) {
		t.Fatalf("halted before limit: %v", err)
	}
	if _, err := g.Resume(61, "S", 5000); !errors.Is(err, ErrHaltNotEnded) {
		t.Fatalf("not-ended before limit: %v", err)
	}
	if _, err := g.Resume(120, "S", 1101); !errors.Is(err, ErrLimit) {
		t.Fatalf("limit after phase satisfied: %v", err)
	}
	g2 := New()
	addExample(t, g2, "S", 1000, 1000, 1, 3, 300)
	if _, err := g2.Resume(10, "S", 1000); !errors.Is(err, ErrNotHalted) {
		t.Fatalf("resume without halt: %v", err)
	}
	if _, err := g2.Trade(11, "S", 1101); !errors.Is(err, ErrLimit) {
		t.Fatalf("limit before band: %v", err)
	}
	// 被拒操作不改时钟：已接受最大 now=50，51 合法但仍在中断。
	if _, err := g.Trade(61, "S", 1000); !errors.Is(err, ErrHalted) {
		t.Fatalf("clock unaffected by rejected ops: %v", err)
	}
}

func TestAddSymbolValidationAndDuplicate(t *testing.T) {
	bad := func(prev, lim, ds, dd, de, w, dur, opn, cls, c int64, x, h int) error {
		g := New()
		return g.AddSymbol("A", prev, lim, ds, dd, de, w, dur, x, h, opn, cls, c)
	}
	cases := []error{
		bad(0, 1000, 500, 200, 800, 10, 60, 0, 100, 0, 1, 3),
		bad(100, 10001, 500, 200, 800, 10, 60, 0, 100, 0, 1, 3),
		bad(100, 1000, 0, 200, 800, 10, 60, 0, 100, 0, 1, 3),
		bad(100, 1000, 500, 200, 10001, 10, 60, 0, 100, 0, 1, 3),
		bad(100, 1000, 500, 200, 800, 0, 60, 0, 100, 0, 1, 3),
		bad(100, 1000, 500, 200, 800, 10, 1_000_001, 0, 100, 0, 1, 3),
		bad(100, 1000, 500, 200, 800, 10, 60, 0, 100, 0, -1, 3),
		bad(100, 1000, 500, 200, 800, 10, 60, 0, 100, 0, 1, 101),
		bad(100, 1000, 500, 200, 800, 10, 60, 0, 100, -1, 1, 3),
		bad(100, 1000, 500, 200, 800, 10, 60, 0, 100, 101, 1, 3),
		bad(100, 1000, 500, 200, 800, 10, 60, 100, 100, 0, 1, 3),
	}
	for i, err := range cases {
		if !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("case %d: %v, want ErrInvalidParam", i, err)
		}
	}
	g := New()
	if err := g.AddSymbol("DUP", 100, 1000, 500, 200, 800, 10, 60, 1, 3, 0, 100, 0); err != nil {
		t.Fatal(err)
	}
	if err := g.AddSymbol("DUP", 100, 1000, 500, 200, 800, 10, 60, 1, 3, 0, 100, 0); !errors.Is(err, ErrDuplicateSymbol) {
		t.Fatalf("duplicate: %v", err)
	}
}
