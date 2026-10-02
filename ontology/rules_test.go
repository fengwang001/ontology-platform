package ontology

import "testing"

func TestReuseRules(t *testing.T) {
	acc := []byte("acc")

	// Max expires; invalid never reused.
	m := newTestMachine(t)
	_, _ = m.NewOrder(acc, []string{"a.com"}, m.Nonce(), 0)
	if err := m.Report("z1", true, 10); err != nil { // 1010
		t.Fatal(err)
	}
	o2, err := m.NewOrder(acc, []string{"a.com", "b.com"}, m.Nonce(), 500)
	if err != nil {
		t.Fatal(err)
	}
	if o2.AuthzIDs[0] != "z1" || o2.AuthzIDs[1] != "z2" {
		t.Fatalf("reuse = %v", o2.AuthzIDs)
	}

	// Two pending authzs for the same ident coexist; tie at equal expires
	// is broken by smallest id.
	m2 := newTestMachine(t)
	_, _ = m2.NewOrder(acc, []string{"k.com"}, m2.Nonce(), 0)
	oB, _ := m2.NewOrder(acc, []string{"k.com"}, m2.Nonce(), 10)
	if oB.AuthzIDs[0] != "z2" {
		t.Fatalf("second pending = %v", oB.AuthzIDs)
	}
	if err := m2.Report("z1", true, 20); err != nil {
		t.Fatal(err)
	}
	if err := m2.Report("z2", true, 20); err != nil {
		t.Fatal(err)
	}
	oC, _ := m2.NewOrder(acc, []string{"k.com"}, m2.Nonce(), 30)
	if oC.AuthzIDs[0] != "z1" {
		t.Fatalf("tie = %v", oC.AuthzIDs)
	}

	// Larger expires wins.
	m3 := newTestMachine(t)
	_, _ = m3.NewOrder(acc, []string{"k.com"}, m3.Nonce(), 0)
	_, _ = m3.NewOrder(acc, []string{"k.com"}, m3.Nonce(), 0)
	if err := m3.Report("z1", true, 20); err != nil {
		t.Fatal(err)
	}
	if err := m3.Report("z2", true, 30); err != nil {
		t.Fatal(err)
	}
	o, _ := m3.NewOrder(acc, []string{"k.com"}, m3.Nonce(), 40)
	if o.AuthzIDs[0] != "z2" {
		t.Fatalf("max expires = %v", o.AuthzIDs)
	}

	// Invalid authz is not reused.
	m4 := newTestMachine(t)
	_, _ = m4.NewOrder(acc, []string{"k.com"}, m4.Nonce(), 0)
	if err := m4.Report("z1", false, 5); err != nil {
		t.Fatal(err)
	}
	o, _ = m4.NewOrder(acc, []string{"k.com"}, m4.Nonce(), 6)
	if o.AuthzIDs[0] != "z2" {
		t.Fatalf("invalid reuse = %v", o.AuthzIDs)
	}

	// Reused authz expiring early invalidates a young order.
	m5 := newTestMachine(t)
	_, _ = m5.NewOrder(acc, []string{"k.com"}, m5.Nonce(), 0)
	if err := m5.Report("z1", true, 10); err != nil { // 1010
		t.Fatal(err)
	}
	oy, _ := m5.NewOrder(acc, []string{"k.com"}, m5.Nonce(), 1000) // order -> 1500
	if s, _ := m5.Status(oy.ID, 1009); s != "ready" {
		t.Fatalf("ready = %s", s)
	}
	if s, _ := m5.Status(oy.ID, 1010); s != "invalid" {
		t.Fatalf("early invalid = %s", s)
	}

	// Expired valid authz is not reused; fresh pending created instead.
	m6 := newTestMachine(t)
	_, _ = m6.NewOrder(acc, []string{"k.com"}, m6.Nonce(), 0)
	if err := m6.Report("z1", true, 10); err != nil {
		t.Fatal(err)
	}
	o, _ = m6.NewOrder(acc, []string{"k.com"}, m6.Nonce(), 1010)
	if o.AuthzIDs[0] != "z2" {
		t.Fatalf("expired reuse = %v", o.AuthzIDs)
	}
}

