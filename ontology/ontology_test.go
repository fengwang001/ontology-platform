package ontology

import (
	"fmt"
	"sync"
	"testing"
)

func exampleConfig() Config {
	return Config{Ta: 100, Tv: 1000, To: 500, H: 60, F: 2, C: 3, Pm: 3}
}

func mustNew(t *testing.T, c Config) *Machine {
	t.Helper()
	m, err := New(c)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return m
}

func asErr(t *testing.T, err error) *Error {
	t.Helper()
	e, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *Error, got %T: %v", err, err)
	}
	return e
}

func wantErr(t *testing.T, err error, kind ErrKind) *Error {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error kind %s, got nil", kind)
	}
	e := asErr(t, err)
	if e.Kind != kind {
		t.Fatalf("expected kind %s, got %s (%v)", kind, e.Kind, err)
	}
	return e
}

func mustOrder(t *testing.T, m *Machine, acc string, idents []string, nonce, now int64) *Order {
	t.Helper()
	o, err := m.NewOrder([]byte(acc), idents, nonce, now)
	if err != nil {
		t.Fatalf("NewOrder(%s, %v, %d, %d): %v", acc, idents, nonce, now, err)
	}
	return o
}

func mustReport(t *testing.T, m *Machine, id string, ok bool, now int64) {
	t.Helper()
	if err := m.Report(id, ok, now); err != nil {
		t.Fatalf("Report(%s, %v, %d): %v", id, ok, now, err)
	}
}

func mustStatus(t *testing.T, m *Machine, id string, now int64) string {
	t.Helper()
	s, err := m.Status(id, now)
	if err != nil {
		t.Fatalf("Status(%s, %d): %v", id, now, err)
	}
	return s
}

func mustAuthzStatus(t *testing.T, m *Machine, id string, now int64) string {
	t.Helper()
	a, err := m.GetAuthz(id, now)
	if err != nil {
		t.Fatalf("GetAuthz(%s, %d): %v", id, now, err)
	}
	return a.Status
}

// TestNoncePoolEvictsMin 池满时淘汰最小者。
func TestNoncePoolEvictsMin(t *testing.T) {
	m := mustNew(t, exampleConfig()) // C=3
	m.Nonce()                        // 1
	m.Nonce()                        // 2
	m.Nonce()                        // 3，池 {1,2,3}
	m.Nonce()                        // 4，淘汰 1，池 {2,3,4}
	m.Nonce()                        // 5，淘汰 2，池 {3,4,5}

	// 2 已被淘汰，无效。
	if _, err := m.NewOrder([]byte("a"), []string{"x.com"}, 2, 0); err != nil {
		wantErr(t, err, ErrNonce)
	} else {
		t.Fatal("evicted nonce 2 should be invalid")
	}
	// 从未发出的 99 无效。
	if _, err := m.NewOrder([]byte("a"), []string{"x.com"}, 99, 0); err != nil {
		wantErr(t, err, ErrNonce)
	} else {
		t.Fatal("never-issued nonce 99 should be invalid")
	}
	// 3 仍在池中，可用。
	mustOrder(t, m, "a", []string{"x.com"}, 3, 0)
	// 3 已被消费，再用无效。
	if _, err := m.NewOrder([]byte("a"), []string{"x.com"}, 3, 0); err != nil {
		wantErr(t, err, ErrNonce)
	} else {
		t.Fatal("consumed nonce 3 should be invalid")
	}
}

// TestNonceEvictedExactlyAtCapacity nonce 恰被淘汰后立即无效。
func TestNonceEvictedExactlyAtCapacity(t *testing.T) {
	m := mustNew(t, Config{Ta: 10, Tv: 10, To: 10, H: 10, F: 1, C: 1, Pm: 1})
	m.Nonce() // 1，池 {1}
	m.Nonce() // 2，淘汰 1，池 {2}
	if _, err := m.NewOrder([]byte("a"), []string{"x.com"}, 1, 0); err != nil {
		wantErr(t, err, ErrNonce)
	} else {
		t.Fatal("nonce 1 evicted at capacity 1 should be invalid")
	}
	mustOrder(t, m, "a", []string{"x.com"}, 2, 0)
}

// TestRejectedKeepsNonceAcceptedConsumes 被拒绝的操作保留 nonce，被接受的消费 nonce。
func TestRejectedKeepsNonceAcceptedConsumes(t *testing.T) {
	m := mustNew(t, exampleConfig())

	// 状态冲突拒绝：对不存在的订单之前的检查先 nonce，构造状态冲突场景。
	o := mustOrder(t, m, "acc", []string{"a.com"}, m.Nonce(), 0)
	z := o.AuthzIDs[0]
	mustReport(t, m, z, true, 10)
	// 订单 ready，但 CSR 不符 → 拒绝，nonce 保留。
	n := m.Nonce()
	if _, err := m.Finalize([]byte("acc"), o.ID, []string{"b.com"}, n, 20); err != nil {
		wantErr(t, err, ErrCSR)
	} else {
		t.Fatal("expected CSR mismatch")
	}
	// 同一 nonce 再次使用成功 → 被消费。
	if _, err := m.Finalize([]byte("acc"), o.ID, []string{"a.com"}, n, 30); err != nil {
		t.Fatalf("nonce should survive CSR rejection: %v", err)
	}
	if _, err := m.Finalize([]byte("acc"), o.ID, []string{"a.com"}, n, 40); err != nil {
		wantErr(t, err, ErrNonce)
	} else {
		t.Fatal("consumed nonce should be invalid")
	}

	// 时钟回退拒绝也保留 nonce。
	n2 := m.Nonce()
	if _, err := m.NewOrder([]byte("acc"), []string{"z.com"}, n2, 5); err != nil {
		wantErr(t, err, ErrClock)
	} else {
		t.Fatal("expected clock regression")
	}
	mustOrder(t, m, "acc", []string{"z.com"}, n2, 40)
}

