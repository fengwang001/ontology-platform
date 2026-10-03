package session_test

import (
	"errors"
	"sync"
	"testing"

	"ontology/caps"
	"ontology/negotiate"
	"ontology/session"
)

func roleTable(t *testing.T, fill uint32, role func(f int) int) *caps.Table {
	t.Helper()
	var specs [caps.NumFeatures]caps.Spec
	for f := 0; f < caps.NumFeatures; f++ {
		specs[f] = caps.Spec{MinV: 1, MaxV: caps.Unbounded, Role: 2}
		if fill&(1<<uint(f)) != 0 {
			specs[f] = caps.Spec{MinV: 1, MaxV: caps.Unbounded, Role: role(f)}
		}
	}
	tab, err := caps.NewTable(specs)
	if err != nil {
		t.Fatal(err)
	}
	return tab
}

func TestRejectOrder_VersionBeforeMissing(t *testing.T) {
	tab := roleTable(t, bitSet(5), func(int) int { return 0 })
	m, _ := session.NewManager(tab)
	if e := m.SetServer(2, caps.Hello{Lo: 9, Hi: 10, Sup: 0}); e != nil {
		t.Fatal(e)
	}
	ch := caps.Hello{Lo: 1, Hi: 2, Sup: bitSet(5), Req: bitSet(5)}
	if _, _, e := m.Negotiate(ch, 0); !errors.Is(e, caps.ErrNoVersion) {
		t.Fatalf("got %v, want ErrNoVersion first", e)
	}
	t.Logf("disjoint ranges + missing sup -> ErrNoVersion (range checked before missing)")
}

func TestRejectOrder_MissingBeforeDeniedBeforeWindow(t *testing.T) {
	// f5: role 2（denied），f6: minV=9（window）；服务端 sup 不含 5、6。
	var specs [caps.NumFeatures]caps.Spec
	for i := range specs {
		specs[i] = caps.Spec{MinV: 1, MaxV: caps.Unbounded, Role: 2}
	}
	specs[5] = caps.Spec{MinV: 1, MaxV: caps.Unbounded, Role: 2}
	specs[6] = caps.Spec{MinV: 9, MaxV: caps.Unbounded, Role: 0}
	tab, _ := caps.NewTable(specs)
	m, _ := session.NewManager(tab)
	if e := m.SetServer(2, caps.Hello{Lo: 1, Hi: 20, Sup: bitSet(6)}); e != nil {
		t.Fatal(e)
	}
	ch := caps.Hello{Lo: 1, Hi: 20, Sup: bitSet(5, 6), Req: bitSet(5, 6)}
	_, _, e := m.Negotiate(ch, 0)
	if e == nil || e.Code != caps.CodeMissing || e.Feature != 5 {
		t.Fatalf("got %v, want ErrMissing(5) smallest", e)
	}
	t.Logf("missing 5 < denied 5 < window 6 -> ErrMissing(5)")

	// 服务端补上 5（role 2 > cr 0）→ ErrDenied(5) 先于 window(f6)。
	if e := m.SetServer(2, caps.Hello{Lo: 1, Hi: 20, Sup: bitSet(5, 6)}); e != nil {
		t.Fatal(e)
	}
	_, _, e = m.Negotiate(ch, 0)
	if e == nil || e.Code != caps.CodeDenied || e.Feature != 5 {
		t.Fatalf("got %v, want ErrDenied(5)", e)
	}
	t.Logf("f5 role2>0 and f6 window-fails -> ErrDenied(5) precedes ErrWindow")

	// cr=2 后 f5 可用；把 f5 从两端必需中去掉，只剩 f6 的窗口失败 → ErrWindow。
	ch2 := caps.Hello{Lo: 1, Hi: 5, Sup: bitSet(5, 6)}
	if e := m.SetServer(2, caps.Hello{Lo: 1, Hi: 5, Sup: bitSet(5, 6), Req: bitSet(6)}); e != nil {
		t.Fatal(e)
	}
	if _, _, e := m.Negotiate(ch2, 2); !errors.Is(e, caps.ErrWindow) {
		t.Fatalf("got %v, want ErrWindow", e)
	}
}

