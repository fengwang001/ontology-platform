package lease

import (
	"errors"
	"testing"
)

// TestMutualExclusionSecondHolderRejectedWhileActive 钉不变量 1（互斥）：
// "任意时刻至多一个持有者的租约有效"，以及实现注释
// "if m.held && m.holder != holder && now < m.expiresAt { return 0, ErrLeaseHeld }"。
// 改坏方式：把 Acquire 里的 ErrLeaseHeld 提前返回删掉，本测试会拿到
// 一个非 nil 之外的行为（B 成功并把 A 挤出）而失败。
func TestMutualExclusionSecondHolderRejectedWhileActive(t *testing.T) {
	clk := newFakeClock(0)
	m := New(clk.now)

	if _, err := m.Acquire("A", 100); err != nil {
		t.Fatalf("Acquire A: %v", err)
	}
	_, err := m.Acquire("B", 100)
	if !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("A 有效期间 B Acquire = %v, want ErrLeaseHeld", err)
	}
}

// TestOtherHolderCanAcquireExactlyAtExpiry 钉实现注释的设计决策：
// "租约有效当且仅当 held && now() < expiresAt（左闭右开区间）"。
// now == expiresAt-1 时他人仍被拒；now == expiresAt 那一刻他人即可 Acquire。
// 改坏方式：把 Acquire 里的 `now < m.expiresAt` 改成 `now <= m.expiresAt`，
// 本测试在边界时刻的 Acquire 会返回 ErrLeaseHeld 而失败。
func TestOtherHolderCanAcquireExactlyAtExpiry(t *testing.T) {
	clk := newFakeClock(0)
	m := New(clk.now)

	tokA, err := m.Acquire("A", 100)
	if err != nil {
		t.Fatalf("Acquire A: %v", err)
	}

	clk.set(99)
	if _, err := m.Acquire("B", 100); !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("now=99（到期前一刻）B Acquire = %v, want ErrLeaseHeld", err)
	}

	clk.set(100)
	tokB, err := m.Acquire("B", 100)
	if err != nil {
		t.Fatalf("now=100（恰好到期）B Acquire = %v, want nil", err)
	}
	if tokB <= tokA {
		t.Fatalf("token 未严格递增: tokA=%d tokB=%d", tokA, tokB)
	}
}

// TestAcquireTokenStrictlyMonotonicAcrossReleaseAndExpiry 钉不变量 2：
// "每次成功 Acquire 返回的 token 严格大于此前发出过的所有 token，
// 永不重复、永不回退；Release 不复位计数器。"对应实现注释
// "即使租约被释放，计数器也不复位（不变量 2）"。
// 改坏方式：把 Release 里的 m.token = 0（复位计数器），或把
// Acquire 里 `m.token++` 挪到成功分支之外/重复 Acquire 不递增，
// 本测试的严格递增断言都会失败。
func TestAcquireTokenStrictlyMonotonicAcrossReleaseAndExpiry(t *testing.T) {
	clk := newFakeClock(0)
	m := New(clk.now)

	var prev uint64
	issue := func(holder string, ttl int64) uint64 {
		t.Helper()
		tok, err := m.Acquire(holder, ttl)
		if err != nil {
			t.Fatalf("Acquire(%q): %v", holder, err)
		}
		if tok <= prev {
			t.Fatalf("token=%d 未严格大于上一个 %d", tok, prev)
		}
		prev = tok
		return tok
	}

	tokA := issue("A", 100)
	_ = issue("A", 100) // 同人重新获取也发新 token
	if err := m.Release("A", prev); err != nil {
		t.Fatalf("Release: %v", err)
	}
	issue("B", 100) // Release 后不复位
	if err := m.Release("B", prev); err != nil {
		t.Fatalf("Release: %v", err)
	}
	issue("C", 100)
	clk.set(1000)  // C 过期
	issue("D", 50) // 过期后他人接管，继续递增

	// 显式钉"永不重复"：历史首个 token 不再被发出。
	if _, err := m.Acquire("D", 50); err != nil {
		t.Fatalf("D 重复 Acquire: %v", err)
	}
	if prev == tokA {
		t.Fatalf("token 回退到了历史值 %d", tokA)
	}
}

// TestSameHolderReacquireIssuesNewTokenAndOldDies 钉实现注释里的设计决策：
// "同一 holder 在自己租约仍有效时再次 Acquire，视为"重新获取"——
// 签发一个全新 token 并刷新到期时刻，而不是报错……旧 token 立即失效，
// fencing 语义不被破坏。"
// 改坏方式：把 Acquire 里"同 holder 直接返回旧 token"（不执行 m.token++），
// 本测试的 tokOld != tokNew 断言失败；若改成报错，Acquire 的 nil 断言失败。
func TestSameHolderReacquireIssuesNewTokenAndOldDies(t *testing.T) {
	clk := newFakeClock(0)
	m := New(clk.now)

	tokOld, err := m.Acquire("A", 100)
	if err != nil {
		t.Fatalf("Acquire A: %v", err)
	}
	if err := m.Write(tokOld, "k", "v1"); err != nil {
		t.Fatalf("旧 token 首次写: %v", err)
	}

	clk.set(50)
	tokNew, err := m.Acquire("A", 100)
	if err != nil {
		t.Fatalf("同人有效期间再次 Acquire 不应报错: %v", err)
	}
	if tokNew == tokOld {
		t.Fatalf("重复 Acquire 应发新 token，两次都是 %d", tokOld)
	}

	// "旧 token 立即失效"：不是当前记录 token，归 ErrStaleToken。
	if err := m.Write(tokOld, "k", "evil"); !errors.Is(err, ErrStaleToken) {
		t.Fatalf("旧 token 写 = %v, want ErrStaleToken", err)
	}
	if got, _ := m.Read("k"); got != "v1" {
		t.Fatalf("被拒写产生了副作用，k=%q", got)
	}

	// "刷新到期时刻"：把时钟拨过旧到期时刻但不超过新到期时刻，新 token 仍可写。
	clk.set(149)
	if err := m.Write(tokNew, "k", "v2"); err != nil {
		t.Fatalf("新到期时刻应被刷新，now=149 写失败: %v", err)
	}
}

// TestAcquireRejectsInvalidArgs 补钉公开契约（冒烟测试只覆盖了 Acquire）：
// "holder == "" → ErrInvalidHolder；ttlMillis <= 0 → ErrInvalidTTL"，
// 且 ttlMillis 为负数同样拒绝。
func TestAcquireRejectsInvalidArgs(t *testing.T) {
	m := New(func() int64 { return 0 })
	if _, err := m.Acquire("", 100); !errors.Is(err, ErrInvalidHolder) {
		t.Fatalf("空 holder: %v", err)
	}
	if _, err := m.Acquire("A", -1); !errors.Is(err, ErrInvalidTTL) {
		t.Fatalf("负 ttl: %v", err)
	}
}
