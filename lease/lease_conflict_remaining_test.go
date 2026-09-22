package lease

import (
	"errors"
	"testing"
	"time"
)

// 回归测试：钉住「ConflictError.Remaining 是持有者此刻的实际剩余时长，
// 而不是请求方传入的 ttl」这条契约——租约跑了 55 秒后撞锁，
// 剩余必须报 5 秒，而不是全量 60 秒。
func TestConflictRemainingIsActual(t *testing.T) {
	c, l := setup()
	if _, err := l.Acquire("alice", time.Minute); err != nil {
		t.Fatal(err)
	}
	c.Advance(55 * time.Second)
	_, err := l.Acquire("bob", time.Minute)
	var conflict *ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected *ConflictError, got %T (%v)", err, err)
	}
	if conflict.Holder != "alice" {
		t.Fatalf("conflict should name alice, got %q", conflict.Holder)
	}
	if conflict.Remaining != 5*time.Second {
		t.Fatalf("remaining should be actual 5s, got %s", conflict.Remaining)
	}
}