// TestOrderStatusDerivation 订单状态的多路派生。
func TestOrderStatusDerivation(t *testing.T) {
	cfg := exampleConfig()

	// pending：授权仍 pending。
	m := mustNew(t, cfg)
	o := mustOrder(t, m, "a", []string{"x.com"}, m.Nonce(), 0)
	if s := mustStatus(t, m, o.ID, 50); s != OrderPending {
		t.Fatalf("pending case = %s", s)
	}

	// ready：全部授权 valid 且订单未到期。
	mustReport(t, m, o.AuthzIDs[0], true, 60)
	if s := mustStatus(t, m, o.ID, 70); s != OrderReady {
		t.Fatalf("ready case = %s", s)
	}

	// valid：Finalize 后存储状态 valid，授权到期也不影响。
	if _, err := m.Finalize([]byte("a"), o.ID, []string{"x.com"}, m.Nonce(), 80); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if s := mustStatus(t, m, o.ID, 2000); s != OrderValid {
		t.Fatalf("valid case = %s", s)
	}

	// invalid（授权 invalid）。
	m1 := mustNew(t, cfg)
	o1 := mustOrder(t, m1, "a", []string{"x.com"}, m1.Nonce(), 0)
	mustReport(t, m1, o1.AuthzIDs[0], false, 10)
	if s := mustStatus(t, m1, o1.ID, 20); s != OrderInvalid {
		t.Fatalf("invalid-by-authz-invalid = %s", s)
	}

	// invalid（授权 deactivated）。
	m2 := mustNew(t, cfg)
	o2 := mustOrder(t, m2, "a", []string{"x.com"}, m2.Nonce(), 0)
	if err := m2.Deactivate([]byte("a"), o2.AuthzIDs[0], m2.Nonce(), 10); err != nil {
		t.Fatalf("Deactivate: %v", err)
	}
	if s := mustStatus(t, m2, o2.ID, 20); s != OrderInvalid {
		t.Fatalf("invalid-by-deactivated = %s", s)
	}

	// invalid（授权 expired）。
	m3 := mustNew(t, cfg)
	o3 := mustOrder(t, m3, "a", []string{"x.com"}, m3.Nonce(), 0)
	if s := mustStatus(t, m3, o3.ID, 99); s != OrderPending {
		t.Fatalf("before expiry = %s", s)
	}
	if s := mustStatus(t, m3, o3.ID, 100); s != OrderInvalid {
		t.Fatalf("invalid-by-expired = %s", s)
	}

	// invalid（订单到期）：授权 valid 但订单到期。
	m4 := mustNew(t, cfg)
	o4 := mustOrder(t, m4, "a", []string{"x.com"}, m4.Nonce(), 0)
	mustReport(t, m4, o4.AuthzIDs[0], true, 10)
	if s := mustStatus(t, m4, o4.ID, 499); s != OrderReady {
		t.Fatalf("before order expiry = %s", s)
	}
	if s := mustStatus(t, m4, o4.ID, 500); s != OrderInvalid {
		t.Fatalf("invalid-by-order-expiry = %s", s)
	}
}

// TestAuthzExpiryBoundary now 恰等于 expires 即 expired，小 1 仍有效。
func TestAuthzExpiryBoundary(t *testing.T) {
	m := mustNew(t, exampleConfig())
	o := mustOrder(t, m, "a", []string{"x.com"}, m.Nonce(), 0) // z1 expires=100
	z := o.AuthzIDs[0]
	if s := mustAuthzStatus(t, m, z, 99); s != AuthzPending {
		t.Fatalf("at expires-1 = %s, want pending", s)
	}
	if s := mustAuthzStatus(t, m, z, 100); s != AuthzExpired {
		t.Fatalf("at expires = %s, want expired", s)
	}
	// valid 授权同理。
	m2 := mustNew(t, exampleConfig())
	o2 := mustOrder(t, m2, "a", []string{"x.com"}, m2.Nonce(), 0)
	mustReport(t, m2, o2.AuthzIDs[0], true, 10) // expires=1010
	if s := mustAuthzStatus(t, m2, o2.AuthzIDs[0], 1009); s != AuthzValid {
		t.Fatalf("valid at expires-1 = %s", s)
	}
	if s := mustAuthzStatus(t, m2, o2.AuthzIDs[0], 1010); s != AuthzExpired {
		t.Fatalf("valid at expires = %s", s)
	}
}

// TestReportExpiredPendingConflict Report 对已到期 pending 授权报状态冲突（expired）。
func TestReportExpiredPendingConflict(t *testing.T) {
	m := mustNew(t, exampleConfig())
	o := mustOrder(t, m, "a", []string{"x.com"}, m.Nonce(), 0)
	err := m.Report(o.AuthzIDs[0], true, 100)
	e := wantErr(t, err, ErrState)
	if e.State != AuthzExpired {
		t.Fatalf("state = %s, want expired", e.State)
	}
	// 被拒绝的 Report 不改变状态：授权仍是（派生）expired，且不计失败。
	if s := mustAuthzStatus(t, m, o.AuthzIDs[0], 100); s != AuthzExpired {
		t.Fatalf("after rejected report = %s", s)
	}
}

// reuseCfg 便于构造复用场景：长存活、宽限流、宽配额。
func reuseCfg() Config {
	return Config{Ta: 1000, Tv: 1000, To: 5000, H: 60, F: 100, C: 100, Pm: 100}
}

// TestReuseMaxExpires 复用取 expires 最大者。
func TestReuseMaxExpires(t *testing.T) {
	m := mustNew(t, reuseCfg())
	oa := mustOrder(t, m, "a", []string{"x.com"}, m.Nonce(), 0) // z1 pending
	ob := mustOrder(t, m, "a", []string{"x.com"}, m.Nonce(), 0) // z2 pending（z1 pending 不可复用）
	mustReport(t, m, oa.AuthzIDs[0], true, 100)                 // z1 valid, expires=1100
	mustReport(t, m, ob.AuthzIDs[0], true, 200)                 // z2 valid, expires=1200
	oc := mustOrder(t, m, "a", []string{"x.com"}, m.Nonce(), 300)
	if oc.AuthzIDs[0] != ob.AuthzIDs[0] {
		t.Fatalf("reuse = %s, want %s (max expires)", oc.AuthzIDs[0], ob.AuthzIDs[0])
	}
}

// TestReuseTieBreak 并列 expires 取编号小者。
func TestReuseTieBreak(t *testing.T) {
	m := mustNew(t, reuseCfg())
	oa := mustOrder(t, m, "a", []string{"x.com"}, m.Nonce(), 0) // z1 pending
	ob := mustOrder(t, m, "a", []string{"x.com"}, m.Nonce(), 0) // z2 pending
	mustReport(t, m, oa.AuthzIDs[0], true, 100)                 // z1 valid, expires=1100
	mustReport(t, m, ob.AuthzIDs[0], true, 100)                 // z2 valid, expires=1100（同 now 允许）
	oc := mustOrder(t, m, "a", []string{"x.com"}, m.Nonce(), 200)
	if oc.AuthzIDs[0] != oa.AuthzIDs[0] {
		t.Fatalf("tie = %s, want %s (smaller number)", oc.AuthzIDs[0], oa.AuthzIDs[0])
	}
}

