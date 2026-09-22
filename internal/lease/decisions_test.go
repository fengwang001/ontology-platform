package lease

import (
	"errors"
	"testing"
)

// 本文件钉住实现在注释里明确声明、但无法从五条不变量直接推出的取舍。
//
// 判别力（改坏方式 -> 失败的测试）：
//   - Release 增加"过期即返回 ErrLeaseExpired"的提前返回
//     -> TestReleaseExpiredLeaseSucceeds 失败。
//   - Acquire 对"同人重复获取"改为返回 ErrLeaseHeld
//     -> TestSameHolderReAcquireIssuesNewToken 失败。
//   - Acquire 同人重复获取时复用旧 token（不执行 token++）
//     -> TestSameHolderReAcquireIssuesNewToken 的 tok2==tok1+1 断言失败。
//   - Acquire 同人重复获取时不刷新 expiresAt
//     -> TestSameHolderReAcquireRefreshesExpiry 失败。

// TestSameHolderReAcquireIssuesNewToken 钉住 Acquire 注释：
// "同一 holder 在自己租约仍有效时再次 Acquire，视为重新获取——签发一个
// 全新 token 并刷新到期时刻，而不是报错……旧 token 立即失效"。
func TestSameHolderReAcquireIssuesNewToken(t *testing.T) {
	m, c := newTestManager()
	tok1, _ := m.Acquire("A", 100)
	c.Set(50) // 租约仍有效（50 < 100）

	tok2, err := m.Acquire("A", 100)
	if err != nil {
		t.Fatalf("同人有效期内重复 Acquire 应成功, got %v", err)
	}
	if tok2 != tok1+1 {
		t.Fatalf("tok2 = %d, 期望 %d：必须签发全新 token（不变量 2）", tok2, tok1+1)
	}
	// 旧 token 立即失效（不变量 3）。
	if err := m.Write(tok1, "k", "x"); !errors.Is(err, ErrStaleToken) {
		t.Fatalf("旧 token Write = %v, 期望 ErrStaleToken", err)
	}
	// 新 token 可用，且期间互斥不被打破（不变量 1）。
	if _, err := m.Acquire("B", 100); !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("A 仍持有, B Acquire = %v, 期望 ErrLeaseHeld", err)
	}
	if err := m.Write(tok2, "k", "v"); err != nil {
		t.Fatalf("新 token Write 应成功, got %v", err)
	}
}

// TestSameHolderReAcquireRefreshesExpiry 钉住同一注释的"刷新到期时刻"：
// 重复获取后，租约寿命从重新获取的时刻重新计算，而非沿用旧到期时刻。
func TestSameHolderReAcquireRefreshesExpiry(t *testing.T) {
	m, c := newTestManager()
	m.Acquire("A", 100) // expiresAt = 100
	c.Set(90)
	tok2, _ := m.Acquire("A", 100) // expiresAt 应刷新为 190

	c.Set(150) // 旧到期时刻 100 已过；若未刷新此处必失败
	if err := m.Write(tok2, "k", "v"); err != nil {
		t.Fatalf("重复获取应刷新到期时刻, now=150 Write = %v", err)
	}
	c.Set(190) // 新到期时刻边界：右开，失效
	if err := m.Write(tok2, "k", "x"); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("now=190 Write = %v, 期望 ErrLeaseExpired", err)
	}
}

// TestReleaseExpiredLeaseSucceeds 钉住 Release 注释：
// "holder 与 token 都匹配当前记录时，即使租约已经过期，Release 也算成功
// （幂等清理）"。并验证清理后记录确实被清除、token 计数器不复位。
func TestReleaseExpiredLeaseSucceeds(t *testing.T) {
	m, c := newTestManager()
	tok, _ := m.Acquire("A", 100)
	c.Set(500) // 已过期很久，但记录仍在、holder 与 token 都匹配

	if err := m.Release("A", tok); err != nil {
		t.Fatalf("过期但匹配的 Release 应成功（幂等清理）, got %v", err)
	}
	// 记录已清除：再次 Release/Renew 都应是 ErrNotHolder 而非 ErrLeaseExpired。
	if err := m.Release("A", tok); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("清理后再次 Release = %v, 期望 ErrNotHolder", err)
	}
	if err := m.Renew("A", tok, 100); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("清理后 Renew = %v, 期望 ErrNotHolder", err)
	}
	// 计数器不复位（不变量 2）：下一个 Acquire 必须拿到 tok+1。
	tok2, err := m.Acquire("B", 100)
	if err != nil || tok2 != tok+1 {
		t.Fatalf("Release 后 Acquire = (%d, %v), 期望 token %d", tok2, err, tok+1)
	}
}

// TestReleaseFailedKeepsLease 钉住 Release 注释"防止误清他人的租约记录"：
// 任何失败的 Release 都不得改变当前租约记录。
func TestReleaseFailedKeepsLease(t *testing.T) {
	m, _ := newTestManager()
	tok, _ := m.Acquire("A", 100)

	if err := m.Release("B", tok); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("他人 Release = %v, 期望 ErrNotHolder", err)
	}
	if err := m.Release("A", tok+1); !errors.Is(err, ErrTokenMismatch) {
		t.Fatalf("错 token Release = %v, 期望 ErrTokenMismatch", err)
	}
	// 两次失败的 Release 之后，A 的租约必须完好。
	if err := m.Write(tok, "k", "v"); err != nil {
		t.Fatalf("失败的 Release 不得清除租约, Write = %v", err)
	}
	if _, err := m.Acquire("B", 100); !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("租约应仍被 A 持有, B Acquire = %v", err)
	}
}
