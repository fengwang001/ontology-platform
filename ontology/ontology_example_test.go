package ontology

import "testing"

func TestSpecWalkthrough(t *testing.T) {
	m, err := New(Config{Ta: 100, Tv: 1000, To: 500, H: 60, F: 2, C: 3, Pm: 3})
	if err != nil {
		t.Fatal(err)
	}
	if n1, n2, n3, n4 := m.Nonce(), m.Nonce(), m.Nonce(), m.Nonce(); n1 != 1 || n2 != 2 || n3 != 3 || n4 != 4 {
		t.Fatalf("nonce seq = %d %d %d %d", n1, n2, n3, n4)
	}

	acc := []byte("acc")
	if _, err := m.NewOrder(acc, []string{"a.com", "b.com"}, 1, 0); !isKind(err, KindNonce) {
		t.Fatalf("nonce 1: %v", err)
	}
	o1, err := m.NewOrder(acc, []string{"a.com", "b.com"}, 2, 0)
	if err != nil || o1.Expires != 500 || len(o1.AuthzIDs) != 2 ||
		o1.AuthzIDs[0] != "z1" || o1.AuthzIDs[1] != "z2" {
		t.Fatalf("o1 = %+v err %v", o1, err)
	}
	z1, _ := m.GetAuthz("z1", 0)
	if z1.Status != "pending" || z1.Expires != 100 {
		t.Fatalf("z1 = %+v", z1)
	}

	if err := m.Report("z1", true, 10); err != nil {
		t.Fatal(err)
	}
	if s, _ := m.Status("o1", 10); s != "pending" {
		t.Fatalf("status@10 = %s", s)
	}
	if err := m.Report("z2", true, 20); err != nil {
		t.Fatal(err)
	}
	if s, _ := m.Status("o1", 20); s != "ready" {
		t.Fatalf("status@20 = %s", s)
	}
	sn, err := m.Finalize(acc, "o1", []string{"b.com", "a.com"}, 3, 30)
	if err != nil || sn != 1 {
		t.Fatalf("finalize = %d %v", sn, err)
	}
	if _, err := m.Finalize(acc, "o1", []string{"b.com", "a.com"}, 4, 30); !isKind(err, KindState) {
		t.Fatalf("refinalize = %v", err)
	}
	// nonce 4 stays in the pool.
	o2, err := m.NewOrder(acc, []string{"a.com", "c.com"}, 4, 450)
	if err != nil {
		t.Fatal(err)
	}
	if o2.Expires != 950 || len(o2.AuthzIDs) != 2 ||
		o2.AuthzIDs[0] != "z1" || o2.AuthzIDs[1] != "z3" {
		t.Fatalf("o2 = %+v", o2)
	}
	if s, _ := m.Status("o2", 549); s != "pending" {
		t.Fatalf("o2@549 = %s", s)
	}
	if s, _ := m.Status("o2", 550); s != "invalid" {
		t.Fatalf("o2@550 = %s", s)
	}

	n := m.Nonce()
	o3, err := m.NewOrder(acc, []string{"a.com"}, n, 1005)
	if err != nil {
		t.Fatal(err)
	}
	if o3.Expires != 1505 {
		t.Fatalf("o3 expires = %d", o3.Expires)
	}
	if s, _ := m.Status("o3", 1005); s != "ready" {
		t.Fatalf("o3@1005 = %s", s)
	}
	if s, _ := m.Status("o3", 1010); s != "invalid" {
		t.Fatalf("o3@1010 = %s", s)
	}
}

func TestRateLimitWindow(t *testing.T) {
	m, _ := New(Config{Ta: 200, Tv: 1000, To: 500, H: 60, F: 2, C: 100, Pm: 3})
	n1, n2, n3 := m.Nonce(), m.Nonce(), m.Nonce()
	if _, err := m.NewOrder([]byte("acc"), []string{"c.com"}, n1, 0); err != nil {
		t.Fatal(err)
	}
	if err := m.Report("z1", false, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := m.NewOrder([]byte("acc"), []string{"c.com"}, n2, 105); err != nil {
		t.Fatal(err)
	}
	if err := m.Report("z2", false, 130); err != nil {
		t.Fatal(err)
	}
	_, err := m.NewOrder([]byte("acc"), []string{"c.com"}, n3, 159)
	if !isKind(err, KindRateLimited) {
		t.Fatalf("159 = %v", err)
	}
	n4 := m.Nonce() // n3 was not consumed
	if n4 == n3 {
		t.Fatal("nonce reused")
	}
	if _, err := m.NewOrder([]byte("acc"), []string{"c.com"}, n3, 160); err != nil {
		t.Fatalf("160 = %v", err)
	}
}

func TestQuotaExample(t *testing.T) {
	mk := func() *Machine {
		m, _ := New(Config{Ta: 100, Tv: 1000, To: 500, H: 60, F: 2, C: 100, Pm: 3})
		return m
	}
	acc := []byte("acc")

	m := mk()
	n1, n2 := m.Nonce(), m.Nonce()
	if _, err := m.NewOrder(acc, []string{"a.com", "b.com", "c.com"}, n1, 0); err != nil {
		t.Fatal(err)
	}
	_, err := m.NewOrder(acc, []string{"d.com"}, n2, 99)
	if !isKind(err, KindQuota) {
		t.Fatalf("quota = %v", err)
	}

	m = mk()
	n1, n2 = m.Nonce(), m.Nonce()
	if _, err := m.NewOrder(acc, []string{"a.com", "b.com", "c.com"}, n1, 0); err != nil {
		t.Fatal(err)
	}
	if err := m.Report("z1", true, 50); err != nil {
		t.Fatal(err)
	}
	if _, err := m.NewOrder(acc, []string{"d.com"}, n2, 99); err != nil {
		t.Fatalf("after report = %v", err)
	}

	m = mk()
	n1, n2 = m.Nonce(), m.Nonce()
	if _, err := m.NewOrder(acc, []string{"a.com", "b.com", "c.com"}, n1, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := m.NewOrder(acc, []string{"d.com"}, n2, 100); err != nil {
		t.Fatalf("after expiry = %v", err)
	}

	// Other accounts do not share quota.
	m = mk()
	n1, n2 = m.Nonce(), m.Nonce()
	if _, err := m.NewOrder([]byte("a1"), []string{"a.com", "b.com", "c.com"}, n1, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := m.NewOrder([]byte("a2"), []string{"a.com", "b.com", "c.com"}, n2, 0); err != nil {
		t.Fatalf("other account = %v", err)
	}
}

func isKind(err error, kind string) bool {
	e, ok := err.(*Error)
	return ok && e.Kind == kind
}
