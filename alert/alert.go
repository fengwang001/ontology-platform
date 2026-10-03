// Package alert 按 SLA 阈值档（50/80/100）产出至少一次、可 Ack 的告警。
package alert

import (
	"errors"
	"sort"
	"sync"

	"ontology/cal"
	"ontology/sla"
)

// ErrNotDue 该档在 now 尚未到期。
var ErrNotDue = errors.New("alert: threshold not due")

// Levels 为三个阈值档（百分比）。
var Levels = []uint8{50, 80, 100}

// Alert 是一条告警记录。
type Alert struct {
	ID    []byte
	Level uint8
	At    int64
}

// Logger 为可选的判定日志输出。
type Logger interface {
	Printf(format string, args ...any)
}

// Manager 依据 sla.Manager 现算各档触发时刻并维护 Ack 集合。
type Manager struct {
	mu     sync.Mutex
	sm     *sla.Manager
	cal    *cal.Calendar
	acked  map[ackKey]bool
	logger Logger
}

type ackKey struct {
	id string
	q  uint8
}

// NewManager 创建告警管理器。
func NewManager(sm *sla.Manager, c *cal.Calendar) *Manager {
	return &Manager{sm: sm, cal: c, acked: make(map[ackKey]bool)}
}

// SetLogger 注入日志器（nil 关闭）。
func (m *Manager) SetLogger(l Logger) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logger = l
}

// Tick 返回所有触发时刻 <= now 且未确认的告警，按 (At, ID, Level) 排序。
// 每次 Tick 都重复返回未确认告警（至少一次投递）。
func (m *Manager) Tick(now int64) ([]Alert, error) {
	if err := m.cal.CheckClock(now); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Alert
	for _, id := range m.sm.IDs() {
		budget, ok := m.sm.Budget(id)
		if !ok {
			continue
		}
		for _, q := range Levels {
			if m.acked[ackKey{string(id), q}] {
				continue
			}
			target := sla.Threshold(budget, q)
			at, due, err := m.sm.Trigger(id, target, now)
			if err != nil {
				m.log("Tick now=%d id=%q q=%d -> error=%v", now, id, q, err)
				return nil, err
			}
			if due && at <= now {
				out = append(out, Alert{ID: append([]byte(nil), id...), Level: q, At: at})
				m.log("Tick now=%d id=%q q=%d target=%d -> due at=%d", now, id, q, target, at)
			} else {
				m.log("Tick now=%d id=%q q=%d target=%d -> not due", now, id, q, target)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].At != out[j].At {
			return out[i].At < out[j].At
		}
		if c := compareBytes(out[i].ID, out[j].ID); c != 0 {
			return c < 0
		}
		return out[i].Level < out[j].Level
	})
	m.cal.AdvanceClock(now)
	m.log("Tick now=%d -> %d alert(s)", now, len(out))
	return out, nil
}

// Ack 确认一档：未到期报 ErrNotDue，已确认为成功无操作。
func (m *Manager) Ack(id []byte, q, now int64) error {
	validQ := false
	for _, lvl := range Levels {
		if int64(lvl) == q {
			validQ = true
		}
	}
	if len(id) == 0 || !validQ {
		return cal.ErrArgument
	}
	if err := m.cal.CheckClock(now); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := ackKey{string(id), uint8(q)}
	if m.acked[key] {
		m.cal.AdvanceClock(now)
		m.log("Ack now=%d id=%q q=%d -> already acked (noop)", now, id, q)
		return nil
	}
	budget, ok := m.sm.Budget(id)
	if !ok {
		return sla.ErrNotFound
	}
	at, due, err := m.sm.Trigger(id, sla.Threshold(budget, uint8(q)), now)
	if err != nil {
		return err
	}
	if !due || at > now {
		m.log("Ack now=%d id=%q q=%d -> not due", now, id, q)
		return ErrNotDue
	}
	m.acked[key] = true
	m.cal.AdvanceClock(now)
	m.log("Ack now=%d id=%q q=%d at=%d -> confirmed", now, id, q, at)
	return nil
}

func compareBytes(a, b []byte) int {
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

func (m *Manager) log(format string, args ...any) {
	if m.logger != nil {
		m.logger.Printf(format, args...)
	}
}