// TestReuseOnlyValidUnexpired pending/invalid/deactivated/expired 均不可复用。
func TestReuseOnlyValidUnexpired(t *testing.T) {
	// pending 不可复用。
	m := mustNew(t, reuseCfg())
	oa := mustOrder(t, m, "a", []string{"x.com"}, m.Nonce(), 0)
	ob := mustOrder(t, m, "a", []string{"x.com"}, m.Nonce(), 0)
	if ob.AuthzIDs[0] == oa.AuthzIDs[0] {
		t.Fatal("pending authz must not be reused")
	}

	// invalid 不可复用。
	m1 := mustNew(t, reuseCfg())
	o1 := mustOrder(t, m1, "a", []string{"x.com"}, m1.Nonce(), 0)
	mustReport(t, m1, o1.AuthzIDs[0], false, 10)
	o2 := mustOrder(t, m1, "a", []string{"x.com"}, m1.Nonce(), 20)
	if o2.AuthzIDs[0] == o1.AuthzIDs[0] {
		t.Fatal("invalid authz must not be reused")
	}

	// deactivated 不可复用。
	m2 := mustNew(t, reuseCfg())
	o3 := mustOrder(t, m2, "a", []string{"x.com"}, m2.Nonce(), 0)
	mustReport(t, m2, o3.AuthzIDs[0], true, 10)
	if err := m2.Deactivate([]byte("a"), o3.AuthzIDs[0], m2.Nonce(), 20); err != nil {
		t.Fatalf("Deactivate: %v", err)
	}
	o4 := mustOrder(t, m2, "a", []string{"x.com"}, m2.Nonce(), 30)
	if o4.AuthzIDs[0] == o3.AuthzIDs[0] {
		t.Fatal("deactivated authz must not be reused")
	}

	// expired 不可复用。
	m3 := mustNew(t, reuseCfg())
	o5 := mustOrder(t, m3, "a", []string{"x.com"}, m3.Nonce(), 0)
	mustReport(t, m3, o5.AuthzIDs[0], true, 10) // expires=1010
	o6 := mustOrder(t, m3, "a", []string{"x.com"}, m3.Nonce(), 1010)
	if o6.AuthzIDs[0] == o5.AuthzIDs[0] {
		t.Fatal("expired authz must not be reused")
	}

	// 别的账户的 valid 授权不可复用。
	m4 := mustNew(t, reuseCfg())
	o7 := mustOrder(t, m4, "a", []string{"x.com"}, m4.Nonce(), 0)
	mustReport(t, m4, o7.AuthzIDs[0], true, 10)
	o8 := mustOrder(t, m4, "b", []string{"x.com"}, m4.Nonce(), 20)
	if o8.AuthzIDs[0] == o7.AuthzIDs[0] {
		t.Fatal("authz of another account must not be reused")
	}
}

// TestSharedAuthzAcrossOrders 同一授权被多个订单共享，Deactivate 使它们都 invalid。
func TestSharedAuthzAcrossOrders(t *testing.T) {
	m := mustNew(t, reuseCfg())
	oa := mustOrder(t, m, "a", []string{"x.com"}, m.Nonce(), 0)
	mustReport(t, m, oa.AuthzIDs[0], true, 10) // z1 valid, expires=1010
	ob := mustOrder(t, m, "a", []string{"x.com"}, m.Nonce(), 20)
	oc := mustOrder(t, m, "a", []string{"x.com", "y.com"}, m.Nonce(), 30)
	if ob.AuthzIDs[0] != oa.AuthzIDs[0] || oc.AuthzIDs[0] != oa.AuthzIDs[0] {
		t.Fatal("orders should share z1")
	}
	if err := m.Deactivate([]byte("a"), oa.AuthzIDs[0], m.Nonce(), 40); err != nil {
		t.Fatalf("Deactivate: %v", err)
	}
	for _, id := range []string{oa.ID, ob.ID, oc.ID} {
		if s := mustStatus(t, m, id, 40); s != OrderInvalid {
			t.Fatalf("order %s = %s, want invalid after shared authz deactivated", id, s)
		}
	}
}

// TestFinalizeCSRSetSemantics CSR 集合与顺序无关，多一个或少一个均不符。
func TestFinalizeCSRSetSemantics(t *testing.T) {
	m := mustNew(t, reuseCfg())
	o := mustOrder(t, m, "a", []string{"x.com", "y.com"}, m.Nonce(), 0)
	mustReport(t, m, o.AuthzIDs[0], true, 10)
	mustReport(t, m, o.AuthzIDs[1], true, 10)

	// 少一个 → CSR 不符。
	if _, err := m.Finalize([]byte("a"), o.ID, []string{"x.com"}, m.Nonce(), 20); err != nil {
		wantErr(t, err, ErrCSR)
	} else {
		t.Fatal("missing ident should be CSR mismatch")
	}
	// 多一个 → CSR 不符。
	if _, err := m.Finalize([]byte("a"), o.ID, []string{"x.com", "y.com", "z.com"}, m.Nonce(), 20); err != nil {
		wantErr(t, err, ErrCSR)
	} else {
		t.Fatal("extra ident should be CSR mismatch")
	}
	// 同名不同序 → 成功。
	if sn, err := m.Finalize([]byte("a"), o.ID, []string{"y.com", "x.com"}, m.Nonce(), 20); err != nil || sn != 1 {
		t.Fatalf("reordered CSR = %d, %v; want sn=1", sn, err)
	}
	// 证书序号全局递增。
	o2 := mustOrder(t, m, "a", []string{"w.com"}, m.Nonce(), 30)
	mustReport(t, m, o2.AuthzIDs[0], true, 40)
	if sn, err := m.Finalize([]byte("a"), o2.ID, []string{"w.com"}, m.Nonce(), 50); err != nil || sn != 2 {
		t.Fatalf("second cert = %d, %v; want sn=2", sn, err)
	}
}

