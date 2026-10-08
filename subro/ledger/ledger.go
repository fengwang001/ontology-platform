// Package ledger 记录每一方已实际发放的金额，
// 并在每次被接受的操作后把应得与已发放的最小差结清为调整记录。
package ledger

import (
	"ontology/subro/alloc"
	"ontology/subro/internal/clog"
)

// Party 参与分配的一方。
type Party int

const (
	Insured    Party = iota // 被保险人
	Insurer                 // 保险人
	ThirdParty              // 责任第三方（接收超额退还）
)

func (p Party) String() string {
	switch p {
	case Insured:
		return "insured"
	case Insurer:
		return "insurer"
	default:
		return "third_party"
	}
}

// Direction 调整方向。
type Direction int

const (
	Pay      Direction = iota // 补发：应得大于已发放
	Clawback                  // 追回：应得小于已发放
)

func (d Direction) String() string {
	if d == Pay {
		return "pay"
	}
	return "clawback"
}

// Adjustment 一条最小差调整记录。
type Adjustment struct {
	Seq       int64     // 全局单调序号，保证重放可比对
	Party     Party     // 调整对象
	Direction Direction // 补发或追回
	Amount    int64     // 调整金额（恒为正）
	Now       int64     // 产生该记录的操作的 now
	Cause     string    // 产生该记录的操作类型
}

// Ledger 单案件的已发放台账与调整流水。
// 结清只读写三个已发放计数器，开销与历史回收笔数无关。
type Ledger struct {
	disbursed   [3]int64
	adjustments clog.Log[Adjustment]
}

// Settle 把应得与已发放之差立即结清：应得大于已发放则补发，
// 小于则追回；每方至多一条记录，差额为零不产生记录。
// 调整金额恰为最小差，不做全额追回再重发。
func (l *Ledger) Settle(ent alloc.Entitlements, now int64, cause string, nextSeq func() int64) []Adjustment {
	want := [3]int64{ent.Insured, ent.Insurer, ent.ThirdParty}
	var out []Adjustment
	for p := Party(0); p <= ThirdParty; p++ {
		diff := want[p] - l.disbursed[p]
		if diff == 0 {
			continue
		}
		dir, amt := Pay, diff
		if diff < 0 {
			dir, amt = Clawback, -diff
		}
		out = append(out, Adjustment{
			Seq:       nextSeq(),
			Party:     p,
			Direction: dir,
			Amount:    amt,
			Now:       now,
			Cause:     cause,
		})
		l.disbursed[p] = want[p]
	}
	for _, a := range out {
		l.adjustments.Append(a)
	}
	return out
}

// Disbursed 返回三方当前已实际发放金额。
func (l *Ledger) Disbursed() alloc.Entitlements {
	return alloc.Entitlements{
		Insured:    l.disbursed[Insured],
		Insurer:    l.disbursed[Insurer],
		ThirdParty: l.disbursed[ThirdParty],
	}
}

// Adjustments 返回全部调整记录的副本。
func (l *Ledger) Adjustments() []Adjustment {
	return l.adjustments.Slice()
}
