package lease

import (
	"errors"
	"testing"
)

// 本文件钉住"到期那一刻"的边界取舍。实现注释原话：
//   - manager.go: "租约有效当且仅当 held && now() < expiresAt（左闭右开区间）"
//   - errors.go ErrLeaseExpired: "租约在 now >= expiresAt 时失效，即到期那一刻起
//     续约与写入都算失效……采用左闭右开区间 [start, expiresAt) 可以让新持有者在
//     now == expiresAt 时立刻 Acquire 成功，而旧持有者在同一时刻的写必须被拒绝"
//
// 判别力（改坏方式 -> 失败的测试）：
//   - validLocked / Renew / Write 中边界判定由 now < expiresAt 改成
//     now <= expiresAt（或 now >= expiresAt 改成 now > expiresAt）
//     -> TestBoundaryWriteAtExpiry、TestBoundaryRenewAtExpiry、
//        TestBoundaryOtherAcquiresAtExpiry 全部失败。

// TestBoundaryWriteValidBeforeExpiry 钉住左闭侧：now == expiresAt-1 时
// 写入与续约仍然有效（不变量 5 的另一面：未到期就必须有效）。
func TestBoundaryWriteValidBeforeExpiry(t *testing.T) {
	m, c := newTestManager()
	tok, err := m.Acquire("A", 100) // expiresAt = 100
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	c.Set(99)
	if err := m.Write(tok, "k", "v"); err != nil {
		t.Fatalf("now=expiresAt-1 时 Write 应成功, got %v", err)
	}
	if err := m.Renew("A", tok, 100); err != nil { // expiresAt = 199
		t.Fatalf("now=expiresAt-1 时 Renew 应成功, got %v", err)
	}
	c.Set(198)
	if err := m.Write(tok, "k", "v2"); err != nil {
		t.Fatalf("续约后 now=expiresAt-1 时 Write 应成功, got %v", err)
	}
}

// TestBoundaryWriteAtExpiry 钉住右开侧：now == expiresAt 那一刻，
// Write 必须返回 ErrLeaseExpired（而不是成功、也不是 ErrStaleToken）。
func TestBoundaryWriteAtExpiry(t *testing.T) {
	m, c := newTestManager()
	tok, _ := m.Acquire("A", 100)
	c.Set(100) // 恰好到期
	err := m.Write(tok, "k", "v")
	if !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("到期那一刻 Write = %v, 期望 ErrLeaseExpired", err)
	}
	if errors.Is(err, ErrStaleToken) {
		t.Fatal("到期那一刻 Write 不应归为 ErrStaleToken：token 本身仍匹配记录")
	}
	if _, ok := m.Read("k"); ok {
		t.Fatal("被拒的写不得留下痕迹（不变量 4）")
	}
}

// TestBoundaryRenewAtExpiry 钉住右开侧：now == expiresAt 那一刻，
// Renew 必须返回 ErrLeaseExpired（holder 与 token 都匹配时）。
func TestBoundaryRenewAtExpiry(t *testing.T) {
	m, c := newTestManager()
	tok, _ := m.Acquire("A", 100)
	c.Set(100) // 恰好到期
	if err := m.Renew("A", tok, 100); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("到期那一刻 Renew = %v, 期望 ErrLeaseExpired", err)
	}
	// 续约失败后到期时刻不得被刷新：下一刻写入仍须被拒。
	if err := m.Write(tok, "k", "v"); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("失败的 Renew 不得刷新租约, Write = %v", err)
	}
}

// TestBoundaryOtherAcquiresAtExpiry 钉住实现注释"新持有者在
// now == expiresAt 时立刻 Acquire 成功"：同一时刻旧持有者已失效、
// 新持有者已可接管，两侧共同保证互斥（不变量 1）不出现空窗或重叠。
func TestBoundaryOtherAcquiresAtExpiry(t *testing.T) {
	m, c := newTestManager()
	tokA, _ := m.Acquire("A", 100)

	c.Set(99)
	if _, err := m.Acquire("B", 100); !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("now=99 时 B Acquire = %v, 期望 ErrLeaseHeld", err)
	}

	c.Set(100) // 恰好到期：同一时刻判旧写失效、新 Acquire 成功
	tokB, err := m.Acquire("B", 100)
	if err != nil {
		t.Fatalf("now=expiresAt 时 B Acquire 应成功, got %v", err)
	}
	if tokB != tokA+1 {
		t.Fatalf("tokB = %d, 期望 %d（不变量 2：严格递增）", tokB, tokA+1)
	}
	if err := m.Write(tokA, "k", "evil"); !errors.Is(err, ErrStaleToken) {
		t.Fatalf("同一时刻旧 token 写 = %v, 期望 ErrStaleToken（不变量 3）", err)
	}
	if err := m.Write(tokB, "k", "ok"); err != nil {
		t.Fatalf("新持有者同一时刻写应成功, got %v", err)
	}
}
