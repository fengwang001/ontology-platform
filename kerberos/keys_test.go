package kerberos

import (
	"reflect"
	"testing"
)

func TestRejectedAuthNotCachedAndExpired(t *testing.T) {
	k := mustKDC(t, 100, 1000, 5, 50)
	t1, _ := k.IssueTGT([]byte("a"), 0, 200, 0, 10)
	// skew rejection must not cache
	if err := k.Authenticate(t1.ID, 0, 20); reasonOf(err) != ReasonClockSkew {
		t.Fatal(err)
	}
	if err := k.Authenticate(t1.ID, 11, 15); err != nil {
		t.Fatalf("skew-rejected auth must not populate cache: %v", err)
	}
	// expired-ticket rejection must not cache: issue a TGT [0,30) and renew
	// nothing; the TGS at 15 yields a service ticket [15,30); auth at 30 fails.
	g, _ := k.IssueTGT([]byte("b"), 0, 100, 0, 16)     // [16,100)
	st, err := k.TGS(g.ID, []byte("web"), 1000, 0, 17) // [17,100)
	if err != nil || st.End != 100 {
		t.Fatalf("service: %+v %v", st, err)
	}
	// an expired ticket cannot be created without first advancing beyond all
	// live clocks; instead directly assert failed auths do not cache, using a
	// ticket that expires: issue another short chain at a later now.
	g2, _ := k.IssueTGT([]byte("c"), 0, 100, 0, 90) // [90,100)
	if err := k.Authenticate(g2.ID, 100, 100); reasonOf(err) != ReasonExpired {
		t.Fatalf("want expired, got %v", err)
	}
	if _, cached := k.cache.m[replayKey{id: g2.ID, time: 100}]; cached {
		t.Fatal("expired rejection must not populate cache")
	}
}

func TestChangeKeyIssuedBoundary(t *testing.T) {
	// issued == 10; key change at 10 keeps the ticket valid
	k := mustKDC(t, 100, 1000, 5, 50)
	t1, _ := k.IssueTGT([]byte("alice"), 0, 500, 2000, 10)
	if err := k.ChangeKey([]byte("alice"), 10); err != nil {
		t.Fatal(err)
	}
	if _, err := k.Renew(t1.ID, 105); err != nil {
		t.Fatalf("issued == c should survive: %v", err)
	}
	// a later change invalidates: renewal does not refresh issued
	if err := k.ChangeKey([]byte("alice"), 106); err != nil {
		t.Fatal(err)
	}
	if _, err := k.Renew(t1.ID, 107); reasonOf(err) != ReasonKeyChanged {
		t.Fatalf("renewal must not refresh issued: %v", err)
	}
}

func TestChangeKeyAfterIssue(t *testing.T) {
	k := mustKDC(t, 100, 1000, 5, 50)
	t1, _ := k.IssueTGT([]byte("alice"), 0, 500, 2000, 10)
	if _, err := k.Renew(t1.ID, 100); err != nil {
		t.Fatal(err)
	}
	if err := k.ChangeKey([]byte("alice"), 120); err != nil {
		t.Fatal(err)
	}
	if _, err := k.Renew(t1.ID, 130); reasonOf(err) != ReasonKeyChanged {
		t.Fatal(err)
	}
	// TGT key change also invalidates a service ticket of same subject
	g, _ := k.IssueTGT([]byte("bob"), 0, 500, 0, 200)
	st, _ := k.TGS(g.ID, []byte("web"), 500, 0, 210)
	if err := k.ChangeKey([]byte("bob"), 215); err != nil {
		t.Fatal(err)
	}
	if err := k.Authenticate(st.ID, 220, 220); reasonOf(err) != ReasonKeyChanged {
		t.Fatalf("service ticket key check: %v", err)
	}
	if _, cached := k.cache.m[replayKey{id: st.ID, time: 220}]; cached {
		t.Fatal("key-changed rejection must not populate cache")
	}
}

