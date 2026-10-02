package kerberos

import (
	"fmt"
	"sync"
	"testing"
)

func TestConfigAndIssueBounds(t *testing.T) {
	invalidConfigs := [][4]int64{{0, 1, 1, 1}, {1, 0, 1, 1}, {1, 1, 0, 1}, {1, 1, 1, 0}, {6, 5, 1, 1}, {1_000_000_001, 1_000_000_001, 1, 1}}
	for index, cfg := range invalidConfigs {
		if _, err := NewKDC(cfg[0], cfg[1], cfg[2], cfg[3]); err == nil || err.(*Error).Reason != ErrInvalidConfig {
			t.Fatalf("case %d: got %v", index, err)
		}
	}

	k, _ := NewKDC(5, 20, 1, 50)
	shorterTill, err := k.IssueTGT([]byte("a"), 10, 13, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	assertTicket(t, shorterTill, 10, 13, 0, false)

	shorterLife, err := k.IssueTGT([]byte("a"), 10, 30, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	assertTicket(t, shorterLife, 10, 15, 0, false)

	equalLimit, err := k.IssueTGT([]byte("a"), 0, 16, 16, 11)
	if err != nil {
		t.Fatal(err)
	}
	assertTicket(t, equalLimit, 11, 16, 0, false)

	oneMore, err := k.IssueTGT([]byte("a"), 0, 16, 17, 12)
	if err != nil {
		t.Fatal(err)
	}
	assertTicket(t, oneMore, 12, 16, 17, false)

	atPostLimit, err := k.IssueTGT([]byte("a"), 62, 100, 0, 12)
	if err != nil {
		t.Fatal(err)
	}
	assertTicket(t, atPostLimit, 62, 67, 0, true)
	if _, err := k.IssueTGT([]byte("a"), 64, 100, 0, 13); err == nil || err.(*Error).Reason != ErrTooFarPostdated {
		t.Fatalf("postdated limit: got %v", err)
	}
}

func TestPostdatedAndServiceCapping(t *testing.T) {
	k, _ := NewKDC(100, 1000, 5, 50)
	tb, err := k.IssueTGT([]byte("bob"), 40, 300, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := k.TGS(tb.ID, []byte("web"), 100, 0, 40); err == nil || err.(*Error).Reason != ErrInvalidTicket {
		t.Fatalf("postdated at start: got %v", err)
	}
	tb, err = k.Validate(tb.ID, 40)
	if err != nil {
		t.Fatal(err)
	}
	assertTicket(t, tb, 40, 140, 0, false)

	k2, _ := NewKDC(100, 1000, 5, 50)
	t1, err := k2.IssueTGT([]byte("alice"), 0, 500, 2000, 10)
	if err != nil {
		t.Fatal(err)
	}
	t1, err = k2.Renew(t1.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	service, err := k2.TGS(t1.ID, []byte("web"), 1000, 1500, 150)
	if err != nil {
		t.Fatal(err)
	}
	assertTicket(t, service, 150, 200, 1010, false)

	t1, err = k2.Renew(t1.ID, 190)
	if err != nil {
		t.Fatal(err)
	}
	assertTicket(t, t1, 190, 290, 1010, false)
	storedService := k2.tickets[service.ID].ticket
	if storedService.End != 200 {
		t.Fatalf("service end changed to %d", storedService.End)
	}
	service, err = k2.Renew(service.ID, 195)
	if err != nil {
		t.Fatal(err)
	}
	assertTicket(t, service, 195, 245, 1010, false)
}

func TestRenewalLifetimeAndExpiry(t *testing.T) {
	k, _ := NewKDC(10, 20, 1, 50)
	ticket, err := k.IssueTGT([]byte("a"), 0, 20, 21, 1)
	if err != nil {
		t.Fatal(err)
	}
	ticket, err = k.Renew(ticket.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	assertTicket(t, ticket, 10, 20, 21, false)
	ticket, err = k.Renew(ticket.ID, 15)
	if err != nil {
		t.Fatal(err)
	}
	assertTicket(t, ticket, 15, 21, 21, false)
	if _, err := k.Renew(ticket.ID, 20); err == nil || err.(*Error).Reason != ErrRenewLimit {
		t.Fatalf("equal new end: got %v", err)
	}

	other, err := k.IssueTGT([]byte("a"), 0, 40, 0, 21)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := k.Renew(other.ID, 25); err == nil || err.(*Error).Reason != ErrNotRenewable {
		t.Fatalf("non-renewable: got %v", err)
	}
	if _, err := k.Renew(ticket.ID, 21); err == nil || err.(*Error).Reason != ErrExpired {
		t.Fatalf("end equality: got %v", err)
	}
}

func TestReplayCacheBoundariesAndRejections(t *testing.T) {
	k, _ := NewKDC(100, 1000, 5, 50)
	ticket, err := k.IssueTGT([]byte("a"), 0, 200, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := k.Authenticate(ticket.ID, 145, 150); err != nil {
		t.Fatal(err)
	}
	if _, err := k.Authenticate(ticket.ID, 145, 150); err == nil || err.(*Error).Reason != ErrReplay {
		t.Fatalf("replay: got %v", err)
	}
	if len(k.replay.entries) != 1 {
		t.Fatalf("entries=%d", len(k.replay.entries))
	}
	if _, err := k.Authenticate(ticket.ID, 145, 151); err == nil || err.(*Error).Reason != ErrClockSkew {
		t.Fatalf("skew priority: got %v", err)
	}
	if len(k.replay.entries) != 1 {
		t.Fatalf("rejected skew auth changed cache: %d", len(k.replay.entries))
	}
	if err := k.ChangeKey([]byte("z"), 151); err != nil {
		t.Fatal(err)
	}
	if len(k.replay.entries) != 0 {
		t.Fatalf("expired entry retained: %d", len(k.replay.entries))
	}
	if _, err := k.Authenticate(ticket.ID, 151, 151); err != nil {
		t.Fatal(err)
	}
	if _, err := k.Authenticate(ticket.ID, 152, 151); err != nil {
		t.Fatal(err)
	}
	if len(k.replay.entries) != 2 {
		t.Fatalf("different auth times entries=%d", len(k.replay.entries))
	}
}

func TestZeroTimeAndExactReplayExpiry(t *testing.T) {
	k, _ := NewKDC(10, 20, 5, 10)
	ticket, err := k.IssueTGT([]byte("a"), 0, 10, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	assertTicket(t, ticket, 0, 10, 0, false)
	if _, err := k.Authenticate(ticket.ID, 0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := k.Authenticate(ticket.ID, 0, 5); err == nil || err.(*Error).Reason != ErrReplay {
		t.Fatalf("exact expiry should replay: %v", err)
	}
	if err := k.ChangeKey([]byte("z"), 5); err != nil {
		t.Fatal(err)
	}
	if len(k.replay.entries) != 1 {
		t.Fatalf("entry at expires should remain, got %d", len(k.replay.entries))
	}
	if err := k.ChangeKey([]byte("z"), 6); err != nil {
		t.Fatal(err)
	}
	if len(k.replay.entries) != 0 {
		t.Fatalf("entry should be gone after expiry, got %d", len(k.replay.entries))
	}
}

func TestClockSkewBeforeTicketState(t *testing.T) {
	k, _ := NewKDC(5, 10, 1, 10)
	ticket, err := k.IssueTGT([]byte("a"), 0, 10, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := k.Authenticate(ticket.ID, 0, 2); err == nil || err.(*Error).Reason != ErrClockSkew {
		t.Fatalf("skew before expired/invalid checks, got %v", err)
	}
}

func TestKeyChanges(t *testing.T) {
	k, _ := NewKDC(100, 1000, 5, 50)
	ticket, err := k.IssueTGT([]byte("a"), 0, 200, 1000, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := k.Renew(ticket.ID, 100); err != nil {
		t.Fatal(err)
	}
	if err := k.ChangeKey([]byte("a"), 120); err != nil {
		t.Fatal(err)
	}
	_, err = k.Renew(ticket.ID, 130)
	assertReason(t, err, ErrKeyChanged)
	_, err = k.Authenticate(ticket.ID, 130, 130)
	assertReason(t, err, ErrKeyChanged)
	if len(k.replay.entries) != 0 {
		t.Fatalf("invalid auth entered replay cache: %d", len(k.replay.entries))
	}

	postdated, err := k.IssueTGT([]byte("b"), 150, 200, 0, 140)
	if err != nil {
		t.Fatal(err)
	}
	if err = k.ChangeKey([]byte("b"), 145); err != nil {
		t.Fatal(err)
	}
	_, err = k.Validate(postdated.ID, 150)
	assertReason(t, err, ErrKeyChanged)

	equal, err := k.IssueTGT([]byte("c"), 0, 200, 0, 146)
	if err != nil {
		t.Fatal(err)
	}
	if err := k.ChangeKey([]byte("c"), 146); err != nil {
		t.Fatal(err)
	}
	if _, err := k.Authenticate(equal.ID, 146, 146); err != nil {
		t.Fatalf("issued == change time failed: %v", err)
	}
}

func TestRejectedOperationKeepsStateAndClock(t *testing.T) {
	k, _ := NewKDC(100, 1000, 5, 50)
	if _, err := k.IssueTGT(nil, 0, 200, 0, 10); err == nil || err.(*Error).Reason != ErrInvalidParameter {
		t.Fatal("empty subject accepted")
	}
	if _, err := k.IssueTGT([]byte("a"), 0, 200, 0, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := k.IssueTGT([]byte("a"), 0, 200, 0, 9); err == nil || err.(*Error).Reason != ErrClockRollback {
		t.Fatal("rollback accepted")
	}
	if k.next != 1 || k.now != 10 {
		t.Fatalf("state changed by rejection: next=%d now=%d", k.next, k.now)
	}
}

func TestConcurrentAuthenticateOneSuccess(t *testing.T) {
	k, _ := NewKDC(100, 1000, 5, 50)
	ticket, err := k.IssueTGT([]byte("a"), 0, 200, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	const count = 64
	var wg sync.WaitGroup
	results := make(chan error, count)
	for range count {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := k.Authenticate(ticket.ID, 50, 50)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else if err.(*Error).Reason != ErrReplay {
			t.Fatal(err)
		}
	}
	if successes != 1 {
		t.Fatalf("successes=%d", successes)
	}
}

func assertReason(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("missing error, want %s", want)
	}
	if err.(*Error).Reason != want {
		t.Fatalf("reason=%s, want %s", err.(*Error).Reason, want)
	}
}

var _ = fmt.Sprintf
