package authz

import (
	"sync"

	"ontology/count"
)

// Manager 维护人员权限并驱动超容差差异的审批。
type Manager struct {
	eng *count.Engine
	mu  sync.Mutex

	canApprove map[string]bool
	canSenior  map[string]bool
}

func New(eng *count.Engine) *Manager {
	return &Manager{
		eng:        eng,
		canApprove: make(map[string]bool),
		canSenior:  make(map[string]bool),
	}
}

func (m *Manager) Grant(person []byte, approve, senior bool) error {
	// Grant 设置人员权限。
	if len(person) < 1 || len(person) > 32 {
		return ErrInvalid
	}
	key := string(person)
	m.mu.Lock()
	defer m.mu.Unlock()
	if approve {
		m.canApprove[key] = true
	} else {
		delete(m.canApprove, key)
	}
	if senior {
		m.canSenior[key] = true
	} else {
		delete(m.canSenior, key)
	}
	return nil
}

func (m *Manager) Approve(task, loc, approver []byte) error {
	return m.decide(task, loc, approver, true)
}

func (m *Manager) Reject(task, loc, approver []byte) error {
	return m.decide(task, loc, approver, false)
}

func (m *Manager) decide(task, loc, approver []byte, approve bool) error {
	if len(task) < 1 || len(task) > 32 || len(loc) < 1 || len(loc) > 32 ||
		len(approver) < 1 || len(approver) > 32 {
		return ErrInvalid
	}
	// Pending 内含“不存在 > 状态不符”校验。
	counters, diff, price, err := m.eng.Pending(task, loc)
	if err != nil {
		return mapErr(err)
	}
	akey := string(approver)
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.canApprove[akey] {
		return ErrNoApprove
	}
	for _, c := range counters {
		if string(c) == akey {
			return ErrRotate
		}
	}
	amount := abs64(diff) * price
	if approve && amount > m.eng.Lim() && !m.canSenior[akey] {
		return ErrNoSenior
	}
	if err := m.eng.CommitApproval(task, loc, approve); err != nil {
		return mapErr(err)
	}
	return nil
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

func mapErr(err error) error {
	switch err {
	case count.ErrInvalid:
		return ErrInvalid
	case count.ErrNotFound:
		return ErrNotFound
	case count.ErrState:
		return ErrState
	case count.ErrConflict:
		return ErrConflict
	case count.ErrStock:
		return ErrStock
	default:
		return err
	}
}
