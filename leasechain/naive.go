package leasechain

import (
	"sort"
	"time"
)

// naiveLease 为朴素参照模型中的租约：纯切片、线性扫描、无指针优化。
type naiveLease struct {
	id         int64
	landlordID string
	tenantID   string
	parentID   int64
	start, end int
	rent       int64
	recognized bool
	terminated bool
}

type naiveArrear struct {
	id, leaseID  int64
	due          int
	amount, paid int64
}

type naiveRecourse struct {
	id, arrearID, payerID int64
	amount                int64
}

type naiveOneTime struct {
	id               int64
	landlord, tenant string
	parentID         int64
	start, end       int
	rent             int64
	used             bool
}

type naiveGeneral struct {
	landlord, tenant string
	revoked          bool
}

type naiveBill struct {
	leaseID  int64
	due      int
	amount   int64
	arrearID int64
}

// NaiveModel 是独立编写的朴素参照实现：重放操作日志得到相同的链、欠费与追偿。
type NaiveModel struct {
	cfg       Config
	lastNow   int
	nextID    int64
	leases    []naiveLease
	arrears   []naiveArrear
	recourses []naiveRecourse
	oneTimes  []naiveOneTime
	generals  []naiveGeneral
	bills     []naiveBill
}

// NewNaive 创建朴素模型。
func NewNaive(cfg Config) *NaiveModel { return &NaiveModel{cfg: cfg} }

// opKind 为操作日志的类型枚举。
type opKind int

const (
	opMaster opKind = iota
	opGrantGeneral
	opRevokeGeneral
	opGrantOneTime
	opSublease
	opRecognize
	opTerminate
	opExit
	opPay
	opAdvance
)

// Op 为一条确定性操作日志。
type Op struct {
	Kind opKind
	Now  int
	ID   int64
	I    int64
	A, B string
	X, Y int
	R    int64
}

func (m *NaiveModel) lease(id int64) *naiveLease {
	for i := range m.leases {
		if m.leases[i].id == id {
			return &m.leases[i]
		}
	}
	return nil
}

func (m *NaiveModel) activeChild(parentID int64) *naiveLease {
	for i := range m.leases {
		l := &m.leases[i]
		if !l.terminated && l.parentID == parentID {
			return l
		}
	}
	return nil
}

func (m *NaiveModel) depth(l *naiveLease) int {
	d := 0
	for l.parentID != 0 {
		l = m.lease(l.parentID)
		d++
	}
	return d
}

func (m *NaiveModel) ancestors(id int64) []int64 {
	var out []int64
	for id != 0 {
		l := m.lease(id)
		if l == nil {
			break
		}
		out = append(out, id)
		id = l.parentID
	}
	return out
}

// naiveDueList 独立实现月账期枚举，刻意不调用 installments。
func naiveDueList(start, end, payDay int, rent int64) []naiveBill {
	if end <= start || rent <= 0 {
		return nil
	}
	t0 := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	first := t0.AddDate(0, 0, start)
	y, mo := first.Year(), int(first.Month())
	var out []naiveBill
	for {
		dt := time.Date(y, time.Month(mo), payDay, 0, 0, 0, 0, time.UTC)
		due := int(dt.Sub(t0).Hours() / 24)
		if due < start {
			due = start
		}
		if due >= end {
			break
		}
		out = append(out, naiveBill{due: due, amount: rent})
		nxt := time.Date(y, time.Month(mo), 1, 0, 0, 0, 0, time.UTC).AddDate(0, 1, 0)
		y, mo = nxt.Year(), int(nxt.Month())
	}
	return out
}

