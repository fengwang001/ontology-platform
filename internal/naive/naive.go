// Package naive 是按题目规则独立写成的朴素对照模型：
// 不做任何汇总量增量缓存，每次都从显式历史事件逐笔重算，
// 实现刻意简单直白，用于与主系统做差分测试。
package naive

import "fmt"

type Config struct {
	Long, Short int64
	RefundPct   [3]int
	ChangePct   [3]int
	MaxChanges  int
	VoucherTTL  int64
}

type Flight struct {
	Fare, Departure int64
	Cancelled       bool
}

type Voucher struct {
	ID        string
	Owner     string
	Amount    int64
	ExpiresAt int64
	Used      bool
}

// ChangeEvent 记录一次被接受的改签，用于逐笔重算现金与改签费。
type ChangeEvent struct {
	Time        int64
	FromFlight  string
	ToFlight    string
	FromFare    int64
	ToFare      int64
	FromDep     int64
	Fee         int64
	CashPaid    int64
	VoucherUsed int64
	VoucherID   string // 负差价生成的新券
	Involuntary bool
}

type Ticket struct {
	Owner      string
	FlightID   string
	Fare       int64
	Dep        int64
	Refunded   bool
	Invol      bool
	RefundCash int64
	History    []ChangeEvent
}

type RefundOut struct {
	Cash, Fare, Fee, ChangeFees int64
	Invol                       bool
}

type ChangeOut struct {
	Fee, Diff, Total, NewVoucher, VoucherUsed, CashDue int64
	NewVoucherID                                       string
	Invol                                              bool
	NewFare, NewDep                                    int64
	Changes                                            int
}

type RefModel struct {
	cfg      Config
	now      int64
	flights  map[string]*Flight
	tickets  map[string]*Ticket
	vouchers map[string]*Voucher
	vcSeq    int
}

func New(cfg Config) *RefModel {
	return &RefModel{
		cfg:      cfg,
		flights:  map[string]*Flight{},
		tickets:  map[string]*Ticket{},
		vouchers: map[string]*Voucher{},
	}
}

func (m *RefModel) RegisterFlight(id string, fare, dep int64) error {
	if id == "" || fare < 0 || dep < 0 {
		return fmt.Errorf("invalid flight args")
	}
	if m.flights[id] != nil {
		return fmt.Errorf("flight exists")
	}
	m.flights[id] = &Flight{Fare: fare, Departure: dep}
	return nil
}

func (m *RefModel) PurchaseTicket(id, owner, flightID string) error {
	if id == "" || owner == "" || flightID == "" {
		return fmt.Errorf("invalid purchase args")
	}
	f := m.flights[flightID]
	if f == nil || f.Cancelled {
		return fmt.Errorf("bad flight")
	}
	if m.tickets[id] != nil {
		return fmt.Errorf("ticket exists")
	}
	m.tickets[id] = &Ticket{Owner: owner, FlightID: flightID, Fare: f.Fare, Dep: f.Departure}
	return nil
}

func (m *RefModel) CancelFlight(id string, now int64) (int, error) {
	if id == "" || now < 0 {
		return 0, fmt.Errorf("invalid cancel args")
	}
	if now < m.now {
		return 0, fmt.Errorf("clock rewind")
	}
	f := m.flights[id]
	if f == nil {
		return 0, fmt.Errorf("flight not found")
	}
	if f.Cancelled {
		return 0, fmt.Errorf("already cancelled")
	}
	f.Cancelled = true
	n := 0
	for _, t := range m.tickets {
		if !t.Refunded && t.FlightID == id {
			t.Invol = true
			n++
		}
	}
	m.now = now
	return n, nil
}

func ceilFee(fare int64, pct int) int64 {
	if pct <= 0 || fare <= 0 {
		return 0
	}
	return (fare*int64(pct) + 99) / 100
}

// tierPct 朴素地重算档位：0 远 / 1 中 / 2 近；departed 单独返回。
func tierPct(delta, long, short int64, pct [3]int) (int, bool) {
	if delta <= 0 {
		return pct[2], true
	}
	switch {
	case delta >= long:
		return pct[0], false
	case delta >= short:
		return pct[1], false
	default:
		return pct[2], false
	}
}

// changeFeesTotal 从历史逐笔累加“自愿改签”的改签费。
func changeFeesTotal(t *Ticket) int64 {
	var sum int64
	for _, e := range t.History {
		if !e.Involuntary {
			sum += e.Fee
		}
	}
	return sum
}

func voluntaryChanges(t *Ticket) int {
	n := 0
	for _, e := range t.History {
		if !e.Involuntary {
			n++
		}
	}
	return n
}

func (m *RefModel) Refund(id string, now int64) (RefundOut, error) {
	if id == "" || now < 0 {
		return RefundOut{}, fmt.Errorf("invalid refund args")
	}
	if now < m.now {
		return RefundOut{}, fmt.Errorf("clock rewind")
	}
	t := m.tickets[id]
	if t == nil {
		return RefundOut{}, fmt.Errorf("ticket not found")
	}
	if t.Refunded {
		return RefundOut{}, fmt.Errorf("already refunded")
	}
	out := RefundOut{Fare: t.Fare, Invol: t.Invol}
	if t.Invol {
		out.ChangeFees = changeFeesTotal(t)
		out.Cash = t.Fare + out.ChangeFees
	} else {
		pct, departed := tierPct(t.Dep-now, m.cfg.Long, m.cfg.Short, m.cfg.RefundPct)
		if departed {
			return RefundOut{}, fmt.Errorf("departed")
		}
		out.Fee = ceilFee(t.Fare, pct)
		out.Cash = t.Fare - out.Fee
	}
	t.Refunded = true
	t.RefundCash = out.Cash
	m.now = now
	return out, nil
}

