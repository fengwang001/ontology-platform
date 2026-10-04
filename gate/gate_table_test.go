package gate

import (
	"errors"
	"testing"
)

// step 为一次网关注入操作；want 用 errors.Is 匹配，nil 表示放行。
type step struct {
	desc string
	f    func(g *Gateway) error
	want error
}

func reg(now int64, a, gr string) step {
	return step{"register", func(g *Gateway) error { return g.Register(now, []byte(a), []byte(gr)) }, nil}
}
func setlim(now int64, s string, la, lg, d int64) step {
	return step{"setlimit", func(g *Gateway) error { g.SetLimit(now, []byte(s), la, lg, d); return nil }, nil}
}
func hedge(now int64, a, s string, side Side, h int64, want error) step {
	return step{"sethedge", func(g *Gateway) error { return g.SetHedge(now, []byte(a), []byte(s), side, h) }, want}
}
func ord(now int64, o, a, s string, side Side, off Offset, q int64, want error) step {
	return step{"order", func(g *Gateway) error {
		return g.Order(now, []byte(o), []byte(a), []byte(s), side, off, q)
	}, want}
}
func fill(now int64, o string, q int64, want error) step {
	return step{"fill", func(g *Gateway) error { return g.Fill(now, []byte(o), q) }, want}
}
func cancel(now int64, o string, want error) step {
	return step{"cancel", func(g *Gateway) error { return g.Cancel(now, []byte(o)) }, want}
}
func reset(now int64) step {
	return step{"resetday", func(g *Gateway) error { g.ResetDay(now); return nil }, nil}
}

type tableCase struct {
	name  string
	steps []step
	post  func(t *testing.T, g *Gateway)
}

func runTable(t *testing.T, tc tableCase) {
	t.Helper()
	g := New()
	for i, st := range tc.steps {
		err := st.f(g)
		if !errors.Is(err, st.want) {
			t.Fatalf("%s: step %d (%s): want %v, got %v", tc.name, i, st.desc, st.want, err)
		}
	}
	if tc.post != nil {
		tc.post(t, g)
	}
}

func b(s string) []byte { return []byte(s) }

