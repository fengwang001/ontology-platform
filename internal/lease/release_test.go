package lease

import (
	"errors"
	"testing"
)

// 本文件钉住 manager.go Release 的设计决策原文：
//   "设计决策：holder 与 token 都匹配当前记录时，即使租约已经过期，
//    Release 也算成功（幂等清理）。理由：释放的期望终态是
//    '该 holder 不再持有租约'，而过期的租约本来就已失效，
//    让清理路径幂等可以简化客户端的 defer Release 写法，
//    且不违反任何一条不变量。"
// 以及 manager.go 中 token 字段的注释：
//   "即使租约被释放，计数器也不复位（不变量 2）。"
//
// 判别力（改坏实现 → 失败的测试）：
//   - 在 Release 里加一段 `if now >= m.expiresAt { return ErrLeaseExpired }`
//     → TestReleaseExpiredLeaseSucceeds 失败。
//   - 把 Release 里的 `m.held = false` 之外再加 `m.token = 0`
//     → TestReleaseDoesNotResetTokenCounter 失败。

// 已过期但 holder/token 都匹配的租约，Release 必须成功（幂等清理）。
func TestReleaseExpiredLeaseSucceeds(t *testing.T) {
	var clk testClock
	m := New(clk.now)
	tok, err := m.Acquire("A", 100)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	clk.set(200) // 已过期，记录仍保留
	if err := m.Release("A", tok); err != nil {
		t.Fatalf("Release expired lease = %v, want nil（幂等清理）", err)
	}
	// 释放后他人可以立即获取，不必等任何时钟条件。
	if _, err := m.Acquire("B", 100); err != nil {
		t.Fatalf("Acquire after Release = %v, want nil", err)
	}
}

// 过期租约被他人接管后，原 holder 再 Release 必须返回 ErrNotHolder，
// 且不得误清新持有者的记录（Release 注释："防止误清他人的租约记录"）。
func TestReleaseAfterPreemptReturnsNotHolder(t *testing.T) {
	var clk testClock
	m := New(clk.now)
	tokA, err := m.Acquire("A", 100)
	if err != nil {
		t.Fatalf("Acquire A: %v", err)
	}
	clk.set(100)
	tokB, err := m.Acquire("B", 100)
	if err != nil {
		t.Fatalf("Acquire B: %v", err)
	}
	if err := m.Release("A", tokA); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("Release after preempt = %v, want ErrNotHolder", err)
	}
	// B 的租约必须完好：仍能写。
	if err := m.Write(tokB, "k", "v"); err != nil {
		t.Fatalf("B Write after A's failed Release = %v, want nil", err)
	}
}

// 钉不变量 2：Release 不复位 token 计数器。
// 释放后再次 Acquire，token 必须继续递增而不是从 1 重来。
func TestReleaseDoesNotResetTokenCounter(t *testing.T) {
	var clk testClock
	m := New(clk.now)
	tok1, err := m.Acquire("A", 100)
	if err != nil {
		t.Fatalf("Acquire #1: %v", err)
	}
	if err := m.Release("A", tok1); err != nil {
		t.Fatalf("Release: %v", err)
	}
	tok2, err := m.Acquire("A", 100)
	if err != nil {
		t.Fatalf("Acquire #2: %v", err)
	}
	if tok2 <= tok1 {
		t.Fatalf("Release 后 token 回退或重复（违反不变量 2）: %d -> %d", tok1, tok2)
	}
	// 旧 token 在释放后必须彻底失效：写与续约都被拒。
	if err := m.Write(tok1, "k", "v"); !errors.Is(err, ErrStaleToken) {
		t.Fatalf("Write with released token = %v, want ErrStaleToken", err)
	}
	if err := m.Renew("A", tok1, 100); !errors.Is(err, ErrTokenMismatch) {
		t.Fatalf("Renew with released token = %v, want ErrTokenMismatch", err)
	}
}

// 已释放的租约再 Release：记录已清除，holder 不再匹配 → ErrNotHolder。
// 钉住 Release 注释中"已释放"归入 ErrNotHolder 的分类。
func TestDoubleReleaseReturnsNotHolder(t *testing.T) {
	var clk testClock
	m := New(clk.now)
	tok, err := m.Acquire("A", 100)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := m.Release("A", tok); err != nil {
		t.Fatalf("first Release: %v", err)
	}
	if err := m.Release("A", tok); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("second Release = %v, want ErrNotHolder", err)
	}
}
