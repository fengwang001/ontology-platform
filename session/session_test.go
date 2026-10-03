package session_test

import (
	"errors"
	"sync"
	"testing"

	"ontology/caps"
	"ontology/negotiate"
	"ontology/session"
)

// 题面特性表：0:(1,inf,0) 1:(2,5,0) 2:(3,inf,1) 3:(4,inf,0)
func exampleTable(t *testing.T) *caps.Table {
	t.Helper()
	var specs [caps.NumFeatures]caps.Spec
	specs[0] = caps.Spec{MinV: 1, MaxV: caps.Unbounded, Role: 0}
	specs[1] = caps.Spec{MinV: 2, MaxV: 5, Role: 0}
	specs[2] = caps.Spec{MinV: 3, MaxV: caps.Unbounded, Role: 1}
	specs[3] = caps.Spec{MinV: 4, MaxV: caps.Unbounded, Role: 0}
	for i := 4; i < caps.NumFeatures; i++ {
		specs[i] = caps.Spec{MinV: 1, MaxV: caps.Unbounded, Role: 2}
	}
	tab, err := caps.NewTable(specs)
	if err != nil {
		t.Fatalf("NewTable: %v", err)
	}
	return tab
}

func bitSet(fs ...int) uint32 {
	var b uint32
	for _, f := range fs {
		b |= 1 << uint(f)
	}
	return b
}

func TestExample1_BasicNegotiation(t *testing.T) {
	m, _ := session.NewManager(exampleTable(t))
	if e := m.SetServer(2, caps.Hello{Lo: 2, Hi: 4, Sup: bitSet(0, 1, 2, 3)}); e != nil {
		t.Fatal(e)
	}
	ch := caps.Hello{Lo: 1, Hi: 6, Sup: bitSet(0, 1, 2, 3)}
	sid, res, e := m.Negotiate(ch, 0)
	if e != nil {
		t.Fatal(e)
	}
	if sid != 1 || res.Version != 4 || res.Enabled != bitSet(0, 1, 3) {
		t.Fatalf("got sid=%d v=%d E=%032b", sid, res.Version, res.Enabled)
	}
	t.Logf("input: {lo1 hi6 sup=0123} cr=0 server[2,4] -> sid=1 v=4 E={0,1,3}; reason upper=min(6,4)=4, f2 role1>0 excluded")

	if e := m.Use(sid, 1); e != nil {
		t.Fatalf("Use(1): %v", e)
	}
	if e := m.Use(sid, 2); !errors.Is(e, caps.ErrNotEnabled) || e.Feature != 2 {
		t.Fatalf("Use(2) = %v, want ErrNotEnabled(2)", e)
	}
	v, en, u, e := m.Info(sid)
	if e != nil || v != 4 || en != bitSet(0, 1, 3) || u != bitSet(1) {
		t.Fatalf("Info = (%d,%b,%b,%v)", v, en, u, e)
	}
}

func TestExample1_UpgradeAndPin(t *testing.T) {
	tab := exampleTable(t)
	ch := caps.Hello{Lo: 1, Hi: 6, Sup: bitSet(0, 1, 2, 3)}

	m, _ := session.NewManager(tab)
	if e := m.SetServer(2, caps.Hello{Lo: 2, Hi: 4, Sup: bitSet(0, 1, 2, 3)}); e != nil {
		t.Fatal(e)
	}
	sid, _, e := m.Negotiate(ch, 0)
	if e != nil {
		t.Fatal(e)
	}
	if e := m.Use(sid, 1); e != nil {
		t.Fatal(e)
	}
	if e := m.SetServer(2, caps.Hello{Lo: 2, Hi: 8, Sup: bitSet(0, 1, 2, 3)}); e != nil {
		t.Fatal(e)
	}
	res, e := m.Renegotiate(sid)
	if e != nil {
		t.Fatal(e)
	}
	if res.Version != 4 || res.Enabled != bitSet(0, 1, 3) {
		t.Fatalf("pinned reneg got v=%d E=%b", res.Version, res.Enabled)
	}
	t.Logf("reneg with U={1}: Q={1} upper=min(6,5-1)=4 -> v pinned 4, E unchanged")

	sid2, _, e := m.Negotiate(ch, 0)
	if e != nil {
		t.Fatal(e)
	}
	res2, e := m.Renegotiate(sid2)
	if e != nil {
		t.Fatal(e)
	}
	if res2.Version != 6 || res2.Enabled != bitSet(0, 3) {
		t.Fatalf("fresh reneg got v=%d E=%b, want 6/{0,3}", res2.Version, res2.Enabled)
	}

	sid3, res3, e := m.Negotiate(ch, 0)
	if e != nil {
		t.Fatal(e)
	}
	if sid3 != 3 || res3.Version != 6 {
		t.Fatalf("new session got sid=%d v=%d", sid3, res3.Version)
	}
}