func TestSharedAuthzDeactivate(t *testing.T) {
	m := newTestMachine(t)
	acc := []byte("acc")
	_, _ = m.NewOrder(acc, []string{"k.com"}, m.Nonce(), 0)
	if err := m.Report("z1", true, 10); err != nil {
		t.Fatal(err)
	}
	o2, _ := m.NewOrder(acc, []string{"k.com"}, m.Nonce(), 20)
	o3, _ := m.NewOrder(acc, []string{"k.com"}, m.Nonce(), 30)
	if o2.AuthzIDs[0] != "z1" || o3.AuthzIDs[0] != "z1" {
		t.Fatalf("not shared: %v %v", o2.AuthzIDs, o3.AuthzIDs)
	}
	if err := m.Deactivate(acc, "z1", m.Nonce(), 40); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{"o2", "o3"} {
		if s, _ := m.Status(ref, 40); s != "invalid" {
			t.Fatalf("%s = %s", ref, s)
		}
	}
	if err := m.Deactivate(acc, "z1", m.Nonce(), 41); !isKind(err, KindState) {
		t.Fatalf("redeactivate = %v", err)
	}
	if err := m.Deactivate([]byte("other"), "z1", m.Nonce(), 42); !isKind(err, KindNotFound) {
		t.Fatalf("foreign = %v", err)
	}
}

func TestFinalizeCSR(t *testing.T) {
	m := newTestMachine(t)
	acc := []byte("acc")
	_, _ = m.NewOrder(acc, []string{"a.com", "b.com"}, m.Nonce(), 0)
	if err := m.Report("z1", true, 10); err != nil {
		t.Fatal(err)
	}
	if err := m.Report("z2", true, 10); err != nil {
		t.Fatal(err)
	}
	n := m.Nonce()
	if _, err := m.Finalize(acc, "o1", []string{"a.com", "b.com", "c.com"}, n, 20); !isKind(err, KindCSR) {
		t.Fatalf("extra = %v", err)
	}
	if _, err := m.Finalize(acc, "o1", []string{"a.com"}, n, 20); !isKind(err, KindCSR) {
		t.Fatalf("missing = %v", err)
	}
	if _, err := m.Finalize(acc, "o1", []string{"a.com", "x.com"}, n, 20); !isKind(err, KindCSR) {
		t.Fatalf("different = %v", err)
	}
	// Nonce survived the rejected finalize calls.
	sn, err := m.Finalize(acc, "o1", []string{"b.com", "a.com"}, n, 20)
	if err != nil || sn != 1 {
		t.Fatalf("finalize = %d %v", sn, err)
	}
	// Certificate serials are globally monotonic.
	_, _ = m.NewOrder(acc, []string{"c.com"}, m.Nonce(), 21)
	if err := m.Report("z3", true, 22); err != nil {
		t.Fatal(err)
	}
	sn, err = m.Finalize(acc, "o2", []string{"c.com"}, m.Nonce(), 23)
	if err != nil || sn != 2 {
		t.Fatalf("serial = %d %v", sn, err)
	}
}

func TestFailureWindowBoundary(t *testing.T) {
	m, _ := New(Config{Ta: 1000, Tv: 1000, To: 500, H: 60, F: 2, C: 30, Pm: 20})
	acc := []byte("acc")
	_, _ = m.NewOrder(acc, []string{"c.com"}, m.Nonce(), 0)
	if err := m.Report("z1", false, 100); err != nil {
		t.Fatal(err)
	}
	_, _ = m.NewOrder(acc, []string{"c.com"}, m.Nonce(), 101)
	if err := m.Report("z2", false, 130); err != nil {
		t.Fatal(err)
	}
	// t+H == now means out of window; now-1 still counts both.
	if _, err := m.NewOrder(acc, []string{"c.com"}, m.Nonce(), 159); !isKind(err, KindRateLimited) {
		t.Fatalf("159 = %v", err)
	}
	if _, err := m.NewOrder(acc, []string{"c.com"}, m.Nonce(), 160); err != nil {
		t.Fatalf("160 = %v", err)
	}
}

