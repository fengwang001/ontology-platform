package ontology

import (
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"testing"
)

type differentialConfig struct {
	off    int64
	qs     int64
	qe     int64
	window int64
	cap    int64
	limit  int
}

type differentialOperation struct {
	kind int
	key  []byte
	prio int64
	now  int64
}

type naiveBatch struct {
	key       []byte
	count     int64
	f         int64
	seq       int64
	mergeable bool
}

type naiveModel struct {
	cfg     differentialConfig
	lastNow int64
	seq     int64
	batches []naiveBatch
	sent    map[int64]int64
}

func (m *naiveModel) localMinute(t int64) int64 {
	minute := (t + m.cfg.off) % 1440
	if minute < 0 {
		minute += 1440
	}
	return minute
}

func (m *naiveModel) localDay(t int64) int64 {
	shifted := t + m.cfg.off
	minute := shifted % 1440
	if minute < 0 {
		minute += 1440
	}
	return (shifted - minute) / 1440
}

func (m *naiveModel) shifted(t int64) int64 {
	minute := m.localMinute(t)
	quiet := false
	if m.cfg.qs < m.cfg.qe {
		quiet = minute >= m.cfg.qs && minute < m.cfg.qe
	} else if m.cfg.qs > m.cfg.qe {
		quiet = minute >= m.cfg.qs || minute < m.cfg.qe
	}
	if !quiet {
		return t
	}

	wait := (m.cfg.qe - minute) % 1440
	if wait < 0 {
		wait += 1440
	}
	return t + wait
}

func (m *naiveModel) advanceTo(now int64) []Delivery {
	var delivered []Delivery
	if now > m.lastNow {
		m.sort()
		for minute := m.lastNow + 1; minute <= now; minute++ {
			delivered = append(delivered, m.processAt(minute)...)
		}
		m.lastNow = now
		return delivered
	}
	m.sort()
	return m.processAt(now)
}

func (m *naiveModel) processAt(now int64) []Delivery {
	var delivered []Delivery

	for {
		if len(m.batches) == 0 || m.batches[0].f > now {
			return delivered
		}

		current := m.batches[0]
		m.batches = m.batches[1:]
		day := m.localDay(current.f)
		if m.sent[day] < m.cfg.cap {
			m.sent[day]++
			delivered = append(delivered, Delivery{
				Key:   append([]byte(nil), current.key...),
				Count: current.count,
				At:    current.f,
			})
			continue
		}

		current.f = m.shifted((day+1)*1440 - m.cfg.off)
		m.batches = append(m.batches, current)
		m.sort()
	}
}

func (m *naiveModel) sort() {
	slices.SortFunc(m.batches, func(a, b naiveBatch) int {
		if a.f != b.f {
			if a.f < b.f {
				return -1
			}
			return 1
		}
		if a.seq < b.seq {
			return -1
		}
		if a.seq > b.seq {
			return 1
		}
		return 0
	})
}

func (m *naiveModel) normalBatch(key string) *naiveBatch {
	for i := range m.batches {
		if m.batches[i].mergeable && string(m.batches[i].key) == key {
			return &m.batches[i]
		}
	}
	return nil
}

func (m *naiveModel) add(key []byte, count, f int64, mergeable bool) {
	m.seq++
	m.batches = append(m.batches, naiveBatch{
		key:       append([]byte(nil), key...),
		count:     count,
		f:         f,
		seq:       m.seq,
		mergeable: mergeable,
	})
}

func (m *naiveModel) submit(op differentialOperation) ([]Delivery, error) {
	if err := validateOperation(op, true); err != nil {
		return nil, err
	}
	if op.now < m.lastNow {
		return nil, ErrClockMovedBackwards
	}

	needsBatch := op.prio == 1
	if op.prio == 0 {
		projection := m.clone()
		projection.advanceTo(op.now)
		needsBatch = projection.normalBatch(string(op.key)) == nil
	}
	if needsBatch && len(m.batches) >= m.cfg.limit {
		return nil, ErrQueueFull
	}

	delivered := m.advanceTo(op.now)
	key := append([]byte(nil), op.key...)
	switch op.prio {
	case 2:
		m.sent[m.localDay(op.now)]++
		delivered = append(delivered, Delivery{Key: key, Count: 1, At: op.now})
	case 1:
		m.add(key, 1, m.shifted(op.now), false)
	case 0:
		if existing := m.normalBatch(string(key)); existing != nil {
			existing.count++
		} else {
			m.add(key, 1, m.shifted(op.now+m.cfg.window), true)
		}
	}
	return delivered, nil
}

func (m *naiveModel) poll(now int64) ([]Delivery, error) {
	op := differentialOperation{kind: 1, now: now}
	if err := validateOperation(op, false); err != nil {
		return nil, err
	}
	if now < m.lastNow {
		return nil, ErrClockMovedBackwards
	}
	return m.advanceTo(now), nil
}

