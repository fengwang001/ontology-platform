package alert

import (
	"sync"

	"ontology/cal"
	"ontology/sla"
)

// Alarm 是一条至少一次投递的告警。
type Alarm struct {
	ID []byte
	Q  int
	T  int64
}

var levels = []int{50, 80, 100}

// Manager 按阈值档产出告警；内嵌 sla 管理器与共享日历。
type Manager struct {
	mu    sync.Mutex
	s     *sla.Manager
	c     *cal.Calendar
	acked map[string]map[int]bool
}

func NewManager(c *cal.Calendar) (*Manager, *sla.Manager) {
	m := &Manager{c: c, acked: map[string]map[int]bool{}}
	m.s = sla.NewManager(c)
	return m, m.s
}

// Wrap 复用已有 sla 管理器。
func Wrap(s *sla.Manager) *Manager {
	return &Manager{s: s, c: s.Cal(), acked: map[string]map[int]bool{}}
}

func (m *Manager) Tick(now int64) ([]Alarm, error) {
	if now < 0 || now > cal.MaxTime {
		return nil, ErrInvalid
	}
	if err := m.c.CheckClock(now); err != nil {
		return nil, mapCalErr(err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.s.Lock()
	defer m.s.Unlock()
	m.c.RLock()
	var out []Alarm
	for _, id := range m.s.IDsLocked() {
		ti := m.s.TimerLocked(id)
		budget := ti.Budget()
		for _, q := range levels {
			if m.acked[id][q] {
				continue
			}
			need := ceilDiv(budget*int64(q), 100)
			t, ok := m.s.TriggerLocked(ti, need)
			if ok && t <= now {
				out = append(out, Alarm{ID: []byte(id), Q: q, T: t})
			}
		}
	}
	m.c.RUnlock()
	m.c.AdvanceClock(now)
	sortAlarms(out)
	return out, nil
}

func (m *Manager) Ack(id []byte, q int, now int64) error {
	if len(id) == 0 || now < 0 || now > cal.MaxTime {
		return ErrInvalid
	}
	if q != 50 && q != 80 && q != 100 {
		return ErrInvalid
	}
	if err := m.c.CheckClock(now); err != nil {
		return mapCalErr(err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.s.Lock()
	defer m.s.Unlock()
	key := string(id)
	ti := m.s.TimerLocked(key)
	if ti == nil {
		return sla.ErrNotFound
	}
	m.c.RLock()
	need := ceilDiv(ti.Budget()*int64(q), 100)
	t, ok := m.s.TriggerLocked(ti, need)
	m.c.RUnlock()
	if !ok || t > now {
		return ErrNotDue
	}
	if m.acked[key] == nil {
		m.acked[key] = map[int]bool{}
	}
	if !m.acked[key][q] {
		m.acked[key][q] = true
		m.c.AdvanceClock(now)
	}
	return nil
}

func ceilDiv(a, b int64) int64 { return (a + b - 1) / b }

func mapCalErr(err error) error {
	switch err {
	case cal.ErrInvalid:
		return ErrInvalid
	case cal.ErrClock:
		return cal.ErrClock
	default:
		return err
	}
}

func sortAlarms(a []Alarm) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && lessAlarm(a[j], a[j-1]); j-- {
			a[j-1], a[j] = a[j], a[j-1]
		}
	}
}

func lessAlarm(x, y Alarm) bool {
	if x.T != y.T {
		return x.T < y.T
	}
	if c := bytesCompare(x.ID, y.ID); c != 0 {
		return c < 0
	}
	return x.Q < y.Q
}

func bytesCompare(a, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return int(a[i]) - int(b[i])
		}
	}
	return len(a) - len(b)
}
