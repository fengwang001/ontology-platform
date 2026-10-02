package ontology

import "testing"

func TestSpecExample(t *testing.T) {
	cfg := baseCfg()
	s := mustNew(t, cfg)
	T0, err := s.Mint([]byte("t1"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	T1, _ := Attenuate(cfg, T0, []byte("ops:read,list"))
	T2, _ := Attenuate(cfg, T1, []byte("exp:100"))
	T2b, _ := Attenuate(cfg, T1, []byte("res:/a"))
	r, err := s.Revoke(T2, 2, 10)
	if err != nil || !r.Revoked {
		t.Fatalf("revoke: %v %+v", err, r)
	}
	e := asErr(t, s.Verify(T2, Request{Op: "read", Res: "/x", Amount: 0}, 99))
	if e.Kind != RejectRevoked || e.Prefix != 2 {
		t.Fatalf("T2@99: %+v", e)
	}
	e = asErr(t, s.Verify(T2b, Request{Op: "read", Res: "/x", Amount: 0}, 99))
	if e.Kind != RejectCaveat || e.Index != 2 {
		t.Fatalf("T2b@99: %+v", e)
	}
	e = asErr(t, s.Verify(T2, Request{Op: "read", Res: "/x", Amount: 0}, 100))
	if e.Kind != RejectCaveat || e.Index != 2 {
		t.Fatalf("T2@100: %+v", e)
	}
	r, err = s.Revoke(T2, 2, 100)
	if err != nil || !r.Expired {
		t.Fatalf("expired revoke: %v %+v", err, r)
	}
	e = asErr(t, s.Verify(T0, Request{Op: "read", Res: "/x", Amount: 0}, 15))
	if e.Kind != RejectClockRollback {
		t.Fatalf("rollback: %+v", e)
	}

	// Fresh instance: short prefix covers descendants, not ancestor/siblings.
	s2 := mustNew(t, cfg)
	if _, err := s2.Revoke(T2, 1, 20); err != nil {
		t.Fatal(err)
	}
	e = asErr(t, s2.Verify(T2, Request{Op: "read", Res: "/x", Amount: 0}, 99))
	if e.Kind != RejectRevoked || e.Prefix != 1 {
		t.Fatalf("j=1 priority: %+v", e)
	}
	e = asErr(t, s2.Verify(T1, Request{Op: "read", Res: "/x", Amount: 0}, 99))
	if e.Kind != RejectRevoked || e.Prefix != 1 {
		t.Fatalf("T1 revoked: %+v", e)
	}
	e = asErr(t, s2.Verify(T2b, Request{Op: "read", Res: "/a", Amount: 0}, 99))
	if e.Kind != RejectRevoked || e.Prefix != 1 {
		t.Fatalf("T2b sibling descendant revoked: %+v", e)
	}
	if err := s2.Verify(T0, Request{Op: "read", Res: "/x", Amount: 0}, 99); err != nil {
		t.Fatalf("T0 ancestor must be unaffected: %v", err)
	}
}

func TestRevokeBoundMinExp(t *testing.T) {
	cfg := baseCfg()
	s := mustNew(t, cfg)
	T0, _ := s.Mint([]byte("e"), nil, 0)
	T1, _ := Attenuate(cfg, T0, []byte("exp:200"))
	T2, _ := Attenuate(cfg, T1, []byte("exp:100"))
	T3, _ := Attenuate(cfg, T2, []byte("exp:150"))
	// k=3: bound = min(200,100,150) = 100; boundary now=100 => expired.
	r, err := s.Revoke(T3, 3, 99)
	if err != nil || !r.Revoked {
		t.Fatalf("revoke@99: %v %+v", err, r)
	}
	if st := s.Snapshot(); st.ActiveRecords != 1 {
		t.Fatalf("active=%d", st.ActiveRecords)
	}
	r, err = s.Revoke(T3, 3, 100)
	if err != nil || !r.Expired {
		t.Fatalf("boundary expiry: %v %+v", err, r)
	}
	if st := s.Snapshot(); st.ActiveRecords != 0 || st.HeapPops != 1 {
		t.Fatalf("post-expiry state: %+v", st)
	}
	// k=1 at now=100 has bound 200 -> still live.
	r, err = s.Revoke(T3, 1, 100)
	if err != nil || !r.Revoked {
		t.Fatalf("k=1: %v %+v", err, r)
	}
	if st := s.Snapshot(); st.ActiveRecords != 1 {
		t.Fatalf("active=%d", st.ActiveRecords)
	}
}

func TestMalformedExpIgnoredInBound(t *testing.T) {
	cfg := baseCfg()
	s := mustNew(t, cfg)
	T0, _ := s.Mint([]byte("g"), nil, 0)
	T1, _ := Attenuate(cfg, T0, []byte("exp:00"))
	T2, _ := Attenuate(cfg, T1, []byte("exp:50"))
	if _, err := s.Revoke(T2, 2, 10); err != nil {
		t.Fatal(err)
	}
	if st := s.Snapshot(); st.ActiveRecords != 1 {
		t.Fatalf("bound must ignore malformed exp: %+v", st)
	}
	U0, _ := s.Mint([]byte("g2"), nil, 20)
	U1, _ := Attenuate(cfg, U0, []byte("exp:00"))
	if _, err := s.Revoke(U1, 1, 30); err != nil {
		t.Fatal(err)
	}
	e := asErr(t, s.Verify(U1, Request{Op: "x", Res: "/", Amount: 0}, 99))
	if e.Kind != RejectRevoked {
		t.Fatalf("infinite bound record must persist: %+v", e)
	}
}

func TestExpiredRecordInvisibleAndReclaimed(t *testing.T) {
	cfg := baseCfg()
	s := mustNew(t, cfg)
	T0, _ := s.Mint([]byte("h"), nil, 0)
	T1, _ := Attenuate(cfg, T0, []byte("exp:100"))
	if _, err := s.Revoke(T1, 1, 0); err != nil {
		t.Fatal(err)
	}
	e := asErr(t, s.Verify(T1, Request{Op: "x", Res: "/", Amount: 0}, 99))
	if e.Kind != RejectRevoked {
		t.Fatalf("at 99: %+v", e)
	}
	e = asErr(t, s.Verify(T1, Request{Op: "x", Res: "/", Amount: 0}, 100))
	if e.Kind != RejectCaveat || e.Index != 1 {
		t.Fatalf("at 100 record must be invisible: %+v", e)
	}
	if st := s.Snapshot(); st.ActiveRecords != 0 || st.HeapPops != 1 {
		t.Fatalf("state: %+v", st)
	}
}

func TestRevokeLimitCountedAfterGC(t *testing.T) {
	cfg := baseCfg()
	cfg.Rm = 2
	s := mustNew(t, cfg)
	var tokens []*Token
	for i := 0; i < 2; i++ {
		tok, _ := s.Mint([]byte{byte('i' + i)}, nil, 0)
		t1, _ := Attenuate(cfg, tok, []byte("exp:100"))
		tokens = append(tokens, t1)
	}
	inf, _ := s.Mint([]byte("z"), nil, 1)
	if _, err := s.Revoke(tokens[0], 1, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Revoke(inf, 0, 20); err != nil {
		t.Fatal(err)
	}
	before := s.Snapshot()
	_, err := s.Revoke(tokens[1], 1, 30)
	if e := asErr(t, err); e.Kind != RejectLimit {
		t.Fatalf("want limit got %v", err)
	}
	after := s.Snapshot()
	if after.Clock != before.Clock || after.ActiveRecords != 2 || after.HeapPops != before.HeapPops {
		t.Fatalf("rejection changed state: before=%+v after=%+v", before, after)
	}
	// At now=100 the new record itself has bound 100: expired marker.
	r, err := s.Revoke(tokens[1], 1, 100)
	if err != nil {
		t.Fatalf("post-GC capacity: %v", err)
	}
	if !r.Expired {
		t.Fatalf("bound=100 at now=100 must report expired, got %+v", r)
	}
	if r, err := s.Revoke(inf, 0, 100); err != nil || !r.Revoked {
		t.Fatalf("idempotent: %v %+v", err, r)
	}
	if st := s.Snapshot(); st.ActiveRecords != 1 {
		t.Fatalf("active=%d", st.ActiveRecords)
	}
}

func TestRejectedOpsDoNotChangeState(t *testing.T) {
	cfg := baseCfg()
	s := mustNew(t, cfg)
	tok, _ := s.Mint([]byte("q"), [][]byte{[]byte("amt:5")}, 10)
	before := s.Snapshot()
	if e := asErr(t, s.Verify(tok, Request{Op: "", Res: "/", Amount: 0}, 20)); e.Kind != RejectInvalid {
		t.Fatalf("want invalid got %+v", e)
	}
	if st := s.Snapshot(); st != before {
		t.Fatalf("invalid op changed state: %+v", st)
	}
	if e := asErr(t, s.Verify(tok, Request{Op: "x", Res: "/", Amount: 0}, 5)); e.Kind != RejectClockRollback {
		t.Fatalf("want rollback got %+v", e)
	}
	if st := s.Snapshot(); st != before {
		t.Fatalf("rollback changed state: %+v", st)
	}
	mid := s.Snapshot()
	forged := &Token{ID: append([]byte(nil), tok.ID...), Caveats: tok.Caveats, Sig: []byte("forged")}
	if e := asErr(t, s.Verify(forged, Request{Op: "x", Res: "/", Amount: 0}, 20)); e.Kind != RejectBadSignature {
		t.Fatalf("want bad sig got %+v", e)
	}
	st := s.Snapshot()
	if st.Clock != 20 || st.MacCalls != mid.MacCalls+int64(len(tok.Caveats)+1) {
		t.Fatalf("bad-sig state: %+v", st)
	}
	if st.RevocationLookups != 0 {
		t.Fatalf("bad sig must perform 0 table queries, got %d", st.RevocationLookups)
	}
	_, rerr := s.Revoke(forged, 1, 25)
	if e := asErr(t, rerr); e.Kind != RejectBadSignature {
		t.Fatalf("revoke forged: %+v", e)
	}
	if st := s.Snapshot(); st.ActiveRecords != 0 {
		t.Fatalf("forged revoke inserted: %+v", st)
	}
}

func TestMacCallAndLookupCounters(t *testing.T) {
	cfg := baseCfg()
	ResetAttenuateCounter()
	s := mustNew(t, cfg)
	start := s.Snapshot()
	t0, _ := s.Mint([]byte("n"), nil, 0)
	if got := s.Snapshot().MacCalls - start.MacCalls; got != 1 {
		t.Fatalf("mint n=0 mac calls=%d", got)
	}
	t1, err := Attenuate(cfg, t0, []byte("amt:1"))
	if err != nil {
		t.Fatal(err)
	}
	if AttenuateMacCalls() != 1 {
		t.Fatal("attenuate must call mac exactly once")
	}
	t2, _ := Attenuate(cfg, t1, []byte("amt:2"))
	if AttenuateMacCalls() != 2 {
		t.Fatal("second attenuate must call mac once")
	}
	start = s.Snapshot()
	if err := s.Verify(t2, Request{Op: "x", Res: "/", Amount: 0}, 0); err != nil {
		t.Fatal(err)
	}
	if got := s.Snapshot().MacCalls - start.MacCalls; got != 3 {
		t.Fatalf("verify n=2 mac calls=%d want 3", got)
	}
	// Revoking prefix 0: verify stops after exactly 1 table query.
	if _, err := s.Revoke(t2, 0, 1); err != nil {
		t.Fatal(err)
	}
	s.ResetCounters()
	e := asErr(t, s.Verify(t2, Request{Op: "x", Res: "/", Amount: 0}, 2))
	if e.Kind != RejectRevoked || e.Prefix != 0 {
		t.Fatalf("want revoked j=0 got %+v", e)
	}
	if st := s.Snapshot(); st.RevocationLookups != 1 {
		t.Fatalf("lookup should stop at j=0: %d", st.RevocationLookups)
	}
	// Non-revoked token performs n+1 queries.
	u0, _ := s.Mint([]byte("n2"), [][]byte{[]byte("amt:1"), []byte("amt:2")}, 3)
	s.ResetCounters()
	if err := s.Verify(u0, Request{Op: "x", Res: "/", Amount: 0}, 3); err != nil {
		t.Fatal(err)
	}
	if st := s.Snapshot(); st.RevocationLookups != 3 {
		t.Fatalf("full scan queries=%d want 3", st.RevocationLookups)
	}
}
