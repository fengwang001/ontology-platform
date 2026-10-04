package session_test

import (
	"errors"
	"sync"
	"testing"

	"ontology/session"
)

func TestSpecExample(t *testing.T) {
	e := session.New(20, 5, 60, 10)
	mustOK(t, e.SetRate(1, 3, 0))
	mustOK(t, e.SetRate(2, 1, 0))
	mustOK(t, e.TopUp("a", 100, 0))
	mustOK(t, e.Open("s1", "a", 0))
	mustOK(t, e.Open("s2", "a", 0))

	r := mustUpdate(t, e, "s1", 1, 1, 0, 50, 0)
	assertReply(t, r, session.Reply{Granted: 20, ValidUntil: 60})
	r = mustUpdate(t, e, "s2", 1, 1, 0, 10, 10)
	assertReply(t, r, session.Reply{Granted: 13, Final: true, ValidUntil: 70})
	r = mustUpdate(t, e, "s2", 2, 2, 0, 5, 20)
	assertReply(t, r, session.Reply{Granted: 1, Final: true, ValidUntil: 80})
	r = mustUpdate(t, e, "s1", 2, 1, 25, 20, 30)
	assertReply(t, r, session.Reply{Charged: 20, Unbilled: 5, Final: true, Denied: true})
	r2, err := e.Update("s1", 2, 1, 99, 99, 31)
	if err != nil {
		t.Fatalf("retransmission returned error: %v", err)
	}
	assertReply(t, r2, r)

	mustOK(t, e.Close("s2", 3, map[int]int64{1: 13}, 70))
}

func TestDustBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name    string
		lmin    int64
		want    int64
		granted int64
		final   bool
	}{
		{name: "change equals lmin", lmin: 5, want: 28, granted: 28, final: false},
		{name: "change below lmin", lmin: 6, want: 30, granted: 33, final: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := session.New(30, tc.lmin, 100, 2)
			mustOK(t, e.SetRate(1, 3, 0))
			mustOK(t, e.TopUp("a", 100, 0))
			mustOK(t, e.Open("s", "a", 0))
			r := mustUpdate(t, e, "s", 1, 1, 0, tc.want, 0)
			assertReply(t, r, session.Reply{Granted: tc.granted, Final: tc.final, ValidUntil: 100})
		})
	}
}

func TestDeniedAffordability(t *testing.T) {
	e := session.New(20, 5, 100, 3)
	mustOK(t, e.SetRate(1, 3, 0))
	mustOK(t, e.SetRate(2, 1, 0))
	mustOK(t, e.TopUp("a", 2, 0))
	mustOK(t, e.Open("cheap", "a", 0))
	mustOK(t, e.Open("expensive", "a", 0))

	r := mustUpdate(t, e, "cheap", 1, 2, 0, 10, 0)
	assertReply(t, r, session.Reply{Granted: 2, Final: true, ValidUntil: 100})
	r, err := e.Update("expensive", 1, 1, 0, 1, 1)
	if err != nil {
		t.Fatalf("Denied is not an error: %v", err)
	}
	assertReply(t, r, session.Reply{Final: true, Denied: true})
}

func TestExpiryAndPriceChange(t *testing.T) {
	e := session.New(20, 5, 60, 2)
	mustOK(t, e.SetRate(1, 3, 0))
	mustOK(t, e.SetRate(2, 1, 0))
	mustOK(t, e.TopUp("a", 100, 0))
	mustOK(t, e.Open("s", "a", 0))
	mustOK(t, e.Open("s2", "a", 0))
	assertReply(t, mustUpdate(t, e, "s", 1, 1, 0, 50, 0),
		session.Reply{Granted: 20, ValidUntil: 60})
	assertReply(t, mustUpdate(t, e, "s2", 1, 1, 0, 10, 10),
		session.Reply{Granted: 13, Final: true, ValidUntil: 70})
	assertReply(t, mustUpdate(t, e, "s2", 2, 2, 0, 5, 20),
		session.Reply{Granted: 1, Final: true, ValidUntil: 80})
	mustOK(t, e.SetRate(1, 4, 25))
	assertReply(t, mustUpdate(t, e, "s", 2, 1, 25, 20, 30),
		session.Reply{Charged: 20, Unbilled: 5, Final: true, Denied: true})

	assertReply(t, mustUpdate(t, e, "s2", 3, 1, 15, 0, 70),
		session.Reply{Charged: 13, Unbilled: 2})
	mustOK(t, e.TopUp("a", 40, 71))
	assertReply(t, mustUpdate(t, e, "s2", 4, 1, 0, 10, 71),
		session.Reply{Granted: 10, Final: true, ValidUntil: 131})
	mustOK(t, e.Close("s2", 5, nil, 71))
}

