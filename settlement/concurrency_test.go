package settlement

import (
	"sync"
	"testing"
	"time"
)

// TestConcurrentLinearizabilitySmoke：多 goroutine 并发混合操作，
// 只要求不产生竞态、panic 或破坏不变量（可串行化由单把互斥锁在结构上保证）。
func TestConcurrentLinearizabilitySmoke(t *testing.T) {
	e := newTestEngine()
	const n = 4
	for i := 0; i < n; i++ {
		pid := pidN(i)
		setupProsumer(t, e, pid)
	}

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for k := 0; k < 300; k++ {
				i := (g + k) % n
				pid := pidN(i)
				m := MustMonth("2026-01")
				day := 1 + (k % 31)
				hour := (g*7 + k) % 24
				st := atMonth(m, day, hour)
				_ = e.RegisterReading(pid, Reading{Start: st, ImportWh: k % 5, ExportWh: k % 3})
				_, _ = e.Query(pid, "2026-01")
				_ = e.SetPrices(pid, "2026-02", Prices{ImportPricePerWh: 1, SurplusPricePerWh: 1})
				_ = e.SetParams(pid, "2026-02", Params{ContractPowerW: 5000, MonthlyCreditableW: 1000, CreditValidMonths: 1})
			}
		}(g)
	}
	wg.Wait()

	// 全部月补齐后顺序封账，验证并发期间状态一致、恒等式成立。
	for i := 0; i < n; i++ {
		pid := pidN(i)
		fillMonth(t, e, pid, MustMonth("2026-01"), nil)
		closeMonth(t, e, pid, "2026-01")
		assertInvariants(t, e, pid, "2026-01")
	}
}

func pidN(i int) string { return "cp" + string(rune('a'+i)) }

// TestCloseScaling 封账开销应与已封账月数无关：
// 每月固定读数、净下网（不触碰额度堆），分别测量封第 1、24、48 个月的耗时。
func TestCloseScaling(t *testing.T) {
	measure := func(months int) time.Duration {
		e := newTestEngine()
		setupProsumer(t, e, "p")
		for k := 0; k < months; k++ {
			m := MustMonth("2026-01").add(k)
			fillMonth(t, e, "p", m, map[dayHour][2]int{{1, 1}: {10, 0}})
		}
		for k := 0; k < months-1; k++ {
			closeMonth(t, e, "p", MustMonth("2026-01").add(k).String())
		}
		target := MustMonth("2026-01").add(months - 1)
		// 多次测量末月封账，取中位级别稳定值。
		var best time.Duration
		for r := 0; r < 20; r++ {
			ee := newTestEngine()
			setupProsumer(t, ee, "p")
			for k := 0; k < months; k++ {
				m := MustMonth("2026-01").add(k)
				fillMonth(t, ee, "p", m, map[dayHour][2]int{{1, 1}: {10, 0}})
			}
			for k := 0; k < months-1; k++ {
				closeMonth(t, ee, "p", MustMonth("2026-01").add(k).String())
			}
			start := time.Now()
			closeMonth(t, ee, "p", target.String())
			d := time.Since(start)
			if best == 0 || d < best {
				best = d
			}
		}
		return best
	}

	d1 := measure(1)
	d24 := measure(24)
	d48 := measure(48)
	t.Logf("close month#1=%v #24=%v #48=%v (固定每月744个间隔)", d1, d24, d48)
	// 封账主成本是当月读数收集（固定 744 间隔），不随历史月数增长；
	// 允许 4 倍噪声余量，证明不存在与历史月数成比例的部分。
	if d48 > 4*d1+100*time.Microsecond {
		t.Fatalf("封账第48个月开销随历史月数增长: %v vs %v", d48, d1)
	}
}
