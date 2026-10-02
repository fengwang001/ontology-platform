package kerberos

import (
	"errors"
	"testing"
)

func mustKDC(t *testing.T, L, R, S, P int64) *KDC {
	t.Helper()
	k, err := NewKDC(L, R, S, P)
	if err != nil {
		t.Fatalf("NewKDC: %v", err)
	}
	return k
}

func reasonOf(err error) string {
	if err == nil {
		return ""
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Reason
	}
	return err.Error()
}

func (k *KDC) ticket(id int64) (*Ticket, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	t, ok := k.tickets[id]
	if !ok {
		return nil, newError("", ReasonTicketNotFound, "")
	}
	return cloneTicket(t), nil
}

func TestConfigInvalid(t *testing.T) {
	bad := [][4]int64{
		{0, 1000, 5, 50},
		{100, 1000, 0, 50},
		{100, 1000, 5, 0},
		{101, 100, 5, 50}, // L > R
		{1_000_000_001, 1_000_000_001, 5, 50},
		{-1, 1000, 5, 50},
	}
	for i, c := range bad {
		if _, err := NewKDC(c[0], c[1], c[2], c[3]); reasonOf(err) != ReasonConfigInvalid {
			t.Fatalf("case %d: got %v, want config_invalid", i, err)
		}
	}
}

func TestIssueTGTEndMin(t *testing.T) {
	k := mustKDC(t, 100, 1000, 5, 50)
	t1, err := k.IssueTGT([]byte("a"), 0, 80, 0, 10) // end = till
	if err != nil || t1.End != 80 {
		t.Fatalf("end = %d, err=%v", t1.End, err)
	}
	t2, err := k.IssueTGT([]byte("a"), 0, 500, 0, 20) // end = s+L
	if err != nil || t2.End != 120 {
		t.Fatalf("end = %d, err=%v", t2.End, err)
	}
	if t1.ID != 1 || t2.ID != 2 {
		t.Fatalf("ids = %d,%d", t1.ID, t2.ID)
	}
}

func TestRenewTillEqualityBoundary(t *testing.T) {
	k := mustKDC(t, 100, 1000, 5, 50)
	eq, err := k.IssueTGT([]byte("a"), 0, 110, 110, 10) // rt == end
	if err != nil || eq.RenewTill != 0 {
		t.Fatalf("rt==end: RenewTill=%d err=%v", eq.RenewTill, err)
	}
	gt, err := k.IssueTGT([]byte("a"), 0, 110, 111, 10) // rt == end+1
	if err != nil || gt.RenewTill != 111 {
		t.Fatalf("rt==end+1: RenewTill=%d err=%v", gt.RenewTill, err)
	}
}

func TestPostdateBoundary(t *testing.T) {
	k := mustKDC(t, 100, 1000, 5, 50)
	ok, err := k.IssueTGT([]byte("a"), 60, 300, 0, 10) // start == now+P
	if err != nil || !ok.Invalid || ok.Start != 60 {
		t.Fatalf("boundary ok: %+v err=%v", ok, err)
	}
	if _, err = k.IssueTGT([]byte("a"), 111, 300, 0, 60); reasonOf(err) != ReasonPostdatedTooFar {
		t.Fatalf("start == now+P+1: %v", err)
	}
	t3, err := k.IssueTGT([]byte("a"), 0, 300, 0, 60)
	if err != nil || t3.Start != 60 || t3.Invalid {
		t.Fatalf("zero start: %+v err=%v", t3, err)
	}
}

