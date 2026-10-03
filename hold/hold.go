package hold

import "errors"

var (
	ErrInvalid = errors.New("hold: invalid argument")
	ErrRole    = errors.New("hold: insufficient role")
	ErrClock   = errors.New("hold: clock moved backwards")
	ErrAlready = errors.New("hold: subject is already held")
	ErrNotHeld = errors.New("hold: subject is not held")
)

type Manager struct {
	clock int
	held  map[int]bool
}

func NewManager(systems int) *Manager {
	return &Manager{held: make(map[int]bool)}
}

func (m *Manager) Hold(role, subject, now int) error {
	if role < 1 || role > 3 || subject < 1 || subject > 1_000_000 || now < 0 {
		return ErrInvalid
	}
	if role != 2 {
		return ErrRole
	}
	if now < m.clock {
		return ErrClock
	}
	if m.held[subject] {
		return ErrAlready
	}
	m.clock = now
	m.held[subject] = true
	return nil
}

func (m *Manager) Release(role, subject, now int) error {
	if role < 1 || role > 3 || subject < 1 || subject > 1_000_000 || now < 0 {
		return ErrInvalid
	}
	if role != 2 {
		return ErrRole
	}
	if now < m.clock {
		return ErrClock
	}
	if !m.held[subject] {
		return ErrNotHeld
	}
	m.clock = now
	delete(m.held, subject)
	return nil
}

func (m *Manager) IsHeld(subject int) bool {
	return m.held[subject]
}
