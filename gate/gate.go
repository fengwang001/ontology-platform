package gate

import (
	"errors"
	"sync"

	"ontology/exposure"
	"ontology/limit"
)

type Side = limit.Side

type Offset int

const (
	Open Offset = iota + 1
	Close
)

const (
	Long  = limit.Long
	Short = limit.Short
)

var (
	ErrInvalidArgument  = errors.New("invalid argument")
	ErrClockRollback    = errors.New("clock rollback")
	ErrNotFound         = errors.New("not found")
	ErrDuplicateAccount = errors.New("duplicate account")
	ErrDuplicateOrder   = errors.New("duplicate order")
	ErrInvalidState     = errors.New("invalid state")
	ErrCloseOverLimit   = errors.New("close quantity exceeds available position")
	ErrAccountLimit     = errors.New("account position limit exceeded")
	ErrGroupLimit       = errors.New("group position limit exceeded")
	ErrDayOpenLimit     = errors.New("day opening limit exceeded")
)

type order struct {
	acct   string
	sym    string
	side   Side
	offset Offset
	remain int64
	closed bool
}

type Gateway struct {
	mu       sync.RWMutex
	now      int64
	registry *limit.Registry
	tracker  *exposure.Tracker
	orders   map[string]*order
	touched  int
}

func New() *Gateway {
	registry := limit.NewRegistry()
	return &Gateway{
		registry: registry,
		tracker:  exposure.NewTracker(registry),
		orders:   make(map[string]*order),
	}
}

func (g *Gateway) Register(now int64, acct, group []byte) error {
	if err := validateNow(now); err != nil {
		return err
	}
	if !validID(acct) || !validID(group) {
		return ErrInvalidArgument
	}
	account := string(acct)
	groupName := string(group)

	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.checkClock(now); err != nil {
		return err
	}
	if err := g.registry.Register(account, groupName); err != nil {
		if errors.Is(err, limit.ErrDuplicateAccount) {
			return ErrDuplicateAccount
		}
		return err
	}
	g.tracker.Register(account)
	g.now = now
	return nil
}