func TestCloseImplicitSettlementAndReplay(t *testing.T) {
	e := session.New(20, 5, 100, 2)
	mustOK(t, e.SetRate(1, 3, 0))
	mustOK(t, e.SetRate(2, 1, 0))
	mustOK(t, e.TopUp("a", 100, 0))
	mustOK(t, e.Open("s", "a", 0))
	mustUpdate(t, e, "s", 1, 1, 0, 10, 0)
	mustUpdate(t, e, "s", 2, 2, 0, 2, 10)
	mustOK(t, e.Close("s", 3, nil, 20))
	if err := e.Close("s", 3, nil, 20); !errors.Is(err, session.ErrSessionNotFound) {
		t.Fatalf("close replay: got %v, want session not found", err)
	}
}

func TestSessionLimitAndRejectionOrder(t *testing.T) {
	e := session.New(20, 5, 100, 1)
	mustOK(t, e.TopUp("a", 100, 0))
	mustOK(t, e.Open("s1", "a", 0))
	assertError(t, e.Open("s2", "a", 0), session.ErrSessionLimit)
	assertError(t, e.Open("s1", "a", 0), session.ErrSessionExists)
	assertError(t, e.TopUp("a", 100, -1), session.ErrInvalidArgument)
	mustOK(t, e.TopUp("b", 1, 5))
	assertError(t, e.TopUp("a", 100, 3), session.ErrClockBack)
	assertUpdateError(t, e, "missing", 1, 1, 1, 0, 11, session.ErrSessionNotFound)
	assertUpdateError(t, e, "s1", 3, 1, 0, 0, 11, session.ErrSequence)
	assertUpdateError(t, e, "s1", 1, 9, 0, 1, 11, session.ErrInvalidArgument)
	mustOK(t, e.SetRate(1, 3, 12))
	assertUpdateError(t, e, "s1", 1, 1, 1, 0, 12, session.ErrNoGrant)
}

func TestConcurrentOperations(t *testing.T) {
	e := session.New(20, 5, 100, 100)
	mustOK(t, e.SetRate(1, 3, 0))
	var wg sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			acct := "a" + string(rune('0'+worker))
			sess := "s" + string(rune('0'+worker))
			_ = e.TopUp(acct, 100, 0)
			if err := e.Open(sess, acct, 0); err == nil {
				_, _ = e.Update(sess, 1, 1, 0, 5, 0)
				_ = e.Close(sess, 2, nil, 0)
			}
		}(worker)
	}
	wg.Wait()
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func mustUpdate(t *testing.T, e *session.Engine, sess string, n int64, rg int, used int64, want int64, now int64) session.Reply {
	t.Helper()
	r, err := e.Update(sess, n, rg, used, want, now)
	if err != nil {
		t.Fatalf("Update(%s,%d): %v", sess, n, err)
	}
	return r
}

func assertReply(t *testing.T, got, want session.Reply) {
	t.Helper()
	if got != want {
		t.Fatalf("reply mismatch\ngot:  %+v\nwant: %+v", got, want)
	}
}

func assertError(t *testing.T, err error, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("got %v, want errors.Is %v", err, target)
	}
}

func assertUpdateError(t *testing.T, e *session.Engine, sess string, n int64, rg int, used int64, want int64, now int64, target error) {
	t.Helper()
	_, err := e.Update(sess, n, rg, used, want, now)
	assertError(t, err, target)
}
