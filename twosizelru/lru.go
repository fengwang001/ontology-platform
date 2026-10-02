package twosizelru

import (
	"sync"
)

type RejectReason string

const (
	RejectInvalidArgument RejectReason = "invalid argument"
	RejectClockRollback   RejectReason = "clock rollback"
	RejectPageNotFound    RejectReason = "page not found"
	RejectUnpinUnderflow  RejectReason = "unpin underflow"
	RejectAllPinned       RejectReason = "all pages pinned"
)

func (r RejectReason) Error() string {
	return string(r)
}

func (r RejectReason) Is(target error) bool {
	reason, ok := target.(RejectReason)
	return ok && reason == r
}

const (
	minPage = 0
	maxPage = 1_000_000
	maxNow  = 1_000_000_000_000_000
)

type Result struct {
	Hit         bool
	Read        bool
	Evicted     bool
	EvictedPage int
}

type node struct {
	page      int
	youngPrev *node
	youngNext *node
	oldPrev   *node
	oldNext   *node
	first     *int64
	pin       int
	inYoung   bool
}

type Manager struct {
	mu            sync.Mutex
	capacity      int
	oldPercent    int
	tolerance     int
	promotionStay int64
	maxNow        int64
	pages         map[int]*node
	youngHead     *node
	youngTail     *node
	oldHead       *node
	oldTail       *node
	youngLen      int
	oldLen        int
}

func New(capacity int, oldPercent int, tolerance int, promotionStay int64) (*Manager, error) {
	if capacity < 4 || capacity > 1_000_000 ||
		oldPercent < 5 || oldPercent > 95 ||
		tolerance < 0 || tolerance > capacity ||
		promotionStay < 0 || promotionStay > 1_000_000_000 {
		return nil, RejectInvalidArgument
	}

	return &Manager{
		capacity:      capacity,
		oldPercent:    oldPercent,
		tolerance:     tolerance,
		promotionStay: promotionStay,
		pages:         make(map[int]*node),
	}, nil
}

