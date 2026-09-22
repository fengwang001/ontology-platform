package lease

import (
	"errors"
	"testing"
)

// 钉 Release 注释的核心取舍（manager.go）：
// "holder 与 token 都匹配当前记录时，即使租约已经过期，Release 也算
// 成功（幂等清理）。理由：释放的期望终态是'该 holder 不再持有租约'，
// 而过期的租约本来就已失效，让清理路径幂等可以简化客户端的
// defer Release 写法，且不违反任何一条不变量。但 holder 不匹配
// （已被他人抢占）仍返回 ErrNotHolder，token 不符仍返回
// ErrTokenMismatch，防止误清他人的租约记录。"

// 已经过期、但 holder 与 token 都匹配：Release 必须成功，不是
// ErrLeaseExpired。判别力：若在 Release 加过期检查返回
// ErrLeaseExpired，本测试失败。
func TestReleaseExpiredMatchingSucceeds(t *testing.T) {
	var clock int64
	m := New(func() int64 { return clock })
	tok, _ := m.Acquire("A", 100)
	if err := m.Write(tok, "k", "v"); err != nil {
		t.Fatalf("种子写: %v", err)
	}
	clock = 100 // 恰好到期

	if err := m.Release("A", tok); err != nil {
		t.Fatalf("过期但匹配的 Release = %v, want nil（幂等成功）", err)
	}

	// 终态：记录已清除。A 再 Renew/Release 都变成 ErrNotHolder。
	if err := m.Renew("A", tok, 100); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("Release 后 Renew = %v, want ErrNotHolder", err)
	}
	if err := m.Release("A", tok); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("重复 Release = %v, want ErrNotHolder", err)
	}
	// 记录清除后旧 token 写入归类为 ErrStaleToken（!m.held）。
	if err := m.Write(tok, "k", "x"); !errors.Is(err, ErrStaleToken) {
		t.Fatalf("Release 后旧 token 写 = %v, want ErrStaleToken", err)
	}
	// store 内容不受 Release 影响。
	if got, _ := m.Read("k"); got != "v" {
		t.Fatalf("Read(k) = %q, want v", got)
	}
}

// 过期很久（远超到期时刻）后 Release 同样幂等成功，
// 钉"即使租约已经过期"不只是边界那一刻。
func TestReleaseLongExpiredMatchingSucceeds(t *testing.T) {
	var clock int64
	m := New(func() int64 { return clock })
	tok, _ := m.Acquire("A", 10)
	clock = 1_000_000

	if err := m.Release("A", tok); err != nil {
		t.Fatalf("过期很久后 Release = %v, want nil", err)
	}
}

// 已被他人抢占后，旧持有者 Release 自己的旧 token 必须失败：
// ErrNotHolder，"防止误清他人的租约记录"。
// 判别力：若 Release 只看 token 数值或忽略 holder，本测试失败。
func TestReleaseAfterPreemptedReturnsNotHolder(t *testing.T) {
	var clock int64
	m := New(func() int64 { return clock })
	tokA, _ := m.Acquire("A", 100)
	clock = 100
	tokB, err := m.Acquire("B", 100)
	if err != nil {
		t.Fatalf("B Acquire: %v", err)
	}

	if err := m.Release("A", tokA); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("被抢占者 Release = %v, want ErrNotHolder", err)
	}
	// 关键：B 的租约记录必须完好无损，仍能正常续约与写入。
	if err := m.Renew("B", tokB, 100); err != nil {
		t.Fatalf("A 的失败 Release 误伤了 B 的租约: Renew %v", err)
	}
	if err := m.Write(tokB, "k", "b"); err != nil {
		t.Fatalf("B 写入: %v", err)
	}
}

// holder 匹配但 token 不符（同 holder 重获后用旧 token Release）：
// ErrTokenMismatch，且当前租约绝不能被误清。
// 判别力：删掉 token 校验会让旧 token 清掉新租约，本测试失败。
func TestReleaseWrongTokenReturnsMismatch(t *testing.T) {
	var clock int64
	m := New(func() int64 { return clock })
	old, _ := m.Acquire("A", 100)
	cur, _ := m.Acquire("A", 100) // 重获，old 变旧

	if err := m.Release("A", old); !errors.Is(err, ErrTokenMismatch) {
		t.Fatalf("旧 token Release = %v, want ErrTokenMismatch", err)
	}
	// 当前租约仍在：cur 可写；再用 cur 释放才成功。
	if err := m.Write(cur, "k", "v"); err != nil {
		t.Fatalf("误清后当前租约失效: Write %v", err)
	}
	if err := m.Release("A", cur); err != nil {
		t.Fatalf("正确 token Release: %v", err)
	}
}

// 参数校验仍先行：空 holder 直接 ErrInvalidHolder，不需要锁内分类。
func TestReleaseEmptyHolder(t *testing.T) {
	m := New(func() int64 { return 0 })
	if err := m.Release("", 1); !errors.Is(err, ErrInvalidHolder) {
		t.Fatalf("空 holder Release = %v, want ErrInvalidHolder", err)
	}
}

// 正常（未过期）Release 成功后租约立即空闲，他人不必等到到期即可
// Acquire——钉 Release 的"清除/覆盖该记录"语义。
func TestReleaseWhileActiveMakesLeaseFree(t *testing.T) {
	var clock int64
	m := New(func() int64 { return clock })
	tok, _ := m.Acquire("A", 100)
	if err := m.Release("A", tok); err != nil {
		t.Fatalf("Release: %v", err)
	}
	tokB, err := m.Acquire("B", 100)
	if err != nil {
		t.Fatalf("Release 后 B 应立即 Acquire 成功, got %v", err)
	}
	if tokB != tok+1 {
		t.Fatalf("token = %d, want %d", tokB, tok+1)
	}
}
