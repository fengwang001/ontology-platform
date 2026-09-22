package lease

import (
	"errors"
	"testing"
)

// 本文件钉住 manager.go Acquire 的设计决策原文：
//   "设计决策：同一 holder 在自己租约仍有效时再次 Acquire，
//    视为'重新获取'——签发一个全新 token 并刷新到期时刻，而不是报错。
//    ……客户端在丢失旧 token（如重启后状态未持久化）时可以用同一身份
//    安全地重新获取，旧 token 立即失效，fencing 语义不被破坏。"
//
// 判别力（改坏实现 → 失败的测试）：
//   - 把 Acquire 的条件 `m.holder != holder` 删掉（同 holder 也报 ErrLeaseHeld）
//     → TestSameHolderReacquireIssuesNewToken 失败。
//   - 把 `m.token++` 挪到条件分支之后（同 holder 复用旧 token）
//     → TestSameHolderReacquireIssuesNewToken 失败（token 不递增）。
//   - 重新获取后不清旧 token 的效（例如 Write 接受任何 <= 当前值的 token）
//     → TestOldTokenStaleAfterReacquire 失败。

// 同 holder 在租约有效时再次 Acquire：成功、发新 token（严格递增）。
func TestSameHolderReacquireIssuesNewToken(t *testing.T) {
	var clk testClock
	m := New(clk.now)
	tok1, err := m.Acquire("A", 100)
	if err != nil {
		t.Fatalf("Acquire #1: %v", err)
	}
	clk.set(50) // 租约仍然有效
	tok2, err := m.Acquire("A", 100)
	if err != nil {
		t.Fatalf("re-Acquire while valid = %v, want nil（不是续约也不是报错）", err)
	}
	if tok2 <= tok1 {
		t.Fatalf("重复 Acquire 未发新 token（违反不变量 2）: %d -> %d", tok1, tok2)
	}
}

// 重新获取后旧 token 立即失效：写被拒（ErrStaleToken），且 Read 不变。
func TestOldTokenStaleAfterReacquire(t *testing.T) {
	var clk testClock
	m := New(clk.now)
	tok1, err := m.Acquire("A", 100)
	if err != nil {
		t.Fatalf("Acquire #1: %v", err)
	}
	if err := m.Write(tok1, "k", "old"); err != nil {
		t.Fatalf("Write with tok1: %v", err)
	}
	tok2, err := m.Acquire("A", 100)
	if err != nil {
		t.Fatalf("re-Acquire: %v", err)
	}
	if err := m.Write(tok1, "k", "evil"); !errors.Is(err, ErrStaleToken) {
		t.Fatalf("Write with old token = %v, want ErrStaleToken", err)
	}
	if got, _ := m.Read("k"); got != "old" {
		t.Fatalf("被拒绝的写改变了内容（违反不变量 4）: got %q", got)
	}
	// 旧 token 连续约也不行。
	if err := m.Renew("A", tok1, 100); !errors.Is(err, ErrTokenMismatch) {
		t.Fatalf("Renew with old token = %v, want ErrTokenMismatch", err)
	}
	// 新 token 正常工作。
	if err := m.Write(tok2, "k", "new"); err != nil {
		t.Fatalf("Write with new token = %v, want nil", err)
	}
}

// 重新获取会刷新到期时刻：越过旧 expiresAt 后新租约仍然有效。
// 钉住决策中"刷新到期时刻"这半句——若实现只发新 token 不刷新，
// 本测试会在 now=150 时拿到 ErrLeaseExpired。
func TestReacquireRefreshesExpiry(t *testing.T) {
	var clk testClock
	m := New(clk.now)
	if _, err := m.Acquire("A", 100); err != nil { // 旧 expiresAt = 100
		t.Fatalf("Acquire #1: %v", err)
	}
	clk.set(50)
	tok2, err := m.Acquire("A", 100) // 新 expiresAt 应为 150
	if err != nil {
		t.Fatalf("re-Acquire: %v", err)
	}
	clk.set(149) // 越过旧到期时刻，但在新到期时刻之前
	if err := m.Write(tok2, "k", "v"); err != nil {
		t.Fatalf("Write past old expiry = %v, want nil（到期时刻应已刷新）", err)
	}
}

// 他人持有有效租约时，第三方 Acquire 必须被拒（不变量 1 的 Acquire 一侧），
// 且拒绝不发出 token：下一次成功 Acquire 的 token 只比上次大 1。
func TestFailedAcquireDoesNotConsumeToken(t *testing.T) {
	var clk testClock
	m := New(clk.now)
	tok1, err := m.Acquire("A", 100)
	if err != nil {
		t.Fatalf("Acquire A: %v", err)
	}
	if _, err := m.Acquire("B", 100); !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("Acquire B while held = %v, want ErrLeaseHeld", err)
	}
	if err := m.Release("A", tok1); err != nil {
		t.Fatalf("Release: %v", err)
	}
	tok2, err := m.Acquire("B", 100)
	if err != nil {
		t.Fatalf("Acquire B after release: %v", err)
	}
	if tok2 != tok1+1 {
		t.Fatalf("失败的 Acquire 消耗了 token: tok1=%d tok2=%d", tok1, tok2)
	}
}