func (m *Manager) Access(page int, now int64) (Result, error) {
	if err := validatePage(page); err != nil {
		return Result{}, err
	}
	if err := validateNow(now); err != nil {
		return Result{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if now < m.maxNow {
		return Result{}, RejectClockRollback
	}

	result := Result{}
	if existing := m.pages[page]; existing != nil {
		m.accessExisting(existing, now)
		result.Hit = true
	} else {
		first := now
		readResult, err := m.readMissing(page, &first)
		if err != nil {
			return Result{}, err
		}
		result = readResult
		result.Read = true
	}

	m.maxNow = now
	return result, nil
}

func (m *Manager) Prefetch(page int, now int64) (Result, error) {
	if err := validatePage(page); err != nil {
		return Result{}, err
	}
	if err := validateNow(now); err != nil {
		return Result{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if now < m.maxNow {
		return Result{}, RejectClockRollback
	}

	result := Result{Hit: m.pages[page] != nil}
	if !result.Hit {
		readResult, err := m.readMissing(page, nil)
		if err != nil {
			return Result{}, err
		}
		result = readResult
		result.Read = true
	}

	m.maxNow = now
	return result, nil
}

func (m *Manager) Pin(page int) error {
	if err := validatePage(page); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	target := m.pages[page]
	if target == nil {
		return RejectPageNotFound
	}
	target.pin++
	return nil
}

func (m *Manager) Unpin(page int) error {
	if err := validatePage(page); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	target := m.pages[page]
	if target == nil {
		return RejectPageNotFound
	}
	if target.pin == 0 {
		return RejectUnpinUnderflow
	}
	target.pin--
	return nil
}

func (m *Manager) Lists() ([]int, []int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	young := make([]int, 0, m.youngLen)
	for current := m.youngHead; current != nil; current = current.youngNext {
		young = append(young, current.page)
	}

	old := make([]int, 0, m.oldLen)
	for current := m.oldHead; current != nil; current = current.oldNext {
		old = append(old, current.page)
	}

	return young, old
}

func validatePage(page int) error {
	if page < minPage || page > maxPage {
		return RejectInvalidArgument
	}
	return nil
}

func validateNow(now int64) error {
	if now < 0 || now > maxNow {
		return RejectInvalidArgument
	}
	return nil
}

func (m *Manager) accessExisting(target *node, now int64) {
	if target.inYoung {
		m.removeYoung(target)
		m.pushYoungHead(target)
		return
	}

	if target.first == nil {
		first := now
		target.first = &first
		return
	}

	if now-*target.first < m.promotionStay {
		return
	}

	m.removeOld(target)
	m.pushYoungHead(target)
	m.rebalance()
}

func (m *Manager) readMissing(page int, first *int64) (Result, error) {
	result := Result{}
	if len(m.pages) == m.capacity {
		victim := m.findVictim()
		if victim == nil {
			return Result{}, RejectAllPinned
		}
		page := victim.page
		m.evict(victim)
		result.Evicted = true
		result.EvictedPage = page
	}

	target := &node{page: page, first: first}
	m.pages[page] = target
	m.pushOldHead(target)
	m.rebalance()
	return result, nil
}

func (m *Manager) findVictim() *node {
	for current := m.oldTail; current != nil; current = current.oldPrev {
		if current.pin == 0 {
			return current
		}
	}
	for current := m.youngTail; current != nil; current = current.youngPrev {
		if current.pin == 0 {
			return current
		}
	}
	return nil
}

func (m *Manager) evict(target *node) {
	if target.inYoung {
		m.removeYoung(target)
	} else {
		m.removeOld(target)
	}
	delete(m.pages, target.page)
	*target = node{}
}

func (m *Manager) rebalance() {
	target := (m.youngLen + m.oldLen) * m.oldPercent / 100

	for m.oldLen < target && m.youngLen > 0 {
		targetNode := m.youngTail
		m.removeYoung(targetNode)
		m.pushOldHead(targetNode)
	}

	for m.oldLen > target+m.tolerance {
		targetNode := m.oldHead
		m.removeOld(targetNode)
		m.pushYoungTail(targetNode)
	}
}

func (m *Manager) pushYoungHead(target *node) {
	target.inYoung = true
	target.youngPrev = nil
	target.youngNext = m.youngHead
	if m.youngHead != nil {
		m.youngHead.youngPrev = target
	} else {
		m.youngTail = target
	}
	m.youngHead = target
	m.youngLen++
}

func (m *Manager) pushYoungTail(target *node) {
	target.inYoung = true
	target.youngPrev = m.youngTail
	target.youngNext = nil
	if m.youngTail != nil {
		m.youngTail.youngNext = target
	} else {
		m.youngHead = target
	}
	m.youngTail = target
	m.youngLen++
}

func (m *Manager) removeYoung(target *node) {
	if target.youngPrev != nil {
		target.youngPrev.youngNext = target.youngNext
	} else {
		m.youngHead = target.youngNext
	}
	if target.youngNext != nil {
		target.youngNext.youngPrev = target.youngPrev
	} else {
		m.youngTail = target.youngPrev
	}
	target.youngPrev = nil
	target.youngNext = nil
	target.inYoung = false
	m.youngLen--
}

func (m *Manager) pushOldHead(target *node) {
	target.inYoung = false
	target.oldPrev = nil
	target.oldNext = m.oldHead
	if m.oldHead != nil {
		m.oldHead.oldPrev = target
	} else {
		m.oldTail = target
	}
	m.oldHead = target
	m.oldLen++
}

func (m *Manager) removeOld(target *node) {
	if target.oldPrev != nil {
		target.oldPrev.oldNext = target.oldNext
	} else {
		m.oldHead = target.oldNext
	}
	if target.oldNext != nil {
		target.oldNext.oldPrev = target.oldPrev
	} else {
		m.oldTail = target.oldPrev
	}
	target.oldPrev = nil
	target.oldNext = nil
	m.oldLen--
}