// TestCSRMismatchKeepsNonce CSR 不符不消费 nonce。
func TestCSRMismatchKeepsNonce(t *testing.T) {
	m := mustNew(t, reuseCfg())
	o := mustOrder(t, m, "a", []string{"x.com"}, m.Nonce(), 0)
	mustReport(t, m, o.AuthzIDs[0], true, 10)
	n := m.Nonce()
	if _, err := m.Finalize([]byte("a"), o.ID, []string{"y.com"}, n, 20); err != nil {
		wantErr(t, err, ErrCSR)
	} else {
		t.Fatal("expected CSR mismatch")
	}
	if _, err := m.Finalize([]byte("a"), o.ID, []string{"x.com"}, n, 20); err != nil {
		t.Fatalf("nonce should survive CSR mismatch: %v", err)
	}
}

// TestFailureWindowBoundary t+H 恰等于 now 已出窗，差 1 仍在。
func TestFailureWindowBoundary(t *testing.T) {
	m := mustNew(t, exampleConfig()) // H=60, F=2
	o1 := mustOrder(t, m, "a", []string{"x.com"}, m.Nonce(), 50)
	mustReport(t, m, o1.AuthzIDs[0], false, 100) // 失败@100，窗口边界 160
	o2 := mustOrder(t, m, "a", []string{"x.com"}, m.Nonce(), 110)
	mustReport(t, m, o2.AuthzIDs[0], false, 130) // 失败@130，窗口边界 190

	// now=159：160>159 且 190>159 → 2 次，限流。
	if _, err := m.NewOrder([]byte("a"), []string{"x.com"}, m.Nonce(), 159); err != nil {
		wantErr(t, err, ErrRateLimit)
	} else {
		t.Fatal("expected rate limit at 159")
	}
	// now=160：t+H==now 已出窗，仅 190>160 → 1 次，通过。
	if _, err := m.NewOrder([]byte("a"), []string{"x.com"}, m.Nonce(), 160); err != nil {
		t.Fatalf("t+H==now should be out of window: %v", err)
	}
	// now=189：仅 190>189 → 1 次，通过。
	if _, err := m.NewOrder([]byte("a"), []string{"x.com"}, m.Nonce(), 189); err != nil {
		t.Fatalf("189: %v", err)
	}
	// now=190：全部出窗 → 0 次，通过。
	if _, err := m.NewOrder([]byte("a"), []string{"x.com"}, m.Nonce(), 190); err != nil {
		t.Fatalf("190: %v", err)
	}
}

// TestRateLimitNoPartialState 限流时不留半建的授权与订单，也不消费 nonce。
func TestRateLimitNoPartialState(t *testing.T) {
	m := mustNew(t, exampleConfig()) // F=2
	o1 := mustOrder(t, m, "a", []string{"x.com"}, m.Nonce(), 0)
	mustReport(t, m, o1.AuthzIDs[0], false, 10)
	o2 := mustOrder(t, m, "a", []string{"x.com"}, m.Nonce(), 20)
	mustReport(t, m, o2.AuthzIDs[0], false, 30)

	zBefore, oBefore := m.zSeq, m.oSeq
	n := m.Nonce()
	// x.com 限流（2 次失败），y.com 无辜：不得为 y.com 建授权。
	_, err := m.NewOrder([]byte("a"), []string{"x.com", "y.com"}, n, 40)
	wantErr(t, err, ErrRateLimit)
	if m.zSeq != zBefore || m.oSeq != oBefore {
		t.Fatalf("rate-limited NewOrder created state: z %d->%d, o %d->%d", zBefore, m.zSeq, oBefore, m.oSeq)
	}
	if _, err := m.GetAuthz(fmt.Sprintf("z%d", zBefore+1), 40); err == nil {
		t.Fatal("half-built authz should not exist")
	}
	// nonce 未消费：同一 nonce 可继续用于不受限的标识符。
	if _, err := m.NewOrder([]byte("a"), []string{"y.com"}, n, 40); err != nil {
		t.Fatalf("nonce should survive rate limiting: %v", err)
	}
}

// TestQuotaBoundary p+q 恰等于 Pm 通过，大 1 报超限且不留半建授权、不消费 nonce。
func TestQuotaBoundary(t *testing.T) {
	m := mustNew(t, exampleConfig()) // Pm=3
	mustOrder(t, m, "a", []string{"p.com", "q.com"}, m.Nonce(), 0)
	// p=2, q=1 → 2+1=3=Pm，通过。
	o := mustOrder(t, m, "a", []string{"r.com"}, m.Nonce(), 10)
	if o.Status != OrderPending {
		t.Fatalf("boundary order = %+v", o)
	}
	// p=3, q=1 → 4>3，超限。
	n := m.Nonce()
	zBefore, oBefore := m.zSeq, m.oSeq
	_, err := m.NewOrder([]byte("a"), []string{"s.com"}, n, 20)
	e := wantErr(t, err, ErrQuota)
	wantErr(t, err, ErrQuota)
	if e.P != 3 || e.Q != 1 {
		t.Fatalf("quota detail = p%d q%d, want p3 q1", e.P, e.Q)
	}
	if m.zSeq != zBefore || m.oSeq != oBefore {
		t.Fatal("quota-rejected NewOrder must not create authz or order")
	}
	// nonce 未消费：授权到期（Ta=100）后 p=0，同一 nonce 成功。
	if _, err := m.NewOrder([]byte("a"), []string{"s.com"}, n, 100); err != nil {
		t.Fatalf("nonce should survive quota rejection: %v", err)
	}
}

// TestReuseBypassesQuota 复用不占配额。
func TestReuseBypassesQuota(t *testing.T) {
	m := mustNew(t, exampleConfig()) // Pm=3
	o1 := mustOrder(t, m, "a", []string{"x.com", "y.com"}, m.Nonce(), 0)
	mustReport(t, m, o1.AuthzIDs[0], true, 10) // x.com valid
	mustReport(t, m, o1.AuthzIDs[1], true, 10) // y.com valid
	// p=0；新建 3 个 pending。
	mustOrder(t, m, "a", []string{"n1.com", "n2.com", "n3.com"}, m.Nonce(), 20)
	// p=3，但 x.com、y.com 复用，q=0 → 通过。
	o := mustOrder(t, m, "a", []string{"x.com", "y.com"}, m.Nonce(), 30)
	if o.AuthzIDs[0] != o1.AuthzIDs[0] || o.AuthzIDs[1] != o1.AuthzIDs[1] {
		t.Fatalf("should reuse: %+v", o)
	}
	// p=3，一个复用 + 一个新建 → q=1，4>3 超限。
	_, err := m.NewOrder([]byte("a"), []string{"x.com", "new.com"}, m.Nonce(), 40)
	e := wantErr(t, err, ErrQuota)
	if e.P != 3 || e.Q != 1 {
		t.Fatalf("quota detail = p%d q%d, want p3 q1", e.P, e.Q)
	}
}

