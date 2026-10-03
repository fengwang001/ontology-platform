package ledger

import (
	"errors"
	"sync"
	"testing"
)

func TestDepositLimitAndAvail(t *testing.T) {
	l := New()
	for i := 0; i < 999; i++ {
		if err := l.Deposit("a", 1_000_000_000_000); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.Deposit("a", 1_000_000_000_000); err != nil {
		t.Fatal(err)
	}
	if err := l.Deposit("a", 1); !errors.Is(err, ErrLimit) {
		t.Fatalf("over limit err=%v want ErrLimit", err)
	}
	if l.Bal("a") != 1_000_000_000_000_000 {
		t.Fatalf("limit reject changed bal=%d", l.Bal("a"))
	}
	if err := l.Deposit("", 1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty acct: %v", err)
	}
	if err := l.Deposit("b", 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("zero amount: %v", err)
	}
	if err := l.Deposit("b", 1_000_000_000_001); !errors.Is(err, ErrInvalid) {
		t.Fatalf("huge amount: %v", err)
	}
	if l.Avail("ghost") != 0 {
		t.Fatal("unknown account should have zero avail")
	}
}

func TestFreezeConfirmFlow(t *testing.T) {
	l := New()
	if err := l.Deposit("a", 100); err != nil {
		t.Fatal(err)
	}
	if !l.Freeze("a", 100) {
		t.Fatal("equal avail must pass")
	}
	if l.Freeze("a", 1) {
		t.Fatal("freeze beyond avail must fail")
	}
	l.Unfreeze("a", 40)
	if l.Avail("a") != 40 {
		t.Fatalf("avail=%d want 40", l.Avail("a"))
	}
	l.Confirm("a", 60)
	if l.Bal("a") != 40 || l.Fz("a") != 0 || l.Avail("a") != 40 {
		t.Fatalf("bal=%d fz=%d", l.Bal("a"), l.Fz("a"))
	}
}

// 并发 Deposit/Freeze/Unfreeze 下 fz 恒不超过 bal。
func TestConcurrentSafety(t *testing.T) {
	l := New()
	if err := l.Deposit("a", 1_000_000); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				if l.Freeze("a", 10) {
					if i%2 == 0 {
						l.Confirm("a", 10)
					} else {
						l.Unfreeze("a", 10)
					}
				}
			}
		}()
	}
	wg.Wait()
	if l.Fz("a") != 0 {
		t.Fatalf("fz=%d want 0", l.Fz("a"))
	}
	if l.Bal("a") < 0 || l.Bal("a") > 1_000_000 {
		t.Fatalf("bal=%d out of range", l.Bal("a"))
	}
}
