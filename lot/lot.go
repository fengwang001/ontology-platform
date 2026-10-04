package lot

// Dir 为持仓方向；多空互不抵消。
type Dir int

const (
	Long  Dir = +1
	Short Dir = -1
)

// Batch 为一笔今仓批次。
type Batch struct {
	Price int64
	Qty   int64
}

// Position 为 (账户,合约,方向) 维度的持仓。
// qy 为昨仓手数，sp0 为昨仓计价所用上一结算价；today 为今仓 FIFO 批次。
type Position struct {
	qy      int64
	sp0     int64
	today   []Batch
	head    int
	touched int
}

// New 建空持仓。
func New() *Position { return &Position{} }

// QY 返回昨仓手数。
func (p *Position) QY() int64 { return p.qy }

// SP0 返回昨仓结算价。
func (p *Position) SP0() int64 { return p.sp0 }

// TodayQty 返回今仓总手数。
func (p *Position) TodayQty() int64 {
	var n int64
	for _, b := range p.today[p.head:] {
		n += b.Qty
	}
	return n
}

// Touched 返回最近一次 CloseToday 触碰的今仓批次数。
func (p *Position) Touched() int { return p.touched }

// Open 追加一笔今仓批次。
func (p *Position) Open(price, qty int64) {
	p.today = append(p.today, Batch{Price: price, Qty: qty})
}

// SnapshotToday 返回当前有效今仓批次的拷贝。
func (p *Position) SnapshotToday() []Batch {
	out := make([]Batch, 0, len(p.today)-p.head)
	for _, b := range p.today[p.head:] {
		if b.Qty > 0 {
			out = append(out, b)
		}
	}
	return out
}

// CanYesterday / CanToday / CanAuto 为平仓量预检。
func (p *Position) CanYesterday(qty int64) bool { return qty <= p.qy }
func (p *Position) CanToday(qty int64) bool     { return qty <= p.TodayQty() }
func (p *Position) CanAuto(qty int64) bool      { return qty <= p.qy+p.TodayQty() }

// ClosedLot 描述被平掉的一段仓位。
type ClosedLot struct {
	OpenPrice int64
	Qty       int64
}

// CloseYesterday 平昨，返回被平手数；调用前须通过 CanYesterday。
func (p *Position) CloseYesterday(qty int64) int64 {
	p.qy -= qty
	return qty
}

// CloseToday 按 FIFO 平今，返回被平批次明细；调用前须通过 CanToday。
// touched 只统计实际进入扣减循环的批次数，故恒有
// touched <= 被完全平掉批次数 + 1，与今仓批次总数无关。
func (p *Position) CloseToday(qty int64) []ClosedLot {
	lots := make([]ClosedLot, 0, 1)
	p.touched = 0
	rem := qty
	for rem > 0 {
		b := &p.today[p.head]
		p.touched++
		take := rem
		if take > b.Qty {
			take = b.Qty
		}
		lots = append(lots, ClosedLot{OpenPrice: b.Price, Qty: take})
		b.Qty -= take
		rem -= take
		if b.Qty == 0 {
			p.head++
		}
	}
	if p.head == len(p.today) {
		p.today = p.today[:0]
		p.head = 0
	}
	return lots
}

// Notional 返回保证金口径名义额：qy×sp0 + Σ今仓手数×开仓价。
func (p *Position) Notional() int64 {
	n := p.qy * p.sp0
	for _, b := range p.today[p.head:] {
		n += b.Qty * b.Price
	}
	return n
}

// MTM 以结算价 sp 计算盯市盈亏（s 为方向符号 ±1），不改变持仓。
func (p *Position) MTM(s Dir, mult, sp int64) int64 {
	pnl := int64(s) * (sp - p.sp0) * p.qy * mult
	for _, b := range p.today[p.head:] {
		pnl += int64(s) * (sp - b.Price) * b.Qty * mult
	}
	return pnl
}

// Merge 将全部今仓并入昨仓并以 sp 计价（须在 MTM 之后调用）。
func (p *Position) Merge(sp int64) {
	for _, b := range p.today[p.head:] {
		p.qy += b.Qty
	}
	p.today = p.today[:0]
	p.head = 0
	p.sp0 = sp
}