func (m *RefModel) Change(ticketID, targetID, voucherID string, cash, now int64) (ChangeOut, error) {
	if ticketID == "" || targetID == "" || now < 0 || cash < 0 {
		return ChangeOut{}, fmt.Errorf("invalid change args")
	}
	if now < m.now {
		return ChangeOut{}, fmt.Errorf("clock rewind")
	}
	t := m.tickets[ticketID]
	if t == nil {
		return ChangeOut{}, fmt.Errorf("ticket not found")
	}
	if t.Refunded {
		return ChangeOut{}, fmt.Errorf("already refunded")
	}
	tf := m.flights[targetID]
	if tf == nil || tf.Cancelled || targetID == t.FlightID {
		return ChangeOut{}, fmt.Errorf("bad target")
	}
	if !t.Invol && voluntaryChanges(t) >= m.cfg.MaxChanges {
		return ChangeOut{}, fmt.Errorf("change limit")
	}

	out := ChangeOut{Invol: t.Invol, Diff: tf.Fare - t.Fare}
	if t.Invol {
		if voucherID != "" || cash != 0 {
			return ChangeOut{}, fmt.Errorf("payment mismatch: due=0 paid=%d", cash)
		}
	} else {
		pct, departed := tierPct(t.Dep-now, m.cfg.Long, m.cfg.Short, m.cfg.ChangePct)
		if departed {
			return ChangeOut{}, fmt.Errorf("departed")
		}
		out.Fee = ceilFee(t.Fare, pct)
		out.Total = out.Fee
		if out.Diff > 0 {
			out.Total += out.Diff
		} else if out.Diff < 0 {
			out.NewVoucher = -out.Diff
		}
		if out.Total > 0 && voucherID != "" {
			v := m.vouchers[voucherID]
			switch {
			case v == nil:
				return ChangeOut{}, fmt.Errorf("voucher not found")
			case v.Owner != t.Owner:
				return ChangeOut{}, fmt.Errorf("voucher owner")
			case now >= v.ExpiresAt:
				return ChangeOut{}, fmt.Errorf("voucher expired")
			case v.Used || v.Amount <= 0:
				return ChangeOut{}, fmt.Errorf("voucher used")
			}
			if v.Amount < out.Total {
				out.VoucherUsed = v.Amount
			} else {
				out.VoucherUsed = out.Total
			}
		}
		out.CashDue = out.Total - out.VoucherUsed
		if cash != out.CashDue {
			return ChangeOut{}, fmt.Errorf("payment mismatch: due=%d paid=%d", out.CashDue, cash)
		}
	}

	// 落地
	if out.VoucherUsed > 0 {
		v := m.vouchers[voucherID]
		v.Amount -= out.VoucherUsed
		if v.Amount <= 0 {
			v.Amount = 0
			v.Used = true
		}
	}
	ev := ChangeEvent{
		Time: now, FromFlight: t.FlightID, ToFlight: targetID,
		FromFare: t.Fare, ToFare: tf.Fare, FromDep: t.Dep,
		Fee: out.Fee, CashPaid: out.CashDue, VoucherUsed: out.VoucherUsed,
		Involuntary: t.Invol,
	}
	if out.NewVoucher > 0 {
		m.vcSeq++
		vid := fmt.Sprintf("V%06d", m.vcSeq)
		m.vouchers[vid] = &Voucher{ID: vid, Owner: t.Owner, Amount: out.NewVoucher, ExpiresAt: now + m.cfg.VoucherTTL}
		out.NewVoucherID = vid
		ev.VoucherID = vid
	}
	t.FlightID = targetID
	t.Fare = tf.Fare
	t.Dep = tf.Departure
	wasInvol := t.Invol
	t.Invol = false
	t.History = append(t.History, ev)
	out.NewFare, out.NewDep, out.Changes = tf.Fare, tf.Departure, voluntaryChanges(t)
	_ = wasInvol
	m.now = now
	return out, nil
}

// CashNet 逐笔重算“历次现金支付 - 历次现金退回”，用于守恒校验。
func (m *RefModel) CashNet(ticketID string) (paid, refunded int64, ok bool) {
	t := m.tickets[ticketID]
	if t == nil {
		return 0, 0, false
	}
	refunded = t.RefundCash
	for _, e := range t.History {
		if !e.Involuntary {
			paid += e.CashPaid
		}
	}
	return paid, refunded, true
}

func (m *RefModel) Ticket(id string) (*Ticket, bool) {
	t, ok := m.tickets[id]
	return t, ok
}

func (m *RefModel) Voucher(id string) (*Voucher, bool) {
	v, ok := m.vouchers[id]
	return v, ok
}