func (g *Gateway) SetLimit(now int64, sym []byte, account, group, day int64) error {
	if err := validateNow(now); err != nil {
		return err
	}
	if !validID(sym) || !validLimit(account) || !validLimit(group) || !validLimit(day) {
		return ErrInvalidArgument
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.checkClock(now); err != nil {
		return err
	}
	g.touched = 0
	if err := g.registry.Set(string(sym), account, group, day); err != nil {
		return err
	}
	g.now = now
	return nil
}

func (g *Gateway) SetHedge(now int64, acct, sym []byte, side Side, hedge int64) error {
	if err := validateNow(now); err != nil {
		return err
	}
	if !validID(acct) || !validID(sym) || !validSide(side) || !validLimit(hedge) {
		return ErrInvalidArgument
	}
	account := string(acct)
	symbol := string(sym)

	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.checkClock(now); err != nil {
		return err
	}
	if _, ok := g.registry.Group(account); !ok {
		return ErrNotFound
	}
	g.touched = 0
	g.tracker.SetHedge(account, symbol, side, hedge)
	g.touched++
	g.now = now
	return nil
}

func (g *Gateway) Order(now int64, oid, acct, sym []byte, side Side, offset Offset, qty int64) error {
	if err := validateNow(now); err != nil {
		return err
	}
	if !validID(oid) || !validID(acct) || !validID(sym) || !validSide(side) || !validOffset(offset) || !validQty(qty) {
		return ErrInvalidArgument
	}
	id := string(oid)
	account := string(acct)
	symbol := string(sym)

	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.checkClock(now); err != nil {
		return err
	}
	groupName, ok := g.registry.Group(account)
	if !ok {
		return ErrNotFound
	}
	accountLimit, groupLimit, dayLimit, symbolOK := g.registry.Limits(symbol)
	if !symbolOK {
		return ErrNotFound
	}
	if _, ok := g.orders[id]; ok {
		return ErrDuplicateOrder
	}

	g.touched = 0
	if offset == Close {
		if qty > g.tracker.CloseAvailable(account, symbol, side) {
			return ErrCloseOverLimit
		}
	} else {
		exposureValue := g.tracker.Exposure(account, symbol, side)
		hedge := g.registry.Hedge(account, symbol, side)
		if exposureValue+qty > accountLimit+hedge {
			return ErrAccountLimit
		}
		groupExposure := g.tracker.GroupExposure(groupName, symbol, side)
		contribution := exposureValue - hedge
		if contribution < 0 {
			contribution = 0
		}
		newContribution := exposureValue + qty - hedge
		if newContribution < 0 {
			newContribution = 0
		}
		if groupExposure-contribution+newContribution > groupLimit {
			return ErrGroupLimit
		}
		if g.tracker.DayOpen(account, symbol)+qty > dayLimit {
			return ErrDayOpenLimit
		}
	}

	g.orders[id] = &order{acct: account, sym: symbol, side: side, offset: offset, remain: qty}
	if offset == Open {
		g.tracker.SubmitOpen(account, symbol, side, qty)
	} else {
		g.tracker.SubmitClose(account, symbol, side, qty)
	}
	g.touched++
	g.now = now
	return nil
}

func (g *Gateway) Fill(now int64, oid []byte, qty int64) error {
	if err := validateNow(now); err != nil {
		return err
	}
	if !validID(oid) || !validQty(qty) {
		return ErrInvalidArgument
	}
	id := string(oid)

	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.checkClock(now); err != nil {
		return err
	}
	entry, ok := g.orders[id]
	if !ok {
		return ErrNotFound
	}
	if entry.closed || entry.remain == 0 {
		return ErrInvalidState
	}
	if qty > entry.remain {
		return ErrInvalidState
	}

	g.touched = 0
	if entry.offset == Open {
		g.tracker.FillOpen(entry.acct, entry.sym, entry.side, qty)
	} else {
		g.tracker.FillClose(entry.acct, entry.sym, entry.side, qty)
	}
	g.touched++
	entry.remain -= qty
	if entry.remain == 0 {
		entry.closed = true
	}
	g.now = now
	return nil
}

func (g *Gateway) Cancel(now int64, oid []byte) error {
	if err := validateNow(now); err != nil {
		return err
	}
	if !validID(oid) {
		return ErrInvalidArgument
	}
	id := string(oid)

	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.checkClock(now); err != nil {
		return err
	}
	entry, ok := g.orders[id]
	if !ok {
		return ErrNotFound
	}
	if entry.closed || entry.remain == 0 {
		return ErrInvalidState
	}

	g.touched = 0
	if entry.offset == Open {
		g.tracker.ReleaseOpen(entry.acct, entry.sym, entry.side, entry.remain)
	} else {
		g.tracker.ReleaseClose(entry.acct, entry.sym, entry.side, entry.remain)
	}
	g.touched++
	entry.remain = 0
	entry.closed = true
	g.now = now
	return nil
}

func (g *Gateway) ResetDay(now int64) error {
	if err := validateNow(now); err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.checkClock(now); err != nil {
		return err
	}
	g.tracker.ResetDay()
	g.now = now
	return nil
}

func (g *Gateway) Position(acct, sym []byte, side Side) int64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.tracker.Position(string(acct), string(sym), side)
}

func (g *Gateway) PendingOpen(acct, sym []byte, side Side) int64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.tracker.PendingOpen(string(acct), string(sym), side)
}

func (g *Gateway) PendingClose(acct, sym []byte, side Side) int64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.tracker.PendingClose(string(acct), string(sym), side)
}

func (g *Gateway) Exposure(acct, sym []byte, side Side) int64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.tracker.Exposure(string(acct), string(sym), side)
}

func (g *Gateway) DayOpen(acct, sym []byte) int64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.tracker.DayOpen(string(acct), string(sym))
}

func (g *Gateway) GroupExposure(group, sym []byte, side Side) int64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.tracker.GroupExposure(string(group), string(sym), side)
}

func (g *Gateway) checkClock(now int64) error {
	if now < g.now {
		return ErrClockRollback
	}
	return nil
}

func validateNow(now int64) error {
	if now < 0 || now > 1_000_000_000_000 {
		return ErrInvalidArgument
	}
	return nil
}

func validID(value []byte) bool {
	return len(value) > 0
}

func validQty(value int64) bool {
	return value >= 1 && value <= 1_000_000_000
}

func validLimit(value int64) bool {
	return value >= 0 && value <= 1_000_000_000
}

func validSide(side Side) bool {
	return side == Long || side == Short
}

func validOffset(offset Offset) bool {
	return offset == Open || offset == Close
}
