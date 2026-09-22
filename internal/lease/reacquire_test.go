package lease

// 本文件钉住 Acquire 的设计决策（manager.go Acquire 注释原话）：
// "同一 holder 在自己租约仍有效时再次 Acquire，视为'重新获取'——
// 签发一个全新 token 并刷新到期时刻，而不是报错。……
// 旧 token 立即失效，fencing 语义不被破坏。"

import (
	"errors"
	"testing"
)

// 钉：有效期间同 holder 再 Acquire 不报错，而是发一个严格更大的新 token。
//
// 改坏对应（判别力）：
//   - 若 Acquire 对同 holder 直接 `return 0, ErrLeaseHeld`，
//     本测试失败；
//   - 若把 `m.token++` 挪到 holder/TTL 参数校验之前（提前发号），
//     TestReacquireDoesNotConsumeTokenOnInvalidArgs 失败。
func TestReacquireSameHolderIssuesNewToken(t *testing.T) {
	var clock int64
	m := New(func() int64 { return clock })

	tok1, err := m.Acquire("A", 10)
	if err != nil {
		t.Fatalf("首次 Acquire: %v", err)
	}
	clock = 5 // 租约仍有效
	tok2, err := m.Acquire("A", 10)
	if err != nil {
		t.Fatalf("有效期间同 holder 重获应成功, got %v", err)
	}
	if tok2 != tok1+1 {
		t.Fatalf("新 token = %d, want %d", tok2, tok1+1)
	}
}

// 钉："旧 token 立即失效，fencing 语义不被破坏。"
// 重获后旧 token 写必须是 ErrStaleToken，且不得有副作用。
//
// 改坏对应（判别力）：若重获时不覆盖 m.token（复用旧号），
// 旧 token 仍能写，本测试失败。
func TestReacquireOldTokenImmediatelyInvalid(t *testing.T) {
	var clock int64
	m := New(func() int64 { return clock })
	tok1, _ := m.Acquire("A", 100)
	if err := m.Write(tok1, "k", "v1"); err != nil {
		t.Fatalf("旧 token 首次写: %v", err)
	}

	tok2, _ := m.Acquire("A", 100) // 仍有效，重获
	if err := m.Write(tok1, "k", "stale"); !errors.Is(err, ErrStaleToken) {
		t.Fatalf("重获后旧 token 写 = %v, want ErrStaleToken", err)
	}
	if got, _ := m.Read("k"); got != "v1" {
		t.Fatalf("被拒的旧 token 写污染了 store: %q", got)
	}
	if err := m.Write(tok2, "k", "v2"); err != nil {
		t.Fatalf("新 token 写应成功: %v", err)
	}

	// 旧 token 的 Renew/Release 同样按"token 不符"处理，不能误操作新租约。
	if err := m.Renew("A", tok1, 100); !errors.Is(err, ErrTokenMismatch) {
		t.Fatalf("旧 token Renew = %v, want ErrTokenMismatch", err)
	}
	if err := m.Release("A", tok1); !errors.Is(err, ErrTokenMismatch) {
		t.Fatalf("旧 token Release = %v, want ErrTokenMismatch", err)
	}
}

// 钉：重获"刷新到期时刻"——以重获时刻为起点重新计时，
// 而不是沿用旧的 expiresAt。
func TestReacquireRefreshesExpiry(t *testing.T) {
	var clock int64
	m := New(func() int64 { return clock })
	tok1, _ := m.Acquire("A", 10) // expiresAt = 10

	clock = 9                       // 距旧到期只剩 1ms
	tok2, err := m.Acquire("A", 10) // 重获后 expiresAt 应为 19
	if err != nil {
		t.Fatalf("重获: %v", err)
	}
	clock = 15 // 若沿用旧 expiresAt=10 此刻已过期
	if err := m.Write(tok2, "k", "v"); err != nil {
		t.Fatalf("重获应已刷新到期时刻, Write: %v", err)
	}
	clock = 19 // 恰好等于新的到期时刻：必须失效
	if err := m.Write(tok2, "k", "x"); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("新到期时刻写 = %v, want ErrLeaseExpired", err)
	}
	_ = tok1
}

// 钉参数校验在发号之前：非法 Acquire 不得消耗 token 序号，
// 否则下一次合法 Acquire 的号会出现非预期跳号（不变量 2 的发号纪律）。
//
// 改坏对应（判别力）：把 manager.go Acquire 中的 `m.token++`
// 挪到两个参数校验之前，本测试失败。
func TestReacquireDoesNotConsumeTokenOnInvalidArgs(t *testing.T) {
	m := New(func() int64 { return 0 })
	tok1, _ := m.Acquire("A", 10)
	if _, err := m.Acquire("", 10); !errors.Is(err, ErrInvalidHolder) {
		t.Fatalf("空 holder: %v", err)
	}
	if _, err := m.Acquire("A", 0); !errors.Is(err, ErrInvalidTTL) {
		t.Fatalf("ttl=0: %v", err)
	}
	tok2, err := m.Acquire("A", 10) // 同 holder 有效重获
	if err != nil {
		t.Fatalf("合法重获: %v", err)
	}
	if tok2 != tok1+1 {
		t.Fatalf("非法调用消耗了 token: %d, want %d", tok2, tok1+1)
	}
}
