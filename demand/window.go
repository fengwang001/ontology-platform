package demand

import "math/big"

// windowRing 以滑差切片为粒度保存已发生用电量。
// 任意时刻活着的窗口恰好覆盖最近 windowSec/slipSec 个完整切片，
// 因此评估开销只与重叠窗口个数有关，与历史上报次数无关。
type windowRing struct {
	cfg Config

	// cells[i] 保存某切片用电量在公分母 den 下的整数分子。
	// 上报区间功率为 energy/dur（整数比），分摊到切片后分母始终整除
	// 滑差粒度相关的公分母；为容纳任意配置，den 用 big.Int 统一放大。
	cells []int64
	// gen[i] 为 cells[i] 当前数据所属的绝对切片序号；不一致即视为空切片。
	gen []int64
	// den 为 cells 的公分母（正整数）；首个区间写入时确定并保持不变。
	den int64
	// started 表示是否已收到首次上报。
	started bool
	// lastSlice 为最近一次已完成关窗处理的切片序号。
	lastSlice int64

	firstReport int64 // 首次被接受上报时刻
}

func newWindowRing(cfg Config) *windowRing {
	n := cfg.OverlapCount()
	return &windowRing{
		cfg:         cfg,
		cells:       make([]int64, n),
		gen:         make([]int64, n),
		started:     false,
		lastSlice:   -1,
		firstReport: -1,
		den:         1,
	}
}

func (r *windowRing) index(slice int64) int {
	n := r.cfg.OverlapCount()
	m := slice % n
	if m < 0 {
		m += n
	}
	return int(m)
}

func (r *windowRing) cellValid(slice int64) bool {
	if !r.started || slice < 0 {
		return false
	}
	return r.gen[r.index(slice)] == slice
}

// advance 把 (prev, now] 内恒定功率对应的用电量 energy 写入切片环。
//
// 关窗纪律：切片 s 会在 n 个切片之后（写入切片 s+n 时）被物理覆盖；
// 最后使用它的窗口结束于 (s+1)*slip。因此写入切片 k 前，先按结束时刻
// 升序补关最后切片 <= k-n 的窗口。now 恰好落在边界时，结束于 now 的
// 窗口此刻到期（其最后一片为 now/slip-1），也要立即关窗。
type closeFn func(endAt int64, energy *frac)

func (r *windowRing) advance(prev, now int64, energy *frac, onClose closeFn) {
	if !r.started {
		r.started = true
		r.firstReport = now
		r.lastSlice = -1
	}

	// 区间功率 = energy/dur（整数比）。统一把公分母设为 1，
	// 切片分摊值 = energy*overlap/dur，用整数（可能放大）表示。
	dur := now - prev
	eNum, eDen := energy.num(), energy.den()
	_ = eDen

	slip := r.cfg.SlipSec
	lo := prev / slip
	hi := now / slip
	for s := lo; s <= hi; s++ {
		sStart := s * slip
		sEnd := sStart + slip
		overlap := min64(sEnd, now) - max64(sStart, prev)
		if overlap > 0 {
			// 覆盖切片 s（[sStart,sEnd)）前，所有结束时刻 <= sStart 的窗口
			// 均已到期（最后切片 <= s-1），而它们中最老者恰好还在使用
			// 即将被覆盖的槽 s-n。稀疏上报下一次可能跨越多个窗口，
			// 因此必须把水位推进到 s-1，先关窗再覆盖。
			r.closeUpTo(s-1, onClose)
			// 该切片能量 = eNum/eDen * overlap/dur
			// => 分子 eNum*overlap，分母 eDen*dur。
			r.addCellFrac(s, eNum*overlap, eDen*dur)
		}
	}

	// 区间数据落盘后，关闭所有结束时刻 <= now 的窗口。
	r.closeUpTo(now/slip-1, onClose)
}

// addCellFrac 向切片 s 累加 num/den（千瓦秒）。
// 统一缩放到环的公分母 r.den：若新分母与 r.den 不同则整体放大所有切片。
func (r *windowRing) addCellFrac(s, num, den int64) {
	if den != r.den {
		// 把既有切片与新值统一到 lcm(r.den, den)。
		g := new(big.Int).GCD(nil, nil, big.NewInt(r.den), big.NewInt(den)).Int64()
		newDen := r.den / g * den
		oldFactor := newDen / r.den
		newFactor := newDen / den
		for i := range r.cells {
			r.cells[i] *= oldFactor
		}
		r.den = newDen
		num *= newFactor
	}
	i := r.index(s)
	if r.gen[i] != s {
		r.cells[i] = 0
		r.gen[i] = s
	}
	r.cells[i] += num
}

// closeUpTo 关闭所有最后切片 <= slice 且尚未关闭的窗口，按结束时刻升序回调。幂等。
func (r *windowRing) closeUpTo(slice int64, onClose closeFn) {
	for s := r.lastSlice + 1; s <= slice; s++ {
		end := (s + 1) * r.cfg.SlipSec
		if end <= 0 {
			// 时刻 0 之前不存在窗口，仅推进水位而不回调。
			r.lastSlice = s
			continue
		}
		onClose(end, r.windowEnergy(end))
		r.lastSlice = s
	}
}

// windowEnergy 返回结束于 end 的窗口内已发生用电量。
// 早于首次上报时刻的部分为天然空切片，按无用电处理。
func (r *windowRing) windowEnergy(end int64) *frac {
	n := r.cfg.OverlapCount()
	endSlice := end/r.cfg.SlipSec - 1
	out := newFrac()
	var sum int64
	for k := int64(0); k < n; k++ {
		s := endSlice - k
		if s < 0 {
			break
		}
		if r.cellValid(s) {
			sum += r.cells[r.index(s)]
		}
	}
	out.v.SetFrac(big.NewInt(sum), big.NewInt(r.den))
	return out
}

// activeEnds 返回 now 时刻所有"尚未结束、且已覆盖当前时刻"的窗口结束时刻（升序）。
func (r *windowRing) activeEnds(now int64) []int64 {
	n := r.cfg.OverlapCount()
	slip := r.cfg.SlipSec
	next := (now/slip + 1) * slip // 严格大于 now 的最近结束时刻
	ends := make([]int64, 0, n)
	for k := int64(0); k < n; k++ {
		if end := next + k*slip; end > now {
			ends = append(ends, end)
		}
	}
	return ends
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