func (m *naiveModel) clone() *naiveModel {
	copyModel := &naiveModel{
		cfg:     m.cfg,
		lastNow: m.lastNow,
		seq:     m.seq,
		batches: make([]naiveBatch, len(m.batches)),
		sent:    make(map[int64]int64, len(m.sent)),
	}
	for i, batch := range m.batches {
		copyModel.batches[i] = batch
		copyModel.batches[i].key = append([]byte(nil), batch.key...)
	}
	for day, count := range m.sent {
		copyModel.sent[day] = count
	}
	return copyModel
}

func validateOperation(op differentialOperation, submit bool) error {
	if submit && len(op.key) == 0 {
		return ErrEmptyKey
	}
	if submit && (op.prio < 0 || op.prio > 2) {
		return ErrInvalidPriority
	}
	if op.now < 0 || op.now > 1_000_000_000_000 {
		return ErrInvalidTime
	}
	return nil
}

func formatDeliveries(delivered []Delivery) string {
	if len(delivered) == 0 {
		return "[]"
	}
	parts := make([]string, 0, len(delivered))
	for _, delivery := range delivered {
		parts = append(parts, fmt.Sprintf("(%s,%d,%d)", delivery.Key, delivery.Count, delivery.At))
	}
	return "[" + join(parts, ",") + "]"
}

func join(values []string, separator string) string {
	result := ""
	for i, value := range values {
		if i > 0 {
			result += separator
		}
		result += value
	}
	return result
}

func TestRandomDifferential(t *testing.T) {
	random := rand.New(rand.NewSource(1))
	const groups = 2000
	const operationsPerGroup = 20

	for group := 0; group < groups; group++ {
		cfg := differentialConfig{
			off:    int64(random.Intn(1561) - 720),
			qs:     int64(random.Intn(1440)),
			window: int64(random.Intn(1441)),
			cap:    int64(1 + random.Intn(4)),
			limit:  1 + random.Intn(8),
		}
		cfg.qe = int64(random.Intn(1440))
		if random.Intn(10) == 0 {
			cfg.qe = cfg.qs
		}

		scheduler, err := NewScheduler(cfg.off, cfg.qs, cfg.qe, cfg.window, cfg.cap, cfg.limit)
		if err != nil {
			t.Fatalf("group=%d config=%+v constructor err=%v", group, cfg, err)
		}
		model := &naiveModel{cfg: cfg, sent: make(map[int64]int64)}
		var now int64

		t.Logf("group=%d config={off:%d qs:%d qe:%d G:%d Cap:%d M:%d} basis=random replay seed=1",
			group, cfg.off, cfg.qs, cfg.qe, cfg.window, cfg.cap, cfg.limit)

		for index := 0; index < operationsPerGroup; index++ {
			op := differentialOperation{kind: random.Intn(2), now: now}
			if op.kind == 0 {
				op.key = []byte{byte('a' + random.Intn(6))}
				op.prio = int64(random.Intn(3))
			}
			if random.Intn(10) == 0 {
				switch random.Intn(5) {
				case 0:
					op.kind = 0
					op.key = nil
					op.prio = int64(random.Intn(3))
				case 1:
					op.kind = 0
					op.prio = 3
				case 2:
					op.now = -1
				case 3:
					op.now = 1_000_000_000_001
				case 4:
					if now > 0 {
						op.now = now - 1
					}
				}
			}

			var actual []Delivery
			var actualErr error
			if op.kind == 0 {
				actual, actualErr = scheduler.Submit(op.key, op.prio, op.now)
			} else {
				actual, actualErr = scheduler.Poll(op.now)
			}

			var expected []Delivery
			var expectedErr error
			if op.kind == 0 {
				expected, expectedErr = model.submit(op)
			} else {
				expected, expectedErr = model.poll(op.now)
			}

			t.Logf("group=%d op=%d input=%s actual=%s/%v naive=%s/%v basis=exact delivery order and rejection identity",
				group, index, formatDifferentialInput(op), formatDeliveries(actual), actualErr,
				formatDeliveries(expected), expectedErr)

			if !errors.Is(actualErr, expectedErr) {
				t.Fatalf("group=%d op=%d input=%s actual err=%v naive err=%v", group, index,
					formatDifferentialInput(op), actualErr, expectedErr)
			}
			if !equalDeliveries(actual, expected) {
				t.Fatalf("group=%d op=%d input=%s actual=%s naive=%s", group, index,
					formatDifferentialInput(op), formatDeliveries(actual), formatDeliveries(expected))
			}

			if actualErr == nil && op.now >= now {
				now = op.now
			}
			if op.kind == 0 && actualErr == nil {
				now += int64(random.Intn(721))
			}
		}
	}
}

func formatDifferentialInput(op differentialOperation) string {
	if op.kind == 0 {
		return fmt.Sprintf("Submit(%s,%d,%d)", op.key, op.prio, op.now)
	}
	return fmt.Sprintf("Poll(%d)", op.now)
}

func equalDeliveries(actual, expected []Delivery) bool {
	if len(actual) != len(expected) {
		return false
	}
	for i := range actual {
		if string(actual[i].Key) != string(expected[i].Key) ||
			actual[i].Count != expected[i].Count ||
			actual[i].At != expected[i].At {
			return false
		}
	}
	return true
}