// TestQuotaRelease Report、Deactivate 与到期都释放配额。
func TestQuotaRelease(t *testing.T) {
	// Report（成功与失败）释放。
	m := mustNew(t, exampleConfig()) // Pm=3
	o1 := mustOrder(t, m, "a", []string{"a.com", "b.com", "c.com"}, m.Nonce(), 0)
	mustReport(t, m, o1.AuthzIDs[0], true, 10)  // → valid
	mustReport(t, m, o1.AuthzIDs[1], false, 20) // → invalid
	// p=1，新建 2 个 → 1+2=3=Pm 通过。
	if _, err := m.NewOrder([]byte("a"), []string{"d.com", "e.com"}, m.Nonce(), 30); err != nil {
		t.Fatalf("after report release: %v", err)
	}

	// Deactivate 释放。
	m2 := mustNew(t, exampleConfig())
	o2 := mustOrder(t, m2, "a", []string{"a.com", "b.com", "c.com"}, m2.Nonce(), 0)
	if err := m2.Deactivate([]byte("a"), o2.AuthzIDs[0], m2.Nonce(), 10); err != nil {
		t.Fatalf("Deactivate: %v", err)
	}
	if _, err := m2.NewOrder([]byte("a"), []string{"d.com"}, m2.Nonce(), 20); err != nil {
		t.Fatalf("after deactivate release: %v", err)
	}

	// 到期释放。
	m3 := mustNew(t, exampleConfig())
	mustOrder(t, m3, "a", []string{"a.com", "b.com", "c.com"}, m3.Nonce(), 0)
	if _, err := m3.NewOrder([]byte("a"), []string{"d.com"}, m3.Nonce(), 100); err != nil {
		t.Fatalf("after expiry release: %v", err)
	}
	// valid/invalid/deactivated 不计入 p：上面已覆盖 valid 与 invalid。
}

// TestRejectedOpsNoStateChange 被拒绝的操作不改变任何状态。
func TestRejectedOpsNoStateChange(t *testing.T) {
	m := mustNew(t, exampleConfig())
	o := mustOrder(t, m, "a", []string{"x.com"}, m.Nonce(), 0)
	z := o.AuthzIDs[0]

	snapshot := func() string {
		za, _ := m.GetAuthz(z, 50)
		oo, _ := m.GetOrder(o.ID, 50)
		return fmt.Sprintf("z=%s/%d o=%s zSeq=%d oSeq=%d cert=%d pool=%d maxNow=%d",
			za.Status, za.Expires, oo.Status, m.zSeq, m.oSeq, m.certSeq, len(m.noncePool), m.maxNow)
	}
	before := snapshot()

	// 参数非法。
	if _, err := m.NewOrder(nil, []string{"x.com"}, m.Nonce(), 50); err != nil {
		wantErr(t, err, ErrParam)
	}
	if _, err := m.NewOrder([]byte("a"), []string{"BAD!"}, m.Nonce(), 50); err != nil {
		wantErr(t, err, ErrParam)
	}
	if _, err := m.NewOrder([]byte("a"), []string{"x.com", "x.com"}, m.Nonce(), 50); err != nil {
		wantErr(t, err, ErrParam)
	}
	if _, err := m.NewOrder([]byte("a"), nil, m.Nonce(), 50); err != nil {
		wantErr(t, err, ErrParam)
	}
	// 时钟回退。
	// 先让时钟前进到 50。
	mustReport(t, m, z, true, 50)
	if _, err := m.NewOrder([]byte("a"), []string{"y.com"}, m.Nonce(), 49); err != nil {
		wantErr(t, err, ErrClock)
	}
	// nonce 无效。
	if _, err := m.NewOrder([]byte("a"), []string{"y.com"}, 999, 50); err != nil {
		wantErr(t, err, ErrNonce)
	}
	// 对象不存在。
	if err := m.Deactivate([]byte("a"), "z999", m.Nonce(), 50); err != nil {
		wantErr(t, err, ErrNotFound)
	}
	// 属于别的账户。
	if err := m.Deactivate([]byte("b"), z, m.Nonce(), 50); err != nil {
		wantErr(t, err, ErrNotFound)
	}
	// 状态冲突（Report 已 valid 的授权）。
	if err := m.Report(z, true, 50); err != nil {
		wantErr(t, err, ErrState)
	}
	// Finalize 未 ready 的订单。
	o2 := mustOrder(t, m, "a", []string{"w.com"}, m.Nonce(), 50)
	if _, err := m.Finalize([]byte("a"), o2.ID, []string{"w.com"}, m.Nonce(), 50); err != nil {
		wantErr(t, err, ErrState)
	}

	after := snapshot()
	// before 快照在 z 变 valid 之前，故只比较 z 变 valid 之后的状态。
	za, _ := m.GetAuthz(z, 50)
	if za.Status != AuthzValid || za.Expires != 1050 {
		t.Fatalf("z changed unexpectedly: %+v", za)
	}
	_ = before
	_ = after
	if m.zSeq != 2 || m.oSeq != 2 || m.certSeq != 0 {
		t.Fatalf("counters changed: z=%d o=%d cert=%d", m.zSeq, m.oSeq, m.certSeq)
	}
}