func TestRateLimitNoHalfBuildAndFirstIdent(t *testing.T) {
	m, _ := New(Config{Ta: 1000, Tv: 1000, To: 500, H: 60, F: 1, C: 30, Pm: 20})
	acc := []byte("acc")
	_, _ = m.NewOrder(acc, []string{"x.com"}, m.Nonce(), 0)
	if err := m.Report("z1", false, 10); err != nil {
		t.Fatal(err)
	}
	_, err := m.NewOrder(acc, []string{"y.com", "x.com", "z.com"}, m.Nonce(), 20)
	e, ok := err.(*Error)
	if !ok || e.Kind != KindRateLimited || e.Ident != "x.com" || e.Count != 1 {
		t.Fatalf("rate err = %+v", err)
	}
	if _, err := m.GetAuthz("z2", 20); !isKind(err, KindNotFound) {
		t.Fatalf("half-built y z2: %v", err)
	}
	if _, err := m.GetOrder("o2", 20); !isKind(err, KindNotFound) {
		t.Fatalf("half-built order: %v", err)
	}
}

func TestQuotaBoundaryAndNoHalfBuild(t *testing.T) {
	m, _ := New(Config{Ta: 1000, Tv: 1000, To: 500, H: 60, F: 10, C: 30, Pm: 3})
	acc := []byte("acc")
	_, _ = m.NewOrder(acc, []string{"a.com", "b.com", "c.com"}, m.Nonce(), 0)
	// p+q == Pm passes: two pending, one reusable valid -> q=1.
	if err := m.Report("z1", true, 5); err != nil {
		t.Fatal(err)
	}
	o, err := m.NewOrder(acc, []string{"a.com", "d.com"}, m.Nonce(), 6)
	if err != nil || o.AuthzIDs[0] != "z1" || o.AuthzIDs[1] != "z4" {
		t.Fatalf("boundary pass = %+v %v", o, err)
	}
	// One over the limit is rejected without half-built objects.
	n := m.Nonce()
	_, err = m.NewOrder(acc, []string{"e.com", "f.com"}, n, 7)
	e, ok := err.(*Error)
	if !ok || e.Kind != KindQuota || e.Pending != 3 || e.Need != 2 {
		t.Fatalf("quota err = %+v", err)
	}
	if _, err := m.GetAuthz("z5", 7); !isKind(err, KindNotFound) {
		t.Fatalf("half-built authz: %v", err)
	}
	// The kept nonce works after deactivation frees a slot.
	if err := m.Deactivate(acc, "z2", m.Nonce(), 8); err != nil {
		t.Fatal(err)
	}
	if err := m.Deactivate(acc, "z3", m.Nonce(), 9); err != nil {
		t.Fatal(err)
	}
	if _, err := m.NewOrder(acc, []string{"e.com", "f.com"}, n, 10); err != nil {
		t.Fatalf("after release = %v", err)
	}
}

func TestQuotaReleases(t *testing.T) {
	m, _ := New(Config{Ta: 100, Tv: 1000, To: 500, H: 60, F: 10, C: 30, Pm: 2})
	acc := []byte("acc")
	_, _ = m.NewOrder(acc, []string{"a.com", "b.com"}, m.Nonce(), 0)
	// Both pending: quota full.
	if _, err := m.NewOrder(acc, []string{"c.com"}, m.Nonce(), 1); !isKind(err, KindQuota) {
		t.Fatalf("full = %v", err)
	}
	// Expiry releases quota.
	if _, err := m.NewOrder(acc, []string{"c.com"}, m.Nonce(), 100); err != nil {
		t.Fatalf("expiry release = %v", err)
	}
}