func (m *NaiveModel) settle(now int) {
	for i := range m.leases {
		l := &m.leases[i]
		for _, b := range naiveDueList(l.start, l.end, m.cfg.PayDay, l.rent) {
			if b.due > now {
				continue
			}
			exists := false
			for _, x := range m.bills {
				if x.leaseID == l.id && x.due == b.due {
					exists = true
					break
				}
			}
			if !exists {
				b.leaseID = l.id
				m.bills = append(m.bills, b)
			}
		}
	}
	type pr struct {
		leaseID int64
		due     int
		amount  int64
	}
	var pend []pr
	for _, b := range m.bills {
		if now < b.due+m.cfg.G || b.arrearID != 0 {
			continue
		}
		pend = append(pend, pr{b.leaseID, b.due, b.amount})
	}
	sort.Slice(pend, func(i, j int) bool {
		if pend[i].due != pend[j].due {
			return pend[i].due < pend[j].due
		}
		return pend[i].leaseID < pend[j].leaseID
	})
	for _, p := range pend {
		m.nextID++
		m.arrears = append(m.arrears, naiveArrear{
			id: m.nextID, leaseID: p.leaseID, due: p.due, amount: p.amount,
		})
		for i := range m.bills {
			if m.bills[i].leaseID == p.leaseID && m.bills[i].due == p.due {
				m.bills[i].arrearID = m.nextID
			}
		}
	}
	for {
		var cand *naiveLease
		for i := range m.leases {
			l := &m.leases[i]
			if l.terminated || l.end > now {
				continue
			}
			if cand == nil || l.end < cand.end || (l.end == cand.end && l.id < cand.id) {
				cand = l
			}
		}
		if cand == nil {
			return
		}
		m.cascade(cand, cand.end)
	}
}

func (m *NaiveModel) cascade(l *naiveLease, termDate int) {
	l.terminated = true
	l.end = termDate
	child := m.activeChild(l.id)
	if child == nil {
		return
	}
	if child.recognized {
		child.parentID = l.parentID
		return
	}
	m.cascade(child, termDate)
}

// acceptOp 在全部校验通过后推进时钟并执行时间驱动结算。
func (m *NaiveModel) acceptOp(op Op) {
	if op.Now > m.lastNow {
		m.lastNow = op.Now
	}
	m.settle(op.Now)
}

// Snapshot 为朴素模型的可比较全状态（拒绝回滚与模型对照均使用它）。
type Snapshot struct {
	LastNow   int
	NextID    int64
	Leases    []naiveLease
	Arrears   []naiveArrear
	Recourses []naiveRecourse
	OneTimes  []naiveOneTime
	Generals  []naiveGeneral
	Bills     []naiveBill
}

func (m *NaiveModel) snapshotForCopy() Snapshot {
	cp := Snapshot{LastNow: m.lastNow, NextID: m.nextID}
	cp.Leases = append([]naiveLease(nil), m.leases...)
	cp.Arrears = append([]naiveArrear(nil), m.arrears...)
	cp.Recourses = append([]naiveRecourse(nil), m.recourses...)
	cp.OneTimes = append([]naiveOneTime(nil), m.oneTimes...)
	cp.Generals = append([]naiveGeneral(nil), m.generals...)
	cp.Bills = append([]naiveBill(nil), m.bills...)
	return cp
}

func (m *NaiveModel) restore(cp Snapshot) {
	m.lastNow, m.nextID = cp.LastNow, cp.NextID
	m.leases = cp.Leases
	m.arrears = cp.Arrears
	m.recourses = cp.Recourses
	m.oneTimes = cp.OneTimes
	m.generals = cp.Generals
	m.bills = cp.Bills
}

// State 返回排序后的可比较状态（对外用于与正式实现对照）。
func (m *NaiveModel) State() Snapshot {
	cp := m.snapshotForCopy()
	sort.Slice(cp.Leases, func(i, j int) bool { return cp.Leases[i].id < cp.Leases[j].id })
	sort.Slice(cp.Arrears, func(i, j int) bool { return cp.Arrears[i].id < cp.Arrears[j].id })
	sort.Slice(cp.Recourses, func(i, j int) bool { return cp.Recourses[i].id < cp.Recourses[j].id })
	sort.Slice(cp.OneTimes, func(i, j int) bool { return cp.OneTimes[i].id < cp.OneTimes[j].id })
	sort.Slice(cp.Generals, func(i, j int) bool {
		if cp.Generals[i].landlord != cp.Generals[j].landlord {
			return cp.Generals[i].landlord < cp.Generals[j].landlord
		}
		return cp.Generals[i].tenant < cp.Generals[j].tenant
	})
	sort.Slice(cp.Bills, func(i, j int) bool {
		if cp.Bills[i].leaseID != cp.Bills[j].leaseID {
			return cp.Bills[i].leaseID < cp.Bills[j].leaseID
		}
		return cp.Bills[i].due < cp.Bills[j].due
	})
	return cp
}

