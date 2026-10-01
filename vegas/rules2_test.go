package vegas

import "testing"

// m 随样本恰在 t+Wm 出窗变化，窗口全空后只剩本次样本。
func TestSlidingMinWindowExpiry(t *testing.T) {
	l := mustNew(t, Config{L0: 10, Lmin: 1, Lmax: 100, Alpha: 2, Beta: 4, Tmo: 100000, Cooldown: 100000, Wm: 100})
	give := func(now, rtt int64) {
		t.Helper()
		tk, err := l.Acquire(now)
		if err != nil {
			t.Fatal(err)
		}
		if err := l.Release(tk.Seq, Success, rtt, now); err != nil {
			t.Fatal(err)
		}
		t.Logf("sample now=%d rtt=%d -> L=%d m=%d", now, rtt, l.L(), l.MinRTT())
	}
	give(0, 100)
	give(10, 150)
	give(20, 80)
	if l.MinRTT() != 80 {
		t.Fatalf("m=%d want 80", l.MinRTT())
	}
	// (0,100) expire=100 恰不满足 expire>now，出窗；m 仍为 80。
	give(100, 300)
	if l.MinRTT() != 80 {
		t.Fatalf("at 100 m=%d want 80", l.MinRTT())
	}
	// (10,150) expire=110、(20,80) expire=120 均已出窗，只剩 (100,300) 与本次 200。
	give(120, 200)
	if l.MinRTT() != 200 {
		t.Fatalf("at 120 m=%d want 200", l.MinRTT())
	}
	// (100,300) expire=200、(120,200) expire=220 均出窗，窗口全空后只剩本次样本 500。
	give(221, 500)
	if l.MinRTT() != 500 {
		t.Fatalf("at 221 m=%d want 500", l.MinRTT())
	}
}

// L 在 Lmax 与 Lmin 处不越界。
func TestLimitClamping(t *testing.T) {
	l := mustNew(t, Config{L0: 4, Lmin: 2, Lmax: 4, Alpha: 1, Beta: 3, Tmo: 1000, Cooldown: 1000, Wm: 10000})
	tk, _ := l.Acquire(0)
	// 首样本 q=0 触发增长，但已在 Lmax=4。
	if err := l.Release(tk.Seq, Success, 100, 10); err != nil || l.L() != 4 {
		t.Fatalf("clamp max L=%d", l.L())
	}

	l2 := mustNew(t, Config{L0: 3, Lmin: 2, Lmax: 4, Alpha: 1, Beta: 3, Tmo: 100000, Cooldown: 0, Wm: 10000})
	for i := int64(0); i < 50; i++ {
		tk, err := l2.Acquire(i)
		if err == nil {
			_ = l2.Release(tk.Seq, Dropped, 0, i)
		}
	}
	if l2.L() != 2 {
		t.Fatalf("clamp min L=%d", l2.L())
	}
}

// 上限降到低于在途数后，Acquire 一律拒绝直到归还。
func TestAcquireRejectedUntilDrain(t *testing.T) {
	l := mustNew(t, Config{L0: 5, Lmin: 1, Lmax: 10, Alpha: 1, Beta: 2, Tmo: 100000, Cooldown: 0, Wm: 100000})
	held := make([]Token, 5)
	for i := range held {
		held[i], _ = l.Acquire(0)
	}
	// 一次成功高时延归还使 q 很大，L 逐步下降；直接连续丢弃更快：Cd=0。
	for i := 0; i < 4; i++ {
		if err := l.Release(held[i].Seq, Dropped, 0, int64(i+1)); err != nil {
			t.Fatal(err)
		}
	}
	// floor(5*.9)=4 ->3 ->2 ->1：L=1，仍有 1 个在途（held[4]）。
	if l.L() != 1 || l.N() != 1 {
		t.Fatalf("L=%d n=%d", l.L(), l.N())
	}
	for _, now := range []int64{10, 20, 30} {
		if _, err := acquire(t, l, now); err != ErrAtCapacity {
			t.Fatalf("acquire while n>=L: %v", err)
		}
	}
	if err := l.Release(held[4].Seq, Ignored, 0, 40); err != nil || l.N() != 0 {
		t.Fatalf("drain: %v", err)
	}
	tk, err := acquire(t, l, 50)
	if err != nil || tk.W != 1 {
		t.Fatalf("acquire after drain: %+v %v", tk, err)
	}
}

