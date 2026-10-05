// Package frames 维护定长环形帧缓冲与帧字节数的前缀和查询。
package frames

// Ring 是只保留最近 cap 帧的环形缓冲（骨架）。
// 每个槽位记录当前占用该槽位的帧 tick 与“到该帧为止（含全部历史帧）”的累计字节。
type Ring struct {
	cap   int
	slots []slot
	cur   int
	base  int64

	// touched 为非导出计数器：记录 RangeSum 触及的帧记录条数（每个端点一次）。
	touched int
}

type slot struct {
	tick int
	cum  int64
}

// New 创建容量为 k 的环形缓冲。
func New(k int) *Ring {
	return &Ring{cap: k, slots: make([]slot, k)}
}

// Append 追加下一帧，size 为该帧字节数。
func (r *Ring) Append(size int) {
	tick := r.cur + 1
	if tick > r.cap {
		evicted := r.slots[(tick-1)%r.cap]
		if evicted.tick == tick-r.cap {
			r.base = evicted.cum
		}
	}
	last := r.base
	if r.cur > 0 {
		if s := r.slots[(r.cur-1)%r.cap]; s.tick == r.cur {
			last = s.cum
		}
	}
	idx := (tick - 1) % r.cap
	r.slots[idx] = slot{tick: tick, cum: last + int64(size)}
	r.cur = tick
}

// Cur 返回最新帧号（无帧为 0）。
func (r *Ring) Cur() int { return r.cur }

// Low 返回缓冲中最旧帧号（cur=0 时为 1）。
func (r *Ring) Low() int {
	low := r.cur - r.cap + 1
	if low < 1 {
		return 1
	}
	return low
}

// Has 报告帧号 tick 是否仍在缓冲内。
func (r *Ring) Has(tick int) bool {
	if tick < 1 || tick > r.cur {
		return false
	}
	s := r.slots[(tick-1)%r.cap]
	return s.tick == tick
}

// RangeSum 返回闭区间 [from,to] 各帧 size 之和；空区间（from>to）为 0。
func (r *Ring) RangeSum(from, to int) int64 {
	if from > to {
		return 0
	}
	return r.prefix(to) - r.prefix(from-1)
}

// prefix 返回帧 1..tick 的 size 之和；tick=0 为 0。每个端点触及一条帧记录。
func (r *Ring) prefix(tick int) int64 {
	if tick <= 0 {
		return 0
	}
	s := r.slots[(tick-1)%r.cap]
	if s.tick != tick {
		return r.base // tick 已被挤出：base 恰为最近被挤出帧的累计和
	}
	r.touched++
	return s.cum
}

// touchedCount 返回自创建以来触及的帧记录总数（非导出，供同包测试读取）。
func (r *Ring) touchedCount() int { return r.touched }
