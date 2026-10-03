package session_test

import (
	"bytes"
	"testing"

	"ontology/caps"
	"ontology/session"
)

func exampleTable(t *testing.T) *caps.Table {
	t.Helper()
	inf := uint64(1_000_000_001)
	feats := make([]caps.Feature, caps.FeatureCount)
	for i := range feats {
		feats[i] = caps.Feature{MinV: 1, MaxV: inf, Role: 0}
	}
	feats[0] = caps.Feature{MinV: 1, MaxV: inf, Role: 0} // 0: (1, ∞, 0)
	feats[1] = caps.Feature{MinV: 2, MaxV: 5, Role: 0}   // 1: (2, 5, 0)
	feats[2] = caps.Feature{MinV: 3, MaxV: inf, Role: 1} // 2: (3, ∞, 1)
	feats[3] = caps.Feature{MinV: 4, MaxV: inf, Role: 0} // 3: (4, ∞, 0)
	tab, err := caps.NewTable(feats)
	if err != nil {
		t.Fatalf("NewTable: %v", err)
	}
	return tab
}

func buildFeatures(over map[uint8]caps.Feature) []caps.Feature {
	feats := make([]caps.Feature, caps.FeatureCount)
	inf := uint64(1_000_000_001)
	for i := range feats {
		feats[i] = caps.Feature{MinV: 1, MaxV: inf, Role: 0}
	}
	for i, f := range over {
		feats[i] = f
	}
	return feats
}

func mustReason(t *testing.T, err error, want caps.Reason, feat int) {
	t.Helper()
	e := caps.AsError(err)
	if e == nil {
		t.Fatalf("want error reason=%d feat=%d, got nil", want, feat)
	}
	if e.Reason != want || e.Feature != feat {
		t.Fatalf("want reason=%d feat=%d, got reason=%d feat=%d (%v)", want, feat, e.Reason, e.Feature, err)
	}
}

func newCapture() *bytes.Buffer {
	buf := &bytes.Buffer{}
	session.SetLogOutput(buf)
	return buf
}

// 例一：v=4、E={0,1,3}；Use(1) 成功、Use(2) ErrNotEnabled；
// 服务端升级后新会话 v=6 E={0,3}；旧会话 Renegotiate 被使用中的 1 钉在 v=4。
func TestExampleOne(t *testing.T) {
	newCapture()
	m := session.NewManager(exampleTable(t))
	if err := m.SetServer(2, caps.Hello{Lo: 2, Hi: 4, Sup: 0xf, Req: 0}); err != nil {
		t.Fatal(err)
	}
	ch := caps.Hello{Lo: 1, Hi: 6, Sup: 0xf, Req: 0}
	sid, info, err := m.Negotiate(ch, 0)
	if err != nil {
		t.Fatal(err)
	}
	if sid != 1 || info.Ver != 4 || info.Enabled != 0b1011 {
		t.Fatalf("got sid=%d v=%d E=%08b, want sid=1 v=4 E=1011", sid, info.Ver, info.Enabled)
	}
	if err := m.Use(sid, 1); err != nil {
		t.Fatalf("Use(1): %v", err)
	}
	mustReason(t, m.Use(sid, 2), caps.ReasonNotEnabled, 2)

	if err := m.SetServer(2, caps.Hello{Lo: 2, Hi: 8, Sup: 0xf, Req: 0}); err != nil {
		t.Fatal(err)
	}
	sid2, info2, err := m.Negotiate(ch, 0)
	if err != nil {
		t.Fatal(err)
	}
	if sid2 != 2 || info2.Ver != 6 || info2.Enabled != 0b1001 {
		t.Fatalf("new session got v=%d E=%08b, want v=6 E=1001", info2.Ver, info2.Enabled)
	}

	info3, err := m.Renegotiate(sid)
	if err != nil {
		t.Fatalf("Renegotiate: %v", err)
	}
	if info3.Ver != 4 || info3.Enabled != 0b1011 || info3.Using != 0b0010 {
		t.Fatalf("pinned session got v=%d E=%08b U=%08b, want v=4 E=1011 U=0010",
			info3.Ver, info3.Enabled, info3.Using)
	}
}