// 已超时与重复/无效归还报不同错误；rtt=0 被拒且令牌仍可归还。
func TestReleaseErrorDistinction(t *testing.T) {
	l := mustNew(t, baseCfg())
	tk, _ := l.Acquire(0) // expires=50

	// 从未发放的序号无效。
	if err := l.Release(999, Ignored, 0, 10); err != ErrInvalidToken {
		t.Fatalf("never issued: %v", err)
	}
	// 序号 0 无效。
	if err := l.Release(0, Ignored, 0, 10); err != ErrInvalidToken {
		t.Fatalf("seq zero: %v", err)
	}
	// 成功但 rtt=0：拒绝，令牌仍未归还。
	if err := l.Release(tk.Seq, Success, 0, 10); err != ErrInvalidRTT {
		t.Fatalf("rtt zero: %v", err)
	}
	if l.N() != 1 {
		t.Fatalf("rtt rejection must keep token outstanding, n=%d", l.N())
	}
	// 令牌仍可用忽略结果归还。
	if err := l.Release(tk.Seq, Ignored, 0, 11); err != nil || l.N() != 0 {
		t.Fatalf("token still returnable: %v n=%d", err, l.N())
	}
	// 重复归还报无效，与超时错误不同。
	if err := l.Release(tk.Seq, Ignored, 0, 12); err != ErrInvalidToken {
		t.Fatalf("double release: %v", err)
	}

	// 超时后归还报超时错误，再归还仍是超时（在已超时集合中）。
	tk2, _ := l.Acquire(20) // expires=70
	if _, err := l.Acquire(70); err != nil {
		t.Fatal(err)
	}
	if err := l.Release(tk2.Seq, Success, 100, 70); err != ErrTokenTimedOut {
		t.Fatalf("timed out: %v", err)
	}
	if err := l.Release(tk2.Seq, Ignored, 0, 71); err != ErrTokenTimedOut {
		t.Fatalf("timed out again: %v", err)
	}
}

// 三类前置拒绝（非法结果、非法时间、时钟回退）不改变任何状态。
func TestFrontRejectionsChangeNothing(t *testing.T) {
	l := mustNew(t, baseCfg())
	tk, _ := l.Acquire(5)
	if l.N() != 1 || l.L() != 10 {
		t.Fatal("setup")
	}
	// 非法结果最先报告，即使时间也非法。
	if err := l.Release(tk.Seq, Result(99), 0, -1); err != ErrInvalidResult {
		t.Fatalf("priority: %v", err)
	}
	// 时间非法先于时钟回退。
	if _, err := l.Acquire(-1); err != ErrInvalidTime {
		t.Fatalf("negative now: %v", err)
	}
	if _, err := l.Acquire(maxTime + 1); err != ErrInvalidTime {
		t.Fatalf("huge now: %v", err)
	}
	// 时钟回退：maxNow 仍为 5（Acquire(5)）；5 之后再 4 报回退。
	tk6, err := l.Acquire(6)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Acquire(4); err != ErrClockRewind {
		t.Fatalf("rewind: %v", err)
	}
	if err := l.Release(tk.Seq, Ignored, 0, 4); err != ErrClockRewind {
		t.Fatalf("rewind release: %v", err)
	}
	if l.N() != 2 || l.L() != 10 {
		t.Fatalf("state changed by rejected calls: n=%d L=%d", l.N(), l.L())
	}
	// 令牌仍可正常归还（maxNow=6，两个令牌都在 6 归还）。
	if err := l.Release(tk.Seq, Ignored, 0, 6); err != nil {
		t.Fatalf("normal release: %v", err)
	}
	if err := l.Release(tk6.Seq, Ignored, 0, 6); err != nil || l.N() != 0 {
		t.Fatalf("normal release 2: %v n=%d", err, l.N())
	}
}

// 状态类拒绝保留 reap 与 maxNow 推进。
func TestStateRejectionKeepsReap(t *testing.T) {
	l := mustNew(t, baseCfg())
	for i := 0; i < 10; i++ {
		_, _ = l.Acquire(0)
	}
	// now=50：所有令牌超时。此时 Acquire 满员拒绝不可能（reap 后 n=0），
	// 改用已超时归还来验证 reap 已发生：n 应被回收为 0，L 已被 cut 为 9。
	if err := l.Release(1, Ignored, 0, 50); err != ErrTokenTimedOut {
		t.Fatalf("want timeout, got %v", err)
	}
	if l.N() != 0 {
		t.Fatalf("reap should have drained n=%d", l.N())
	}
	if l.L() != 9 {
		t.Fatalf("reap cut should have lowered L=%d", l.L())
	}
	// maxNow 已推进到 50，更早时间报回退。
	if _, err := l.Acquire(49); err != ErrClockRewind {
		t.Fatalf("maxNow advanced: %v", err)
	}

	// Acquire 满员拒绝同样保留 maxNow。
	l2 := mustNew(t, Config{L0: 1, Lmin: 1, Lmax: 1, Alpha: 1, Beta: 2, Tmo: 1000, Cooldown: 0, Wm: 1000})
	_, _ = l2.Acquire(10)
	if _, err := l2.Acquire(11); err != ErrAtCapacity {
		t.Fatalf("capacity: %v", err)
	}
	if _, err := l2.Acquire(10); err != ErrClockRewind {
		t.Fatalf("maxNow after capacity reject: %v", err)
	}

	// rtt 非法拒绝保留 reap：让一个其他令牌在同一次 reap 中超时。
	l3 := mustNew(t, baseCfg())
	a, _ := l3.Acquire(0) // 1, expires 50
	b, _ := l3.Acquire(0) // 2, expires 50
	_ = l3.Release(a.Seq, Ignored, 0, 10)
	// b 在 50 超时；Release(b, success, rtt=0, 50)：reap 先把 b 回收，
	// 因此应报 ErrTokenTimedOut（超时优先于 rtt 校验），n=0 且 cut 生效。
	if err := l3.Release(b.Seq, Success, 0, 50); err != ErrTokenTimedOut {
		t.Fatalf("reap precedes rtt check: %v", err)
	}
	if l3.N() != 0 || l3.L() != 9 {
		t.Fatalf("n=%d L=%d", l3.N(), l3.L())
	}
}
