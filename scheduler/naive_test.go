package scheduler

import (
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"
	"testing"
)

type naiveBatch struct {
	key    string
	count  int
	fireAt int64
	seq    uint64
	normal bool
	late   bool
}

type naiveModel struct {
	off      int
	qs       int
	qe       int
	g        int
	cap      int
	max      int
	last     int64
	lastTick int64
	seq      uint64
	queue    []naiveBatch
	normal   map[string]int
	sent     map[int64]int
}

type testOperation struct {
	kind     int
	key      string
	priority int
	now      int64
}

func newNaiveModel(config Config) *naiveModel {
	return &naiveModel{
		off:      config.OffsetMinutes,
		qs:       config.QuietStart,
		qe:       config.QuietEnd,
		g:        config.MergeWindow,
		cap:      config.DailyCap,
		max:      config.MaxPending,
		lastTick: -1,
		seq:      1,
		normal:   make(map[string]int),
		sent:     make(map[int64]int),
	}
}

func TestRandomNaiveSimulation2000(t *testing.T) {
	for caseIndex := 1; caseIndex <= 2000; caseIndex++ {
		rng := rand.New(rand.NewPCG(uint64(caseIndex), uint64(2000-caseIndex+1)))
		config := Config{
			OffsetMinutes: rng.IntN(1561) - 720,
			QuietStart:    rng.IntN(1440),
			QuietEnd:      rng.IntN(1440),
			MergeWindow:   rng.IntN(1441),
			DailyCap:      1 + rng.IntN(4),
			MaxPending:    1 + rng.IntN(6),
		}
		scheduler := testScheduler(t, config)
		model := newNaiveModel(config)
		operations := make([]testOperation, 0, 60)
		now := int64(0)

		for opIndex := 0; opIndex < 60; opIndex++ {
			switch rng.IntN(10) {
			case 0:
				now--
			case 1:
			default:
				now += int64(rng.IntN(80))
			}
			op := testOperation{now: now}
			if rng.IntN(5) == 0 {
				op.kind = 1
			} else {
				op.kind = 0
				op.priority = rng.IntN(3)
				if rng.IntN(10) == 0 {
					op.key = ""
				} else {
					op.key = string(rune('a' + rng.IntN(5)))
				}
			}
			operations = append(operations, op)

			var got []Delivery
			var gotErr error
			var want []Delivery
			var wantErr error
			if op.kind == 0 {
				got, gotErr = scheduler.Submit(op.key, op.priority, op.now)
				want, wantErr = model.submit(op.key, op.priority, op.now)
			} else {
				got, gotErr = scheduler.Poll(op.now)
				want, wantErr = model.poll(op.now)
			}

			if errorsKind(gotErr) != errorsKind(wantErr) {
				t.Fatalf("case %d op %d error mismatch\ntail log:\n%s\ngot=%v want=%v",
					caseIndex, opIndex, renderNaiveTail(config, operations, got, want), gotErr, wantErr)
			}
			if !deliveriesEqual(got, want) {
				t.Fatalf("case %d op %d deliveries mismatch\ntail log:\n%s\ngot=%#v want=%#v",
					caseIndex, opIndex, renderNaiveTail(config, operations, got, want), got, want)
			}
			if op.kind == 0 {
				t.Logf("case=%d Submit(key=%q,prio=%d,now=%d) -> got=%#v want=%#v err=%v; basis=validate before lock, rollback before pre-state capacity, due heap before notice, ordinary merge only via existing normal map",
					caseIndex, op.key, op.priority, op.now, got, want, gotErr)
			} else {
				t.Logf("case=%d Poll(now=%d) -> got=%#v want=%#v err=%v; basis=repeatedly examine minimum (fireAt,seq), deliver below cap or shift to shifted next local midnight",
					caseIndex, op.now, got, want, gotErr)
			}
			if !naiveStatesMatch(scheduler, model) {
				gotState, wantState := describeSchedulerState(scheduler), describeNaiveState(model)
				t.Fatalf("case %d op %d state mismatch\ntail log:\n%s\ngot state:\n%s\nwant state:\n%s",
					caseIndex, opIndex, renderNaiveTail(config, operations, got, want), gotState, wantState)
			}
		}
	}
}

