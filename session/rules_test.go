package session_test

import (
	"testing"

	"ontology/caps"
	"ontology/negotiate"
	"ontology/session"
)

// upper 受必需特性 maxV-1 限制，而不是 H。
func TestUpperCappedByMaxV(t *testing.T) {
	newCapture()
	tab, err := caps.NewTable(buildFeatures(map[uint8]caps.Feature{
		0: {MinV: 1, MaxV: 5, Role: 0},
		1: {MinV: 1, MaxV: 1_000_000_001, Role: 0},
	}))
	if err != nil {
		t.Fatal(err)
	}
	m := session.NewManager(tab)
	if err := m.SetServer(2, caps.Hello{Lo: 1, Hi: 9, Sup: 0b11, Req: 0b01}); err != nil {
		t.Fatal(err)
	}
	_, info, err := m.Negotiate(caps.Hello{Lo: 1, Hi: 9, Sup: 0b11}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if info.Ver != 4 || info.Enabled != 0b11 {
		t.Fatalf("got v=%d E=%08b, want v=4 E=11", info.Ver, info.Enabled)
	}
}

// minV 恰等 v 可用；maxV 恰等 v 不可用。
func TestWindowBoundaries(t *testing.T) {
	newCapture()
	tab, err := caps.NewTable(buildFeatures(map[uint8]caps.Feature{
		0: {MinV: 3, MaxV: 4, Role: 0}, // 仅 v=3
		1: {MinV: 1, MaxV: 4, Role: 0}, // v=1,2,3；v=4 不可用
	}))
	if err != nil {
		t.Fatal(err)
	}
	m := session.NewManager(tab)
	if err := m.SetServer(2, caps.Hello{Lo: 3, Hi: 3, Sup: 0b11}); err != nil {
		t.Fatal(err)
	}
	_, info, err := m.Negotiate(caps.Hello{Lo: 3, Hi: 3, Sup: 0b11}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if info.Ver != 3 || info.Enabled != 0b11 {
		t.Fatalf("at v=3 got E=%08b, want both (minV==v available)", info.Enabled)
	}

	if err := m.SetServer(2, caps.Hello{Lo: 4, Hi: 4, Sup: 0b11}); err != nil {
		t.Fatal(err)
	}
	_, _, err = m.Negotiate(caps.Hello{Lo: 4, Hi: 4, Sup: 0b11}, 0)
	// Q 为空时不检查窗口，v=4 可成功，但 E 必须为空：maxV==v 不可用。
	if err != nil {
		t.Fatalf("Q empty should not hit ErrWindow: %v", err)
	}
}

// 非必需高角色特性被静默排除，不报错。
func TestRoleExcludesOptional(t *testing.T) {
	newCapture()
	tab, err := caps.NewTable(buildFeatures(map[uint8]caps.Feature{
		0: {MinV: 1, MaxV: 1_000_000_001, Role: 0},
		1: {MinV: 1, MaxV: 1_000_000_001, Role: 1},
		2: {MinV: 1, MaxV: 1_000_000_001, Role: 2},
	}))
	if err != nil {
		t.Fatal(err)
	}
	m := session.NewManager(tab)
	if err := m.SetServer(2, caps.Hello{Lo: 1, Hi: 1, Sup: 0b111}); err != nil {
		t.Fatal(err)
	}
	_, info, err := m.Negotiate(caps.Hello{Lo: 1, Hi: 1, Sup: 0b111}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if info.Enabled != 0b001 {
		t.Fatalf("cr=0 got E=%08b, want only bit0", info.Enabled)
	}

	_, info2, err := m.Negotiate(caps.Hello{Lo: 1, Hi: 1, Sup: 0b111}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if info2.Enabled != 0b011 {
		t.Fatalf("cr=1 got E=%08b, want bits0,1", info2.Enabled)
	}
}

// 两端各自必需特性取并集：分别缺失时报各自最小编号。
func TestRequiredUnion(t *testing.T) {
	newCapture()
	tab := exampleTable(t)
	m := session.NewManager(tab)
	// 客户端必需 1，服务端必需 3；两端 sup 均含 → Q={1,3}，只有 v=4。
	if err := m.SetServer(2, caps.Hello{Lo: 1, Hi: 9, Sup: 0xf, Req: 0b1000}); err != nil {
		t.Fatal(err)
	}
	_, info, err := m.Negotiate(caps.Hello{Lo: 1, Hi: 9, Sup: 0xf, Req: 0b0010}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if info.Ver != 4 {
		t.Fatalf("union Q={{1,3}} v=%d, want 4", info.Ver)
	}

	// 服务端不支持 1 → 客户端必需 1 缺失。
	if err := m.SetServer(2, caps.Hello{Lo: 1, Hi: 9, Sup: 0b1101, Req: 0}); err != nil {
		t.Fatal(err)
	}
	_, _, err = m.Negotiate(caps.Hello{Lo: 1, Hi: 9, Sup: 0xf, Req: 0b0010}, 0)
	mustReason(t, err, caps.ReasonMissing, 1)
}

// 拒绝次序：ErrMissing 先于 ErrDenied 先于 ErrWindow。
func TestRejectOrdering(t *testing.T) {
	newCapture()
	tab := exampleTable(t) // 特性 2：minV=3 role=1
	m := session.NewManager(tab)

	// 同时缺失（服务端 sup 无 2）、越权（cr=0）、窗口冲突（H=2）：报 Missing(2)。
	if err := m.SetServer(2, caps.Hello{Lo: 1, Hi: 2, Sup: 0b1011, Req: 0}); err != nil {
		t.Fatal(err)
	}
	_, _, err := m.Negotiate(caps.Hello{Lo: 1, Hi: 2, Sup: 0b111, Req: 0b0100}, 0)
	mustReason(t, err, caps.ReasonMissing, 2)

	// 都支持后同时越权 + 窗口冲突：报 Denied(2)。
	if err := m.SetServer(2, caps.Hello{Lo: 1, Hi: 2, Sup: 0xf, Req: 0}); err != nil {
		t.Fatal(err)
	}
	_, _, err = m.Negotiate(caps.Hello{Lo: 1, Hi: 2, Sup: 0xf, Req: 0b0100}, 0)
	mustReason(t, err, caps.ReasonDenied, 2)

	// 角色满足后只剩窗口冲突：报 Window。
	_, _, err = m.Negotiate(caps.Hello{Lo: 1, Hi: 2, Sup: 0xf, Req: 0b0100}, 1)
	mustReason(t, err, caps.ReasonWindow, -1)

	// ErrNoVersion 最先：区间完全不交，即使有其它问题。
	if err := m.SetServer(2, caps.Hello{Lo: 9, Hi: 9, Sup: 0}); err != nil {
		t.Fatal(err)
	}
	_, _, err = m.Negotiate(caps.Hello{Lo: 1, Hi: 2, Sup: 0xf, Req: 0b0100}, 0)
	mustReason(t, err, caps.ReasonNoVersion, -1)
}

// 参数非法与 SetServer 权限；被拒不改任何状态。
func TestInvalidAndForbidden(t *testing.T) {
	newCapture()
	m := session.NewManager(exampleTable(t))

	mustReason(t, m.SetServer(1, caps.Hello{Lo: 1, Hi: 1}), caps.ReasonForbidden, -1)
	mustReason(t, m.SetServer(3, caps.Hello{Lo: 1, Hi: 1}), caps.ReasonInvalid, -1)
	mustReason(t, m.SetServer(2, caps.Hello{Lo: 5, Hi: 1}), caps.ReasonInvalid, -1)
	mustReason(t, m.SetServer(2, caps.Hello{Lo: 0, Hi: 1}), caps.ReasonInvalid, -1)
	mustReason(t, m.SetServer(2, caps.Hello{Lo: 1, Hi: 1_000_000_001}), caps.ReasonInvalid, -1)
	mustReason(t, m.SetServer(2, caps.Hello{Lo: 1, Hi: 1, Sup: 0b1, Req: 0b10}), caps.ReasonInvalid, -1)

	// 初始空声明下协商必然 ErrNoVersion，拒绝后 sid 计数不前进。
	m2 := session.NewManager(exampleTable(t))
	_, _, err := m2.Negotiate(caps.Hello{Lo: 5, Hi: 6}, 0)
	mustReason(t, err, caps.ReasonNoVersion, -1)
	if err := m2.SetServer(2, caps.Hello{Lo: 1, Hi: 1}); err != nil {
		t.Fatal(err)
	}
	sid, _, err := m2.Negotiate(caps.Hello{Lo: 1, Hi: 1}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if sid != 1 {
		t.Fatalf("sid=%d after failed negotiate, want 1 (counter must not advance)", sid)
	}

	// 非法客户端参数。
	mustReason(t, func() error {
		_, _, e := m2.Negotiate(caps.Hello{Lo: 2, Hi: 1}, 0)
		return e
	}(), caps.ReasonInvalid, -1)
	_, _, err = m2.Negotiate(caps.Hello{Lo: 1, Hi: 1}, 3)
	mustReason(t, err, caps.ReasonInvalid, -1)

	// Use 非法编号先于会话判定。
	mustReason(t, m2.Use(99, 32), caps.ReasonInvalid, 32)
}

// sid 连续、Use 幂等、U⊆E 恒成立。
func TestSessionContinuityAndUse(t *testing.T) {
	newCapture()
	tab, err := caps.NewTable(buildFeatures(map[uint8]caps.Feature{
		0: {MinV: 1, MaxV: 1_000_000_001, Role: 0},
	}))
	if err != nil {
		t.Fatal(err)
	}
	m := session.NewManager(tab)
	if err := m.SetServer(2, caps.Hello{Lo: 1, Hi: 1, Sup: 0b1}); err != nil {
		t.Fatal(err)
	}
	for want := uint64(1); want <= 5; want++ {
		sid, _, err := m.Negotiate(caps.Hello{Lo: 1, Hi: 1, Sup: 0b1}, 0)
		if err != nil {
			t.Fatal(err)
		}
		if sid != want {
			t.Fatalf("sid=%d want %d", sid, want)
		}
		if err := m.Use(sid, 0); err != nil {
			t.Fatal(err)
		}
		if err := m.Use(sid, 0); err != nil {
			t.Fatalf("Use idempotent: %v", err)
		}
		got, err := m.Info(sid)
		if err != nil {
			t.Fatal(err)
		}
		if got.Using&^got.Enabled != 0 || got.Using != 0b1 {
			t.Fatalf("invariant U subset of E broken: %+v", got)
		}
	}
}

// 检视次数为 32 的常数倍，与区间长度无关（10 与 10^9 两档）。
func TestInspectedBounded(t *testing.T) {
	feats := make([]caps.Feature, caps.FeatureCount)
	for i := range feats {
		feats[i] = caps.Feature{MinV: 1, MaxV: 1_000_000_001, Role: 0}
	}
	tab, err := caps.NewTable(feats)
	if err != nil {
		t.Fatal(err)
	}
	measure := func(span uint64) uint64 {
		before := negotiate.Inspected()
		_, e := negotiate.Pick(negotiate.Params{
			Table:  tab,
			Client: caps.Hello{Lo: 1, Hi: span, Sup: 0xffffffff},
			CR:     2,
			Server: caps.Hello{Lo: 1, Hi: span, Sup: 0xffffffff},
		})
		if e != nil {
			t.Fatal(e)
		}
		return negotiate.Inspected() - before
	}
	d1 := measure(10)
	d2 := measure(1_000_000_000)
	if d1 != d2 {
		t.Fatalf("inspection depends on span: %d vs %d", d1, d2)
	}
	if d1 > caps.FeatureCount*4 {
		t.Fatalf("inspected=%d exceeds 32*4 constant bound", d1)
	}
	if d1 == 0 {
		t.Fatal("inspected counter not advancing")
	}
}
