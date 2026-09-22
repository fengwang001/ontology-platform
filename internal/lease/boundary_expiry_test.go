package lease

import (
	"errors"
	"testing"
)

// 钉实现注释的设计决策（errors.go 中 ErrLeaseExpired）：
// "租约在 now >= expiresAt 时失效，即到期那一刻起续约与写入都算失效。
// 理由：不变量 5 要求'过期即失效'，采用左闭右开区间 [start, expiresAt)
// 可以让新持有者在 now == expiresAt 时立刻 Acquire 成功，而旧持有者在
// 同一时刻的写必须被拒绝——若边界算有效，则同一时刻可能出现两个持有者
// 都认为自己有效，违反互斥。"
//
// 本文件逐侧钉死到期那一刻：Renew / Write / 持有者 Acquire（失败侧）/
// 他人 Acquire（成功侧）。

// Renew 在到期那一刻（now == expiresAt）必须返回 ErrLeaseExpired，
// 且续约不得发生——下一时刻他人应能立即接管。
// 判别力：把 Renew 的 `now >= m.expiresAt` 改成 `>`，本测试失败。
func TestBoundary_RenewRejectedAtExactExpiry(t *testing.T) {
	var clock int64
	m := New(func() int64 { return clock })
	tok, _ := m.Acquire("A", 100) // expiresAt = 100

	clock = 100
	err := m.Renew("A", tok, 100)
	if !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("到期时刻 Renew = %v, want ErrLeaseExpired", err)
	}

	// 续约没有偷偷生效：101 时 B 必须能接管（若错误地续到了 200，
	// 这里会返回 ErrLeaseHeld）。
	clock = 101
	if _, err := m.Acquire("B", 100); err != nil {
		t.Fatalf("被拒续约后 B Acquire: %v（说明到期时刻续约意外生效）", err)
	}
}

// Write 在到期那一刻归 ErrLeaseExpired（token 仍匹配当前记录，
// 只是时间失效），并与前一时刻的成功形成左右侧对照。
// 判别力：把 Write 的 `now >= m.expiresAt` 改成 `>`，本测试失败。
func TestBoundary_WriteSidesAtExpiry(t *testing.T) {
	var clock int64
	m := New(func() int64 { return clock })
	tok, _ := m.Acquire("A", 100)

	clock = 99
	if err := m.Write(tok, "k", "v99"); err != nil {
		t.Fatalf("到期前 1ms Write: %v", err)
	}

	clock = 100
	before := snapshot(m)
	if err := m.Write(tok, "k", "v100"); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("到期时刻 Write = %v, want ErrLeaseExpired", err)
	}
	if after := snapshot(m); !mapsEqual(before, after) {
		t.Fatal("到期时刻被拒写产生了副作用")
	}

	clock = 101
	if err := m.Write(tok, "k", "v101"); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("到期后 Write = %v, want ErrLeaseExpired", err)
	}
}

// 判定有效性（经 Acquire 的抢占检查侧证）：
// now == expiresAt-1 他人 Acquire 被拒（ErrLeaseHeld）；
// now == expiresAt   他人 Acquire 成功接管。
// 判别力：把 Acquire 的 `now < m.expiresAt` 改成 `<=`，
// 本测试的"到期时刻成功接管"断言失败。
func TestBoundary_AcquireSidesAtExpiry(t *testing.T) {
	var clock int64
	m := New(func() int64 { return clock })
	tokA, _ := m.Acquire("A", 100)

	clock = 99
	if _, err := m.Acquire("B", 100); !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("到期前 1ms 他人 Acquire = %v, want ErrLeaseHeld", err)
	}

	clock = 100
	tokB, err := m.Acquire("B", 100)
	if err != nil {
		t.Fatalf("到期时刻他人 Acquire 应成功, got %v", err)
	}
	if tokB != tokA+1 {
		t.Fatalf("接管 token = %d, want %d", tokB, tokA+1)
	}

	// 接管后同一时刻：旧持有者已经不再有效，新持有者写成功。
	if err := m.Write(tokB, "k", "b"); err != nil {
		t.Fatalf("接管者写: %v", err)
	}
}

// 到期那一刻原持有者自己再 Acquire：按 Acquire 注释的"重新获取"决策
// 这是成功的（holder 相同，根本不进 ErrLeaseHeld 分支），
// 与他人 Acquire 在同一时刻成功不冲突——钉死这一左闭右开行为。
// 判别力：若有人给 Acquire 增加"自己过期前不能重获"的提前返回，
// 本测试失败。
func TestBoundary_SameHolderReacquireAtExactExpiry(t *testing.T) {
	var clock int64
	m := New(func() int64 { return clock })
	tok, _ := m.Acquire("A", 100)
	clock = 100

	tok2, err := m.Acquire("A", 100)
	if err != nil {
		t.Fatalf("到期时刻同 holder Acquire = %v, want nil", err)
	}
	if tok2 != tok+1 {
		t.Fatalf("新 token = %d, want %d", tok2, tok+1)
	}
	// 新租约在 100 时刻有效：expiresAt 刷新为 200。
	if err := m.Write(tok2, "k", "v"); err != nil {
		t.Fatalf("重获后写: %v", err)
	}
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
