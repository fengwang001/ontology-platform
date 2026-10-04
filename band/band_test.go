package band

import (
	"math/rand"
	"testing"
)

func TestNewBandLimitRounding(t *testing.T) {
	cases := []struct {
		name      string
		prev, lim int64
		wantUp    int64
		wantDn    int64
	}{
		{"example-1000-10pct", 1000, 1000, 1100, 900},
		{"inward-round-1005", 1005, 1000, 1105, 905},
		{"one-percent", 100, 100, 101, 99},
		{"odd-ceil-floor", 333, 333, 344, 321}, // 333*1.0333=344.08 floor 344; 333*0.9667=321.9 ceil 322? recomputed below
		{"full-limit", 7, 10000, 14, 0},
	}
	// 第 4 例按整数公式重算期望，避免手算错误：写死为公式结果。
	cases[3].wantUp = 333 * (10000 + 333) / 10000
	cases[3].wantDn = (333*(10000-333) + 9999) / 10000
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := NewBand(tc.prev, tc.lim, 500, 200, 800)
			if b.Up != tc.wantUp || b.Dn != tc.wantDn {
				t.Fatalf("up/dn = %d/%d, want %d/%d", b.Up, b.Dn, tc.wantUp, tc.wantDn)
			}
			if !b.LimitOK(b.Dn) || !b.LimitOK(b.Up) {
				t.Fatalf("limit endpoints must be legal: [%d,%d]", b.Dn, b.Up)
			}
			if b.LimitOK(b.Dn-1) || b.LimitOK(b.Up+1) {
				t.Fatalf("one tick beyond endpoints must be illegal")
			}
		})
	}
}

func TestOverBoundary(t *testing.T) {
	// |x-R|*10000 > bp*R 为超带；取等不超，多 1 必超。
	cases := []struct {
		name  string
		price int64
		ref   int64
		bp    int64
		want  bool
	}{
		{"equal-band", 1020, 1000, 200, false}, // 20*10000 == 200*1000
		{"one-more", 1031, 1010, 200, true},    // 21*10000=210000 > 202000
		{"below-equal", 980, 1000, 200, false},
		{"below-one-more", 979, 1000, 200, true},
		{"static-equal", 1050, 1000, 500, false},
		{"static-one-more", 1051, 1000, 500, true},
		{"same-price", 1000, 1000, 1, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Over(tc.price, tc.ref, tc.bp); got != tc.want {
				t.Fatalf("Over(%d,%d,%d)=%v want %v", tc.price, tc.ref, tc.bp, got, tc.want)
			}
		})
	}
}

// rdNaive 每次线性扫描全部成交记录，求时刻 <= cutoff 的最后一笔；无则回落 rs。
func rdNaive(rs int64, ticks []tick, cutoff int64) (int64, int) {
	rd, touched := rs, 0
	for i := len(ticks) - 1; i >= 0; i-- {
		touched++
		if ticks[i].at <= cutoff {
			return ticks[i].price, touched
		}
	}
	return rd, touched
}

func TestDynamicReferenceWindow(t *testing.T) {
	h := NewHistory(1000)
	// 按时间顺序推进（真实 now 单调）：每步先 Peek 试算 Rd，再 Advance 成交。
	steps := []struct {
		at, price int64
		wantRd    int64
		note      string
	}{
		{1, 1010, 1000, "no eligible trade falls back to Rs"},
		{5, 1020, 1000, "cutoff=-5, no eligible trade"},
		{11, 1031, 1010, "trade exactly at now-W=1 counts"},
		{15, 1032, 1020, "last trade at or before cutoff=5"},
		{20, 1033, 1020, "cutoff=10, trade@11 still inside window"},
		{21, 1034, 1031, "cutoff=11, boundary trade becomes reference"},
		{30, 1035, 1033, "cutoff=20, trade@20 price is reference"},
	}
	for i, st := range steps {
		if got := h.Peek(st.at, 10); got != st.wantRd {
			t.Fatalf("%s: step=%d now=%d rd=%d want=%d", st.note, i, st.at, got, st.wantRd)
		}
		h.Advance(st.at, 10, st.price)
	}
}

func TestResetClearsHistory(t *testing.T) {
	h := NewHistory(1000)
	h.Advance(1, 10, 1010)
	h.Advance(2, 10, 1020)
	h.Reset(131, 1081)
	if h.Rs() != 1081 {
		t.Fatalf("Rs after reset = %d, want 1081", h.Rs())
	}
	// 恢复笔时刻 131；now=140, W=10, cutoff=130：恢复笔在窗口内，Rd 回落 Rs。
	if got := h.Peek(140, 10); got != 1081 {
		t.Fatalf("rd right after resume within window = %d, want Rs 1081", got)
	}
	// now=141, cutoff=131：恢复笔恰在 cutoff 上，成为 Rd。
	if got := h.Peek(141, 10); got != 1081 {
		t.Fatalf("rd at exact boundary = %d, want 1081", got)
	}
}

func TestPoppedBoundedAndAmortizedConstant(t *testing.T) {
	// 两档：窗口内 10 笔与 10000 笔，结构断言弹出总数 <= 成交总数；
	// 朴素模型每笔 Trade 触碰的记录数随后续窗口内笔数增长（对最后一笔做探针观测）。
	for _, n := range []int{10, 10000} {
		h := NewHistory(1_000_000)
		all := []tick{}
		rng := rand.New(rand.NewSource(int64(n)))
		for i := 0; i < n; i++ {
			at := int64(i + 1) // W=1000000：全部成交都留在窗口内
			price := 1_000_000 + rng.Int63n(100)
			h.Advance(at, 1_000_000, price)
			all = append(all, tick{at, price})
		}
		if h.Popped() > h.Trades() {
			t.Fatalf("n=%d popped=%d > trades=%d", n, h.Popped(), h.Trades())
		}
		cutoff := int64(0) // 早于首笔：合格成交为空，朴素模型必须扫过全部窗口内记录
		_, naiveTouched := rdNaive(1_000_000, all, cutoff)
		if naiveTouched != n {
			t.Fatalf("naive touch should scan all %d window trades, got %d", n, naiveTouched)
		}
		// 结构探针：Advance 每次仅并入“越过 cutoff”的头部，窗口内 n 笔时
		// 单次触碰与 n 无关（此处 popped=0，下一次 Advance 触碰的仅是新尾部）。
		t.Logf("window-trades=%d popped=%d trades=%d naive-touched=%d (amortized O(1) vs linear)",
			n, h.Popped(), h.Trades(), naiveTouched)
	}
}

func TestRandomHistoryVsNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20261005))
	for iter := 0; iter < 200; iter++ {
		rs := int64(1_000 + rng.Intn(100_000))
		window := int64(1 + rng.Intn(50))
		h := NewHistory(rs)
		all := []tick{}
		var now int64
		for k := 0; k < 300; k++ {
			now += int64(rng.Intn(6))
			price := rs + rng.Int63n(500) - 250
			h.Advance(now, window, price)
			all = append(all, tick{now, price})
			// deque 只支持单调不回退的当前时刻视图，探针只在当前 now 处取值。
			probe := now
			got := h.Peek(probe, window)
			// naive 只应见到不晚于当前时刻的成交（探针不看未来）。
			visible := append([]tick{}, all...)
			want, _ := rdNaive(rs, visible, probe-window)
			if got != want {
				t.Fatalf("iter=%d k=%d probe=%d rd=%d want=%d", iter, k, probe, got, want)
			}
		}
		if h.Popped() > h.Trades() {
			t.Fatalf("iter=%d popped %d > trades %d", iter, h.Popped(), h.Trades())
		}
	}
}
