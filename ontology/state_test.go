package ontology

import "testing"

func newTestMachine(t *testing.T) *Machine {
	t.Helper()
	m, err := New(Config{Ta: 100, Tv: 1000, To: 500, H: 60, F: 3, C: 30, Pm: 20})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestConfigRejected(t *testing.T) {
	bad := []Config{
		{Ta: 0, Tv: 1, To: 1, H: 1, F: 1, C: 1, Pm: 1},
		{Ta: 1, Tv: 1e9 + 1, To: 1, H: 1, F: 1, C: 1, Pm: 1},
		{Ta: 1, Tv: 1, To: 1, H: 1, F: 0, C: 1, Pm: 1},
		{Ta: 1, Tv: 1, To: 1, H: 1, F: 1001, C: 1, Pm: 1},
		{Ta: 1, Tv: 1, To: 1, H: 1, F: 1, C: 0, Pm: 1},
		{Ta: 1, Tv: 1, To: 1, H: 1, F: 1, C: 1e6 + 1, Pm: 1},
		{Ta: 1, Tv: 1, To: 1, H: 1, F: 1, C: 1, Pm: 0},
		{Ta: 1, Tv: 1, To: 1, H: 1, F: 1, C: 1, Pm: 10001},
	}
	for i, c := range bad {
		if _, err := New(c); !isKind(err, KindConfig) {
			t.Fatalf("case %d: %v", i, err)
		}
	}
}

func TestIdentifierValidation(t *testing.T) {
	m := newTestMachine(t)
	n := m.Nonce()
	bad := [][]string{
		{""},
		{"UPPER.com"},
		{"un der"},
		{"a*b"},
		{"*"},
		{"*."},
		{"*.A.com"},
	}
	for i, ids := range bad {
		if _, err := m.NewOrder([]byte("acc"), ids, n, 0); !isKind(err, KindParam) {
			t.Fatalf("case %d: %v", i, err)
		}
	}
	n1, n2 := m.Nonce(), m.Nonce()
	o, err := m.NewOrder([]byte("acc"), []string{"a.com", "*.a.com"}, n1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(o.AuthzIDs) != 2 {
		t.Fatalf("wildcard ids = %v", o.AuthzIDs)
	}
	if _, err := m.NewOrder([]byte("acc"), []string{"a.com", "a.com"}, n2, 0); !isKind(err, KindParam) {
		t.Fatalf("duplicate: %v", err)
	}
}

func TestNoncePoolEviction(t *testing.T) {
	m, _ := New(Config{Ta: 100, Tv: 1000, To: 500, H: 60, F: 3, C: 5, Pm: 20})
	for i := 0; i < 7; i++ {
		m.Nonce()
	}
	for _, n := range []int64{1, 2} {
		if _, err := m.NewOrder([]byte("a"), []string{"x.com"}, n, 0); !isKind(err, KindNonce) {
			t.Fatalf("evicted nonce %d: %v", n, err)
		}
	}
	// A rejected (parameter-invalid) call keeps the nonce.
	if _, err := m.NewOrder([]byte("a"), []string{}, 7, 0); !isKind(err, KindParam) {
		t.Fatalf("param = %v", err)
	}
	if _, err := m.NewOrder([]byte("a"), []string{"x.com"}, 7, 0); err != nil {
		t.Fatalf("use after rejection = %v", err)
	}
	if _, err := m.NewOrder([]byte("a"), []string{"y.com"}, 7, 1); !isKind(err, KindNonce) {
		t.Fatalf("consumed nonce: %v", err)
	}
}

func TestOrderStatusFiveWays(t *testing.T) {
	m := newTestMachine(t)
	acc := []byte("acc")

	// Create every order at now=0 up front; drive time forward afterwards.
	var err error
	var o1, o2, o3, o4, of *Order
	o1, err = m.NewOrder(acc, []string{"a.com", "b.com"}, m.Nonce(), 0)
	if err != nil {
		t.Fatal(err)
	}
	o2, err = m.NewOrder(acc, []string{"c.com", "d.com"}, m.Nonce(), 0)
	if err != nil {
		t.Fatal(err)
	}
	o3, err = m.NewOrder(acc, []string{"e.com", "f.com"}, m.Nonce(), 0)
	if err != nil {
		t.Fatal(err)
	}
	o4, err = m.NewOrder(acc, []string{"g.com"}, m.Nonce(), 0)
	if err != nil {
		t.Fatal(err)
	}
	of, err = m.NewOrder(acc, []string{"h.com"}, m.Nonce(), 0)
	if err != nil {
		t.Fatal(err)
	}

	// pending
	if o1.Status != "pending" {
		t.Fatalf("new = %+v", o1)
	}
	// ready
	if err := m.Report("z1", true, 10); err != nil {
		t.Fatal(err)
	}
	if err := m.Report("z2", true, 10); err != nil {
		t.Fatal(err)
	}
	if s, _ := m.Status("o1", 10); s != "ready" {
		t.Fatalf("ready = %s", s)
	}
	// invalid via invalid authz
	if err := m.Report("z3", false, 11); err != nil {
		t.Fatal(err)
	}
	if s, _ := m.Status(o2.ID, 11); s != "invalid" {
		t.Fatalf("invalid-authz = %s", s)
	}
	// invalid via deactivated authz
	if err := m.Deactivate(acc, "z5", m.Nonce(), 12); err != nil {
		t.Fatal(err)
	}
	if s, _ := m.Status(o3.ID, 12); s != "invalid" {
		t.Fatalf("deactivated = %s", s)
	}
	// invalid via expired authz
	if s, _ := m.Status(o4.ID, 99); s != "pending" {
		t.Fatalf("99 = %s", s)
	}
	if s, _ := m.Status(o4.ID, 100); s != "invalid" {
		t.Fatalf("authz expiry = %s", s)
	}
	// finalized valid survives later authorization expiry (do this while the
	// clock is still before the pending expiry and the order expiry).
	if err := m.Report("z8", true, 20); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Finalize(acc, of.ID, []string{"h.com"}, m.Nonce(), 21); err != nil {
		t.Fatal(err)
	}
	if s, _ := m.Status(of.ID, 100000); s != "valid" {
		t.Fatalf("valid after expiry = %s", s)
	}

	// invalid via order expiry
	if s, _ := m.Status("o1", 500); s != "invalid" {
		t.Fatalf("order expiry = %s", s)
	}
}

func TestExpiryBoundaryAndExpiredReport(t *testing.T) {
	m := newTestMachine(t)
	_, _ = m.NewOrder([]byte("a"), []string{"x.com"}, m.Nonce(), 0)
	if z, _ := m.GetAuthz("z1", 99); z.Status != "pending" {
		t.Fatalf("99 = %s", z.Status)
	}
	if z, _ := m.GetAuthz("z1", 100); z.Status != "expired" {
		t.Fatalf("100 = %s", z.Status)
	}
	if err := m.Report("z1", true, 100); !isKind(err, KindState) {
		t.Fatalf("report expired = %v", err)
	}
	if z, _ := m.GetAuthz("z1", 100); z.Status != "expired" {
		t.Fatalf("mutated after rejected report: %s", z.Status)
	}
}
