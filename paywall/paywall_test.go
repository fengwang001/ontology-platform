package paywall

import (
	"errors"
	"fmt"
	"testing"
)

func addArticles(t *testing.T, p *Paywall, articles ...string) {
	t.Helper()
	for _, article := range articles {
		if err := p.AddArticle(article, false); err != nil {
			t.Fatal(err)
		}
	}
}

func read(t *testing.T, p *Paywall, now int64, device, article string, token *Token) Result {
	t.Helper()
	result, err := p.Read(now, device, article, token)
	if err != nil {
		t.Fatalf("Read(%d,%s,%s): %v", now, device, article, err)
	}
	return result
}

func assertReason(t *testing.T, got Result, want Reason) {
	t.Helper()
	if !got.Allowed || got.Reason != want {
		t.Fatalf("result = %+v, want allowed reason %v", got, want)
	}
}

func TestMergedLoginLogoutExample(t *testing.T) {
	t.Parallel()
	p := New(3, 1000, 1, 1, 100)
	addArticles(t, p, "a1", "a2", "a3", "a4", "a5", "a6")
	if err := p.Login(5, "d2", "u"); err != nil {
		t.Fatal(err)
	}
	assertReason(t, read(t, p, 5, "d2", "a3", nil), Quota)
	assertReason(t, read(t, p, 10, "d", "a1", nil), Quota)
	assertReason(t, read(t, p, 15, "d2", "a4", nil), Quota)
	assertReason(t, read(t, p, 20, "d", "a2", nil), Quota)
	assertReason(t, read(t, p, 25, "d", "a1", nil), AlreadyUnlocked)

	if err := p.Login(30, "d", "u"); err != nil {
		t.Fatal(err)
	}
	result := read(t, p, 31, "d", "a2", nil)
	if result.Allowed || result.Reason != Denied {
		t.Fatalf("revoked quota article result = %+v, want denied", result)
	}
	if err := p.Logout(40, "d"); err != nil {
		t.Fatal(err)
	}
	assertReason(t, read(t, p, 41, "d", "a2", nil), AlreadyUnlocked)
	assertReason(t, read(t, p, 41, "d", "a5", nil), Quota)
	result = read(t, p, 42, "d", "a6", nil)
	if result.Allowed || result.Reason != Denied {
		t.Fatalf("anonymous exhausted result = %+v, want denied", result)
	}

	assertReason(t, read(t, p, 1000, "d", "a1", nil), Quota)
	assertReason(t, read(t, p, 1000, "d2", "a3", nil), Quota)
}

func TestSubscriptionBoundaryAndNoLedgerEntry(t *testing.T) {
	t.Parallel()
	p := New(0, 1000, 1, 1, 100)
	addArticles(t, p, "a")
	if err := p.Subscribe(50, "u", 60); err != nil {
		t.Fatal(err)
	}
	if err := p.Login(51, "d", "u"); err != nil {
		t.Fatal(err)
	}
	assertReason(t, read(t, p, 59, "d", "a", nil), Subscription)
	if err := p.Logout(59, "d"); err != nil {
		t.Fatal(err)
	}
	result := read(t, p, 60, "d", "a", nil)
	if result.Allowed || result.Reason != Denied {
		t.Fatalf("at until result = %+v, want denied", result)
	}
}

func TestFreeArticleDoesNotWriteLedger(t *testing.T) {
	t.Parallel()
	p := New(0, 1000, 1, 1, 100)
	if err := p.AddArticle("f", true); err != nil {
		t.Fatal(err)
	}
	assertReason(t, read(t, p, 1, "d", "f", nil), FreeArticle)
	if err := p.Subscribe(2, "u", 3); err != nil {
		t.Fatal(err)
	}
	if err := p.Login(2, "d", "u"); err != nil {
		t.Fatal(err)
	}
	assertReason(t, read(t, p, 2, "d", "f", nil), Subscription)
	if err := p.Logout(2, "d"); err != nil {
		t.Fatal(err)
	}
	assertReason(t, read(t, p, 3, "d", "f", nil), FreeArticle)
}

func TestGiftRedemptionAndExpiry(t *testing.T) {
	t.Parallel()
	p := New(3, 1000, 1, 1, 100)
	addArticles(t, p, "a7", "a9")
	if err := p.Subscribe(50, "u", 60); err != nil {
		t.Fatal(err)
	}
	token, err := p.Gift(55, "u", "a9")
	if err != nil {
		t.Fatal(err)
	}
	if token.ID() != 1 {
		t.Fatalf("token id = %d, want 1", token.ID())
	}
	assertReason(t, read(t, p, 56, "x", "a9", token), Gift)
	if got := p.ledger.Used("x", 56); got != 0 {
		t.Fatalf("gift used quota = %d, want 0", got)
	}
	result := read(t, p, 57, "y", "a9", token)
	if !result.Allowed || result.Reason != Quota {
		t.Fatalf("full token result = %+v, want quota", result)
	}
	assertReason(t, read(t, p, 58, "x", "a9", token), AlreadyUnlocked)

	if err := p.Login(59, "d", "u"); err != nil {
		t.Fatal(err)
	}
	assertReason(t, read(t, p, 59, "d", "a7", nil), Subscription)
	if err := p.Logout(59, "d"); err != nil {
		t.Fatal(err)
	}
	assertReason(t, read(t, p, 60, "d", "a7", nil), Quota)

	result = read(t, p, 155, "z", "a9", token)
	if !result.Allowed || result.Reason != Quota {
		t.Fatalf("expired token result = %+v, want quota", result)
	}
}