func TestExample2_DeniedAndWindow(t *testing.T) {
	m, _ := session.NewManager(exampleTable(t))
	ch := caps.Hello{Lo: 1, Hi: 6, Sup: bitSet(0, 1, 2, 3), Req: bitSet(2)}

	if e := m.SetServer(2, caps.Hello{Lo: 2, Hi: 4, Sup: bitSet(0, 1, 2, 3)}); e != nil {
		t.Fatal(e)
	}
	if _, _, e := m.Negotiate(ch, 0); e == nil || e.Code != caps.CodeDenied || e.Feature != 2 {
		t.Fatalf("cr=0 req=2: %v, want ErrDenied(2)", e)
	}
	t.Logf("input req={2} cr=0 -> ErrDenied(2); reason f2.role=1 > 0")

	if e := m.SetServer(2, caps.Hello{Lo: 2, Hi: 2, Sup: bitSet(0, 1, 2, 3)}); e != nil {
		t.Fatal(e)
	}
	if _, _, e := m.Negotiate(ch, 1); !errors.Is(e, caps.ErrWindow) {
		t.Fatalf("cr=1 H=2: %v, want ErrWindow", e)
	}
	t.Logf("input req={2} cr=1 H=2 -> ErrWindow; reason lower=max(L,3)=3 > upper=2")

	if e := m.SetServer(2, caps.Hello{Lo: 2, Hi: 9, Sup: bitSet(0, 1, 2, 3)}); e != nil {
		t.Fatal(e)
	}
	ch9 := caps.Hello{Lo: 1, Hi: 9, Sup: bitSet(0, 1, 2, 3), Req: bitSet(2)}
	_, res, e := m.Negotiate(ch9, 1)
	if e != nil {
		t.Fatal(e)
	}
	if res.Version != 9 || res.Enabled != bitSet(0, 2, 3) {
		t.Fatalf("cr=1 H=9 got v=%d E=%b, want 9/{0,2,3}", res.Version, res.Enabled)
	}
}

func TestExample3_FailureKeepsState(t *testing.T) {
	m, _ := session.NewManager(exampleTable(t))
	if e := m.SetServer(2, caps.Hello{Lo: 2, Hi: 4, Sup: bitSet(0, 1, 2, 3)}); e != nil {
		t.Fatal(e)
	}
	ch := caps.Hello{Lo: 1, Hi: 6, Sup: bitSet(0, 1, 2, 3)}
	sid, _, e := m.Negotiate(ch, 0)
	if e != nil {
		t.Fatal(e)
	}
	if e := m.Use(sid, 1); e != nil {
		t.Fatal(e)
	}
	if e := m.SetServer(2, caps.Hello{Lo: 7, Hi: 9, Sup: bitSet(0, 1, 2, 3)}); e != nil {
		t.Fatal(e)
	}
	if _, e := m.Renegotiate(sid); !errors.Is(e, caps.ErrNoVersion) {
		t.Fatalf("reneg: %v, want ErrNoVersion", e)
	}
	v, en, u, e := m.Info(sid)
	if e != nil || v != 4 || en != bitSet(0, 1, 3) || u != bitSet(1) {
		t.Fatalf("state changed after failed reneg: v=%d E=%b U=%b %v", v, en, u, e)
	}
	t.Logf("reneg server lo=7 -> ErrNoVersion (L=7>H=6); state kept v=4 E={0,1,3} U={1}")

	if e := m.Close(sid); e != nil {
		t.Fatal(e)
	}
	if e := m.Use(sid, 1); !errors.Is(e, caps.ErrNoSession) {
		t.Fatalf("Use after Close: %v", e)
	}
	if _, _, _, e := m.Info(sid); !errors.Is(e, caps.ErrNoSession) {
		t.Fatalf("Info after Close: %v", e)
	}
	if _, e := m.Renegotiate(sid); !errors.Is(e, caps.ErrNoSession) {
		t.Fatalf("Reneg after Close: %v", e)
	}
	if e := m.Close(sid); !errors.Is(e, caps.ErrNoSession) {
		t.Fatalf("double Close: %v", e)
	}
}