func TestUnionOfBothRequired(t *testing.T) {
	tab := roleTable(t, bitSet(7, 8), func(int) int { return 0 })
	m, _ := session.NewManager(tab)
	if e := m.SetServer(2, caps.Hello{Lo: 1, Hi: 10, Sup: bitSet(7, 8), Req: bitSet(7)}); e != nil {
		t.Fatal(e)
	}
	// 客户端必需 8，服务端 sup 含 8；Q={7,8}，均被两端 sup 交集覆盖。
	ch := caps.Hello{Lo: 1, Hi: 10, Sup: bitSet(7, 8), Req: bitSet(8)}
	_, res, e := m.Negotiate(ch, 0)
	if e != nil {
		t.Fatalf("union req: %v", e)
	}
	if res.Enabled&bitSet(7, 8) != bitSet(7, 8) {
		t.Fatalf("E=%b missing union-required", res.Enabled)
	}
	// 客户端必需 8 但服务端 sup 不含 8 → ErrMissing(8)。
	if e := m.SetServer(2, caps.Hello{Lo: 1, Hi: 10, Sup: bitSet(7), Req: bitSet(7)}); e != nil {
		t.Fatal(e)
	}
	if _, _, e := m.Negotiate(ch, 0); e == nil || e.Code != caps.CodeMissing || e.Feature != 8 {
		t.Fatalf("client-only req: %v, want ErrMissing(8)", e)
	}
	t.Logf("Q = client.Req | server.Req verified both directions")
}

func TestSetServerPermissionAndInvalid(t *testing.T) {
	tab := roleTable(t, 0, nil)
	m, _ := session.NewManager(tab)
	for _, r := range []int{0, 1, -1, 3} {
		if e := m.SetServer(r, caps.Hello{Lo: 1, Hi: 1}); e == nil {
			t.Fatalf("role %d accepted", r)
		}
	}
	bad := []caps.Hello{
		{Lo: 0, Hi: 1},
		{Lo: 2, Hi: 1},
		{Lo: 1, Hi: caps.MaxVersion + 1},
		{Lo: 1, Hi: 1, Sup: 0, Req: 1},
	}
	for _, h := range bad {
		if e := m.SetServer(2, h); e == nil || e.Code != caps.CodeInvalid {
			t.Fatalf("bad hello %+v accepted: %v", h, e)
		}
	}
	// 被拒后初始空声明仍在：任何与 [1,1] 不相交的协商失败且不推进 sid。
	if _, _, _, e := m.Info(1); !errors.Is(e, caps.ErrNoSession) {
		t.Fatalf("state leaked: %v", e)
	}
}

func TestInvalidArgumentsAndSidMonotonic(t *testing.T) {
	tab := roleTable(t, bitSet(0), func(int) int { return 0 })
	m, _ := session.NewManager(tab)
	if e := m.SetServer(2, caps.Hello{Lo: 1, Hi: 10, Sup: bitSet(0)}); e != nil {
		t.Fatal(e)
	}
	if _, _, e := m.Negotiate(caps.Hello{Lo: 5, Hi: 1}, 0); e == nil || e.Code != caps.CodeInvalid {
		t.Fatalf("bad client hello: %v", e)
	}
	if _, _, e := m.Negotiate(caps.Hello{Lo: 1, Hi: 10, Sup: bitSet(0)}, 3); e == nil || e.Code != caps.CodeInvalid {
		t.Fatalf("bad cr: %v", e)
	}
	if e := m.Use(999, 0); !errors.Is(e, caps.ErrNoSession) {
		t.Fatalf("missing session: %v", e)
	}
	if e := m.Use(1, 32); e == nil || e.Code != caps.CodeInvalid {
		t.Fatalf("bad feature: %v", e)
	}
	ch := caps.Hello{Lo: 1, Hi: 10, Sup: bitSet(0)}
	sid, _, e := m.Negotiate(ch, 0)
	if e != nil || sid != 1 {
		t.Fatalf("first session sid=%d: %v (rejections must not advance counter)", sid, e)
	}
	// 协商被拒不推进 sid。
	if e := m.SetServer(2, caps.Hello{Lo: 20, Hi: 30, Sup: bitSet(0)}); e != nil {
		t.Fatal(e)
	}
	if _, _, e := m.Negotiate(ch, 0); !errors.Is(e, caps.ErrNoVersion) {
		t.Fatalf("want reject: %v", e)
	}
	if e := m.SetServer(2, caps.Hello{Lo: 1, Hi: 10, Sup: bitSet(0)}); e != nil {
		t.Fatal(e)
	}
	sid2, _, _ := m.Negotiate(ch, 0)
	if sid2 != 2 {
		t.Fatalf("sid=%d, want 2 (continuous)", sid2)
	}
	// 关闭后 sid 不被复用。
	if e := m.Close(sid); e != nil {
		t.Fatal(e)
	}
	sid3, _, _ := m.Negotiate(ch, 0)
	if sid3 != 3 {
		t.Fatalf("sid=%d, want 3 (no reuse)", sid3)
	}
}

