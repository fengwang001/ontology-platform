package lease

import (
	"errors"
	"testing"
	"time"
)

// 回归测试：钉住「ConflictError.Remaining 是持有者此刻的实际剩余时长」
// 的契约——不是请求方传入的 ttl。修复前 Remaining 恒等于请求方 ttl。
func TestConflictRemainingIsActual(t *testing.T) {
	c, l := setup()
	if _, err := l.Acquire("alice", time.Minute); err != nil {
		t.Fatal(err)
	}
	c.Advance(55 * time.Second) // 租约已跑 55s，实际只剩 5s
	_, err := l.Acquire("bob", 2*time.Minute)
	if !errors.Is(err, ErrHeld) {
		t.Fatalf("expected ErrHeld, got %v", err)
	}
	var conflict *ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected *ConflictError, got %T", err)
	}
	if conflict.Holder != "alice" {
		t.Fatalf("conflict should name alice, got %q", conflict.Holder)
	}
	if conflict.Remaining != 5*time.Second {
		t.Fatalf("remaining should be actual 5s, not requested ttl; got %s", conflict.Remaining)
	}
}
