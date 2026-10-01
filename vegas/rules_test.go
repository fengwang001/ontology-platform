package vegas

import (
	"errors"
	"testing"
)

// w*2 恰等于 L 不受限；w*2 小于 L 受限但样本仍入窗口并更新 m。
func TestApplicationLimitedBoundary(t *testing.T) {
	l := mustNew(t, Config{L0: 4, Lmin: 1, Lmax: 8, Alpha: 2, Beta: 4, Tmo: 1000, Cooldown: 1000, Wm: 1000})
	tks := make([]Token, 4)
	for i := range tks {
		tks[i], _ = acquire(t, l, 0)
	}
	// w=2：w*2=4 恰等于 L=4，不受限；首样本 rtt=100，q=0<alpha，L->5。
	if err := release(t, l, tks[1].Seq, Success, 100, 10); err != nil || l.L() != 5 {
		t.Fatalf("equal case L=%d", l.L())
	}
	// w=1：w*2=2 < L=5，应用受限：L 不变，样本 rtt=80 入窗口并把 m 更新为 80。
	if err := release(t, l, tks[0].Seq, Success, 80, 20); err != nil || l.L() != 5 || l.MinRTT() != 80 {
		t.Fatalf("limited case L=%d m=%d", l.L(), l.MinRTT())
	}
}

// 首个成功样本 q=0 触发增长；q 恰等于 alpha 与恰等于 beta 时 L 不变。
func TestQThresholds(t *testing.T) {
	l := mustNew(t, Config{L0: 11, Lmin: 1, Lmax: 100, Alpha: 2, Beta: 4, Tmo: 1000, Cooldown: 1000, Wm: 1_000_000})
	var held []Token
	for i := int64(1); i <= 11; i++ {
		tk, err := acquire(t, l, 0)
		if err != nil {
			t.Fatal(err)
		}
		if tk.W != i {
			t.Fatalf("w=%d want %d", tk.W, i)
		}
		held = append(held, tk)
	}
	// 基准样本 w=6：m=100，q=0，L 11->12。
	if err := release(t, l, held[5].Seq, Success, 100, 1); err != nil || l.L() != 12 || l.MinRTT() != 100 {
		t.Fatalf("baseline L=%d m=%d", l.L(), l.MinRTT())
	}
	// L=12,m=100,r=120：q=ceil(12*20/120)=2 恰等于 alpha，不变（w=7,14>=12）。
	if err := release(t, l, held[6].Seq, Success, 120, 2); err != nil || l.L() != 12 {
		t.Fatalf("q==alpha L=%d", l.L())
	}
	// L=12,m=100,r=150：q=ceil(12*50/150)=4 恰等于 beta，不变（w=8,16>=12）。
	if err := release(t, l, held[7].Seq, Success, 150, 3); err != nil || l.L() != 12 {
		t.Fatalf("q==beta L=%d", l.L())
	}
}

// 丢弃降级：向下取整与不低于 Lmin、冷却内不降、恰在 lc+Cd 生效、
// Cd=0 每次都降、取整后 L 未变仍更新 lc。
func TestDropCutAndCooldown(t *testing.T) {
	l := mustNew(t, baseCfg())
	tk1, _ := acquire(t, l, 0)
	tk2, _ := acquire(t, l, 1)
	if err := release(t, l, tk1.Seq, Dropped, 0, 35); err != nil || l.L() != 9 {
		t.Fatalf("first drop L=%d", l.L())
	}
	if err := release(t, l, tk2.Seq, Dropped, 0, 40); err != nil || l.L() != 9 {
		t.Fatalf("cooldown drop L=%d", l.L())
	}
	tk3, _ := acquire(t, l, 45)
	if err := release(t, l, tk3.Seq, Dropped, 0, 45); err != nil || l.L() != 8 {
		t.Fatalf("edge cooldown L=%d", l.L())
	}

	l2 := mustNew(t, Config{L0: 10, Lmin: 2, Lmax: 12, Alpha: 2, Beta: 4, Tmo: 1000, Cooldown: 0, Wm: 1000})
	for i := int64(0); i < 3; i++ {
		tk, _ := l2.Acquire(i)
		if err := l2.Release(tk.Seq, Dropped, 0, i); err != nil {
			t.Fatal(err)
		}
		t.Logf("Cd=0 drop %d L=%d", i, l2.L())
	}
	if l2.L() != 7 {
		t.Fatalf("Cd=0 L=%d want 7", l2.L())
	}

	// L0=3,Lmin=2,Cd=10：first cut floor(3*9/10)=2、lc=0；
	// now=9 冷却；now=10 边界再次 cut（L 仍为 2）且 lc 刷新为 10，故 now=19 仍在冷却。
	l3 := mustNew(t, Config{L0: 3, Lmin: 2, Lmax: 12, Alpha: 2, Beta: 4, Tmo: 1000, Cooldown: 10, Wm: 1000}) // 全字段具名
	dA, _ := l3.Acquire(0)
	dB, _ := l3.Acquire(0)
	dC, _ := l3.Acquire(0)
	if err := l3.Release(dA.Seq, Dropped, 0, 0); err != nil || l3.L() != 2 {
		t.Fatalf("first cut L=%d", l3.L())
	}
	if err := l3.Release(dB.Seq, Dropped, 0, 9); err != nil || l3.L() != 2 {
		t.Fatal("cooldown at 9")
	}
	if err := l3.Release(dC.Seq, Dropped, 0, 10); err != nil || l3.L() != 2 {
		t.Fatal("edge cut at 10")
	}
	// n=0 后在 10 取新令牌用于验证 now=19 仍在以 10 为锚点的冷却窗口内。
	dD, err := l3.Acquire(10)
	if err != nil {
		t.Fatal(err)
	}
	// 若 lc 未刷新为 10（错误地仍为 0），now=19 就会再触发一次 cut。
	if err := l3.Release(dD.Seq, Dropped, 0, 19); err != nil || l3.L() != 2 {
		t.Fatal("cooldown anchored at 10, now=19 must be inactive")
	}
}