func TestUpperCappedByMaxVNotH(t *testing.T) {
	var specs [caps.NumFeatures]caps.Spec
	for i := range specs {
		specs[i] = caps.Spec{MinV: 1, MaxV: caps.Unbounded, Role: 2}
	}
	specs[0] = caps.Spec{MinV: 1, MaxV: 100, Role: 0}
	tab, _ := caps.NewTable(specs)
	ch := caps.Hello{Lo: 1, Hi: 1_000_000_000, Sup: bitSet(0), Req: bitSet(0)}
	for _, tc := range []struct{ hi, want int }{
		{1000, 99}, // v = maxV-1，受 maxV-1 限制而非 H
		{1_000_000_000, 99},
	} {
		res, e := negotiate.Negotiate(tab, ch, 0, caps.Hello{Lo: 1, Hi: tc.hi, Sup: bitSet(0)}, 0)
		if e != nil || res.Version != tc.want {
			t.Fatalf("H=%d got v=%d err=%v want %d", tc.hi, res.Version, e, tc.want)
		}
		t.Logf("H=%d -> v=%d, capped by maxV-1=99", tc.hi, res.Version)
	}
}

func TestInspectedCount_IndependentOfRange(t *testing.T) {
	tab := roleTable(t, 0xFFFFFFFF, func(int) int { return 0 })
	full := uint32(0xFFFFFFFF)
	chSmall := caps.Hello{Lo: 1, Hi: 10, Sup: full, Req: 0}
	chHuge := caps.Hello{Lo: 1, Hi: 1_000_000_000, Sup: full, Req: 0}
	srvSmall := caps.Hello{Lo: 1, Hi: 10, Sup: full}
	srvHuge := caps.Hello{Lo: 1, Hi: 1_000_000_000, Sup: full}
	r1, e := negotiate.Negotiate(tab, chSmall, 2, srvSmall, 0)
	if e != nil {
		t.Fatal(e)
	}
	r2, e := negotiate.Negotiate(tab, chHuge, 2, srvHuge, 0)
	if e != nil {
		t.Fatal(e)
	}
	if r1.Scanned != r2.Scanned {
		t.Fatalf("scanned small=%d huge=%d, must be equal", r1.Scanned, r2.Scanned)
	}
	if r1.Scanned > 4*caps.NumFeatures {
		t.Fatalf("scanned=%d exceeds 4*32 constant multiple", r1.Scanned)
	}
	t.Logf("range 10 vs 1e9: inspected features = %d both (<= 4*32)", r1.Scanned)
}

func TestConcurrentLinearizable(t *testing.T) {
	tab := roleTable(t, 0xFFFFFFFF, func(int) int { return 0 })
	m, _ := session.NewManager(tab)
	if e := m.SetServer(2, caps.Hello{Lo: 1, Hi: 100, Sup: 0xFFFFFFFF}); e != nil {
		t.Fatal(e)
	}
	ch := caps.Hello{Lo: 1, Hi: 100, Sup: 0xFFFFFFFF}
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for k := 0; k < 200; k++ {
				sid, res, e := m.Negotiate(ch, 0)
				if e != nil {
					t.Errorf("negotiate: %v", e)
					return
				}
				// 不变量：E 中每个特性在会话版本可用，U 恒含于 E。
				for f := 0; f < caps.NumFeatures; f++ {
					if res.Enabled&(1<<uint(f)) != 0 && !tab.AvailableAt(f, res.Version) {
						t.Errorf("f=%d enabled but unavailable at v=%d", f, res.Version)
					}
				}
				if e := m.Use(sid, seed%caps.NumFeatures); e != nil {
					t.Errorf("use: %v", e)
				}
				if _, en, u, e := m.Info(sid); e != nil || u&^en != 0 {
					t.Errorf("invariant U subset E: E=%b U=%b %v", en, u, e)
				}
				if _, e := m.Renegotiate(sid); e != nil {
					t.Errorf("reneg: %v", e)
				}
				if _, en, u, e := m.Info(sid); e != nil || u&^en != 0 {
					t.Errorf("post-reneg invariant: E=%b U=%b %v", en, u, e)
				}
				if e := m.Close(sid); e != nil {
					t.Errorf("close: %v", e)
				}
			}
		}(g)
	}
	wg.Wait()
}
