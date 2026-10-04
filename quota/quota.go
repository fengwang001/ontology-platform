package quota

import (
	"errors"

	"ontology/calendar"
)

var ErrInvalidParam = errors.New("quota: invalid parameter")

type Manager struct {
	cal    *calendar.Cal
	lw, lh int64
	used   map[int64]int64
}

func New(cal *calendar.Cal, lw, lh int64) (*Manager, error) {
	if cal == nil || lw < 0 || lw > 86400 || lh < 0 || lh > 86400 {
		return nil, ErrInvalidParam
	}
	return &Manager{cal: cal, lw: lw, lh: lh, used: map[int64]int64{}}, nil
}

// Limit 返回某日额度：节假日 Lh，普通日 Lw。
func (m *Manager) Limit(day int64) int64 {
	if m.cal.IsHoliday(day) {
		return m.lh
	}
	return m.lw
}

func (m *Manager) Used(day int64) int64 { return m.used[day] }

// Remaining 返回 t 当日的剩余额度，非负。
func (m *Manager) Remaining(t int64) int64 {
	day := m.cal.Day(t)
	r := m.Limit(day) - m.used[day]
	if r < 0 {
		return 0
	}
	return r
}

// Add 给某日累加已用时长；delta 必须非负且不超过当日剩余额度。
func (m *Manager) Add(day, delta int64) error {
	if delta < 0 {
		return ErrInvalidParam
	}
	cur := m.used[day]
	if cur+delta > m.Limit(day) {
		return ErrInvalidParam
	}
	if delta == 0 {
		return nil
	}
	m.used[day] = cur + delta
	return nil
}