func TestExample4_SingleVersionWindow(t *testing.T) {
	tab := exampleTable(t)
	ch := caps.Hello{Lo: 1, Hi: 6, Sup: bitSet(0, 1, 2, 3), Req: bitSet(1, 3)}
	for _, tc := range []struct {
		name       string
		lo, hi     int
		ok         bool
		expectCode caps.Code
	}{
		{"H=3 window", 1, 3, false, caps.CodeWindow},
		{"L=5 still window", 5, 6, false, caps.CodeWindow},
		{"L=7 no version", 7, 9, false, caps.CodeNoVersion},
		{"exact v=4", 1, 4, true, 0},
		{"wide still v=4", 2, 9, true, 0},
	} {
		m, _ := session.NewManager(tab)
		if e := m.SetServer(2, caps.Hello{Lo: tc.lo, Hi: tc.hi, Sup: bitSet(0, 1, 2, 3)}); e != nil {
			t.Fatal(e)
		}
		_, res, e := m.Negotiate(ch, 0)
		if tc.ok {
			if e != nil || res.Version != 4 {
				t.Fatalf("%s: got v=%d err=%v want 4", tc.name, res.Version, e)
			}
			if res.Enabled&bitSet(1, 3) != bitSet(1, 3) {
				t.Fatalf("%s: E=%b missing req", tc.name, res.Enabled)
			}
		} else {
			if e == nil || e.Code != tc.expectCode {
				t.Fatalf("%s: %v want code %d", tc.name, e, tc.expectCode)
			}
		}
		t.Logf("input req={{1,3}} server[%d,%d] -> v=%d err=%v; lower=max(L,2,4)=4 upper=min(H,4)=4", tc.lo, tc.hi, res.Version, e)
	}
}

func TestBoundary_MinVInclusiveMaxVExclusive(t *testing.T) {
	var specs [caps.NumFeatures]caps.Spec
	specs[0] = caps.Spec{MinV: 3, MaxV: 5, Role: 0}
	for i := 1; i < caps.NumFeatures; i++ {
		specs[i] = caps.Spec{MinV: 1, MaxV: caps.Unbounded, Role: 2}
	}
	tab, _ := caps.NewTable(specs)
	ch := caps.Hello{Lo: 1, Hi: 10, Sup: bitSet(0), Req: bitSet(0)}
	for _, tc := range []struct {
		hi      int
		v       int
		en      bool
		errCode caps.Code
	}{
		{2, 0, false, caps.CodeWindow},
		{3, 3, true, 0},
		{4, 4, true, 0},
		{5, 4, true, 0},
	} {
		m, _ := session.NewManager(tab)
		if e := m.SetServer(2, caps.Hello{Lo: 1, Hi: tc.hi, Sup: bitSet(0)}); e != nil {
			t.Fatal(e)
		}
		_, res, e := m.Negotiate(ch, 0)
		if tc.errCode != 0 {
			if e == nil || e.Code != tc.errCode {
				t.Fatalf("H=%d: %v want %d", tc.hi, e, tc.errCode)
			}
			continue
		}
		if e != nil || res.Version != tc.v {
			t.Fatalf("H=%d: v=%d err=%v want %d", tc.hi, res.Version, e, tc.v)
		}
		if got := res.Enabled&bitSet(0) != 0; got != tc.en {
			t.Fatalf("H=%d enabled=%v want %v", tc.hi, got, tc.en)
		}
		t.Logf("boundary H=%d -> v=%d f0 enabled (minV<=v<maxV)", tc.hi, res.Version)
	}
}

var _ = negotiate.Result{}
var _ sync.Locker