func TestSameSubjectReusesFullToken(t *testing.T) {
	t.Parallel()
	p := New(0, 1000, 1, 1, 100)
	addArticles(t, p, "a")
	if err := p.Subscribe(1, "u", 10); err != nil {
		t.Fatal(err)
	}
	token, err := p.Gift(1, "u", "a")
	if err != nil {
		t.Fatal(err)
	}
	assertReason(t, read(t, p, 2, "d", "a", token), Gift)
	assertReason(t, read(t, p, 3, "d", "a", token), AlreadyUnlocked)

	if _, err := p.Gift(3, "u", "a"); !errors.Is(err, ErrGiftQuota) {
		t.Fatalf("second gift error = %v, want gift quota", err)
	}
}

func TestRejectionOrderAndNoClockOrStateChange(t *testing.T) {
	t.Parallel()
	p := New(1, 1000, 1, 1, 100)
	addArticles(t, p, "a")

	if _, err := p.Read(1, "", "a", nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("read invalid = %v", err)
	}
	if _, err := p.Read(1, "d", "missing", nil); !errors.Is(err, ErrArticleNotFound) {
		t.Fatalf("missing article = %v", err)
	}
	if _, err := p.Read(0, "d", "missing", nil); !errors.Is(err, ErrArticleNotFound) {
		t.Fatalf("missing before clock = %v", err)
	}
	if _, err := p.Read(10, "d", "a", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Read(9, "d", "a", nil); !errors.Is(err, ErrClockSkew) {
		t.Fatalf("stale read = %v", err)
	}

	if err := p.Login(20, "d", "u"); err != nil {
		t.Fatal(err)
	}
	if err := p.Login(21, "d", "v"); !errors.Is(err, ErrAlreadyBound) {
		t.Fatalf("rebind = %v", err)
	}
	if err := p.Login(19, "d", "v"); !errors.Is(err, ErrClockSkew) {
		t.Fatalf("stale rebind = %v", err)
	}
	if err := p.Logout(21, "x"); !errors.Is(err, ErrNotBound) {
		t.Fatalf("unbound logout = %v", err)
	}
	if err := p.Logout(19, "d"); !errors.Is(err, ErrClockSkew) {
		t.Fatalf("stale logout = %v", err)
	}

	if err := p.Subscribe(30, "u", 30); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("until equal now = %v", err)
	}
	if _, err := p.Gift(30, "u", "a"); !errors.Is(err, ErrNotSubscribed) {
		t.Fatalf("non subscriber = %v", err)
	}
	if _, err := p.Gift(29, "u", "missing"); !errors.Is(err, ErrArticleNotFound) {
		t.Fatalf("gift missing precedence = %v", err)
	}

	if _, err := p.Read(30, "d", "a", nil); err != nil {
		t.Fatalf("equal now after rejected operations should advance: %v", err)
	}
}

func TestReadTouchesAtMostTwoRecordsAcrossHistory(t *testing.T) {
	t.Parallel()
	for _, oldCount := range []int{100, 10000} {
		p := New(1, 100000, oldCount, oldCount+1, 1_000_000)
		if err := p.Subscribe(0, "u", 1_000_000); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < oldCount; i++ {
			article := fmt.Sprintf("old-%05d", i)
			if err := p.AddArticle(article, false); err != nil {
				t.Fatal(err)
			}
			token, err := p.Gift(int64(i), "u", article)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := p.Read(int64(i), "d", article, token); err != nil {
				t.Fatal(err)
			}
		}
		if err := p.AddArticle("fresh", false); err != nil {
			t.Fatal(err)
		}
		p.ledger.ResetTouched()
		if _, err := p.Read(int64(oldCount), "d", "fresh", nil); err != nil {
			t.Fatal(err)
		}
		got := p.ledger.Touched("d", int64(oldCount))
		if got > 2 {
			t.Fatalf("oldCount=%d touched=%d, want <= 2", oldCount, got)
		}
	}
}

func TestReasonOrdering(t *testing.T) {
	t.Parallel()
	p := New(0, 1000, 1, 1, 10)
	if err := p.AddArticle("free", true); err != nil {
		t.Fatal(err)
	}
	if err := p.Subscribe(1, "u", 10); err != nil {
		t.Fatal(err)
	}
	if err := p.Login(1, "d", "u"); err != nil {
		t.Fatal(err)
	}
	token, err := p.Gift(1, "u", "free")
	if err != nil {
		t.Fatal(err)
	}
	assertReason(t, read(t, p, 2, "d", "free", token), Subscription)
	if err := p.Logout(2, "d"); err != nil {
		t.Fatal(err)
	}
	assertReason(t, read(t, p, 2, "d", "free", token), FreeArticle)
}
