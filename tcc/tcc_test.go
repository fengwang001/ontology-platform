package tcc

import (
	"bytes"
	"errors"
	"strings"
	"sync"
	"testing"

	"ontology/ledger"
)

func newEnv(t *testing.T, ttl, N int64, deposit map[string]int64) (*TCC, *ledger.Ledger, *bytes.Buffer) {
	t.Helper()
	lg := ledger.New()
	for a, x := range deposit {
		if err := lg.Deposit(a, x); err != nil {
			t.Fatalf("deposit %s: %v", a, err)
		}
	}
	var buf bytes.Buffer
	rm, err := New(lg, ttl, N, WithLogger(&buf))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return rm, lg, &buf
}

// 题目例子：ttl=10，a 余额 100。
func TestSpecExample(t *testing.T) {
	rm, lg, _ := newEnv(t, 10, 100, map[string]int64{"a": 100})

	if err := rm.Try("x", "1", "a", 60, 0); err != nil {
		t.Fatalf("Try1: %v", err)
	}
	if got := lg.Fz("a"); got != 60 {
		t.Fatalf("fz=%d want 60", got)
	}
	if b, _ := rm.Get("x", "1"); b.Deadline != 10 {
		t.Fatalf("deadline=%d want 10", b.Deadline)
	}
	if err := rm.Try("x", "2", "a", 50, 1); !errors.Is(err, ErrInsufficient) {
		t.Fatalf("Try2 err=%v want ErrInsufficient", err)
	}
	// 被拒绝不推进时钟、不留记录、不解冻。
	if lg.Fz("a") != 60 || rm.Len() != 1 {
		t.Fatalf("reject changed state: fz=%d len=%d", lg.Fz("a"), rm.Len())
	}

	if err := rm.Cancel("x", "3", 2); err != nil {
		t.Fatalf("empty cancel: %v", err)
	}
	if err := rm.Try("x", "3", "a", 10, 3); !errors.Is(err, ErrHanging) {
		t.Fatalf("hanging Try err=%v want ErrHanging", err)
	}

	if err := rm.Confirm("x", "1", 9); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if lg.Bal("a") != 40 || lg.Fz("a") != 0 {
		t.Fatalf("after confirm bal=%d fz=%d want 40/0", lg.Bal("a"), lg.Fz("a"))
	}
}

// 到期恰等（10）与差 1（9）。
func TestConcurrentTryCancel(t *testing.T) {
	for iter := 0; iter < 200; iter++ {
		rm, lg, _ := newEnv(t, 100, 10, map[string]int64{"a": 100})
		start := make(chan struct{})
		var wg sync.WaitGroup
		var tryErr, cancelErr error
		wg.Add(2)
		go func() { defer wg.Done(); <-start; tryErr = rm.Try("x", "1", "a", 60, 0) }()
		go func() { defer wg.Done(); <-start; cancelErr = rm.Cancel("x", "1", 0) }()
		close(start)
		wg.Wait()
		b, _ := rm.Get("x", "1")
		switch {
		case tryErr == nil && cancelErr == nil:
			if b.State != StateCancelled || b.Reason != Cancel ||
				lg.Fz("a") != 0 || lg.Bal("a") != 100 {
				t.Fatalf("iter %d try-then-cancel bad: %+v fz=%d bal=%d",
					iter, b, lg.Fz("a"), lg.Bal("a"))
			}
		case errors.Is(tryErr, ErrHanging) && cancelErr == nil:
			if b.State != StateCancelled || b.Reason != Empty ||
				lg.Fz("a") != 0 || lg.Bal("a") != 100 {
				t.Fatalf("iter %d cancel-first bad: %+v", iter, b)
			}
		default:
			t.Fatalf("iter %d unexpected: try=%v cancel=%v", iter, tryErr, cancelErr)
		}
	}
}

func TestClockRules(t *testing.T) {
	rm, _, _ := newEnv(t, 10, 10, map[string]int64{"a": 100})
	if err := rm.Try("x", "1", "a", 10, -1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("negative now: %v", err)
	}
	must(t, rm.Try("x", "1", "a", 10, 5))
	if err := rm.Try("x", "2", "a", 10, 4); !errors.Is(err, ErrClock) {
		t.Fatalf("backwards: %v", err)
	}
	if _, err := rm.Avail("a", 1_000_000_000_000_001); !errors.Is(err, ErrInvalid) {
		t.Fatalf("now too large: %v", err)
	}
}

func TestLoggingShowsBasis(t *testing.T) {
	rm, _, buf := newEnv(t, 10, 10, map[string]int64{"a": 100})
	must(t, rm.Try("x", "1", "a", 60, 0))
	_ = rm.Try("x", "2", "a", 50, 1)
	if !strings.Contains(buf.String(), "Try") ||
		!strings.Contains(buf.String(), "insufficient") {
		t.Fatalf("log missing input/basis:\n%s", buf.String())
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
