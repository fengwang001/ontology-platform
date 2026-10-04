// Package band 实现涨跌停、静态/动态参考价带宽与成交历史。
package band

// Band 描述单只标的的涨跌停与三条带宽（单位：基点，1bp=1/10000）。
type Band struct {
	Prev int64
	Up   int64
	Dn   int64
	Ds   int64
	Dd   int64
	De   int64
}

// tick 是一笔已成交记录。
type tick struct {
	at    int64
	price int64
}

// History 维护当前静态参考价与升序成交记录，支持窗口语义的动态参考价。
type History struct {
	rs     int64
	deque  []tick // 按时刻升序保留全部成交
	head   int    // deque[head:] 为仍在“最近窗口”候选中的成交
	last   tick   // 最后一笔被丢弃（过期）的成交，即单调时钟下后续 Rd 的最近候选
	has    bool   // last 是否有效
	popped int64  // 累计丢弃次数
	trades int64
}

// NewBand 构造涨跌停与带宽；参数合法性由调用方保证。
//
// 上限 floor(prev*(10000+L)/10000)，下限 ceil(prev*(10000-L)/10000)，向内取整。
func NewBand(prev, limit, ds, dd, de int64) *Band {
	up := prev * (10000 + limit) / 10000
	dn := (prev*(10000-limit) + 9999) / 10000
	return &Band{Prev: prev, Up: up, Dn: dn, Ds: ds, Dd: dd, De: de}
}

// LimitOK 判断价格是否在涨跌停 [dn,up] 内（含端点）。
func (b *Band) LimitOK(price int64) bool { return b.Dn <= price && price <= b.Up }

// Over 判断 price 相对参考价 ref 是否超出带宽 bp（取等不超带）。
// 超带当且仅当 |price-ref|*10000 > bp*ref，全程整数运算。
func Over(price, ref, bp int64) bool {
	diff := price - ref
	if diff < 0 {
		diff = -diff
	}
	return diff*10000 > bp*ref
}

// NewHistory 以静态参考价 rs 建立空成交历史。
func NewHistory(rs int64) *History {
	return &History{rs: rs}
}

// Rd 求 cutoff=now-W 语义下的动态参考价（调用方保证时钟不回退）。
// 时刻 <= cutoff 的成交过期并从队列前端丢弃，每笔终生只丢弃一次。
func (h *History) Peek(now, window int64) int64 {
	cutoff := now - window
	for h.head < len(h.deque) && h.deque[h.head].at <= cutoff {
		h.last = h.deque[h.head]
		h.has = true
		h.head++
		h.popped++
	}
	// last 是时刻 <= cutoff 的最后一笔（被丢弃者中的最末笔）；它必然满足 cutoff。
	if h.has {
		return h.last.price
	}
	return h.rs
}

// AppendTrade 追加一笔时刻 now 的成交；调用前应已用 Peek 完成本次裁剪。
// 同时定期回收 head 之前的底层数组空间。
func (h *History) AppendTrade(now, price int64) {
	if h.head > 0 {
		n := copy(h.deque, h.deque[h.head:])
		h.deque = h.deque[:n]
		h.head = 0
	}
	h.deque = append(h.deque, tick{at: now, price: price})
	h.trades++
}

// Advance 求 Rd（顺带裁剪）并追加时刻 now 的成交；摊还 O(1)。
func (h *History) Advance(now, window, price int64) {
	h.Peek(now, window)
	h.AppendTrade(now, price)
}

// Reset 清空成交历史，静态参考价置为 rs，并以时刻 now 的成交重新开始。
func (h *History) Reset(now, rs int64) {
	h.rs = rs
	h.deque = append(h.deque[:0], tick{at: now, price: rs})
	h.head = 0
	h.has = false
	h.trades++
}

// Popped 返回求 Rd 时丢弃（弹出）过期成交记录的累计次数（测试用非导出计数器）。
func (h *History) Popped() int64 { return h.popped }

// Trades 返回自建立以来计入的成交总数。
func (h *History) Trades() int64 { return h.trades }

// Rs 返回当前静态参考价。
func (h *History) Rs() int64 { return h.rs }