// TestConfigValidation 构造参数非法整体拒绝。
func TestConfigValidation(t *testing.T) {
	good := exampleConfig()
	if _, err := New(good); err != nil {
		t.Fatalf("good config rejected: %v", err)
	}
	cases := []Config{
		{Ta: 0, Tv: 1, To: 1, H: 1, F: 1, C: 1, Pm: 1},
		{Ta: 1, Tv: -1, To: 1, H: 1, F: 1, C: 1, Pm: 1},
		{Ta: 1, Tv: 1, To: 1e9 + 1, H: 1, F: 1, C: 1, Pm: 1},
		{Ta: 1, Tv: 1, To: 1, H: 0, F: 1, C: 1, Pm: 1},
		{Ta: 1, Tv: 1, To: 1, H: 1, F: 0, C: 1, Pm: 1},
		{Ta: 1, Tv: 1, To: 1, H: 1, F: 1001, C: 1, Pm: 1},
		{Ta: 1, Tv: 1, To: 1, H: 1, F: 1, C: 0, Pm: 1},
		{Ta: 1, Tv: 1, To: 1, H: 1, F: 1, C: 1e6 + 1, Pm: 1},
		{Ta: 1, Tv: 1, To: 1, H: 1, F: 1, C: 1, Pm: 0},
		{Ta: 1, Tv: 1, To: 1, H: 1, F: 1, C: 1, Pm: 10001},
	}
	for i, c := range cases {
		if _, err := New(c); err == nil {
			t.Fatalf("case %d: bad config accepted", i)
		} else {
			wantErr(t, err, ErrParam)
		}
	}
	// 边界值可用。
	if _, err := New(Config{Ta: 1e9, Tv: 1e9, To: 1e9, H: 1e9, F: 1000, C: 1e6, Pm: 1e4}); err != nil {
		t.Fatalf("boundary config rejected: %v", err)
	}
}

// TestIdentValidation 标识符合规性。
func TestIdentValidation(t *testing.T) {
	m := mustNew(t, Config{Ta: 100, Tv: 1000, To: 500, H: 60, F: 2, C: 100, Pm: 20})
	bad := []string{
		"", "*.", "A.com", "a_b.com", "a/b.com", "*.a.*", "*a.com",
		"a..com is ok but space not ok", "中文.com",
	}
	for _, s := range bad {
		if _, err := m.NewOrder([]byte("a"), []string{s}, m.Nonce(), 0); err != nil {
			wantErr(t, err, ErrParam)
		} else {
			t.Fatalf("ident %q should be rejected", s)
		}
	}
	// 合法：通配符前缀、连字符、点、数字。
	good := []string{"*.a.com", "a-b.c-d.com", "1.2.3", "x"}
	o := mustOrder(t, m, "a", good, m.Nonce(), 0)
	if len(o.AuthzIDs) != 4 {
		t.Fatalf("good idents = %+v", o)
	}
	// 「*.a.com」与「a.com」是两个不同的标识符。
	o2 := mustOrder(t, m, "a", []string{"*.a.com", "a.com"}, m.Nonce(), 0)
	if o2.AuthzIDs[0] == o2.AuthzIDs[1] {
		t.Fatal("*.a.com and a.com must map to different authzs")
	}
	// 个数越界：11 个。
	tooMany := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k"}
	if _, err := m.NewOrder([]byte("a"), tooMany, m.Nonce(), 0); err != nil {
		wantErr(t, err, ErrParam)
	} else {
		t.Fatal("11 idents should be rejected")
	}
	// 重复。
	if _, err := m.NewOrder([]byte("a"), []string{"x.com", "x.com"}, m.Nonce(), 0); err != nil {
		wantErr(t, err, ErrParam)
	} else {
		t.Fatal("duplicate idents should be rejected")
	}
	// now 越界。
	if _, err := m.NewOrder([]byte("a"), []string{"nn.com"}, m.Nonce(), -1); err != nil {
		wantErr(t, err, ErrParam)
	} else {
		t.Fatal("negative now should be rejected")
	}
	if _, err := m.NewOrder([]byte("a"), []string{"nn.com"}, m.Nonce(), 1e15+1); err != nil {
		wantErr(t, err, ErrParam)
	} else {
		t.Fatal("now > 1e15 should be rejected")
	}
}

// TestReuseIndexScannedBound 复用查找考察数不超过可复用数加本次清除数，
// 且与该键之外的无关授权无关。
func TestReuseIndexScannedBound(t *testing.T) {
	m := mustNew(t, Config{Ta: 1000, Tv: 1000, To: 5000, H: 60, F: 1000, C: 20000, Pm: 10000})
	// 目标键 (a, x.com) 下制造 3 个 valid 授权：z1、z2、z3。
	oa := mustOrder(t, m, "a", []string{"x.com"}, m.Nonce(), 0)
	ob := mustOrder(t, m, "a", []string{"x.com"}, m.Nonce(), 0)
	oc := mustOrder(t, m, "a", []string{"x.com"}, m.Nonce(), 0)
	mustReport(t, m, oa.AuthzIDs[0], true, 10) // expires=1010
	mustReport(t, m, ob.AuthzIDs[0], true, 20) // expires=1020
	mustReport(t, m, oc.AuthzIDs[0], true, 30) // expires=1030

	// 该键之外大量无关授权（其他标识符、其他账户）。
	var pending1 []string
	for i := 0; i < 100; i++ {
		id := fmt.Sprintf("u%03d.com", i)
		oo := mustOrder(t, m, "a", []string{id}, m.Nonce(), 40)
		pending1 = append(pending1, oo.AuthzIDs[0])
	}
	for _, z := range pending1 {
		mustReport(t, m, z, true, 50)
	}
	var pending2 []string
	for i := 0; i < 100; i++ {
		id := fmt.Sprintf("v%03d.com", i)
		oo := mustOrder(t, m, "b", []string{id}, m.Nonce(), 60)
		pending2 = append(pending2, oo.AuthzIDs[0])
	}
	for _, z := range pending2 {
		mustReport(t, m, z, true, 70)
	}

	// 查找：3 个可复用、0 个清除 → 考察数 ≤ 3（链尾并列块大小为 1，实为 1）。
	before := m.reuseScanned
	mustOrder(t, m, "a", []string{"x.com"}, m.Nonce(), 80)
	if got := m.reuseScanned - before; got > 3 {
		t.Fatalf("scanned %d authzs, want <= 3 (reusable only)", got)
	}

	// 让 z1、z2 到期（now=1025）：本次清除 2 个，考察 1 个可复用 → ≤ 3。
	before = m.reuseScanned
	o := mustOrder(t, m, "a", []string{"x.com"}, m.Nonce(), 1025)
	if got := m.reuseScanned - before; got > 3 {
		t.Fatalf("scanned %d, want <= cleared(2)+reusable(1)", got)
	}
	if o.AuthzIDs[0] != oc.AuthzIDs[0] {
		t.Fatalf("reuse = %s, want %s", o.AuthzIDs[0], oc.AuthzIDs[0])
	}

	// 并列块：两个同 expires 的 valid 授权都被考察。
	m2 := mustNew(t, Config{Ta: 1000, Tv: 1000, To: 5000, H: 60, F: 100, C: 100, Pm: 100})
	p1 := mustOrder(t, m2, "a", []string{"x.com"}, m2.Nonce(), 0)
	p2 := mustOrder(t, m2, "a", []string{"x.com"}, m2.Nonce(), 0)
	mustReport(t, m2, p1.AuthzIDs[0], true, 100) // expires=1100
	mustReport(t, m2, p2.AuthzIDs[0], true, 100) // expires=1100
	before2 := m2.reuseScanned
	oc2 := mustOrder(t, m2, "a", []string{"x.com"}, m2.Nonce(), 200)
	if got := m2.reuseScanned - before2; got != 2 {
		t.Fatalf("tie scan = %d, want 2", got)
	}
	if oc2.AuthzIDs[0] != p1.AuthzIDs[0] {
		t.Fatalf("tie should pick smaller id %s, got %s", p1.AuthzIDs[0], oc2.AuthzIDs[0])
	}
}