// Apply 重放一条操作；任何拒绝都通过快照回滚保证不留痕。
func (m *NaiveModel) Apply(op Op) error {
	saved := m.snapshotForCopy()
	if err := m.applyInner(op); err != nil {
		m.restore(saved)
		return err
	}
	return nil
}

func (m *NaiveModel) applyInner(op Op) error {
	switch op.Kind {
	case opMaster:
		if op.A == "" || op.B == "" || op.X >= op.Y || op.R <= 0 {
			return ErrInvalidParam
		}
	case opGrantGeneral, opRevokeGeneral:
		if op.A == "" || op.B == "" {
			return ErrInvalidParam
		}
	case opGrantOneTime, opSublease:
		if op.B == "" || op.I <= 0 || op.X >= op.Y || op.R <= 0 {
			return ErrInvalidParam
		}
		if op.Kind == opGrantOneTime && op.A == "" {
			return ErrInvalidParam
		}
	case opRecognize, opTerminate, opExit:
		if op.I <= 0 || op.A == "" {
			return ErrInvalidParam
		}
	case opPay:
		if op.I <= 0 || op.ID <= 0 || op.R <= 0 {
			return ErrInvalidParam
		}
	}
	if op.Now < m.lastNow {
		return ErrClockRollback
	}
	switch op.Kind {
	case opAdvance:
		m.acceptOp(op)
		return nil
	case opMaster:
		m.acceptOp(op)
		m.nextID++
		m.leases = append(m.leases, naiveLease{
			id: m.nextID, landlordID: op.A, tenantID: op.B,
			start: op.X, end: op.Y, rent: op.R,
		})
	case opGrantGeneral:
		m.acceptOp(op)
		found := false
		for i := range m.generals {
			if m.generals[i].landlord == op.A && m.generals[i].tenant == op.B {
				m.generals[i].revoked = false
				found = true
			}
		}
		if !found {
			m.generals = append(m.generals, naiveGeneral{landlord: op.A, tenant: op.B})
		}
	case opRevokeGeneral:
		m.acceptOp(op)
		for i := range m.generals {
			g := &m.generals[i]
			if g.landlord == op.A && g.tenant == op.B {
				g.revoked = true
			}
		}
	case opGrantOneTime:
		p := m.lease(op.I)
		if p == nil || p.terminated {
			return ErrLeaseUnavailable
		}
		if op.A != p.landlordID {
			return ErrIllegalState
		}
		m.acceptOp(op)
		p = m.lease(op.I)
		if p == nil || p.terminated {
			return ErrLeaseUnavailable
		}
		m.nextID++
		m.oneTimes = append(m.oneTimes, naiveOneTime{
			id: m.nextID, landlord: op.A, tenant: op.B, parentID: op.I,
			start: op.X, end: op.Y, rent: op.R,
		})
	}
	return m.applyChainOps(op)
}

