package locker

import (
	"errors"
	"fmt"
)

// naiveParcel 是朴素模型中的快件；刻意与生产代码结构独立。
type naiveParcel struct {
	tracking  string
	size      Size
	tail      string
	cell      int
	code      int
	deposited int64
	paid      int64
	fails     int
	locked    bool
}

// NaiveModel 用线性扫描重新实现全部业务规则，供差分对照。
// 格口选择扫全部格口，取件码选择扫 1..N——与生产实现的位图/堆无关。
type NaiveModel struct {
	cfg     Config
	cells   []Cell
	busy    map[int]bool
	parcels map[string]*naiveParcel
	codeFor map[int]*naiveParcel
	cooling map[int]int64 // code -> 可再用时刻
	clock   int64
}

func NewNaiveModel(cells []Cell, cfg Config) *NaiveModel {
	return &NaiveModel{
		cfg:     cfg,
		cells:   append([]Cell(nil), cells...),
		busy:    map[int]bool{},
		parcels: map[string]*naiveParcel{},
		codeFor: map[int]*naiveParcel{},
		cooling: map[int]int64{},
	}
}

func (m *NaiveModel) tailOK(phone Phone) (string, bool) { return phone.lastFour() }

type naiveResult struct {
	code int
	cell int
}

func (m *NaiveModel) fee(p *naiveParcel, now int64) int64 {
	return storageFee(m.cfg, p.deposited, now)
}

func (m *NaiveModel) pickCode(now int64) (int, bool) {
	for c, at := range m.cooling {
		if at <= now {
			delete(m.cooling, c)
		}
	}
	for c := 1; c <= m.cfg.CodeCount; c++ {
		if m.codeFor[c] == nil {
			if _, cooling := m.cooling[c]; !cooling {
				return c, true
			}
		}
	}
	return 0, false
}

func (m *NaiveModel) pickCell(size Size) (int, bool) {
	best := -1
	for _, cell := range m.cells {
		if cell.Size < size || m.busy[int(cell.ID)] {
			continue
		}
		bestSize := Size(255)
		if best != -1 {
			bestSize = m.cells[cellIndexOf(m.cells, best)].Size
		}
		if best == -1 || cell.Size < bestSize ||
			(cell.Size == bestSize && int(cell.ID) < best) {
			best = int(cell.ID)
		}
	}
	if best == -1 {
		return 0, false
	}
	return best, true
}

func cellIndexOf(cells []Cell, id int) int {
	for i := range cells {
		if int(cells[i].ID) == id {
			return i
		}
	}
	return -1
}

func (m *NaiveModel) fittingExists(size Size) bool {
	for _, cell := range m.cells {
		if cell.Size >= size {
			return true
		}
	}
	return false
}

func (m *NaiveModel) remove(p *naiveParcel, now int64) {
	delete(m.parcels, p.tracking)
	delete(m.codeFor, p.code)
	delete(m.busy, p.cell)
	m.cooling[p.code] = now + m.cfg.CodeCooldown
}

func (m *NaiveModel) Deposit(t int64, tracking string, size Size, phone Phone) (naiveResult, error) {
	tail, ok := m.tailOK(phone)
	if t < 0 || tracking == "" || !size.valid() || !ok {
		return naiveResult{}, ErrInvalidParam
	}
	if t < m.clock {
		return naiveResult{}, ErrClockRollback
	}
	if m.parcels[tracking] != nil {
		return naiveResult{}, ErrDuplicateTracking
	}
	if !m.fittingExists(size) {
		return naiveResult{}, ErrNoFittingCell
	}
	cellID, found := m.pickCell(size)
	if !found {
		return naiveResult{}, ErrAllFittingBusy
	}
	code, hasCode := m.pickCode(t)
	if !hasCode {
		return naiveResult{}, ErrNoCodeAvailable
	}
	p := &naiveParcel{
		tracking: tracking, size: size, tail: tail,
		cell: cellID, code: code, deposited: t,
	}
	m.parcels[tracking] = p
	m.codeFor[code] = p
	m.busy[cellID] = true
	m.clock = t
	return naiveResult{code: code, cell: cellID}, nil
}

func (m *NaiveModel) Pickup(t int64, code int, phone Phone) (string, error) {
	tail, ok := m.tailOK(phone)
	if t < 0 || code <= 0 || !ok {
		return "", ErrInvalidParam
	}
	if t < m.clock {
		return "", ErrClockRollback
	}
	p := m.codeFor[code]
	if p == nil {
		return "", ErrCodeNotFound
	}
	if p.locked {
		return "", ErrParcelLocked
	}
	if tail != p.tail {
		p.fails++
		if p.fails >= 3 {
			p.locked = true
		}
		return "", ErrPhoneMismatch
	}
	if t-p.deposited >= m.cfg.MaxStorage {
		return "", ErrTimedOut
	}
	due := m.fee(p, t) - p.paid
	if due > 0 {
		return "", ErrUnpaidFee
	}
	tracking := p.tracking
	m.remove(p, t)
	m.clock = t
	return tracking, nil
}

func (m *NaiveModel) Pay(t int64, tracking string, amount int64) error {
	if t < 0 || tracking == "" || amount <= 0 {
		return ErrInvalidParam
	}
	if t < m.clock {
		return ErrClockRollback
	}
	p := m.parcels[tracking]
	if p == nil {
		return ErrTrackingNotFound
	}
	p.paid += amount
	m.clock = t
	return nil
}

func (m *NaiveModel) Recycle(t int64, tracking string) error {
	if t < 0 || tracking == "" {
		return ErrInvalidParam
	}
	if t < m.clock {
		return ErrClockRollback
	}
	p := m.parcels[tracking]
	if p == nil {
		return ErrTrackingNotFound
	}
	if t-p.deposited < m.cfg.MaxStorage {
		return ErrNotTimedOut
	}
	m.remove(p, t)
	m.clock = t
	return nil
}

func (m *NaiveModel) Unlock(t int64, tracking string) error {
	if t < 0 || tracking == "" {
		return ErrInvalidParam
	}
	if t < m.clock {
		return ErrClockRollback
	}
	p := m.parcels[tracking]
	if p == nil {
		return ErrTrackingNotFound
	}
	p.locked = false
	p.fails = 0
	m.clock = t
	return nil
}

func sameErr(a, b error) bool {
	return errors.Is(a, b) || (a == nil && b == nil)
}

func describeOp(kind int, t int64, tr string, size Size, phone string, code, amount int) string {
	switch kind {
	case opDeposit:
		return fmt.Sprintf("Deposit(t=%d tr=%s size=%d phone=%s)", t, tr, size, phone)
	case opPickup:
		return fmt.Sprintf("Pickup(t=%d code=%d phone=%s)", t, code, phone)
	case opPay:
		return fmt.Sprintf("Pay(t=%d tr=%s amount=%d)", t, tr, amount)
	case opRecycle:
		return fmt.Sprintf("Recycle(t=%d tr=%s)", t, tr)
	case opUnlock:
		return fmt.Sprintf("Unlock(t=%d tr=%s)", t, tr)
	}
	return "?"
}