func (m *naiveModel) submit(key string, priority int, now int64) ([]Delivery, error) {
	if key == "" || priority < 0 || priority > 2 || now < 0 || now > 1_000_000_000_000 {
		return nil, ErrInvalidArgument
	}
	if now < m.last {
		return nil, ErrClockRollback
	}
	_, exists := m.normal[key]
	needsBatch := priority == 1 || (priority == 0 && !exists)
	if needsBatch && len(m.queue) >= m.max {
		return nil, ErrQueueFull
	}
	deliveries := m.advance(now)
	switch priority {
	case 2:
		m.sent[naiveLocalDay(now, m.off)]++
		deliveries = append(deliveries, Delivery{Key: key, Count: 1, At: now})
	case 1:
		m.queue = append(m.queue, naiveBatch{
			key: key, count: 1, fireAt: m.shift(now), seq: m.seq, late: true,
		})
		m.seq++
	case 0:
		if index, exists := m.normal[key]; exists {
			m.queue[index].count++
		} else {
			item := naiveBatch{key: key, count: 1, fireAt: m.shift(now + int64(m.g)), seq: m.seq, normal: true, late: true}
			m.normal[key] = len(m.queue)
			m.queue = append(m.queue, item)
			m.seq++
		}
	}
	m.last = now
	return deliveries, nil
}

func (m *naiveModel) poll(now int64) ([]Delivery, error) {
	if now < 0 || now > 1_000_000_000_000 {
		return nil, ErrInvalidArgument
	}
	if now < m.last {
		return nil, ErrClockRollback
	}
	deliveries := m.advance(now)
	m.last = now
	return deliveries, nil
}

func (m *naiveModel) advance(now int64) []Delivery {
	var deliveries []Delivery
	for tick := m.lastTick + 1; tick <= now; tick++ {
		deliveries = append(deliveries, m.processDueAt(tick, false)...)
	}
	deliveries = append(deliveries, m.processDueAt(now, true)...)
	m.lastTick = now
	return deliveries
}

func (m *naiveModel) processDueAt(now int64, lateOnly bool) []Delivery {
	var deliveries []Delivery
	for {
		m.sortQueue()
		if len(m.queue) == 0 || m.queue[0].fireAt > now || (lateOnly && !m.queue[0].late) {
			return deliveries
		}
		current := m.queue[0]
		day := naiveLocalDay(current.fireAt, m.off)
		m.queue = m.queue[1:]
		m.rebuildNormalIndices()
		if m.sent[day] < m.cap {
			if current.normal {
				delete(m.normal, current.key)
			}
			m.sent[day]++
			deliveries = append(deliveries, Delivery{Key: current.key, Count: current.count, At: current.fireAt})
		} else {
			current.fireAt = m.shift(naiveNextDayStart(day, m.off))
			current.late = false
			m.queue = append(m.queue, current)
			m.rebuildNormalIndices()
		}
	}
}

func (m *naiveModel) sortQueue() {
	sort.SliceStable(m.queue, func(i, j int) bool {
		if m.queue[i].fireAt != m.queue[j].fireAt {
			return m.queue[i].fireAt < m.queue[j].fireAt
		}
		return m.queue[i].seq < m.queue[j].seq
	})
	m.rebuildNormalIndices()
}

func (m *naiveModel) rebuildNormalIndices() {
	m.normal = make(map[string]int)
	for i := range m.queue {
		if m.queue[i].normal {
			m.normal[m.queue[i].key] = i
		}
	}
}

func (m *naiveModel) shift(t int64) int64 {
	lm := naiveLocalMinute(t, m.off)
	quiet := false
	if m.qs < m.qe {
		quiet = int64(m.qs) <= lm && lm < int64(m.qe)
	} else if m.qs > m.qe {
		quiet = lm >= int64(m.qs) || lm < int64(m.qe)
	}
	if !quiet {
		return t
	}
	return t + nonNegativeModulo(int64(m.qe)-lm, minutesPerDay)
}

