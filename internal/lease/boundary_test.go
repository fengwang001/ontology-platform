package lease

import (
	"errors"
	"testing"
)

// TestWriteSucceedsOneMsBeforeExpiry 钉左闭右开区间的左闭一侧：
// 实现注释 "租约有效当且仅当 held && now() < expiresAt（左闭右开区间）"，
// Write 的判定为 `if now := m.now(); now >= m.expiresAt { return ErrLeaseExpired }`。
// 改坏方式：把 Write 的 `now >= m.expiresAt` 改成 `now > m.expiresAt`
// 不会让本测试失败，但把它改成 `now+1 >= m.expiresAt`（提前一毫秒过期）会；
// 本测试与 TestWriteRejectedExactlyAtExpiry 一前一后共同夹住边界。
func TestWriteSucceedsOneMsBeforeExpiry(t *testing.T) {
	clk := newFakeClock(0)
	m := New(clk.now)
	tok, _ := m.Acquire("A", 100)

	clk.set(99)
	if err := m.Write(tok, "k", "v"); err != nil {
		t.Fatalf("now=99（到期前一刻）Write = %v, want nil", err)
	}
}

// TestWriteRejectedExactlyAtExpiry 钉实现注释：
// "租约在 now >= expiresAt 时失效，即到期那一刻起续约与写入都算失效"，
// 以及 store.go 的 "token 匹配但租约已过期（含恰好到期的边界时刻）：ErrLeaseExpired"。
// 不变量 5："租约到期后持有者立即不再有效，哪怕他还在用旧 token 写。"
// 改坏方式：把 Write 的 `now >= m.expiresAt` 改成 `now > m.expiresAt`，
// 本测试在 now=100 的写会成功而失败。
func TestWriteRejectedExactlyAtExpiry(t *testing.T) {
	clk := newFakeClock(0)
	m := New(clk.now)
	tok, _ := m.Acquire("A", 100)
	_ = m.Write(tok, "k", "before")

	clk.set(100) // now 恰好等于 expiresAt
	err := m.Write(tok, "k", "after")
	if !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("now=100 Write = %v, want ErrLeaseExpired", err)
	}
	if got, _ := m.Read("k"); got != "before" {
		t.Fatalf("到期时刻被拒的写产生副作用，k=%q", got)
	}
}

// TestRenewRejectedExactlyAtExpiry 钉同一句设计决策在 Renew 上的一侧：
// "租约在 now >= expiresAt 时失效，即到期那一刻起续约与写入都算失效"，
// Renew 实现 `if now >= m.expiresAt { return ErrLeaseExpired }`。
// 改坏方式：把 Renew 的 `now >= m.expiresAt` 改成 `now > m.expiresAt`，
// 本测试在 now=100 的续约会成功（到期被刷到 200）而失败。
func TestRenewRejectedExactlyAtExpiry(t *testing.T) {
	clk := newFakeClock(0)
	m := New(clk.now)
	tok, _ := m.Acquire("A", 100)

	clk.set(100)
	if err := m.Renew("A", tok, 100); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("now=100 Renew = %v, want ErrLeaseExpired", err)
	}

	// 续约失败不得刷新到期时刻：拨到 101 后新 Acquire 之外旧 token 依旧过期。
	clk.set(101)
	if err := m.Renew("A", tok, 100); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("被拒续约不应刷新到期时刻: %v", err)
	}
}

// TestRenewSucceedsOneMsBeforeExpiry 钉 Renew 的有效一侧，与
// TestRenewRejectedExactlyAtExpiry 共同夹住 `< expiresAt` 边界。
// 改坏方式：把 Renew 里 `now >= m.expiresAt` 改成 `now+1 >= m.expiresAt`，
// now=99 的续约被拒，本测试失败。
func TestRenewSucceedsOneMsBeforeExpiry(t *testing.T) {
	clk := newFakeClock(0)
	m := New(clk.now)
	tok, _ := m.Acquire("A", 100)

	clk.set(99)
	if err := m.Renew("A", tok, 100); err != nil {
		t.Fatalf("now=99 Renew = %v, want nil", err)
	}
	// 续约把到期从 100 刷到 199：now=150 的旧 token 写应仍成功。
	clk.set(150)
	if err := m.Write(tok, "k", "v"); err != nil {
		t.Fatalf("续约后 now=150 Write = %v, want nil", err)
	}
}

// TestValidityFlipsAtBoundaryAndExpiredRecordBlocksUntilAcquire 钉 manager.go
// 注释 "过期后记录仍保留，用于把 ErrLeaseExpired 与 ErrNotHolder 区分开"：
// 过期的判定（validLocked: held && now < expiresAt）在边界翻转后，
// 记录仍属于 A——此时 B 不经过 Acquire 不持有任何东西，而 A 的 Renew
// 拿到的是 ErrLeaseExpired（不是 ErrNotHolder）。
// 改坏方式：把 Renew 里过期检查挪到 holder 检查之前并清记录，或把
// `if !m.held || m.holder != holder` 改成过期即视为 NotHolder，
// 本测试的 ErrLeaseExpired 断言失败。
func TestValidityFlipsAtBoundaryAndExpiredRecordBlocksUntilAcquire(t *testing.T) {
	clk := newFakeClock(0)
	m := New(clk.now)
	tok, _ := m.Acquire("A", 100)

	clk.set(100)
	if err := m.Renew("A", tok, 100); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("过期记录仍在，应是 ErrLeaseExpired，得到 %v", err)
	}
	// B 此前从未持有；在 A 过期记录仍在时，B 的 Renew 是 ErrNotHolder。
	if err := m.Renew("B", 999, 100); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("B 从未持有，Renew 应 ErrNotHolder，得到 %v", err)
	}
}
