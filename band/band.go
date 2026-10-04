// Package band 维护单个标的的涨跌停、静态/动态参考价与超带判定。
// Package band 维护单个标的的涨跌停、静态/动态参考价与超带判定。
package band

// Params 是 AddSymbol 的参数集。
type Params struct {
	Prev  int64
	L     int64
	Ds    int64
	Dd    int64
	De    int64
	W     int64
	T     int64
	X     int64
	Hmax  int64
	Open  int64
	Close int64
	C     int64
}

// Symbol 是某标的的不可变价格规则。
type Symbol struct {
	p      Params
	up, dn int64
}

// Tick 是一笔成交记录。
type Tick struct {
	Time  int64
	Price int64
}

// Book 保存静态参考价与成交队列，负责动态参考价。
type Book struct {
	rs     int64
	ticks  []Tick
	head   int
	lastRd int64
	popped int
}

// New 构造 Symbol，返回的 up/dn 向内取整。
func New(p Params) *Symbol {
	up := p.Prev * (10000 + p.L) / 10000
	num := p.Prev * (10000 - p.L)
	dn := num / 10000
	if num%10000 != 0 {
		dn++
	}
	return &Symbol{p: p, up: up, dn: dn}
}

// InLimit 判断价格是否在涨跌停区间内（含端点）。
func (s *Symbol) InLimit(price int64) bool { return s.dn <= price && price <= s.up }

// Up 返回涨停价。
func (s *Symbol) Up() int64 { return s.up }

// Dn 返回跌停价。
func (s *Symbol) Dn() int64 { return s.dn }

// Over 判断 x 相对参考价 R、带宽 D（基点）是否超带；取等不超带。
func Over(x, r, d int64) bool {
	diff := x - r
	if diff < 0 {
		diff = -diff
	}
	return diff*10000 > d*r
}

// P 暴露只读参数。
func (s *Symbol) P() Params { return s.p }

// NewBook 以静态参考价 Rs=prev 建立成交簿。
func NewBook(rs int64) *Book { return &Book{rs: rs, lastRd: rs} }

// Rs 返回静态参考价。
func (b *Book) Rs() int64 { return b.rs }

// SetRs 更新静态参考价。
func (b *Book) SetRs(rs int64) { b.rs = rs }

// Rd 返回 now 时刻的动态参考价，并弹出老化记录；popped 为本次弹出条数。
func (b *Book) Rd(now, w int64) (rd int64, popped int) {
	cutoff := now - w
	for b.head < len(b.ticks) && b.ticks[b.head].Time <= cutoff {
		b.lastRd = b.ticks[b.head].Price
		b.head++
		b.popped++
		popped++
	}
	if b.head == len(b.ticks) {
		b.ticks = b.ticks[:0]
		b.head = 0
	}
	return b.lastRd, popped
}

// At 返回队列中下标 k（0 为最老存活记录）对应的成交。
func (b *Book) At(k int) Tick { return b.ticks[b.head+k] }

// LastRd 返回当前记忆的动态参考价（最近老化记录的价格，无则为 Rs）。
func (b *Book) LastRd() int64 { return b.lastRd }

// Drain 弹出所有成交时刻不晚于 now-W 的队首记录。
func (b *Book) Drain(now, w int64) (popped int) {
	cutoff := now - w
	for b.head < len(b.ticks) && b.ticks[b.head].Time <= cutoff {
		b.lastRd = b.ticks[b.head].Price
		b.head++
		b.popped++
		popped++
	}
	if b.head == len(b.ticks) {
		b.ticks = b.ticks[:0]
		b.head = 0
	}
	return popped
}

// Append 追加一笔成交。
func (b *Book) Append(t Tick) { b.ticks = append(b.ticks, t) }

// Reset 清空成交记录，并以 now 时刻的一笔成交作为唯一记录。
func (b *Book) Reset(rs int64, t Tick) {
	b.rs = rs
	b.lastRd = rs
	b.ticks = append(b.ticks[:0], t)
	b.head = 0
}

// Popped 返回累计弹出次数。
func (b *Book) Popped() int { return b.popped }

// Len 返回当前记录数。
func (b *Book) Len() int { return len(b.ticks) - b.head }