func (m *NaiveModel) applyChainOps(op Op) error {
	switch op.Kind {
	case opSublease:
		parent := m.lease(op.I)
		if parent == nil || parent.terminated {
			return ErrLeaseUnavailable
		}
		if op.B == parent.tenantID {
			return ErrIllegalState
		}
		hasOne := false
		for _, c := range m.oneTimes {
			if !c.used && c.landlord == parent.landlordID && c.tenant == op.B &&
				c.parentID == parent.id && c.start == op.X && c.end == op.Y && c.rent == op.R {
				hasOne = true
			}
		}
		hasGeneral := false
		for _, g := range m.generals {
			if g.landlord == parent.landlordID && g.tenant == op.B && !g.revoked {
				hasGeneral = true
			}
		}
		if !hasOne && !hasGeneral {
			return ErrNoConsent
		}
		if op.X < parent.start || op.Y > parent.end {
			return ErrTermOutOfRange
		}
		if op.R*100 > parent.rent*int64(m.cfg.P) {
			return ErrRentTooHigh
		}
		if m.depth(parent)+1 > m.cfg.D {
			return ErrDepthExceeded
		}
		if m.activeChild(parent.id) != nil {
			return ErrIllegalState
		}
		m.acceptOp(op)
		parent = m.lease(op.I)
		if parent == nil || parent.terminated || m.activeChild(parent.id) != nil {
			return ErrIllegalState
		}
		m.nextID++
		m.leases = append(m.leases, naiveLease{
			id: m.nextID, landlordID: parent.landlordID, tenantID: op.B,
			parentID: parent.id, start: op.X, end: op.Y, rent: op.R,
		})
		for i := range m.oneTimes {
			c := &m.oneTimes[i]
			if !c.used && c.landlord == parent.landlordID && c.tenant == op.B &&
				c.parentID == parent.id && c.start == op.X && c.end == op.Y && c.rent == op.R {
				c.used = true
			}
		}
	case opRecognize:
		l := m.lease(op.I)
		if l == nil || l.terminated {
			return ErrLeaseUnavailable
		}
		if l.landlordID != op.A || l.parentID == 0 || l.recognized {
			return ErrIllegalState
		}
		m.acceptOp(op)
		l = m.lease(op.I)
		if l == nil || l.terminated || l.parentID == 0 || l.recognized {
			return ErrIllegalState
		}
		l.recognized = true
	case opTerminate:
		l := m.lease(op.I)
		if l == nil || l.terminated {
			return ErrLeaseUnavailable
		}
		if op.A != l.tenantID && op.A != l.landlordID {
			return ErrIllegalState
		}
		m.acceptOp(op)
		l = m.lease(op.I)
		if l == nil || l.terminated {
			return ErrLeaseUnavailable
		}
		m.cascade(l, op.Now)
	case opExit:
		l := m.lease(op.I)
		if l == nil || l.terminated {
			return ErrLeaseUnavailable
		}
		if op.A != l.tenantID {
			return ErrIllegalState
		}
		if c := m.activeChild(l.id); c != nil && !c.recognized {
			return ErrIllegalState
		}
		m.acceptOp(op)
		l = m.lease(op.I)
		if l == nil || l.terminated {
			return ErrLeaseUnavailable
		}
		if c := m.activeChild(l.id); c != nil && !c.recognized {
			return ErrIllegalState
		}
		m.cascade(l, op.Now)
	case opPay:
		var ar *naiveArrear
		for i := range m.arrears {
			if m.arrears[i].id == op.I {
				ar = &m.arrears[i]
			}
		}
		payer := m.lease(op.ID)
		if ar == nil || payer == nil || m.lease(ar.leaseID) == nil {
			return ErrLeaseUnavailable
		}
		ok := false
		for _, id := range m.ancestors(ar.leaseID) {
			if id == payer.id {
				ok = true
			}
		}
		if !ok {
			return ErrIllegalState
		}
		if ar.paid >= ar.amount {
			return ErrIllegalState
		}
		if ar.paid+op.R > ar.amount {
			return ErrIllegalState
		}
		m.acceptOp(op)
		for i := range m.arrears {
			if m.arrears[i].id == op.I {
				ar = &m.arrears[i]
			}
		}
		if ar.paid >= ar.amount || ar.paid+op.R > ar.amount {
			return ErrIllegalState
		}
		ar.paid += op.R
		m.nextID++
		m.recourses = append(m.recourses, naiveRecourse{
			id: m.nextID, arrearID: ar.id, payerID: payer.id, amount: op.R,
		})
	}
	return nil
}
