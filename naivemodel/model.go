// Package naivemodel is an independent, deliberately straightforward
// re-implementation of the subrogation allocation rules, used only by
// tests as an oracle for the optimized subro.System. It recomputes
// everything from the full recovery history on every operation and
// derives entitlements with an explicit step-by-step waterfall, so
// that a state-tracking bug in the optimized system cannot be hidden
// by a matching bug here.
package naivemodel

import (
	"fmt"

	"ontology/subro"
)

type recovery struct {
	gross   int64
	expense int64
	at      int64
}

type caseData struct {
	totalLoss   int64
	insurerPaid int64
	deadline    int64
	ratioBP     int64
	waived      bool
	history     []recovery
	paid        map[subro.Party]int64
}

// Model mirrors subro.System with naive internals.
type Model struct {
	clockSet bool
	clock    int64
	seq      int64
	cases    map[string]*caseData
	log      []subro.Adjustment
}

func New() *Model {
	return &Model{cases: make(map[string]*caseData)}
}

func reject(kind subro.ErrKind, op subro.OpKind, id, detail string) *subro.Error {
	return &subro.Error{Kind: kind, Op: op, CaseID: id, Detail: detail}
}

func (m *Model) checkClock(now int64, op subro.OpKind, id string) error {
	if m.clockSet && now < m.clock {
		return reject(subro.ErrClockRollback, op, id, "now is before last accepted operation")
	}
	return nil
}

func (m *Model) accept(now int64) {
	m.clock = now
	m.clockSet = true
}

func (m *Model) RegisterCase(now int64, id string, totalLoss, insurerPaid, deadline, ratioBP int64) error {
	op := subro.OpRegister
	if now < 0 || id == "" || totalLoss < 0 || insurerPaid < 0 || insurerPaid > totalLoss ||
		deadline < 0 || ratioBP < 0 || ratioBP > 10000 {
		return reject(subro.ErrInvalidParam, op, id, "bad registration parameters")
	}
	if _, dup := m.cases[id]; dup {
		return reject(subro.ErrInvalidParam, op, id, "duplicate case id")
	}
	if err := m.checkClock(now, op, id); err != nil {
		return err
	}
	m.cases[id] = &caseData{
		totalLoss:   totalLoss,
		insurerPaid: insurerPaid,
		deadline:    deadline,
		ratioBP:     ratioBP,
		paid:        map[subro.Party]int64{},
	}
	m.accept(now)
	return nil
}

// waterfall distributes net among insured and insurer in priority
// order, one bucket at a time; the leftover is the third-party excess.
func waterfall(net, cap, uncompensated, insurerPaid int64, waived bool) (insured, insurer, third int64) {
	remaining := net
	if remaining > cap {
		third += remaining - cap
		remaining = cap
	}
	if !waived {
		take := uncompensated
		if take > remaining {
			take = remaining
		}
		insured += take
		remaining -= take
	}
	take := insurerPaid
	if take > remaining {
		take = remaining
	}
	insurer += take
	remaining -= take
	third += remaining
	return insured, insurer, third
}

func (c *caseData) netTotal() int64 {
	var net int64
	for _, r := range c.history {
		net += r.gross - r.expense
	}
	return net
}

func (c *caseData) entitled() map[subro.Party]int64 {
	cap := c.totalLoss * c.ratioBP / 10000
	insured, insurer, third := waterfall(c.netTotal(), cap, c.totalLoss-c.insurerPaid, c.insurerPaid, c.waived)
	return map[subro.Party]int64{
		subro.PartyInsured:    insured,
		subro.PartyInsurer:    insurer,
		subro.PartyThirdParty: third,
	}
}

// settle emits one minimal adjustment per party whose entitled amount
// differs from what it has actually been paid.
func (m *Model) settle(id string, c *caseData, op subro.OpKind) []subro.Adjustment {
	ent := c.entitled()
	var out []subro.Adjustment
	for _, p := range []subro.Party{subro.PartyInsured, subro.PartyInsurer, subro.PartyThirdParty} {
		delta := ent[p] - c.paid[p]
		if delta == 0 {
			continue
		}
		out = append(out, subro.Adjustment{
			Seq:       m.seq,
			CaseID:    id,
			Op:        op,
			Party:     p,
			Delta:     delta,
			PaidAfter: ent[p],
		})
		m.seq++
		c.paid[p] = ent[p]
	}
	m.log = append(m.log, out...)
	return out
}

func (m *Model) getCase(op subro.OpKind, id string) (*caseData, error) {
	c, ok := m.cases[id]
	if !ok {
		return nil, reject(subro.ErrCaseNotFound, op, id, "no such case")
	}
	return c, nil
}

