package lease

import (
	"errors"
	"testing"
)

// 边界决策：now == expiresAt 那一刻，续约与写入都算失效，
// 新持有者可立即 Acquire。
func TestExpiryBoundary(t *testing.T) {
	m, c := newManager()
	tok, _ := m.Acquire("A", 100)
	c.Advance(100) // now == expiresAt

	if err := m.Renew("A", tok, 100); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("Renew err = %v, want ErrLeaseExpired", err)
	}
	if err := m.Write(tok, "k", "v"); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("Write err = %v, want ErrLeaseExpired", err)
	}
	if _, err := m.Acquire("B", 100); err != nil {
		t.Fatalf("Acquire B at boundary: %v", err)
	}
}

// 边界决策：被抢占后 Renew 返回 ErrNotHolder，
// 与自己过期返回的 ErrLeaseExpired 不是同一类。
func TestRenewAfterPreemption(t *testing.T) {
	m, c := newManager()
	tokA, _ := m.Acquire("A", 100)
	c.Advance(100)
	if _, err := m.Acquire("B", 100); err != nil {
		t.Fatalf("Acquire B: %v", err)
	}
	if err := m.Renew("A", tokA, 100); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("Renew err = %v, want ErrNotHolder", err)
	}
}

// 边界决策：过期后 Release 幂等成功；被抢占后 Release 返回 ErrNotHolder。
func TestReleaseEdgeCases(t *testing.T) {
	m, c := newManager()
	tokA, _ := m.Acquire("A", 100)
	c.Advance(100) // 过期但记录仍在
	if err := m.Release("A", tokA); err != nil {
		t.Fatalf("Release expired own lease: %v", err)
	}

	tokB, _ := m.Acquire("B", 100)
	if err := m.Release("A", tokA); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("Release err = %v, want ErrNotHolder", err)
	}
	if err := m.Release("B", tokB+1); !errors.Is(err, ErrTokenMismatch) {
		t.Fatalf("Release err = %v, want ErrTokenMismatch", err)
	}
}

// 边界决策：同一 holder 有效期内再次 Acquire 签发新 token，
// 旧 token 立即失效。
func TestReAcquireSameHolder(t *testing.T) {
	m, _ := newManager()
	tok1, _ := m.Acquire("A", 100)
	tok2, err := m.Acquire("A", 100)
	if err != nil {
		t.Fatalf("re-Acquire: %v", err)
	}
	if tok2 <= tok1 {
		t.Fatalf("token %d not > %d", tok2, tok1)
	}
	if err := m.Write(tok1, "k", "v"); !errors.Is(err, ErrStaleToken) {
		t.Fatalf("Write old token err = %v, want ErrStaleToken", err)
	}
	if err := m.Write(tok2, "k", "v"); err != nil {
		t.Fatalf("Write new token: %v", err)
	}
}

// 边界决策：token 是当前值减一与从未发出的巨大值，同属 ErrStaleToken。
func TestStaleTokenSameClass(t *testing.T) {
	m, _ := newManager()
	tok, _ := m.Acquire("A", 100)
	if _, err := m.Acquire("A", 100); err != nil { // 重新获取，tok 变旧
		t.Fatalf("re-Acquire: %v", err)
	}
	if err := m.Write(tok, "k", "v"); !errors.Is(err, ErrStaleToken) {
		t.Fatalf("prev token err = %v, want ErrStaleToken", err)
	}
	if err := m.Write(^uint64(0), "k", "v"); !errors.Is(err, ErrStaleToken) {
		t.Fatalf("huge token err = %v, want ErrStaleToken", err)
	}
}

// 续约成功延长有效期。
func TestRenewExtendsLease(t *testing.T) {
	m, c := newManager()
	tok, _ := m.Acquire("A", 100)
	c.Advance(90)
	if err := m.Renew("A", tok, 100); err != nil {
		t.Fatalf("Renew: %v", err)
	}
	c.Advance(90) // now=180，若未续约已过期
	if err := m.Write(tok, "k", "v"); err != nil {
		t.Fatalf("Write after renew: %v", err)
	}
	c.Advance(100) // now=280 > 190
	if err := m.Write(tok, "k", "v2"); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("Write err = %v, want ErrLeaseExpired", err)
	}
}
