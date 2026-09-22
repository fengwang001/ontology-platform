package lease

import (
	"errors"
	"testing"
)

// 本文件钉住 errors.go 中 ErrNotHolder 的设计决策原文：
//   "设计决策：被抢占后调用 Renew/Release 返回 ErrNotHolder，
//    与'自己持有但已过期'返回的 ErrLeaseExpired 是不同类别。
//    理由：两种情形的恢复策略不同——被抢占意味着锁已被别人拿走，
//    调用者必须重新 Acquire；而自己过期且无人接管时，
//    语义上只是'让租约 lapse 了'。区分开便于调用方分别处理。"
// 以及 manager.go Renew 注释声明的三类错误：
//   "- holder 不是当前记录的持有者（被抢占/从未持有/已释放）：ErrNotHolder
//    - holder 匹配但 token 不符：ErrTokenMismatch
//    - holder 与 token 都匹配但租约已过期：ErrLeaseExpired"
//
// 判别力（改坏实现 → 失败的测试）：
//   - 把 Renew 里 ErrNotHolder 换成 ErrLeaseExpired（或合并两个分支）
//     → TestRenewAfterPreemptReturnsNotHolder 失败。
//   - 把 Renew 里 ErrTokenMismatch 换成 ErrStaleToken
//     → TestRenewWrongTokenReturnsTokenMismatch 失败。
//   - 把 Renew 里"先查 holder 再查过期"的顺序对调（先判过期）
//     → TestRenewAfterPreemptReturnsNotHolder 失败（被抢占者会拿到
//       ErrLeaseExpired 而非 ErrNotHolder）。

// 被抢占后 Renew 必须返回 ErrNotHolder，且与另外两个哨兵可区分。
func TestRenewAfterPreemptReturnsNotHolder(t *testing.T) {
	var clk testClock
	m := New(clk.now)
	tokA, err := m.Acquire("A", 100)
	if err != nil {
		t.Fatalf("Acquire A: %v", err)
	}
	clk.set(100) // A 过期，B 接管
	if _, err := m.Acquire("B", 100); err != nil {
		t.Fatalf("Acquire B: %v", err)
	}
	err = m.Renew("A", tokA, 100)
	if !errors.Is(err, ErrNotHolder) {
		t.Fatalf("Renew after preempt = %v, want ErrNotHolder", err)
	}
	if errors.Is(err, ErrLeaseExpired) || errors.Is(err, ErrTokenMismatch) {
		t.Fatalf("ErrNotHolder 与 ErrLeaseExpired/ErrTokenMismatch 必须是不同哨兵: %v", err)
	}
}

// holder 匹配但 token 不符 → ErrTokenMismatch，且不是另外两个。
func TestRenewWrongTokenReturnsTokenMismatch(t *testing.T) {
	var clk testClock
	m := New(clk.now)
	tok, err := m.Acquire("A", 100)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	err = m.Renew("A", tok+1, 100)
	if !errors.Is(err, ErrTokenMismatch) {
		t.Fatalf("Renew wrong token = %v, want ErrTokenMismatch", err)
	}
	if errors.Is(err, ErrNotHolder) || errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("ErrTokenMismatch 与 ErrNotHolder/ErrLeaseExpired 必须是不同哨兵: %v", err)
	}
}

// holder 与 token 都匹配但已过期（无人接管）→ ErrLeaseExpired，
// 且不是另外两个。注意此时记录仍保留，所以不是 ErrNotHolder。
func TestRenewAfterExpiryReturnsLeaseExpired(t *testing.T) {
	var clk testClock
	m := New(clk.now)
	tok, err := m.Acquire("A", 100)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	clk.set(150) // 过期，但无人接管，记录仍是 A
	err = m.Renew("A", tok, 100)
	if !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("Renew after expiry = %v, want ErrLeaseExpired", err)
	}
	if errors.Is(err, ErrNotHolder) || errors.Is(err, ErrTokenMismatch) {
		t.Fatalf("ErrLeaseExpired 与 ErrNotHolder/ErrTokenMismatch 必须是不同哨兵: %v", err)
	}
}

// 同一组三态在 Release 上同样成立（manager.go Release 注释：
// "holder 不匹配（已被他人抢占）仍返回 ErrNotHolder，
// token 不符仍返回 ErrTokenMismatch，防止误清他人的租约记录"）。
func TestReleaseErrorClassesMatchRenew(t *testing.T) {
	var clk testClock
	m := New(clk.now)
	tokA, err := m.Acquire("A", 100)
	if err != nil {
		t.Fatalf("Acquire A: %v", err)
	}
	if err := m.Release("B", tokA); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("Release by stranger = %v, want ErrNotHolder", err)
	}
	if err := m.Release("A", tokA+99); !errors.Is(err, ErrTokenMismatch) {
		t.Fatalf("Release wrong token = %v, want ErrTokenMismatch", err)
	}
	// 两次失败的 Release 不得清除租约：A 仍能正常写。
	if err := m.Write(tokA, "k", "v"); err != nil {
		t.Fatalf("Write after failed Releases = %v, want nil", err)
	}
}

// 三个哨兵两两互不相同：任何一个都不能 errors.Is 命中另外两个。
// 钉住"三个不同的哨兵错误"这一决策本身（防止有人把它们合并成一个）。
func TestSentinelErrorsAreDistinct(t *testing.T) {
	sentinels := []error{ErrNotHolder, ErrTokenMismatch, ErrLeaseExpired}
	for i, a := range sentinels {
		for j, b := range sentinels {
			if i != j && errors.Is(a, b) {
				t.Fatalf("哨兵错误 %v 与 %v 不可区分", a, b)
			}
		}
	}
}