// 令牌恰在超时时刻回收，差 1 仍有效。
func TestTimeoutBoundary(t *testing.T) {
	l := mustNew(t, baseCfg())
	tk, _ := acquire(t, l, 0) // expires=50
	// L=10，单令牌未满员；差 1（49<50）令牌仍有效，可正常归还。
	if err := release(t, l, tk.Seq, Ignored, 0, 49); err != nil || l.N() != 0 {
		t.Fatalf("release at 49: err=%v n=%d", err, l.N())
	}
	// 重复归还报无效。
	if err := release(t, l, tk.Seq, Ignored, 0, 49); err != ErrInvalidToken {
		t.Fatalf("double release at 49: %v", err)
	}
	tk2, _ := acquire(t, l, 50)
	if tk2.ExpiresAt != 100 {
		t.Fatalf("exp=%d", tk2.ExpiresAt)
	}
	if _, err := acquire(t, l, 99); err != nil {
		t.Fatalf("acquire at 99 should pass: %v", err)
	}
	if err := l.Release(tk2.Seq, Ignored, 0, 99); err != nil {
		t.Fatalf("release at 99: %v", err)
	}
}

// 一次 reap 多个超时令牌，每个都触发 cut，但冷却使其最多只降一次。
func TestReapMultipleTokensOneEffectiveCut(t *testing.T) {
	l := mustNew(t, baseCfg())
	var held []Token
	for i := 0; i < 5; i++ {
		tk, _ := acquire(t, l, 0)
		held = append(held, tk)
	}
	// now=49 丢弃：L->9，lc=49。
	if err := release(t, l, held[0].Seq, Dropped, 0, 49); err != nil || l.L() != 9 {
		t.Fatalf("pre-drop L=%d", l.L())
	}
	// now=50 回收剩余 4 个，每个 cut 一次，但 50<59 全在冷却，L 保持 9。
	if _, err := acquire(t, l, 50); err != nil || l.L() != 9 || l.N() != 1 {
		t.Fatalf("reap at 50 L=%d n=%d", l.L(), l.N())
	}

	l2 := mustNew(t, baseCfg())
	for i := 0; i < 5; i++ {
		_, _ = l2.Acquire(0)
	}
	// 无冷却锚点：首个 cut 使 L=9、lc=60，后续 4 个全部冷却。
	if _, err := l2.Acquire(60); err != nil || l2.L() != 9 {
		t.Fatalf("fresh reap L=%d", l2.L())
	}
}

// reap 先于放行判定与归还处理。
func TestReapBeforeAcquireAndRelease(t *testing.T) {
	l := mustNew(t, baseCfg())
	for i := 0; i < 10; i++ {
		_, _ = l.Acquire(0)
	}
	tk, err := acquire(t, l, 50)
	if err != nil || tk.Seq != 11 || tk.W != 1 || l.N() != 1 {
		t.Fatalf("acquire after reap: %+v %v n=%d", tk, err, l.N())
	}
	if err := l.Release(1, Success, 100, 50); !errors.Is(err, ErrTokenTimedOut) {
		t.Fatalf("timed out release: %v", err)
	}
}
