package lease

import (
	"errors"
	"testing"
)

// 钉 Renew 注释里声明的三类错误必须是三个不同的哨兵：
//   - holder 不是当前记录的持有者（被抢占/从未持有/已释放）：ErrNotHolder
//   - holder 匹配但 token 不符：ErrTokenMismatch
//   - holder 与 token 都匹配但租约已过期：ErrLeaseExpired
// 检查顺序（实现取舍）：先 holder，再 token，最后过期。

func newPreemptedPair(t *testing.T, clk *fakeClock) (*Manager, uint64) {
	t.Helper()
	m := New(clk.now)
	tokA, err := m.Acquire("A", 100)
	if err != nil {
		t.Fatalf("Acquire A: %v", err)
	}
	clk.set(100) // A 到期
	if _, err := m.Acquire("B", 100); err != nil {
		t.Fatalf("B 接管: %v", err)
	}
	return m, tokA
}

// TestPreemptedHolderRenewGetsNotHolder 钉 errors.go 的设计决策原话：
// "被抢占后调用 Renew/Release 返回 ErrNotHolder，与"自己持有但已过期"
// 返回的 ErrLeaseExpired 是不同类别。"
// 改坏方式：把 Renew 的第一个返回换成 ErrLeaseExpired，本测试失败。
func TestPreemptedHolderRenewGetsNotHolder(t *testing.T) {
	clk := newFakeClock(0)
	m, tokA := newPreemptedPair(t, clk)

	err := m.Renew("A", tokA, 100)
	if !errors.Is(err, ErrNotHolder) {
		t.Fatalf("被抢占者 Renew = %v, want ErrNotHolder", err)
	}
}

// TestExpiredButStillHolderRenewGetsLeaseExpired 对照场景：
// holder 与 token 都匹配但租约已过期 → ErrLeaseExpired。
// 改坏方式：把 Renew 的过期分支返回 ErrNotHolder，本测试失败。
func TestExpiredButStillHolderRenewGetsLeaseExpired(t *testing.T) {
	clk := newFakeClock(0)
	m := New(clk.now)
	tok, _ := m.Acquire("A", 100)
	clk.set(100)

	err := m.Renew("A", tok, 100)
	if !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("过期持有者 Renew = %v, want ErrLeaseExpired", err)
	}
}

// TestHolderWithWrongTokenRenewGetsTokenMismatch 对照场景：
// "holder 匹配但 token 不符：ErrTokenMismatch"。
// 用同 holder 重新 Acquire 制造一个"曾经合法、现已过时"的旧 token。
// 改坏方式：把 Renew 的 token 分支返回 ErrNotHolder 或 ErrLeaseExpired，
// 本测试失败。
func TestHolderWithWrongTokenRenewGetsTokenMismatch(t *testing.T) {
	clk := newFakeClock(0)
	m := New(clk.now)
	tokOld, _ := m.Acquire("A", 100)
	tokNew, _ := m.Acquire("A", 100) // 同 holder 重获，A 仍是记录持有者

	err := m.Renew("A", tokOld, 100)
	if !errors.Is(err, ErrTokenMismatch) {
		t.Fatalf("旧 token Renew = %v, want ErrTokenMismatch", err)
	}
	// 当前 token 仍可正常续约，证明错误来自 token 不符而非租约状态。
	if err := m.Renew("A", tokNew, 100); err != nil {
		t.Fatalf("当前 token Renew: %v", err)
	}
}

// TestRenewSentinelsArePairwiseDistinct 钉"三者必须能被 errors.Is 分别判定"：
// 三个哨兵两两不同，且各自场景只命中自己、不命中另外两个。
// 改坏方式：把任意两个哨兵定义成同一个 errors.New 值（或让某个分支
// 返回另一个哨兵），对应一行断言立即失败。
func TestRenewSentinelsArePairwiseDistinct(t *testing.T) {
	if errors.Is(ErrNotHolder, ErrLeaseExpired) ||
		errors.Is(ErrLeaseExpired, ErrTokenMismatch) ||
		errors.Is(ErrNotHolder, ErrTokenMismatch) {
		t.Fatal("ErrNotHolder / ErrLeaseExpired / ErrTokenMismatch 必须两两不同")
	}

	clk := newFakeClock(0)

	// 被抢占 → 只命中 ErrNotHolder。
	m1, tokA := newPreemptedPair(t, newFakeClock(0))
	errPreempted := m1.Renew("A", tokA, 100)
	if !errors.Is(errPreempted, ErrNotHolder) ||
		errors.Is(errPreempted, ErrLeaseExpired) ||
		errors.Is(errPreempted, ErrTokenMismatch) {
		t.Fatalf("抢占错误分类不纯: %v", errPreempted)
	}

	// holder+token 匹配但过期 → 只命中 ErrLeaseExpired。
	m2 := New(clk.now)
	tok, _ := m2.Acquire("A", 100)
	clk.set(100)
	errExpired := m2.Renew("A", tok, 100)
	if !errors.Is(errExpired, ErrLeaseExpired) ||
		errors.Is(errExpired, ErrNotHolder) ||
		errors.Is(errExpired, ErrTokenMismatch) {
		t.Fatalf("过期错误分类不纯: %v", errExpired)
	}

	// holder 匹配、token 不符、且未过期 → 只命中 ErrTokenMismatch。
	m3 := New(newFakeClock(0).now)
	tokOld, _ := m3.Acquire("A", 100)
	_, _ = m3.Acquire("A", 100)
	errMismatch := m3.Renew("A", tokOld, 100)
	if !errors.Is(errMismatch, ErrTokenMismatch) ||
		errors.Is(errMismatch, ErrNotHolder) ||
		errors.Is(errMismatch, ErrLeaseExpired) {
		t.Fatalf("token 错误分类不纯: %v", errMismatch)
	}
}

// TestRenewRejectsInvalidArgs 钉 Renew 与 Acquire 一致的参数契约：
// "holder == "" → ErrInvalidHolder；ttlMillis <= 0 → ErrInvalidTTL"。
// 改坏方式：删掉 Renew 开头任一参数校验，本测试失败。
func TestRenewRejectsInvalidArgs(t *testing.T) {
	m := New(func() int64 { return 0 })
	if err := m.Renew("", 1, 100); !errors.Is(err, ErrInvalidHolder) {
		t.Fatalf("空 holder: %v", err)
	}
	if err := m.Renew("A", 1, 0); !errors.Is(err, ErrInvalidTTL) {
		t.Fatalf("ttl=0: %v", err)
	}
	if err := m.Renew("A", 1, -5); !errors.Is(err, ErrInvalidTTL) {
		t.Fatalf("负 ttl: %v", err)
	}
}