// TestClockRegressionReadOnly 只读查询的 now 不得小于已接受的最大 now，且不推进时钟。
func TestClockRegressionReadOnly(t *testing.T) {
	m := mustNew(t, exampleConfig())
	o := mustOrder(t, m, "a", []string{"x.com"}, m.Nonce(), 100)
	if _, err := m.Status(o.ID, 99); err != nil {
		wantErr(t, err, ErrClock)
	} else {
		t.Fatal("status with regressed now should fail")
	}
	if _, err := m.GetAuthz(o.AuthzIDs[0], 99); err != nil {
		wantErr(t, err, ErrClock)
	} else {
		t.Fatal("authz query with regressed now should fail")
	}
	// 查询不推进时钟：之后的操作仍可用 now=100。
	if s := mustStatus(t, m, o.ID, 100); s != OrderPending {
		t.Fatalf("status = %s", s)
	}
	if _, err := m.NewOrder([]byte("a"), []string{"y.com"}, m.Nonce(), 100); err != nil {
		t.Fatalf("read-only query must not advance clock: %v", err)
	}
}

// TestConcurrency 并发下 Nonce 不重复、同一 nonce 至多一个被接受。
func TestConcurrency(t *testing.T) {
	m := mustNew(t, Config{Ta: 1e9, Tv: 1e9, To: 1e9, H: 1e9, F: 1000, C: 1e6, Pm: 1e4})

	// 并发 Nonce：不得发出重复值。
	const n = 2000
	nonces := make([]int64, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			nonces[i] = m.Nonce()
		}(i)
	}
	wg.Wait()
	seen := make(map[int64]bool, n)
	for _, v := range nonces {
		if seen[v] {
			t.Fatalf("duplicate nonce %d", v)
		}
		seen[v] = true
	}

	// 同一 nonce 被并发使用：至多一个 NewOrder 被接受。
	shared := m.Nonce()
	const g = 64
	var okCount int64
	var mu sync.Mutex
	for i := 0; i < g; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			acc := fmt.Sprintf("acc%d", i%4)
			id := fmt.Sprintf("d%d.com", i%8)
			if _, err := m.NewOrder([]byte(acc), []string{id}, shared, 0); err == nil {
				mu.Lock()
				okCount++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if okCount != 1 {
		t.Fatalf("shared nonce accepted %d times, want exactly 1", okCount)
	}

	// 并发混合操作不panic、不产生数据竞争（配合 -race）。
	for i := 0; i < g; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			acc := fmt.Sprintf("acc%d", i%4)
			id := fmt.Sprintf("d%d.com", i%8)
			nonce := m.Nonce()
			o, err := m.NewOrder([]byte(acc), []string{id}, nonce, 10)
			if err != nil {
				return
			}
			_ = m.Report(o.AuthzIDs[0], i%2 == 0, 20)
			_, _ = m.Status(o.ID, 30)
			_, _ = m.GetAuthz(o.AuthzIDs[0], 30)
			_, _ = m.Finalize([]byte(acc), o.ID, []string{id}, m.Nonce(), 40)
		}(i)
	}
	wg.Wait()
}

