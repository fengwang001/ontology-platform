package undo

import (
	"errors"
	"sync"
)

var (
	ErrInvalidArgument     = errors.New("invalid argument")
	ErrTransactionExists   = errors.New("transaction already exists")
	ErrTransactionNotFound = errors.New("transaction not found")
	ErrTransactionDone     = errors.New("transaction already terminated")
	ErrNoFreeSlot          = errors.New("no free rollback segment slot")
	ErrPageBudgetExceeded  = errors.New("page budget exceeded")
	ErrViewNotFound        = errors.New("read view not found")
)

type Manager struct {
	mu           sync.Mutex
	slots        int
	pageSize     int
	pageBudget   int
	usedSlots    int
	usedPages    int
	slotUsed     []bool
	caches       [2][]*segment
	transactions map[int]*transaction
	history      []*historyEntry
	commits      int
	views        map[int]int
	nextViewID   int
}

type segmentKind int

const (
	insertUndo segmentKind = iota
	updateUndo
)

type segment struct {
	kind    segmentKind
	slot    int
	records int
	pages   int
}

type transaction struct {
	active   bool
	segments [2]*segment
}

type historyEntry struct {
	trxNo   int
	segment *segment
}

func NewManager(slots int, recordsPerPage int, pageBudget int) (*Manager, error) {
	if slots < 1 || slots > 1000 ||
		recordsPerPage < 1 || recordsPerPage > 1000 ||
		pageBudget < 1 || pageBudget > 1_000_000 {
		return nil, ErrInvalidArgument
	}
	return &Manager{
		slots:        slots,
		pageSize:     recordsPerPage,
		pageBudget:   pageBudget,
		slotUsed:     make([]bool, slots),
		transactions: make(map[int]*transaction),
		views:        make(map[int]int),
		nextViewID:   1,
	}, nil
}

func (m *Manager) Begin(transactionID int) error {
	if !validTransactionID(transactionID) {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.transactions[transactionID]; exists {
		return ErrTransactionExists
	}
	m.transactions[transactionID] = &transaction{active: true}
	return nil
}

func (m *Manager) Insert(t int) error {
	if !validTransactionID(t) {
		return ErrInvalidArgument
	}
	return m.record(t, insertUndo)
}

func (m *Manager) Modify(t int) error {
	if !validTransactionID(t) {
		return ErrInvalidArgument
	}
	return m.record(t, updateUndo)
}

func (m *Manager) Commit(t int) (int, error) {
	if !validTransactionID(t) {
		return 0, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	trx, err := m.activeTransactionLocked(t)
	if err != nil {
		return 0, err
	}

	m.commits++
	trxNo := m.commits
	if insert := trx.segments[insertUndo]; insert != nil {
		m.releaseSegmentLocked(insert)
		trx.segments[insertUndo] = nil
	}
	if update := trx.segments[updateUndo]; update != nil {
		m.history = append(m.history, &historyEntry{trxNo: trxNo, segment: update})
		trx.segments[updateUndo] = nil
	}
	trx.active = false
	return trxNo, nil
}

func (m *Manager) Rollback(t int) error {
	if !validTransactionID(t) {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	trx, err := m.activeTransactionLocked(t)
	if err != nil {
		return err
	}
	for _, seg := range trx.segments {
		if seg != nil {
			m.releaseSegmentLocked(seg)
		}
	}
	trx.segments = [2]*segment{}
	trx.active = false
	return nil
}

func (m *Manager) OpenView() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	viewID := m.nextViewID
	m.nextViewID++
	m.views[viewID] = m.commits + 1
	return viewID
}

func (m *Manager) CloseView(viewID int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.views[viewID]; !exists {
		return ErrViewNotFound
	}
	delete(m.views, viewID)
	return nil
}

func (m *Manager) Purge(limit int) ([]int, error) {
	if limit < 1 {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	purgeLimit := m.commits + 1
	for _, viewLimit := range m.views {
		if viewLimit < purgeLimit {
			purgeLimit = viewLimit
		}
	}

	recycled := make([]int, 0)
	for len(recycled) < limit && len(m.history) > 0 {
		entry := m.history[0]
		if entry.trxNo >= purgeLimit {
			break
		}
		m.releaseSegmentLocked(entry.segment)
		m.history = m.history[1:]
		recycled = append(recycled, entry.trxNo)
	}
	return recycled, nil
}

func (m *Manager) record(t int, kind segmentKind) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	trx, err := m.activeTransactionLocked(t)
	if err != nil {
		return err
	}

	seg := trx.segments[kind]
	if seg == nil {
		cache := m.caches[kind]
		if len(cache) > 0 {
			seg = cache[len(cache)-1]
			m.caches[kind] = cache[:len(cache)-1]
			seg.records = 0
		} else {
			seg, err = m.acquireSegmentLocked(kind)
			if err != nil {
				return err
			}
		}
		trx.segments[kind] = seg
	}

	if pagesFor(seg.records+1, m.pageSize) > seg.pages && m.usedPages == m.pageBudget {
		return ErrPageBudgetExceeded
	}

	m.appendRecordLocked(seg)
	return nil
}

func (m *Manager) activeTransactionLocked(t int) (*transaction, error) {
	trx := m.transactions[t]
	if trx == nil {
		return nil, ErrTransactionNotFound
	}
	if !trx.active {
		return nil, ErrTransactionDone
	}
	return trx, nil
}

func (m *Manager) acquireSegmentLocked(kind segmentKind) (*segment, error) {
	if m.usedSlots == m.slots {
		return nil, ErrNoFreeSlot
	}
	if m.usedPages == m.pageBudget {
		return nil, ErrPageBudgetExceeded
	}

	slot := 0
	for m.slotUsed[slot] {
		slot++
	}
	m.slotUsed[slot] = true
	m.usedSlots++
	m.usedPages++
	return &segment{kind: kind, slot: slot, pages: 1}, nil
}

func (m *Manager) appendRecordLocked(seg *segment) error {
	requiredPages := pagesFor(seg.records+1, m.pageSize)
	if requiredPages > seg.pages {
		if m.usedPages == m.pageBudget {
			return ErrPageBudgetExceeded
		}
		m.usedPages++
		seg.pages++
	}
	seg.records++
	return nil
}

func (m *Manager) releaseSegmentLocked(seg *segment) {
	if seg.pages == 1 && 4*seg.records <= 3*m.pageSize {
		m.caches[seg.kind] = append(m.caches[seg.kind], seg)
		return
	}

	m.usedPages -= seg.pages
	m.usedSlots--
	m.slotUsed[seg.slot] = false
}

func pagesFor(records int, pageSize int) int {
	if records == 0 {
		return 1
	}
	return (records + pageSize - 1) / pageSize
}

func validTransactionID(t int) bool {
	return t >= 1 && t <= 1_000_000
}
