package tcc

import (
	"errors"
	"testing"
)

// 到期恰等（10）与差 1（9）。
func TestExpiryBoundary(t *testing.T) {
	t.Run("one-before", func(t *testing.T) {
		rm, lg, _ := newEnv(t, 10, 100, map[string]int64{"a": 100})
		must(t, rm.Try("x", "1", "a", 60, 0))
		if err := rm.Confirm("x", "1", 9); err != nil {
			t.Fatalf("confirm at 9: %v", err)
		}
		if lg.Bal("a") != 40 || lg.Fz("a") != 0 {
			t.Fatalf("bal=%d fz=%d", lg.Bal("a"), lg.Fz("a"))
		}
	})
	t.Run("exact-deadline", func(t *testing.T) {
		rm, lg, _ := newEnv(t, 10, 100, map[string]int64{"a": 100})
		must(t, rm.Try("x", "1", "a", 60, 0))
		if err := rm.Confirm("x", "1", 10); !errors.Is(err, ErrExpired) {
			t.Fatalf("confirm at 10 err=%v want ErrExpired", err)
		}
		if b, _ := rm.Get("x", "1"); b.State != StateTried || lg.Fz("a") != 60 {
			t.Fatal("reject materialized expiry")
		}
		if v, err := rm.Avail("a", 10); err != nil || v != 100 {
			t.Fatalf("Avail at 10 = %d,%v want 100,nil", v, err)
		}
		if lg.Fz("a") != 60 {
			t.Fatalf("Avail materialized expiry: fz=%d", lg.Fz("a"))
		}
		must(t, rm.Try("z", "1", "a", 5, 11))
		if lg.Fz("a") != 5 {
			t.Fatalf("after materialize fz=%d want 5", lg.Fz("a"))
		}
		if b, _ := rm.Get("x", "1"); b.State != StateCancelled || b.Reason != Expired {
			t.Fatalf("state=%v reason=%v", b.State, b.Reason)
		}
		if err := rm.Confirm("x", "1", 12); !errors.Is(err, ErrExpired) {
			t.Fatalf("confirm after expiry err=%v", err)
		}
		if err := rm.Try("x", "1", "a", 10, 13); !errors.Is(err, ErrHanging) {
			t.Fatalf("try after expiry err=%v want ErrHanging", err)
		}
	})
}

// 容量满时：Try 拒绝、空回滚也被拒且不留标记。
func TestCapacity(t *testing.T) {
	rm, _, _ := newEnv(t, 10, 2, map[string]int64{"a": 1000})
	must(t, rm.Try("x", "1", "a", 1, 0))
	must(t, rm.Try("x", "2", "a", 1, 0))
	if err := rm.Try("x", "3", "a", 1, 1); !errors.Is(err, ErrCapacity) {
		t.Fatalf("try full: %v", err)
	}
	if err := rm.Cancel("y", "9", 1); !errors.Is(err, ErrCapacity) {
		t.Fatalf("empty cancel full: %v", err)
	}
	if _, ok := rm.Get("y", "9"); ok {
		t.Fatal("empty-rollback marker must not be written when full")
	}
	rm2, _, _ := newEnv(t, 10, 10, map[string]int64{"a": 1000})
	must(t, rm2.Try("p", "1", "a", 1, 0))
	if err := rm2.Cancel("p", "1", 10); err != nil {
		t.Fatalf("cancel at deadline: %v", err)
	}
	if b, _ := rm2.Get("p", "1"); b.Reason != Expired {
		t.Fatalf("reason=%v want Expired", b.Reason)
	}
	if err := rm2.Try("p", "1", "a", 1, 11); !errors.Is(err, ErrHanging) {
		t.Fatalf("late Try after expiry: %v", err)
	}
}

// 重复 Confirm/Cancel 幂等；终态互斥。
func TestIdempotentTerminal(t *testing.T) {
	rm, lg, _ := newEnv(t, 100, 10, map[string]int64{"a": 100})
	must(t, rm.Try("x", "1", "a", 30, 0))
	must(t, rm.Confirm("x", "1", 1))
	must(t, rm.Confirm("x", "1", 2))
	if lg.Bal("a") != 70 || lg.Fz("a") != 0 {
		t.Fatalf("bal=%d fz=%d", lg.Bal("a"), lg.Fz("a"))
	}
	if err := rm.Cancel("x", "1", 3); !errors.Is(err, ErrConflict) {
		t.Fatalf("cancel after confirm: %v", err)
	}
	must(t, rm.Try("x", "2", "a", 20, 4))
	must(t, rm.Cancel("x", "2", 5))
	must(t, rm.Cancel("x", "2", 6))
	if err := rm.Confirm("x", "2", 7); !errors.Is(err, ErrConflict) {
		t.Fatalf("confirm after cancel: %v", err)
	}
	if lg.Bal("a") != 70 || lg.Fz("a") != 0 {
		t.Fatalf("bal=%d fz=%d want 70/0", lg.Bal("a"), lg.Fz("a"))
	}
}

// Try 幂等：相同账户金额不重复冻结、到期不刷新；不同则 ErrMismatch。
func TestTryIdempotentMismatch(t *testing.T) {
	rm, lg, _ := newEnv(t, 10, 10, map[string]int64{"a": 100})
	must(t, rm.Try("x", "1", "a", 60, 0))
	must(t, rm.Try("x", "1", "a", 60, 5))
	if lg.Fz("a") != 60 {
		t.Fatalf("double freeze: fz=%d", lg.Fz("a"))
	}
	if b, _ := rm.Get("x", "1"); b.Deadline != 10 {
		t.Fatalf("deadline refreshed: %d", b.Deadline)
	}
	if err := rm.Try("x", "1", "a", 59, 6); !errors.Is(err, ErrMismatch) {
		t.Fatalf("mismatch amount: %v", err)
	}
	if err := rm.Try("x", "1", "b", 60, 6); !errors.Is(err, ErrMismatch) {
		t.Fatalf("mismatch acct: %v", err)
	}
}
