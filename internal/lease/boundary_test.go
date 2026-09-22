package lease

// 本文件钉住"到期那一刻"（now 恰好等于 expiresAt）的三个判定侧：
// 续约、写入、有效性（他人能否 Acquire）。
//
// 实现注释原话（errors.go ErrLeaseExpired）：
// "租约在 now >= expiresAt 时失效，即到期那一刻起续约与写入都算失效。
// 采用左闭右开区间 [start, expiresAt) 可以让新持有者在
// now == expiresAt 时立刻 Acquire 成功，而旧持有者在同一时刻
// 的写必须被拒绝。"

import (
	"errors"
	"testing"
)

// 钉决策："到期那一刻起续约……算失效"。
// now == expiresAt 时 Renew 必须返回 ErrLeaseExpired（过期一侧），
// 而不是把边界当作仍有效而续约成功。
//
// 改坏对应（判别力）：manager.go Renew 中 `if now >= m.expiresAt`
// 若改成 `now > m.expiresAt`，本测试失败。
func TestBoundaryRenewExactlyAtExpiry(t *testing.T) {
	var clock int64
	m := New(func() int64 { return clock })
	tok, _ := m.Acquire("A", 10) // expiresAt = 10

	clock = 9 // 到期前一拍续约成功
	if err := m.Renew("A", tok, 10); err != nil {
		t.Fatalf("now=9 Renew: %v", err)
	}
	// 续约后 expiresAt = 19。
	clock = 19 // 恰好等于新的到期时刻
	err := m.Renew("A", tok, 10)
	if !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("now==expiresAt Renew = %v, want ErrLeaseExpired", err)
	}
}

// 钉决策："到期那一刻起……写入……算失效"，且"旧持有者在同一时刻
// 的写必须被拒绝"。
//
// 改坏对应（判别力）：store.go Write 中 `if now := m.now(); now >= m.expiresAt`
// 若改成 `now > m.expiresAt`，本测试失败。
func TestBoundaryWriteExactlyAtExpiry(t *testing.T) {
	var clock int64
	m := New(func() int64 { return clock })
	tok, _ := m.Acquire("A", 10)

	clock = 10
	err := m.Write(tok, "k", "boundary")
	if !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("now==expiresAt Write = %v, want ErrLeaseExpired", err)
	}
	if _, ok := m.Read("k"); ok {
		t.Fatal("到期边界被拒的写不得落盘")
	}
}

// 钉决策："新持有者在 now == expiresAt 时立刻 Acquire 成功"。
// 同一逻辑时刻，旧持有者的 Write/Renew 失败而新持有者 Acquire 成功，
// 这正是左闭右开区间保证互斥（不变量 1）的关键一侧。
//
// 改坏对应（判别力）：manager.go Acquire 中
// `if m.held && m.holder != holder && now < m.expiresAt`
// 若把 `<` 改成 `<=`，则到期那一刻 B 会被误拒，本测试失败。
func TestBoundaryAcquireByOtherExactlyAtExpiry(t *testing.T) {
	var clock int64
	m := New(func() int64 { return clock })
	tokA, _ := m.Acquire("A", 10)

	clock = 10 // 恰好到期
	tokB, err := m.Acquire("B", 10)
	if err != nil {
		t.Fatalf("到期那一刻 B 应能 Acquire, got %v", err)
	}
	if tokB <= tokA {
		t.Fatalf("新 token %d 未严格大于旧 token %d", tokB, tokA)
	}
	// 同一时刻旧持有者的操作仍必须被拒。
	if !errors.Is(m.Write(tokA, "k", "x"), ErrStaleToken) {
		t.Fatal("到期且被接管后，A 的旧 token 写应为 ErrStaleToken")
	}
}

// 钉 validLocked 的区间定义："租约有效当且仅当 held && now() < expiresAt
// （左闭右开区间）"。now=expiresAt-1 有效，now=expiresAt 无效。
// 用 Renew 的成功/失败间接观察该谓词（它是包内唯一在有效分支上
// 改变可观测状态的方法）。
func TestBoundaryValidLockedIsHalfOpenInterval(t *testing.T) {
	var clock int64
	m := New(func() int64 { return clock })
	tok, _ := m.Acquire("A", 10)

	clock = 9
	if err := m.Renew("A", tok, 1); err != nil {
		t.Fatalf("now=9 应仍有效, Renew: %v", err) // expiresAt 变为 10
	}
	clock = 10
	if err := m.Renew("A", tok, 1); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("now=10 应已失效, Renew = %v", err)
	}
}
