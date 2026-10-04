package gate_test

import (
	"errors"
	"fmt"
	"testing"

	"ontology/gate"
)

type op struct {
	kind   string
	now    int64
	oid    string
	acct   string
	group  string
	sym    string
	side   gate.Side
	offset gate.Offset
	qty    int64
	la     int64
	lg     int64
	day    int64
	hedge  int64
	want   error
}

type snapshot struct {
	posLong      int64
	openLong     int64
	closeLong    int64
	exposureLong int64
	groupLong    int64
	dayOpen      int64
}

func runOps(t *testing.T, g *gate.Gateway, ops []op) error {
	t.Helper()
	for i, in := range ops {
		err := doOp(g, in)
		t.Logf("op[%d] input=%+v output=%s reason=%s", i, in, status(err), reason(err))
		if !errors.Is(err, in.want) {
			return fmt.Errorf("op %d %s: got %v, want %v", i, in.kind, err, in.want)
		}
	}
	return nil
}

func applyOps(g *gate.Gateway, ops []op) error {
	for i, in := range ops {
		err := doOp(g, in)
		if !errors.Is(err, in.want) {
			return fmt.Errorf("op %d %s: got %v, want %v", i, in.kind, err, in.want)
		}
	}
	return nil
}

func doOp(g *gate.Gateway, in op) error {
	switch in.kind {
	case "register":
		return g.Register(in.now, b(in.acct), b(in.group))
	case "limit":
		return g.SetLimit(in.now, b(in.sym), in.la, in.lg, in.day)
	case "hedge":
		return g.SetHedge(in.now, b(in.acct), b(in.sym), in.side, in.hedge)
	case "order":
		return g.Order(in.now, b(in.oid), b(in.acct), b(in.sym), in.side, in.offset, in.qty)
	case "fill":
		return g.Fill(in.now, b(in.oid), in.qty)
	case "cancel":
		return g.Cancel(in.now, b(in.oid))
	case "reset":
		return g.ResetDay(in.now)
	default:
		return fmt.Errorf("unknown operation %q", in.kind)
	}
}

func status(err error) string {
	if err == nil {
		return "accepted"
	}
	return "rejected"
}

func reason(err error) string {
	if err == nil {
		return "state atomically updated"
	}
	return err.Error()
}

func b(value string) []byte {
	return []byte(value)
}

func getSnapshot(g *gate.Gateway, acct, group, sym string) snapshot {
	return snapshot{
		posLong:      g.Position(b(acct), b(sym), gate.Long),
		openLong:     g.PendingOpen(b(acct), b(sym), gate.Long),
		closeLong:    g.PendingClose(b(acct), b(sym), gate.Long),
		exposureLong: g.Exposure(b(acct), b(sym), gate.Long),
		groupLong:    g.GroupExposure(b(group), b(sym), gate.Long),
		dayOpen:      g.DayOpen(b(acct), b(sym)),
	}
}

func setupAccount(la, lg, day, hedge int64) func(*gate.Gateway) error {
	return func(g *gate.Gateway) error {
		ops := []op{
			{kind: "register", now: 1, acct: "A", group: "G"},
			{kind: "limit", now: 2, sym: "S", la: la, lg: lg, day: day},
			{kind: "hedge", now: 3, acct: "A", sym: "S", side: gate.Long, hedge: hedge},
		}
		return applyOps(g, ops)
	}
}

func setupFilledLong(qty int64) func(*gate.Gateway) error {
	return func(g *gate.Gateway) error {
		if err := setupAccount(1000, 1000, 1000, 0)(g); err != nil {
			return err
		}
		return applyOps(g, []op{
			{kind: "order", now: 10, oid: "seed", acct: "A", sym: "S", side: gate.Long, offset: gate.Open, qty: qty},
			{kind: "fill", now: 11, oid: "seed", qty: qty},
		})
	}
}

func setupTwo(g *gate.Gateway, la, lg, day int64) error {
	return applyOps(g, []op{
		{kind: "register", now: 1, acct: "A1", group: "G"},
		{kind: "register", now: 2, acct: "A2", group: "G"},
		{kind: "limit", now: 3, sym: "S", la: la, lg: lg, day: day},
	})
}

