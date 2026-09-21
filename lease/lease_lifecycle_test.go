package lease

import (
	"errors"
	"sync"
	"testing"
	"time"

	"ontology/fence"
)

func TestExpiryIsLeftClosedRightOpen(t *testing.T) {
	c, l := setup()
	if _, err := l.Acquire("alice", time.Minute); err != nil {
		t.Fatal(err)
	}
	c.Advance(time.Minute) // now 恰好等于到期时间
	if l.Info().Held {
		t.Fatal("lease must be expired when now == expiry")
	}
	if _, err := l.Acquire("bob", time.Minute); err != nil {
		t.Fatalf("bob should acquire at exact expiry: %v", err)
	}
}

func TestTokenStrictlyIncreasesAcrossRounds(t *testing.T) {
	c, l := setup()
	var prev fence.Token
	for i := 0; i < 5; i++ {
		tok, err := l.Acquire("holder", time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if i > 0 && tok <= prev {
			t.Fatalf("token regressed: %d <= %d", tok, prev)
		}
		prev = tok
		c.Advance(2 * time.Minute) // 过期后被下一轮抢走
	}
}

func TestReleaseThenReacquire(t *testing.T) {
	_, l := setup()
	tok1, err := l.Acquire("alice", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Release("alice"); err != nil {
		t.Fatal(err)
	}
	tok2, err := l.Acquire("bob", time.Hour)
	if err != nil {
		t.Fatalf("should acquire right after release: %v", err)
	}
	if tok2 <= tok1 {
		t.Fatalf("re-acquire token %d not greater than %d", tok2, tok1)
	}
}

func TestReleaseOnlyByHolderAndOnlyOnce(t *testing.T) {
	_, l := setup()
	if _, err := l.Acquire("alice", time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := l.Release("bob"); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("non-holder release should fail, got %v", err)
	}
	if !l.Info().Held {
		t.Fatal("rejected release must not change state")
	}
	if err := l.Release("alice"); err != nil {
		t.Fatal(err)
	}
	if err := l.Release("alice"); !errors.Is(err, ErrNotHeld) {
		t.Fatalf("second release should fail with ErrNotHeld, got %v", err)
	}
}

func TestExpiredHolderCannotRevive(t *testing.T) {
	c, l := setup()
	if _, err := l.Acquire("alice", time.Minute); err != nil {
		t.Fatal(err)
	}
	c.Advance(2 * time.Minute)
	if _, err := l.Renew("alice", time.Minute); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("expired holder renew must fail, got %v", err)
	}
	tok, err := l.Acquire("alice", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if tok != 2 {
		t.Fatalf("re-acquired token should be a new larger value, got %d", tok)
	}
}

func TestInfoZeroWhenNotHeld(t *testing.T) {
	c, l := setup()
	if info := l.Info(); info.Held || info.Holder != "" || info.Remaining != 0 || info.Token != 0 {
		t.Fatalf("never-held info should be zero, got %+v", info)
	}
	if _, err := l.Acquire("alice", time.Minute); err != nil {
		t.Fatal(err)
	}
	c.Advance(2 * time.Minute)
	if info := l.Info(); info.Held || info.Holder != "" || info.Remaining != 0 || info.Token != 0 {
		t.Fatalf("expired info should be zero, got %+v", info)
	}
}

func TestConcurrentAcquireSingleWinner(t *testing.T) {
	_, l := setup()
	const n = 32
	var wg sync.WaitGroup
	tokens := make([]fence.Token, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tokens[i], errs[i] = l.Acquire("client", time.Minute)
		}(i)
	}
	wg.Wait()
	wins := 0
	for i := range errs {
		if errs[i] == nil {
			wins++
			if tokens[i] == 0 {
				t.Fatal("winner got zero token")
			}
		} else if !errors.Is(errs[i], ErrHeld) {
			t.Fatalf("loser should get ErrHeld, got %v", errs[i])
		}
	}
	if wins != 1 {
		t.Fatalf("expected exactly 1 winner, got %d", wins)
	}
}