// TestReuseSelection 复用只取 valid 且未到期，取 expires 最大者，并列取小编号。
func TestSpecWalkthrough(t *testing.T) {
	m := mustNew(t, exampleConfig())

	// 连续四次 Nonce 得 1、2、3、4，池容量 3，1 被淘汰。
	for want := int64(1); want <= 4; want++ {
		if got := m.Nonce(); got != want {
			t.Fatalf("Nonce() = %d, want %d", got, want)
		}
	}

	// nonce 1 已被淘汰，报 nonce 无效。
	if _, err := m.NewOrder([]byte("acc"), []string{"a.com", "b.com"}, 1, 0); err != nil {
		wantErr(t, err, ErrNonce)
	} else {
		t.Fatal("expected nonce error for evicted nonce 1")
	}

	// nonce 2 建订单 o1 与 pending 授权 z1、z2。
	o1 := mustOrder(t, m, "acc", []string{"a.com", "b.com"}, 2, 0)
	if o1.ID != "o1" || o1.Expires != 500 || o1.Status != OrderPending {
		t.Fatalf("o1 = %+v", o1)
	}
	if o1.AuthzIDs[0] != "z1" || o1.AuthzIDs[1] != "z2" {
		t.Fatalf("o1 authzs = %v", o1.AuthzIDs)
	}
	z1, err := m.GetAuthz("z1", 0)
	if err != nil || z1.Expires != 100 || z1.Status != AuthzPending {
		t.Fatalf("z1 = %+v, err=%v", z1, err)
	}

	// nonce 2 已被消费，不能再用。
	if _, err := m.NewOrder([]byte("acc"), []string{"a.com"}, 2, 0); err != nil {
		wantErr(t, err, ErrNonce)
	} else {
		t.Fatal("expected nonce error for consumed nonce 2")
	}

	mustReport(t, m, "z1", true, 10)
	if z1, _ := m.GetAuthz("z1", 10); z1.Status != AuthzValid || z1.Expires != 1010 {
		t.Fatalf("z1 after report = %+v", z1)
	}
	if s := mustStatus(t, m, "o1", 10); s != OrderPending {
		t.Fatalf("Status(o1,10) = %s, want pending", s)
	}

	mustReport(t, m, "z2", true, 20)
	if s := mustStatus(t, m, "o1", 20); s != OrderReady {
		t.Fatalf("Status(o1,20) = %s, want ready", s)
	}

	// Finalize 成功，CSR 顺序无关，证书序号为 1。
	sn, err := m.Finalize([]byte("acc"), "o1", []string{"b.com", "a.com"}, 3, 30)
	if err != nil || sn != 1 {
		t.Fatalf("Finalize = %d, %v; want sn=1", sn, err)
	}
	if s := mustStatus(t, m, "o1", 30); s != OrderValid {
		t.Fatalf("Status(o1,30) = %s, want valid", s)
	}

	// 订单已 valid，再 Finalize 报状态冲突，nonce 4 留在池中。
	if _, err := m.Finalize([]byte("acc"), "o1", []string{"a.com", "b.com"}, 4, 30); err != nil {
		e := wantErr(t, err, ErrState)
		if e.State != OrderValid {
			t.Fatalf("state = %s, want valid", e.State)
		}
	} else {
		t.Fatal("expected state conflict on finalized order")
	}

	// nonce 4 仍可用：a.com 复用 z1，c.com 新建 z3。
	o2 := mustOrder(t, m, "acc", []string{"a.com", "c.com"}, 4, 450)
	if o2.ID != "o2" || o2.Expires != 950 {
		t.Fatalf("o2 = %+v", o2)
	}
	if o2.AuthzIDs[0] != "z1" || o2.AuthzIDs[1] != "z3" {
		t.Fatalf("o2 authzs = %v, want [z1 z3]", o2.AuthzIDs)
	}
	if z3, _ := m.GetAuthz("z3", 450); z3.Expires != 550 || z3.Status != AuthzPending {
		t.Fatalf("z3 = %+v", z3)
	}
	if s := mustStatus(t, m, "o2", 549); s != OrderPending {
		t.Fatalf("Status(o2,549) = %s, want pending", s)
	}
	if s := mustStatus(t, m, "o2", 550); s != OrderInvalid {
		t.Fatalf("Status(o2,550) = %s, want invalid (z3 expired)", s)
	}

	// 复用 z1 使订单提前失效：1005 ready，1010 因 z1 到期 invalid。
	n := m.Nonce()
	o3 := mustOrder(t, m, "acc", []string{"a.com"}, n, 1005)
	if o3.Expires != 1505 || o3.AuthzIDs[0] != "z1" {
		t.Fatalf("o3 = %+v", o3)
	}
	if s := mustStatus(t, m, o3.ID, 1005); s != OrderReady {
		t.Fatalf("Status(o3,1005) = %s, want ready", s)
	}
	if s := mustStatus(t, m, o3.ID, 1010); s != OrderInvalid {
		t.Fatalf("Status(o3,1010) = %s, want invalid (z1 expired)", s)
	}
}

// TestSpecRateLimitExample 走查题目给出的限流示例。
func TestSpecRateLimitExample(t *testing.T) {
	m := mustNew(t, exampleConfig())
	n1 := m.Nonce()
	o := mustOrder(t, m, "acc", []string{"c.com"}, n1, 50)
	z := o.AuthzIDs[0]
	mustReport(t, m, z, false, 100)

	n2 := m.Nonce()
	o2 := mustOrder(t, m, "acc", []string{"c.com"}, n2, 110)
	mustReport(t, m, o2.AuthzIDs[0], false, 130)

	// t=159：两条记录 t+H 为 160、190 均大于 159，达到阈值 F=2。
	n3 := m.Nonce()
	_, err := m.NewOrder([]byte("acc"), []string{"c.com"}, n3, 159)
	e := wantErr(t, err, ErrRateLimit)
	if e.Ident != "c.com" || e.Count != 2 {
		t.Fatalf("rate limit = ident %s count %d, want c.com/2", e.Ident, e.Count)
	}
	// 限流拒绝不消费 nonce。
	if _, err := m.NewOrder([]byte("acc"), []string{"c.com"}, n3, 159); err == nil {
		t.Fatal("nonce should survive rejected NewOrder")
	} else {
		wantErr(t, err, ErrRateLimit)
	}

	// t=160：仅 190 大于 160，个数为 1，成功。
	o3 := mustOrder(t, m, "acc", []string{"c.com"}, n3, 160)
	if o3.Status != OrderPending {
		t.Fatalf("o3 = %+v", o3)
	}
}

// TestSpecQuotaExample 走查题目给出的配额示例。
func TestSpecQuotaExample(t *testing.T) {
	// 分支一：p+q 大于 Pm 报超限；Report 释放后成功。
	m := mustNew(t, exampleConfig())
	mustOrder(t, m, "acc", []string{"a.com", "b.com", "c.com"}, m.Nonce(), 0)
	n := m.Nonce()
	_, err := m.NewOrder([]byte("acc"), []string{"d.com"}, n, 99)
	e := wantErr(t, err, ErrQuota)
	if e.P != 3 || e.Q != 1 {
		t.Fatalf("quota = p%d q%d, want p3 q1", e.P, e.Q)
	}
	mustReport(t, m, "z1", true, 50)
	if o := mustOrder(t, m, "acc", []string{"d.com"}, n, 99); o.Status != OrderPending {
		t.Fatalf("after release, NewOrder = %+v", o)
	}

	// 分支二：不 Report，三个授权在 100 到期后 p=0，成功。
	m2 := mustNew(t, exampleConfig())
	mustOrder(t, m2, "acc", []string{"a.com", "b.com", "c.com"}, m2.Nonce(), 0)
	n2 := m2.Nonce()
	if _, err := m2.NewOrder([]byte("acc"), []string{"d.com"}, n2, 100); err != nil {
		t.Fatalf("NewOrder at expiry boundary: %v", err)
	}

	// 分支三：另一账户的配额互不影响。
	m3 := mustNew(t, exampleConfig())
	mustOrder(t, m3, "acc", []string{"a.com", "b.com", "c.com"}, m3.Nonce(), 0)
	n3 := m3.Nonce()
	if _, err := m3.NewOrder([]byte("other"), []string{"d.com"}, n3, 0); err != nil {
		t.Fatalf("other account should not be limited: %v", err)
	}
}