func TestGatewayTable(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(*gate.Gateway) error
		ops    []op
		verify func(*testing.T, *gate.Gateway)
	}{
		{
			name: "题目完整示例",
			setup: func(g *gate.Gateway) error {
				return applyOps(g, []op{
					{kind: "register", now: 1, acct: "A1", group: "G"},
					{kind: "register", now: 2, acct: "A2", group: "G"},
					{kind: "limit", now: 3, sym: "S", la: 100, lg: 150, day: 1000},
					{kind: "hedge", now: 4, acct: "A1", sym: "S", side: gate.Long, hedge: 50},
					{kind: "order", now: 5, oid: "a1-open", acct: "A1", sym: "S", side: gate.Long, offset: gate.Open, qty: 140},
					{kind: "fill", now: 6, oid: "a1-open", qty: 120},
					{kind: "order", now: 7, oid: "a2-open-60", acct: "A2", sym: "S", side: gate.Long, offset: gate.Open, qty: 60},
					{kind: "fill", now: 8, oid: "a2-open-60", qty: 50},
				})
			},
			ops: []op{
				{kind: "order", now: 9, oid: "a2-block", acct: "A2", sym: "S", side: gate.Long, offset: gate.Open, qty: 1, want: gate.ErrGroupLimit},
				{kind: "order", now: 10, oid: "a1-account", acct: "A1", sym: "S", side: gate.Long, offset: gate.Open, qty: 11, want: gate.ErrAccountLimit},
				{kind: "order", now: 11, oid: "a1-group", acct: "A1", sym: "S", side: gate.Long, offset: gate.Open, qty: 10, want: gate.ErrGroupLimit},
				{kind: "order", now: 12, oid: "a1-close-130", acct: "A1", sym: "S", side: gate.Long, offset: gate.Close, qty: 130, want: gate.ErrCloseOverLimit},
				{kind: "order", now: 13, oid: "a1-close-100", acct: "A1", sym: "S", side: gate.Long, offset: gate.Close, qty: 100},
				{kind: "fill", now: 14, oid: "a1-close-100", qty: 100},
				{kind: "order", now: 15, oid: "a1-close-21", acct: "A1", sym: "S", side: gate.Long, offset: gate.Close, qty: 21, want: gate.ErrCloseOverLimit},
				{kind: "hedge", now: 16, acct: "A1", sym: "S", side: gate.Long, hedge: 0},
				{kind: "limit", now: 17, sym: "S", la: 30, lg: 150, day: 1000},
				{kind: "order", now: 18, oid: "a2-passive", acct: "A2", sym: "S", side: gate.Long, offset: gate.Open, qty: 1, want: gate.ErrAccountLimit},
				{kind: "order", now: 19, oid: "a2-close", acct: "A2", sym: "S", side: gate.Long, offset: gate.Close, qty: 50},
			},
			verify: func(t *testing.T, g *gate.Gateway) {
				want := snapshot{posLong: 20, openLong: 20, exposureLong: 40, groupLong: 100, dayOpen: 140}
				if got := getSnapshot(g, "A1", "G", "S"); got != want {
					t.Fatalf("A1 snapshot=%+v want=%+v", got, want)
				}
			},
		},
		{
			name:  "三项限额取等与多一",
			setup: setupAccount(10, 20, 100, 0),
			ops: []op{
				{kind: "order", now: 10, oid: "eq-account", acct: "A", sym: "S", side: gate.Long, offset: gate.Open, qty: 10},
				{kind: "cancel", now: 11, oid: "eq-account"},
				{kind: "order", now: 12, oid: "over-account", acct: "A", sym: "S", side: gate.Long, offset: gate.Open, qty: 11, want: gate.ErrAccountLimit},
				{kind: "limit", now: 13, sym: "S", la: 20, lg: 20, day: 100},
				{kind: "order", now: 14, oid: "eq-group", acct: "A", sym: "S", side: gate.Long, offset: gate.Open, qty: 20},
				{kind: "cancel", now: 15, oid: "eq-group"},
				{kind: "limit", now: 16, sym: "S", la: 1000, lg: 1000, day: 100},
				{kind: "order", now: 17, oid: "eq-day", acct: "A", sym: "S", side: gate.Long, offset: gate.Open, qty: 100},
				{kind: "cancel", now: 18, oid: "eq-day"},
				{kind: "order", now: 19, oid: "over-day", acct: "A", sym: "S", side: gate.Long, offset: gate.Open, qty: 101, want: gate.ErrDayOpenLimit},
			},
		},
		{
			name:  "拒绝优先级账户组日",
			setup: setupAccount(1, 1, 1, 0),
			ops:   []op{{kind: "order", now: 10, oid: "x", acct: "A", sym: "S", side: gate.Long, offset: gate.Open, qty: 2, want: gate.ErrAccountLimit}},
		},
		{
			name: "组限额先于日内限额",
			setup: func(g *gate.Gateway) error {
				if err := setupTwo(g, 1000, 10, 10); err != nil {
					return err
				}
				return applyOps(g, []op{{kind: "order", now: 10, oid: "seed", acct: "A1", sym: "S", side: gate.Long, offset: gate.Open, qty: 8}})
			},
			ops: []op{{kind: "order", now: 11, oid: "group-before-day", acct: "A2", sym: "S", side: gate.Long, offset: gate.Open, qty: 3, want: gate.ErrGroupLimit}},
		},
		{
			name: "套保富余不可跨账户调剂",
			setup: func(g *gate.Gateway) error {
				if err := setupTwo(g, 1000, 100, 1000); err != nil {
					return err
				}
				return applyOps(g, []op{{kind: "hedge", now: 6, acct: "A1", sym: "S", side: gate.Long, hedge: 50}})
			},
			ops: []op{{kind: "order", now: 10, oid: "a2-101", acct: "A2", sym: "S", side: gate.Long, offset: gate.Open, qty: 101, want: gate.ErrGroupLimit}},
		},
		{
			name:  "在途平仓占用可平量但不减敞口",
			setup: setupFilledLong(60),
			ops: []op{
				{kind: "order", now: 20, oid: "close-40", acct: "A", sym: "S", side: gate.Long, offset: gate.Close, qty: 40},
				{kind: "order", now: 21, oid: "close-21", acct: "A", sym: "S", side: gate.Long, offset: gate.Close, qty: 21, want: gate.ErrCloseOverLimit},
				{kind: "cancel", now: 22, oid: "close-40"},
				{kind: "order", now: 23, oid: "close-61", acct: "A", sym: "S", side: gate.Long, offset: gate.Close, qty: 61, want: gate.ErrCloseOverLimit},
			},
			verify: func(t *testing.T, g *gate.Gateway) {
				if got := g.Exposure(b("A"), b("S"), gate.Long); got != 60 {
					t.Fatalf("exposure=%d want 60", got)
				}
			},
		},
		{
			name:  "部分成交撤单与日切",
			setup: setupAccount(10000, 10000, 1000, 0),
			ops: []op{
				{kind: "order", now: 10, oid: "o", acct: "A", sym: "S", side: gate.Long, offset: gate.Open, qty: 300},
				{kind: "fill", now: 11, oid: "o", qty: 100},
				{kind: "order", now: 12, oid: "c", acct: "A", sym: "S", side: gate.Long, offset: gate.Close, qty: 100},
				{kind: "cancel", now: 13, oid: "o"},
				{kind: "reset", now: 14},
				{kind: "order", now: 15, oid: "after-reset", acct: "A", sym: "S", side: gate.Long, offset: gate.Open, qty: 800},
				{kind: "order", now: 16, oid: "after-reset-over-limit", acct: "A", sym: "S", side: gate.Long, offset: gate.Open, qty: 201, want: gate.ErrDayOpenLimit},
				{kind: "cancel", now: 17, oid: "after-reset"},
				{kind: "order", now: 18, oid: "after-reset-ok", acct: "A", sym: "S", side: gate.Long, offset: gate.Open, qty: 800},
			},
			verify: func(t *testing.T, g *gate.Gateway) {
				if got := g.PendingOpen(b("A"), b("S"), gate.Long); got != 800 {
					t.Fatalf("pending=%d want 800", got)
				}
			},
		},
		{
			name:  "拒绝次序",
			setup: setupAccount(1000, 1000, 1000, 0),
			ops: []op{
				{kind: "order", now: 10, oid: "o", acct: "A", sym: "S", side: gate.Long, offset: gate.Open, qty: 1},
				{kind: "order", now: 9, oid: "clock", acct: "A", sym: "S", side: gate.Long, offset: gate.Open, qty: 1, want: gate.ErrClockRollback},
				{kind: "order", now: 11, oid: "o", acct: "A", sym: "S", side: gate.Long, offset: gate.Open, qty: 1, want: gate.ErrDuplicateOrder},
				{kind: "fill", now: 12, oid: "o", qty: 2, want: gate.ErrInvalidState},
				{kind: "fill", now: 13, oid: "missing", qty: 1, want: gate.ErrNotFound},
				{kind: "order", now: 14, oid: "bad", acct: "A", sym: "S", side: gate.Side(9), offset: gate.Open, qty: 1, want: gate.ErrInvalidArgument},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := gate.New()
			if tc.setup != nil {
				if err := tc.setup(g); err != nil {
					t.Fatalf("setup: %v", err)
				}
			}
			if err := runOps(t, g, tc.ops); err != nil {
				t.Fatal(err)
			}
			if tc.verify != nil {
				tc.verify(t, g)
			}
		})
	}
}