func naiveLocalMinute(t int64, off int) int64 {
	return nonNegativeModulo(t+int64(off), minutesPerDay)
}

func naiveLocalDay(t int64, off int) int64 {
	return floorDiv(t+int64(off), minutesPerDay)
}

func naiveNextDayStart(day int64, off int) int64 {
	return (day+1)*minutesPerDay - int64(off)
}

func describeSchedulerState(scheduler *Scheduler) string {
	parts := []string{fmt.Sprintf("last=%d seq=%d", scheduler.lastNow, scheduler.nextSeq)}
	queue := sortedSchedulerBatches(scheduler)
	for _, item := range queue {
		parts = append(parts, fmt.Sprintf("batch key=%s count=%d fire=%d seq=%d normal=%t", item.key, item.count, item.fireAt, item.seq, item.normal))
	}
	return strings.Join(parts, "\n")
}

func describeNaiveState(model *naiveModel) string {
	model.sortQueue()
	parts := []string{fmt.Sprintf("last=%d seq=%d", model.last, model.seq)}
	for _, item := range model.queue {
		parts = append(parts, fmt.Sprintf("batch key=%s count=%d fire=%d seq=%d normal=%t", item.key, item.count, item.fireAt, item.seq, item.normal))
	}
	return strings.Join(parts, "\n")
}

func errorsKind(err error) string {
	switch err {
	case nil:
		return ""
	case ErrInvalidArgument:
		return "invalid"
	case ErrClockRollback:
		return "rollback"
	case ErrQueueFull:
		return "full"
	default:
		return err.Error()
	}
}

func deliveriesEqual(got []Delivery, want []Delivery) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func naiveStatesMatch(scheduler *Scheduler, model *naiveModel) bool {
	if scheduler.lastNow != model.last ||
		len(scheduler.pending) != len(model.queue) ||
		len(scheduler.sent) != len(model.sent) ||
		len(scheduler.normal) != len(model.normal) {
		return false
	}
	for day, count := range model.sent {
		if scheduler.sent[day] != count {
			return false
		}
	}
	model.sortQueue()
	actualQueue := sortedSchedulerBatches(scheduler)
	for i, item := range model.queue {
		actual := actualQueue[i]
		if actual.key != item.key || actual.count != item.count || actual.fireAt != item.fireAt || actual.seq != item.seq || actual.normal != item.normal {
			return false
		}
	}
	return true
}

func sortedSchedulerBatches(scheduler *Scheduler) []*batch {
	queue := append([]*batch(nil), scheduler.pending...)
	sort.SliceStable(queue, func(i, j int) bool {
		if queue[i].fireAt != queue[j].fireAt {
			return queue[i].fireAt < queue[j].fireAt
		}
		return queue[i].seq < queue[j].seq
	})
	return queue
}

func renderNaiveTail(config Config, operations []testOperation, got []Delivery, want []Delivery) string {
	lines := []string{fmt.Sprintf("input config=%+v", config)}
	start := len(operations) - 8
	if start < 0 {
		start = 0
	}
	for i := start; i < len(operations); i++ {
		op := operations[i]
		if op.kind == 0 {
			lines = append(lines, fmt.Sprintf("input op=%d Submit(key=%q prio=%d now=%d)", i, op.key, op.priority, op.now))
		} else {
			lines = append(lines, fmt.Sprintf("input op=%d Poll(now=%d)", i, op.now))
		}
	}
	lines = append(lines,
		fmt.Sprintf("output got=%#v", got),
		fmt.Sprintf("output want=%#v", want),
		"basis: due heap order (fireAt, seq); urgent increments day sent and returns immediately; ordinary merges only with existing normal batch; full-day batches shift to shifted next local midnight",
	)
	return strings.Join(lines, "\n")
}