// 未调用 Use 的会话在服务端升级后 Renegotiate：v=6、E={0,3}。
func TestRenegotiateWithoutUse(t *testing.T) {
	newCapture()
	m := session.NewManager(exampleTable(t))
	if err := m.SetServer(2, caps.Hello{Lo: 2, Hi: 4, Sup: 0xf, Req: 0}); err != nil {
		t.Fatal(err)
	}
	sid, _, err := m.Negotiate(caps.Hello{Lo: 1, Hi: 6, Sup: 0xf}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SetServer(2, caps.Hello{Lo: 2, Hi: 8, Sup: 0xf}); err != nil {
		t.Fatal(err)
	}
	info, err := m.Renegotiate(sid)
	if err != nil {
		t.Fatal(err)
	}
	if info.Ver != 6 || info.Enabled != 0b1001 {
		t.Fatalf("got v=%d E=%08b, want v=6 E=1001", info.Ver, info.Enabled)
	}
}

// 例二：req={2}、cr=0 → ErrDenied(2)；cr=1、H=2 → ErrWindow；H=9 → v=9 E 含 2 不含 1。
func TestExampleTwo(t *testing.T) {
	newCapture()
	m := session.NewManager(exampleTable(t))
	if err := m.SetServer(2, caps.Hello{Lo: 2, Hi: 9, Sup: 0xf, Req: 0}); err != nil {
		t.Fatal(err)
	}
	ch := caps.Hello{Lo: 1, Hi: 9, Sup: 0xf, Req: 0b0100}

	_, _, err := m.Negotiate(ch, 0)
	mustReason(t, err, caps.ReasonDenied, 2)

	if err := m.SetServer(2, caps.Hello{Lo: 2, Hi: 2, Sup: 0xf}); err != nil {
		t.Fatal(err)
	}
	ch.Hi = 2
	_, _, err = m.Negotiate(ch, 1)
	mustReason(t, err, caps.ReasonWindow, -1)

	if err := m.SetServer(2, caps.Hello{Lo: 2, Hi: 9, Sup: 0xf}); err != nil {
		t.Fatal(err)
	}
	ch.Hi = 9
	_, info, err := m.Negotiate(ch, 1)
	if err != nil {
		t.Fatal(err)
	}
	if info.Ver != 9 || info.Enabled&0b0100 == 0 || info.Enabled&0b0010 != 0 {
		t.Fatalf("got v=%d E=%08b, want v=9 with bit2 and without bit1", info.Ver, info.Enabled)
	}
}

// 例三：钉住会话在服务端区间移到 7..9 后 Renegotiate 报 ErrNoVersion 且原状不变；Close 后任何操作报 ErrNoSession。
func TestExampleThree(t *testing.T) {
	newCapture()
	m := session.NewManager(exampleTable(t))
	if err := m.SetServer(2, caps.Hello{Lo: 2, Hi: 4, Sup: 0xf}); err != nil {
		t.Fatal(err)
	}
	sid, _, err := m.Negotiate(caps.Hello{Lo: 1, Hi: 6, Sup: 0xf}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Use(sid, 1); err != nil {
		t.Fatal(err)
	}
	if err := m.SetServer(2, caps.Hello{Lo: 7, Hi: 9, Sup: 0xf}); err != nil {
		t.Fatal(err)
	}
	_, err = m.Renegotiate(sid)
	mustReason(t, err, caps.ReasonNoVersion, -1)
	got, err := m.Info(sid)
	if err != nil {
		t.Fatal(err)
	}
	if got.Ver != 4 || got.Enabled != 0b1011 || got.Using != 0b0010 {
		t.Fatalf("session mutated after failed renegotiate: %+v", got)
	}
	if err := m.Close(sid); err != nil {
		t.Fatal(err)
	}
	mustReason(t, m.Use(sid, 0), caps.ReasonNoSession, -1)
	mustReason(t, m.Close(sid), caps.ReasonNoSession, -1)
	_, errInfo := m.Info(sid)
	mustReason(t, errInfo, caps.ReasonNoSession, -1)
	_, err = m.Renegotiate(sid)
	mustReason(t, err, caps.ReasonNoSession, -1)
}

// 例四：Q={1,3} 只在 v=4 成功；L=5 → ErrWindow；特性 3 缺失 → ErrMissing(3)。
func TestExampleFour(t *testing.T) {
	newCapture()
	m := session.NewManager(exampleTable(t))
	if err := m.SetServer(2, caps.Hello{Lo: 2, Hi: 9, Sup: 0xf, Req: 0b1000}); err != nil {
		t.Fatal(err)
	}
	_, info, err := m.Negotiate(caps.Hello{Lo: 1, Hi: 9, Sup: 0xf, Req: 0b0010}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if info.Ver != 4 || info.Enabled != 0b1011 {
		t.Fatalf("got v=%d E=%08b, want v=4 E=1011", info.Ver, info.Enabled)
	}

	if err := m.SetServer(2, caps.Hello{Lo: 5, Hi: 9, Sup: 0xf, Req: 0b1000}); err != nil {
		t.Fatal(err)
	}
	_, _, err = m.Negotiate(caps.Hello{Lo: 5, Hi: 9, Sup: 0xf, Req: 0b0010}, 0)
	mustReason(t, err, caps.ReasonWindow, -1)

	if err := m.SetServer(2, caps.Hello{Lo: 1, Hi: 3, Sup: 0b0111, Req: 0b0010}); err != nil {
		t.Fatal(err)
	}
	_, _, err = m.Negotiate(caps.Hello{Lo: 1, Hi: 3, Sup: 0xf, Req: 0b1000}, 0)
	mustReason(t, err, caps.ReasonMissing, 3)
}