func TestPostdatedMustValidate(t *testing.T) {
	k := mustKDC(t, 100, 1000, 5, 50)
	tb, err := k.IssueTGT([]byte("bob"), 40, 300, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := k.TGS(tb.ID, []byte("web"), 1000, 0, 20); reasonOf(err) != ReasonNotYetValid {
		t.Fatalf("tgs early: %v", err)
	}
	if _, err := k.Validate(tb.ID, 30); reasonOf(err) != ReasonNotYetValid {
		t.Fatalf("validate early: %v", err)
	}
	// now == start: started, but invalid flag still blocks TGS
	if _, err := k.TGS(tb.ID, []byte("web"), 1000, 0, 40); reasonOf(err) != ReasonInvalidFlag {
		t.Fatalf("tgs at start unvalidated: %v", err)
	}
	other, _ := k.IssueTGT([]byte("carol"), 0, 300, 0, 40)
	if _, err := k.Validate(other.ID, 40); reasonOf(err) != ReasonNotPostdated {
		t.Fatalf("validate normal ticket: %v", err)
	}
	v, err := k.Validate(tb.ID, 45)
	if err != nil || v.Invalid {
		t.Fatalf("validate: %+v err=%v", v, err)
	}
	st, err := k.TGS(tb.ID, []byte("web"), 1000, 0, 45)
	if err != nil || st.Start != 45 || st.End != 140 {
		t.Fatalf("tgs after validate: %+v err=%v", st, err)
	}
}

func TestServiceTicketCappedByTGT(t *testing.T) {
	k := mustKDC(t, 100, 1000, 5, 50)
	g, _ := k.IssueTGT([]byte("alice"), 0, 500, 2000, 10)
	g2, err := k.Renew(g.ID, 100) // [100,200), rt=1010
	if err != nil {
		t.Fatal(err)
	}
	st, err := k.TGS(g2.ID, []byte("web"), 1000, 1500, 150)
	if err != nil || st.End != 200 || st.RenewTill != 1010 || st.Start != 150 {
		t.Fatalf("service: %+v %v", st, err)
	}
	// renewing TGT afterwards must not extend the service ticket
	if _, err := k.Renew(g2.ID, 190); err != nil {
		t.Fatal(err)
	}
	stAgain, _ := k.ticket(st.ID)
	if stAgain.End != 200 {
		t.Fatalf("service end changed to %d", stAgain.End)
	}
	// service renewal is independent of TGT end
	// (the service ticket is currently [150,200) with life 50; renew at 195
	// after the TGT renewal at 190 -> e'=245, independent of TGT end)
	sr, err := k.Renew(st.ID, 195)
	if err != nil || sr.End != 245 {
		t.Fatalf("service renew: %+v %v", sr, err)
	}
}

func TestRenewalUsesCurrentLife(t *testing.T) {
	k := mustKDC(t, 100, 1000, 5, 50)
	t1, _ := k.IssueTGT([]byte("alice"), 0, 500, 2000, 10)
	type step struct{ now, start, end int64 }
	steps := []step{
		{100, 100, 200}, {190, 190, 290}, {280, 280, 380},
		{370, 370, 470}, {460, 460, 560}, {550, 550, 650},
		{640, 640, 740}, {730, 730, 830}, {820, 820, 920}, {910, 910, 1010},
	}
	cur := t1
	for _, s := range steps {
		cur, _ = k.Renew(t1.ID, s.now)
		if cur.Start != s.start || cur.End != s.end {
			t.Fatalf("at %d: got [%d,%d) want [%d,%d)", s.now, cur.Start, cur.End, s.start, s.end)
		}
	}
	if _, err := k.Renew(t1.ID, 1000); reasonOf(err) != ReasonRenewLimit {
		t.Fatalf("ceiling renew (e'==end): %v", err)
	}
}

func TestExpiredAtEnd(t *testing.T) {
	k := mustKDC(t, 100, 1000, 5, 50)
	t1, _ := k.IssueTGT([]byte("a"), 0, 110, 1000, 10) // [10,110)
	if _, err := k.Renew(t1.ID, 110); reasonOf(err) != ReasonExpired {
		t.Fatalf("renew at end: %v", err)
	}
	if err := k.Authenticate(t1.ID, 110, 110); reasonOf(err) != ReasonExpired {
		t.Fatalf("auth at end: %v", err)
	}
	if err := k.Authenticate(t1.ID, 109, 109); err != nil {
		t.Fatalf("auth at 109: %v", err)
	}
}

func TestSkewBoundaryAndOrder(t *testing.T) {
	k := mustKDC(t, 100, 1000, 5, 50)
	t1, _ := k.IssueTGT([]byte("a"), 0, 200, 0, 10)
	if err := k.Authenticate(t1.ID, 15, 20); err != nil { // |diff| == S
		t.Fatalf("skew=S: %v", err)
	}
	expired, _ := k.IssueTGT([]byte("b"), 0, 40, 0, 25)                              // [25,35)
	if err := k.Authenticate(expired.ID, 25, 31); reasonOf(err) != ReasonClockSkew { // skew 6
		t.Fatalf("skew precedes expiry: %v", err)
	}
	if err := k.Authenticate(t1.ID, 15, 31); reasonOf(err) != ReasonClockSkew {
		t.Fatalf("skew precedes replay: %v", err)
	}
}

func TestReplayWindow(t *testing.T) {
	k := mustKDC(t, 100, 1000, 5, 50)
	t1, _ := k.IssueTGT([]byte("a"), 0, 1000, 0, 0) // [0,100)
	if err := k.Authenticate(t1.ID, 10, 15); err != nil {
		t.Fatal(err)
	}
	// now == authTime+S: entry still present
	if err := k.Authenticate(t1.ID, 10, 15); reasonOf(err) != ReasonReplay {
		t.Fatalf("exact boundary replay: %v", err)
	}
	if k.MaxNow() != 15 {
		t.Fatalf("maxNow = %d", k.MaxNow())
	}
	// advance time one more via an unrelated accepted op
	if err := k.ChangeKey([]byte("someone-else"), 16); err != nil {
		t.Fatal(err)
	}
	// same authTime now necessarily fails skew first; the important property
	// is that the entry no longer occupies the cache:
	if _, cached := k.cache.m[replayKey{id: t1.ID, time: 10}]; cached {
		t.Fatal("entry at authTime+S+1 should have been reclaimed")
	}
	if err := k.Authenticate(t1.ID, 11, 16); err != nil {
		t.Fatalf("different authTime collides: %v", err)
	}
}
