package lease

import (
	"errors"
	"testing"
)

// testClock 是可手动推进的逻辑时钟，替代 time.Now。
type testClock struct{ t int64 }

func (c *testClock) now() int64      { return c.t }
func (c *testClock) set(t int64)     { c.t = t }
func (c *testClock) advance(d int64) { c.t += d }

// 本文件钉住"到期边界"这条设计决策，原文（errors.go ErrLeaseExpired 注释）：
//   "设计决策：租约在 now >= expiresAt 时失效，即到期那一刻起续约与写入都算失效。
//    ……采用左闭右开区间 [start, expiresAt) 可以让新持有者在 now == expiresAt 时
//    立刻 Acquire 成功，而旧持有者在同一时刻的写必须被拒绝"
//
// 判别力（改坏实现 → 失败的测试）：
//   - 把 Renew 里的 `now >= m.expiresAt` 改成 `now > m.expiresAt`
//     → TestRenewAtExpiryBoundaryFails 失败（边界时刻续约会被放行）。
//   - 把 Write 里的 `now >= m.expiresAt` 改成 `now > m.expiresAt`
//     → TestWriteAtExpiryBoundaryFails 失败（边界时刻写入会被放行）。
//   - 把 Acquire 里的 `now < m.expiresAt` 改成 `now <= m.expiresAt`
//     → TestAcquireByOtherAtExpiryBoundarySucceeds 失败（新持有者抢不到）。

// 钉不变量 5（过期即失效）+ 上述设计决策的"写入"一侧：
// now 恰好等于 expiresAt 时，Write 必须返回 ErrLeaseExpired。
func TestWriteAtExpiryBoundaryFails(t *testing.T) {
	var clk testClock
	m := New(clk.now)
	tok, err := m.Acquire("A", 100)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	clk.set(100) // now == expiresAt，边界时刻
	err = m.Write(tok, "k", "v")
	if !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("Write at expiry = %v, want ErrLeaseExpired", err)
	}
	if _, ok := m.Read("k"); ok {
		t.Fatal("被拒绝的 Write 不应留下痕迹（不变量 4）")
	}
}

// 钉同一条决策的"续约"一侧：now == expiresAt 时 Renew 返回 ErrLeaseExpired。
func TestRenewAtExpiryBoundaryFails(t *testing.T) {
	var clk testClock
	m := New(clk.now)
	tok, err := m.Acquire("A", 100)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	clk.set(100) // now == expiresAt
	if err := m.Renew("A", tok, 100); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("Renew at expiry = %v, want ErrLeaseExpired", err)
	}
}

// 钉左闭右开区间的左闭一侧：now == expiresAt-1 时租约仍有效，
// 写入与续约都必须成功。防止有人把边界改"紧"（now+1 >= expiresAt 之类）。
func TestJustBeforeExpiryStillValid(t *testing.T) {
	var clk testClock
	m := New(clk.now)
	tok, err := m.Acquire("A", 100)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	clk.set(99) // 边界前一毫秒
	if err := m.Write(tok, "k", "v"); err != nil {
		t.Fatalf("Write one ms before expiry = %v, want nil", err)
	}
	if err := m.Renew("A", tok, 100); err != nil {
		t.Fatalf("Renew one ms before expiry = %v, want nil", err)
	}
}

// 钉设计决策中"新持有者在 now == expiresAt 时立刻 Acquire 成功"：
// 边界时刻旧租约已失效，他人可以立即接管（这也保证互斥不出现空窗重叠）。
func TestAcquireByOtherAtExpiryBoundarySucceeds(t *testing.T) {
	var clk testClock
	m := New(clk.now)
	tokA, err := m.Acquire("A", 100)
	if err != nil {
		t.Fatalf("Acquire A: %v", err)
	}
	clk.set(100) // now == expiresAt
	tokB, err := m.Acquire("B", 100)
	if err != nil {
		t.Fatalf("Acquire B at boundary = %v, want nil", err)
	}
	if tokB <= tokA {
		t.Fatalf("token 未严格递增（不变量 2）: A=%d B=%d", tokA, tokB)
	}
	// 同一时刻，旧持有者 A 的写必须被拒——两侧只能活一个（不变量 1）。
	if err := m.Write(tokA, "k", "v"); !errors.Is(err, ErrStaleToken) {
		t.Fatalf("old holder Write after preemption = %v, want ErrStaleToken", err)
	}
}

// 钉不变量 5：过期后哪怕只过 1 毫秒，旧持有者用旧 token 写也立即失效。
func TestWriteAfterExpiryRejected(t *testing.T) {
	var clk testClock
	m := New(clk.now)
	tok, err := m.Acquire("A", 100)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	clk.set(101)
	if err := m.Write(tok, "k", "v"); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("Write after expiry = %v, want ErrLeaseExpired", err)
	}
	if _, ok := m.Read("k"); ok {
		t.Fatal("被拒绝的 Write 不应留下痕迹（不变量 4）")
	}
}