func TestPostdatedKeyChanged(t *testing.T) {
	k := mustKDC(t, 100, 1000, 5, 50)
	tb, _ := k.IssueTGT([]byte("bob"), 40, 300, 0, 10) // issued=10
	if err := k.ChangeKey([]byte("bob"), 30); err != nil {
		t.Fatal(err)
	}
	if _, err := k.Validate(tb.ID, 45); reasonOf(err) != ReasonKeyChanged {
		t.Fatalf("postdated validate key check: %v", err)
	}
}

func TestServiceTicketIssuedAtTGS(t *testing.T) {
	k := mustKDC(t, 100, 1000, 5, 50)
	g, _ := k.IssueTGT([]byte("alice"), 0, 500, 0, 10)
	// key change before the service ticket issue: TGT becomes unusable
	if err := k.ChangeKey([]byte("alice"), 20); err != nil {
		t.Fatal(err)
	}
	if _, err := k.TGS(g.ID, []byte("web"), 500, 0, 30); reasonOf(err) != ReasonKeyChanged {
		t.Fatalf("tgs key check: %v", err)
	}
}

func TestRejectedOpLeavesStateAndClock(t *testing.T) {
	k := mustKDC(t, 100, 1000, 5, 50)
	t1, _ := k.IssueTGT([]byte("a"), 0, 500, 0, 100)
	before, _ := k.ticket(t1.ID)
	if _, err := k.Renew(t1.ID, 101); reasonOf(err) != ReasonNotRenewable {
		t.Fatal(err)
	}
	// clock rewind rejected, maxNow unchanged
	if err := k.ChangeKey([]byte("x"), 99); reasonOf(err) != ReasonClockRewind {
		t.Fatalf("rewind: %v", err)
	}
	if k.MaxNow() != 100 {
		t.Fatalf("maxNow = %d", k.MaxNow())
	}
	after, _ := k.ticket(t1.ID)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("ticket mutated by rejected ops")
	}
	if k.nextID != 1 {
		t.Fatalf("nextID = %d", k.nextID)
	}
}

func TestServiceNameValidation(t *testing.T) {
	k := mustKDC(t, 100, 1000, 5, 50)
	g, _ := k.IssueTGT([]byte("a"), 0, 500, 0, 10)
	if _, err := k.TGS(g.ID, nil, 500, 0, 20); reasonOf(err) != ReasonInvalidParam {
		t.Fatalf("empty service: %v", err)
	}
	if _, err := k.TGS(g.ID, []byte("krbtgt"), 500, 0, 20); reasonOf(err) != ReasonInvalidParam {
		t.Fatalf("krbtgt service: %v", err)
	}
	if _, err := k.IssueTGT(nil, 0, 500, 0, 20); reasonOf(err) != ReasonInvalidParam {
		t.Fatalf("empty subject: %v", err)
	}
	nonTGT, _ := k.TGS(g.ID, []byte("web"), 500, 0, 20)
	if _, err := k.TGS(nonTGT.ID, []byte("db"), 500, 0, 21); reasonOf(err) != ReasonNotTGT {
		t.Fatalf("service ticket as TGT: %v", err)
	}
}

func TestOutOfBoundsParams(t *testing.T) {
	k := mustKDC(t, 100, 1000, 5, 50)
	g, _ := k.IssueTGT([]byte("a"), 0, 500, 0, 0)
	cases := []error{
		func() error { _, e := k.IssueTGT([]byte("a"), 0, 500, 0, -1); return e }(),
		func() error { _, e := k.IssueTGT([]byte("a"), 0, 500, maxTime+1, 5); return e }(),
		func() error { _, e := k.TGS(g.ID, []byte("web"), 500, 0, maxTime+1); return e }(),
		func() error { return k.Authenticate(g.ID, -1, 5) }(),
	}
	for i, e := range cases {
		if reasonOf(e) != ReasonInvalidParam {
			t.Fatalf("case %d: %v", i, e)
		}
	}
}
