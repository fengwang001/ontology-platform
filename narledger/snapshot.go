package narledger

import (
	"fmt"
	"sort"
)

// Snapshot 是可重放比对的完整状态快照。
type Snapshot struct {
	LastNow int64
	Batches map[string]BatchView // key -> 批次视图
	Orders  map[string]OrderView
}

// OrderView 单据对外视图。
type OrderView struct {
	Dept      string
	Applicant string
	Drug      string
	Lots      []string
	Status    string
	Qty       int64
	Shortfall int64
	CreatedAt int64
	Deadline  int64
	Used      int64
	Returned  int64
	Residue   int64
}

// BatchView 批次账面视图。
type BatchView struct {
	Drug     string
	Lot      string
	Balance  int64
	ExpireAt int64
}

// Snapshot 导出确定性快照：map 内容固定，键排序由 EqualSnap 负责。
func (l *Ledger) Snapshot() Snapshot {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := Snapshot{
		LastNow: l.clk.lastNow,
		Batches: make(map[string]BatchView),
		Orders:  make(map[string]OrderView),
	}
	for key, b := range l.stock.batches {
		s.Batches[key] = BatchView{Drug: b.drug, Lot: b.lot, Balance: b.qty, ExpireAt: b.expireAt}
	}
	for id, o := range l.orders.byID {
		v := OrderView{
			Dept:      o.dept,
			Applicant: o.applicant,
			Drug:      o.drug,
			Status:    o.status.String(),
			Qty:       o.qty,
			Shortfall: o.shortfall,
			CreatedAt: o.createdAt,
			Deadline:  o.deadline,
			Used:      o.used,
			Returned:  o.returned,
			Residue:   o.residue,
		}
		for _, a := range o.allocs {
			v.Lots = append(v.Lots, a.lot+":"+itoa(a.qty))
		}
		s.Orders[id] = v
	}
	return s
}

// EqualSnap 深度比较两份快照，返回首个差异描述（空串表示一致）。
func EqualSnap(a, b Snapshot) string {
	if a.LastNow != b.LastNow {
		return fmt.Sprintf("LastNow %d != %d", a.LastNow, b.LastNow)
	}
	if len(a.Batches) != len(b.Batches) {
		return fmt.Sprintf("batch count %d != %d", len(a.Batches), len(b.Batches))
	}
	for k, va := range a.Batches {
		vb, ok := b.Batches[k]
		if !ok {
			return "missing batch " + k
		}
		if va != vb {
			return fmt.Sprintf("batch %s: %+v != %+v", k, va, vb)
		}
	}
	if len(a.Orders) != len(b.Orders) {
		return fmt.Sprintf("order count %d != %d", len(a.Orders), len(b.Orders))
	}
	for k, va := range a.Orders {
		vb, ok := b.Orders[k]
		if !ok {
			return "missing order " + k
		}
		if orderViewCmp(va, vb) != "" {
			return fmt.Sprintf("order %s: %+v != %+v", k, va, vb)
		}
	}
	return ""
}

func orderViewCmp(a, b OrderView) string {
	if a.Dept != b.Dept || a.Applicant != b.Applicant || a.Drug != b.Drug ||
		a.Status != b.Status || a.Qty != b.Qty || a.Shortfall != b.Shortfall ||
		a.CreatedAt != b.CreatedAt || a.Deadline != b.Deadline ||
		a.Used != b.Used || a.Returned != b.Returned || a.Residue != b.Residue ||
		!sameStrings(a.Lots, b.Lots) {
		return "differ"
	}
	return ""
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// InvariantError 描述一条被违反的不变量。
type InvariantError struct{ Msg string }

func (e *InvariantError) Error() string { return e.Msg }

// CheckInvariant 校验：任一药品账面总量 = 入库 - 销毁 - 累计领出 + 累计退回。
// 累计领出/退回直接从已接受单据按批次汇总，独立于日常计数器。
func (l *Ledger) CheckInvariant() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	issued := make(map[string]int64)
	returned := make(map[string]int64)
	for _, o := range l.orders.byID {
		for _, a := range o.allocs {
			issued[a.drug] += a.qty
		}
		returned[o.drug] += o.returned
	}
	bookSum := make(map[string]int64)
	for _, b := range l.stock.batches {
		bookSum[b.drug] += b.qty
	}
	drugs := make(map[string]struct{})
	for d := range l.stock.received {
		drugs[d] = struct{}{}
	}
	for d := range bookSum {
		drugs[d] = struct{}{}
	}
	var all []string
	for d := range drugs {
		all = append(all, d)
	}
	sort.Strings(all)
	for _, d := range all {
		want := l.stock.received[d] - l.stock.destroyed[d] - issued[d] + returned[d]
		if bookSum[d] != want {
			return &InvariantError{Msg: fmt.Sprintf(
				"药品 %s 账面 %d != 入库 %d - 销毁 %d - 领出 %d + 退回 %d = %d",
				d, bookSum[d], l.stock.received[d], l.stock.destroyed[d],
				issued[d], returned[d], want)}
		}
	}
	return nil
}
