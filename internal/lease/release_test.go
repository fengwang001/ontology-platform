package lease

import (
	"errors"
	"testing"
)

// TestReleaseExpiredMatchingLeaseSucceeds 钉 Release 注释的设计决策原话：
// "holder 与 token 都匹配当前记录时，即使租约已经过期，Release 也算成功
// （幂等清理）……让清理路径幂等可以简化客户端的 defer Release 写法。"
// 改坏方式：在 Release 清除记录前加上 `if now >= expiresAt { return ErrLeaseExpired }`，
// 本测试失败。
func TestReleaseExpiredMatchingLeaseSucceeds(t *testing.T) {
	clk := newFakeClock(0)
	m := New(clk.now)
	tok, _ := m.Acquire("A", 100)
	clk.set(100) // 恰好到期

	if err := m.Release("A", tok); err != nil {
		t.Fatalf("holder+token 匹配的过期 Release 应幂等成功，得到 %v", err)
	}
}

// TestReleasedRecordIsClearedAndTokenCounterKept 钉 Release 注释的两句话：
// "清除租约记录，但 token 计数器不复位（不变量 2）"以及
// "只有 Release 成功或他人 Acquire 才会清除/覆盖该记录"。
// 改坏方式：Release 不置 m.held=false（记录没清），则 B 无法 Acquire；
// 若把 m.token 复位为 0，新 token 与历史重复，严格递增长断言失败。
func TestReleasedRecordIsClearedAndTokenCounterKept(t *testing.T) {
	m := New(newFakeClock(0).now)
	tok, _ := m.Acquire("A", 100)
	if err := m.Release("A", tok); err != nil {
		t.Fatalf("Release: %v", err)
	}

	// 记录已清除：他人可立即 Acquire，且拿到的 token 严格大于历史值。
	tokB, err := m.Acquire("B", 100)
	if err != nil {
		t.Fatalf("Release 后 B 应能 Acquire: %v", err)
	}
	if tokB <= tok {
		t.Fatalf("token 计数器被复位: 历史=%d 新=%d", tok, tokB)
	}

	// 旧持有者在记录清除后再 Release/ Renew 都是 ErrNotHolder（"已释放"）。
	if err := m.Release("A", tok); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("已释放后 A 再 Release = %v, want ErrNotHolder", err)
	}
	if err := m.Renew("A", tok, 100); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("已释放后 A Renew = %v, want ErrNotHolder", err)
	}
}

// TestReleaseByPreemptedHolderGetsNotHolder 钉 Release 注释：
// "但 holder 不匹配（已被他人抢占）仍返回 ErrNotHolder……防止误清他人的
// 租约记录。"抢占发生在他人记录之上，A 的 Release 绝不能清掉 B 的记录。
// 改坏方式：把 Release 的 holder 校验删掉/放宽，A 的 Release 返回 nil
// 且 B 的租约被误清，本测试失败。
func TestReleaseByPreemptedHolderGetsNotHolder(t *testing.T) {
	clk := newFakeClock(0)
	m := New(clk.now)
	tokA, _ := m.Acquire("A", 100)
	clk.set(100)
	tokB, _ := m.Acquire("B", 100)

	if err := m.Release("A", tokA); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("被抢占者 Release = %v, want ErrNotHolder", err)
	}
	// B 的记录必须原封不动：B 自己仍可写、可续约。
	if err := m.Write(tokB, "k", "v"); err != nil {
		t.Fatalf("被抢占者的 Release 误伤了 B 的租约: Write %v", err)
	}
}

// TestReleaseWithWrongTokenGetsTokenMismatchAndKeepsLease 钉 Release 注释：
// "token 不符仍返回 ErrTokenMismatch，防止误清他人的租约记录。"
// 改坏方式：把 Release 的 `if token != m.token` 提前返回删掉，
// 旧 token 也能释放当前租约，本测试失败（随后当前 token 的写会失败）。
func TestReleaseWithWrongTokenGetsTokenMismatchAndKeepsLease(t *testing.T) {
	m := New(newFakeClock(0).now)
	tokOld, _ := m.Acquire("A", 100)
	tokCur, _ := m.Acquire("A", 100) // 同 holder 重获，tokOld 成为"前一个值"

	if err := m.Release("A", tokOld); !errors.Is(err, ErrTokenMismatch) {
		t.Fatalf("旧 token Release = %v, want ErrTokenMismatch", err)
	}
	// 租约记录未被误清：当前 token 依然有效。
	if err := m.Write(tokCur, "k", "v"); err != nil {
		t.Fatalf("错误 token 的 Release 不应影响当前租约: %v", err)
	}
}

// TestReleaseEmptyHolderRejected 钉 Release 的参数校验：
// "if holder == "" { return ErrInvalidHolder }"。
// 改坏方式：删掉该校验，空串会走到记录比较分支得到 ErrNotHolder，
// 本测试的 ErrInvalidHolder 断言失败。
func TestReleaseEmptyHolderRejected(t *testing.T) {
	m := New(newFakeClock(0).now)
	if err := m.Release("", 1); !errors.Is(err, ErrInvalidHolder) {
		t.Fatalf("空 holder Release = %v, want ErrInvalidHolder", err)
	}
}
