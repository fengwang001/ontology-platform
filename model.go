package ontology

// ID 是供应商、商品等实体的整数编号。
type ID int64

// BatchID 是批次编号，在同一供应商同一商品内按到货先后递增分配。
type BatchID int64

// LineID 是结算行的全局唯一编号。
type LineID int64

// Batch 描述一个寄售批次。
type Batch struct {
	Supplier  ID
	Product   ID
	Number    BatchID
	Arrival   int64
	Quantity  int64
	Remaining int64
	Duration  int64
}

// ExpiredAt 返回批次到期时刻（到货时刻 + 寄售期限）。
func (b *Batch) ExpiredAt() int64 { return b.Arrival + b.Duration }

// ExpiredAtTime 判断批次在时刻 now 是否到期（到期是时刻的纯函数）。
// 自到货时刻起恰好满期限即到期：now >= arrival+duration。
func (b *Batch) ExpiredAtTime(now int64) bool { return now >= b.ExpiredAt() }

// SettlementLine 描述一笔领用拆分出的一行结算记录。
type SettlementLine struct {
	ID          LineID
	ConsumeTime int64
	Supplier    ID
	Product     ID
	BatchNumber BatchID
	Quantity    int64
	UnitPrice   int64
	Reversed    int64
}

// Amount 返回该行原始领用金额。
func (l *SettlementLine) Amount() int64 { return l.Quantity * l.UnitPrice }

// ReversedAmount 返回该行已冲销金额（按原结算行单价）。
func (l *SettlementLine) ReversedAmount() int64 { return l.Reversed * l.UnitPrice }

// Receipt 是领用成功后返回的收据。
type Receipt struct {
	Time  int64
	Lines []SettlementLine
}

// Statement 是某供应商某周期的对账单。
type Statement struct {
	Supplier ID
	Start    int64
	End      int64
	Consumed int64
	Reversed int64
	Net      int64
}
