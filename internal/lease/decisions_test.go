package lease

import (
	"errors"
	"testing"
)

// 决策 1：到期那一刻（now == expiresAt）续约与写入都算失效。
func TestExpiryBoundaryIsExclusive(t *testing.T) {
	m, c := newManager()
	tok, _ := m.Acquire("A", 100)
	c.set(99) // 还差 1ms 到期：仍有效
	if err := m.Renew("A", tok, 100); err != nil {
		t.Fatalf("Renew before expiry = %v, want nil", err)
	}
	if err := m.Write(tok, "k", "v"); err != nil {
		t.Fatalf("Write before expiry = %v, want nil", err)
	}
	c.set(199) // 续约后 expiresAt = 99+100 = 199，此刻恰好到期
	if err := m.Renew("A", tok, 100); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("Renew at expiry = %v, want ErrLeaseExpired", err)
	}
	if err := m.Write(tok, "k", "x"); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("Write at expiry = %v, want ErrLeaseExpired", err)
	}
	if got, _ := m.Read("k"); got != "v" {
		t.Fatalf("Read = %q, want %q (rejected write must not land)", got, "v")
	}
}

// 决策 2：被抢占后 Renew 报 ErrNotHolder，与 ErrLeaseExpired 不同类。
func TestRenewAfterPreemption(t *testing.T) {
	m, c := newManager()
	tokA, _ := m.Acquire("A", 100)
	c.add(100)
	if _, err := m.Acquire("B", 100); err != nil {
		t.Fatal(err)
	}
	err := m.Renew("A", tokA, 100)
	if !errors.Is(err, ErrNotHolder) {
		t.Fatalf("Renew after preemption = %v, want ErrNotHolder", err)
	}
	if errors.Is(err, ErrLeaseExpired) {
		t.Fatal("preemption must not be reported as ErrLeaseExpired")
	}
}

// 决策 2 的另一半：仍是登记持有者但租约自然到期，报 ErrLeaseExpired。
func TestRenewAfterNaturalExpiry(t *testing.T) {
	m, c := newManager()
	tok, _ := m.Acquire("A", 100)
	c.add(100)
	if err := m.Renew("A", tok, 100); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("Renew after expiry = %v, want ErrLeaseExpired", err)
	}
}

// 决策 3：释放已过期或已被抢占的租约算失败。
func TestReleaseInvalidLease(t *testing.T) {
	m, c := newManager()
	tokA, _ := m.Acquire("A", 100)
	c.add(100)
	if err := m.Release("A", tokA); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("Release expired = %v, want ErrLeaseExpired", err)
	}
	tokB, _ := m.Acquire("B", 100)
	if err := m.Release("A", tokA); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("Release preempted = %v, want ErrNotHolder", err)
	}
	if err := m.Release("B", tokA); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("Release wrong token = %v, want ErrNotHolder", err)
	}
	if err := m.Release("B", tokB); err != nil {
		t.Fatalf("Release current holder = %v, want nil", err)
	}
	// 已释放后再释放：租约纪元已不存在。
	if err := m.Release("B", tokB); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("Release twice = %v, want ErrNotHolder", err)
	}
}

// 决策 4：持有者在租约有效时重复 Acquire 报 ErrAlreadyHeld，
// 不发新 token、不延长租约。
func TestReAcquireWhileHolding(t *testing.T) {
	m, c := newManager()
	tok, _ := m.Acquire("A", 100)
	if _, err := m.Acquire("A", 100); !errors.Is(err, ErrAlreadyHeld) {
		t.Fatalf("re-Acquire = %v, want ErrAlreadyHeld", err)
	}
	c.add(100) // 原租约按原时刻到期，证明重复 Acquire 没有续期效果
	if _, err := m.Acquire("B", 100); err != nil {
		t.Fatalf("Acquire after original expiry = %v, want nil", err)
	}
	if err := m.Write(tok, "k", "v"); !errors.Is(err, ErrStaleToken) {
		t.Fatalf("old token after new epoch = %v, want ErrStaleToken", err)
	}
}

// 决策 5：被取代的旧 token 与从未签发的巨大 token 是不同类错误，
// 但都被 fencing 拒绝且无副作用。
func TestStaleVsUnknownToken(t *testing.T) {
	m, c := newManager()
	tokA, _ := m.Acquire("A", 100)
	if err := m.Write(tokA, "k", "v"); err != nil {
		t.Fatal(err)
	}
	c.add(100)
	tokB, _ := m.Acquire("B", 100)
	if err := m.Write(tokB-1, "k", "stale"); !errors.Is(err, ErrStaleToken) {
		t.Fatalf("Write(current-1) = %v, want ErrStaleToken", err)
	}
	if err := m.Write(1<<60, "k", "forged"); !errors.Is(err, ErrUnknownToken) {
		t.Fatalf("Write(huge) = %v, want ErrUnknownToken", err)
	}
	if got, _ := m.Read("k"); got != "v" {
		t.Fatalf("Read = %q, want %q", got, "v")
	}
}

// 续约从当前时刻起算，而不是在旧到期时刻上累加。
func TestRenewExtendsFromNow(t *testing.T) {
	m, c := newManager()
	tok, _ := m.Acquire("A", 100)
	c.set(50)
	if err := m.Renew("A", tok, 100); err != nil {
		t.Fatal(err)
	}
	c.set(149) // 旧上界 100 已过，新上界 150 未到：仍有效
	if err := m.Write(tok, "k", "v"); err != nil {
		t.Fatalf("Write within renewed lease = %v, want nil", err)
	}
	c.set(150)
	if err := m.Write(tok, "k", "x"); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("Write at renewed expiry = %v, want ErrLeaseExpired", err)
	}
}