func TestValidationAndRejectOrder(t *testing.T) {
	cases := []tableCase{
		{
			"invalid args take precedence",
			[]step{
				{"reg neg now", func(g *Gateway) error { return g.Register(-1, b("A"), b("G")) }, ErrInvalid},
				{"reg now > 1e12", func(g *Gateway) error { return g.Register(1e12+1, b("A"), b("G")) }, ErrInvalid},
				{"reg empty acct", func(g *Gateway) error { return g.Register(1, nil, b("G")) }, ErrInvalid},
				{"reg empty group", func(g *Gateway) error { return g.Register(1, b("A"), nil) }, ErrInvalid},
				{"order empty oid", func(g *Gateway) error {
					return g.Order(1, nil, b("A"), b("S"), Long, Open, 1)
				}, ErrInvalid},
				{"order qty 0", func(g *Gateway) error {
					return g.Order(1, b("o"), b("A"), b("S"), Long, Open, 0)
				}, ErrInvalid},
				{"order qty >1e9", func(g *Gateway) error {
					return g.Order(1, b("o"), b("A"), b("S"), Long, Open, 1e9+1)
				}, ErrInvalid},
				{"order bad side", func(g *Gateway) error {
					return g.Order(1, b("o"), b("A"), b("S"), Side(0), Open, 1)
				}, ErrInvalid},
				{"order bad offset", func(g *Gateway) error {
					return g.Order(1, b("o"), b("A"), b("S"), Long, Offset(7), 1)
				}, ErrInvalid},
				{"setlimit neg ignored", func(g *Gateway) error {
					g.SetLimit(1, b("S"), -1, 0, 0)
					return nil
				}, nil},
				{"hedge >1e9", func(g *Gateway) error {
					return g.SetHedge(1, b("A"), b("S"), Long, 1e9+1)
				}, ErrInvalid},
				{"fill qty 0", func(g *Gateway) error { return g.Fill(1, b("o"), 0) }, ErrInvalid},
			},
			func(t *testing.T, g *Gateway) {
				if _, ok := g.reg.Limits(b("S")); ok {
					t.Fatalf("invalid SetLimit must not register symbol")
				}
			},
		},
		{
			"clock monotonic; rejected op does not move clock",
			[]step{
				reg(5, "A", "G"),
				{"reg at 4", func(g *Gateway) error { return g.Register(4, b("B"), b("G")) }, ErrClock},
				{"clock error precedes duplicate", func(g *Gateway) error {
					return g.Register(4, b("A"), b("G"))
				}, ErrClock},
				reg(5, "B", "G"),
				setlim(5, "S", 10, 10, 10),
				{"order at 3 clock", func(g *Gateway) error {
					return g.Order(3, b("o"), b("A"), b("S"), Long, Open, 1)
				}, ErrClock},
			},
			nil,
		},
		{
			"duplicate register fixes group",
			[]step{
				reg(1, "A", "G"),
				{"re-register", func(g *Gateway) error { return g.Register(2, b("A"), b("G2")) }, ErrDuplicate},
			},
			func(t *testing.T, g *Gateway) {
				gr, ok := g.reg.Group(b("A"))
				if !ok || string(gr) != "G" {
					t.Fatalf("group = %q,%v want G,true", gr, ok)
				}
			},
		},
		{
			"not found ordering: acct/sym before oid dup",
			[]step{
				reg(1, "A", "G"),
				setlim(2, "S", 10, 10, 10),
				ord(3, "o", "A", "S", Long, Open, 5, nil),
				{"unknown acct with dup oid", func(g *Gateway) error {
					return g.Order(4, b("o"), b("X"), b("S"), Long, Open, 1)
				}, ErrNotFound},
				{"unknown sym with dup oid", func(g *Gateway) error {
					return g.Order(4, b("o"), b("A"), b("X"), Long, Open, 1)
				}, ErrNotFound},
				{"dup live oid", func(g *Gateway) error {
					return g.Order(4, b("o"), b("A"), b("S"), Long, Open, 1)
				}, ErrDuplicateOID},
				{"fill unknown oid", func(g *Gateway) error { return g.Fill(4, b("z"), 1) }, ErrNotFound},
				{"cancel unknown oid", func(g *Gateway) error { return g.Cancel(4, b("z")) }, ErrNotFound},
				{"hedge unknown acct", func(g *Gateway) error {
					return g.SetHedge(4, b("X"), b("S"), Long, 1)
				}, ErrNotFound},
			},
			nil,
		},
		{
			"state: fill over remain and terminated orders",
			[]step{
				reg(1, "A", "G"),
				setlim(2, "S", 100, 100, 1000),
				ord(3, "o", "A", "S", Long, Open, 10, nil),
				fill(4, "o", 4, nil),
				fill(5, "o", 7, ErrState),
				fill(5, "o", 6, nil),
				{"reuse oid after full fill", func(g *Gateway) error {
					return g.Order(6, b("o"), b("A"), b("S"), Long, Open, 1)
				}, ErrState},
				{"fill terminated", func(g *Gateway) error { return g.Fill(6, b("o"), 1) }, ErrState},
				{"cancel terminated", func(g *Gateway) error { return g.Cancel(6, b("o")) }, ErrState},
			},
			func(t *testing.T, g *Gateway) {
				o, ok := g.LookupOrder(b("o"))
				if !ok || o.Remain != 0 {
					t.Fatalf("order remain = %d ok=%v", o.Remain, ok)
				}
			},
		},
		{
			"state after cancel",
			[]step{
				reg(1, "A", "G"),
				setlim(2, "S", 100, 100, 1000),
				ord(3, "o", "A", "S", Long, Open, 10, nil),
				fill(4, "o", 3, nil),
				cancel(5, "o", nil),
				cancel(6, "o", ErrState),
				fill(6, "o", 1, ErrState),
			},
			nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { runTable(t, tc) })
	}
}