func (m *Model) Recover(now int64, id string, gross, expense int64) ([]subro.Adjustment, error) {
	op := subro.OpRecover
	if now < 0 || gross < 0 || expense < 0 || expense > gross {
		return nil, reject(subro.ErrInvalidParam, op, id, "require 0 <= expense <= gross")
	}
	if err := m.checkClock(now, op, id); err != nil {
		return nil, err
	}
	c, err := m.getCase(op, id)
	if err != nil {
		return nil, err
	}
	if now > c.deadline {
		return nil, reject(subro.ErrPastDeadline, op, id, "recovery after deadline")
	}
	c.history = append(c.history, recovery{gross: gross, expense: expense, at: now})
	adjs := m.settle(id, c, op)
	m.accept(now)
	return adjs, nil
}

func (m *Model) AdjustRatio(now int64, id string, ratioBP int64) ([]subro.Adjustment, error) {
	op := subro.OpAdjustRate
	if now < 0 || ratioBP < 0 || ratioBP > 10000 {
		return nil, reject(subro.ErrInvalidParam, op, id, "ratio outside [0, 10000] basis points")
	}
	if err := m.checkClock(now, op, id); err != nil {
		return nil, err
	}
	c, err := m.getCase(op, id)
	if err != nil {
		return nil, err
	}
	c.ratioBP = ratioBP
	adjs := m.settle(id, c, op)
	m.accept(now)
	return adjs, nil
}

func (m *Model) SupplementPayment(now int64, id string, amount int64) ([]subro.Adjustment, error) {
	op := subro.OpSupplement
	if now < 0 || amount <= 0 {
		return nil, reject(subro.ErrInvalidParam, op, id, "non-positive amount")
	}
	if err := m.checkClock(now, op, id); err != nil {
		return nil, err
	}
	c, err := m.getCase(op, id)
	if err != nil {
		return nil, err
	}
	if c.insurerPaid+amount > c.totalLoss {
		return nil, reject(subro.ErrExceedsTotalLoss, op, id, "insurer paid would exceed total loss")
	}
	c.insurerPaid += amount
	adjs := m.settle(id, c, op)
	m.accept(now)
	return adjs, nil
}

func (m *Model) Waive(now int64, id string) ([]subro.Adjustment, error) {
	op := subro.OpWaive
	if now < 0 {
		return nil, reject(subro.ErrInvalidParam, op, id, "negative now")
	}
	if err := m.checkClock(now, op, id); err != nil {
		return nil, err
	}
	c, err := m.getCase(op, id)
	if err != nil {
		return nil, err
	}
	if now > c.deadline {
		return nil, reject(subro.ErrPastDeadline, op, id, "waiver after deadline")
	}
	if c.waived {
		return nil, reject(subro.ErrAlreadyWaived, op, id, "waiver already declared")
	}
	c.waived = true
	adjs := m.settle(id, c, op)
	m.accept(now)
	return adjs, nil
}

// Snapshot recomputes the full case view from history.
func (m *Model) Snapshot(id string) (subro.CaseSnapshot, error) {
	c, ok := m.cases[id]
	if !ok {
		return subro.CaseSnapshot{}, reject(subro.ErrCaseNotFound, "snapshot", id, "no such case")
	}
	ent := c.entitled()
	var gross, expense int64
	for _, r := range c.history {
		gross += r.gross
		expense += r.expense
	}
	return subro.CaseSnapshot{
		ID:            id,
		TotalLoss:     c.totalLoss,
		InsurerPaid:   c.insurerPaid,
		Uncompensated: c.totalLoss - c.insurerPaid,
		RatioBP:       c.ratioBP,
		Deadline:      c.deadline,
		Waived:        c.waived,
		GrossTotal:    gross,
		ExpenseTotal:  expense,
		NetTotal:      gross - expense,
		Cap:           c.totalLoss * c.ratioBP / 10000,
		Entitled: subro.Entitlement{
			Insured:    ent[subro.PartyInsured],
			Insurer:    ent[subro.PartyInsurer],
			ThirdParty: ent[subro.PartyThirdParty],
		},
		Paid: subro.Entitlement{
			Insured:    c.paid[subro.PartyInsured],
			Insurer:    c.paid[subro.PartyInsurer],
			ThirdParty: c.paid[subro.PartyThirdParty],
		},
		Recoveries: len(c.history),
	}, nil
}

// Adjustments returns the full adjustment log of a case.
func (m *Model) Adjustments(id string) []subro.Adjustment {
	var out []subro.Adjustment
	for _, a := range m.log {
		if a.CaseID == id {
			out = append(out, a)
		}
	}
	return out
}

func (m *Model) String() string {
	return fmt.Sprintf("naivemodel.Model{cases:%d adjustments:%d}", len(m.cases), len(m.log))
}
