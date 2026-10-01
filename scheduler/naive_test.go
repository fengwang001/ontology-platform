package scheduler

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"sort"
	"testing"
)

type naivePod struct {
	id       string
	priority int
	addedAt  int64
	attempts int64
	failBits int64
	expires  int64
	parked   int64
	popSeq   int64
	state    podState
}

type naiveModel struct {
	base      int64
	max       int64
	retention int64
	now       int64
	seq       int64
	events    []int64
	pods      map[string]*naivePod
}

type opKind uint8

const (
	opAdd opKind = iota
	opPop
	opDone
	opEvent
	opAdvance
	opRemove
)

type randomOp struct {
	kind      opKind
	now       int64
	id        string
	priority  int
	outcome   Outcome
	failBits  int64
	eventMask int64
}

func newNaiveModel(base, max, retention int64) *naiveModel {
	return &naiveModel{
		base:      base,
		max:       max,
		retention: retention,
		pods:      make(map[string]*naivePod),
	}
}

func (m *naiveModel) advance(now int64) {
	for _, entry := range m.pods {
		if entry.state == stateBackoff && entry.expires <= now {
			entry.state = stateActive
		}
	}
	for _, entry := range m.pods {
		if entry.state == stateUnschedulable && entry.parked+m.retention <= now {
			m.route(entry, now)
		}
	}
}

func (m *naiveModel) route(entry *naivePod, now int64) {
	if now < entry.expires {
		entry.state = stateBackoff
	} else {
		entry.state = stateActive
	}
}

func (m *naiveModel) related(entry *naivePod) bool {
	for index := entry.popSeq; index < m.seq; index++ {
		if entry.failBits == 0 || uint8(entry.failBits)&uint8(m.events[index]) != 0 {
			return true
		}
	}
	return false
}

func (m *naiveModel) deadline(entry *naivePod, now int64) int64 {
	shift := entry.attempts - 1
	duration := m.max
	if shift < 63 {
		value := m.base << shift
		if value < m.max {
			duration = value
		}
	}
	return now + duration
}

func (m *naiveModel) apply(op randomOp) (string, bool, error) {
	switch op.kind {
	case opAdd:
		if op.id == "" || op.now < 0 {
			return "", false, ErrInvalidArgument
		}
		if op.now < m.now {
			return "", false, ErrClockRollback
		}
		if _, exists := m.pods[op.id]; exists {
			return "", false, ErrPodAlreadyExists
		}
		m.pods[op.id] = &naivePod{
			id:       op.id,
			priority: op.priority,
			addedAt:  op.now,
			state:    stateActive,
		}
		m.now = op.now
	case opPop:
		if op.now < 0 {
			return "", false, ErrInvalidArgument
		}
		if op.now < m.now {
			return "", false, ErrClockRollback
		}
		m.advance(op.now)
		m.now = op.now
		var candidates []*naivePod
		for _, entry := range m.pods {
			if entry.state == stateActive {
				candidates = append(candidates, entry)
			}
		}
		if len(candidates) == 0 {
			return "", false, nil
		}
		sort.Slice(candidates, func(i, j int) bool {
			left, right := candidates[i], candidates[j]
			if left.priority != right.priority {
				return left.priority > right.priority
			}
			if left.addedAt != right.addedAt {
				return left.addedAt < right.addedAt
			}
			return left.id < right.id
		})
		entry := candidates[0]
		entry.attempts++
		entry.popSeq = m.seq
		entry.state = stateInFlight
		return entry.id, true, nil
	case opDone:
		if op.id == "" || op.now < 0 ||
			(op.outcome != OutcomeScheduled && op.outcome != OutcomeFailed) ||
			op.failBits < 0 || op.failBits > 255 {
			return "", false, ErrInvalidArgument
		}
		if op.now < m.now {
			return "", false, ErrClockRollback
		}
		entry, exists := m.pods[op.id]
		if !exists {
			return "", false, ErrPodNotFound
		}
		if entry.state != stateInFlight {
			return "", false, ErrPodNotInFlight
		}
		m.now = op.now
		if op.outcome == OutcomeScheduled {
			delete(m.pods, op.id)
			return "", false, nil
		}
		entry.failBits = op.failBits
		entry.expires = m.deadline(entry, op.now)
		if m.related(entry) {
			entry.state = stateBackoff
		} else {
			entry.state = stateUnschedulable
			entry.parked = op.now
		}
	case opEvent:
		if op.eventMask < 1 || op.eventMask > 255 || op.now < 0 {
			return "", false, ErrInvalidArgument
		}
		if op.now < m.now {
			return "", false, ErrClockRollback
		}
		m.seq++
		m.events = append(m.events, op.eventMask)
		for _, entry := range m.pods {
			if entry.state == stateUnschedulable &&
				(entry.failBits == 0 || uint8(entry.failBits)&uint8(op.eventMask) != 0) {
				m.route(entry, op.now)
			}
		}
		m.now = op.now
	case opAdvance:
		if op.now < 0 {
			return "", false, ErrInvalidArgument
		}
		if op.now < m.now {
			return "", false, ErrClockRollback
		}
		m.advance(op.now)
		m.now = op.now
	case opRemove:
		if op.id == "" {
			return "", false, ErrInvalidArgument
		}
		if _, exists := m.pods[op.id]; !exists {
			return "", false, ErrPodNotFound
		}
		delete(m.pods, op.id)
	}
	return "", false, nil
}

func makeRandomOp(rng *rand.Rand, model *naiveModel, step int) randomOp {
	now := model.now + int64(rng.IntN(4))
	if model.now > 2 && rng.IntN(10) == 0 {
		now = model.now - int64(1+rng.IntN(3))
	}
	op := randomOp{kind: opKind(rng.IntN(6)), now: now, failBits: int64(rng.IntN(258)) - 1, eventMask: int64(rng.IntN(257))}
	if rng.IntN(8) == 0 {
		op.outcome = Outcome(rng.IntN(2) + 1)
	} else {
		op.outcome = OutcomeFailed
	}
	idIndex := rng.IntN(12)
	op.id = fmt.Sprintf("pod-%02d", idIndex)
	op.priority = rng.IntN(8) - 2
	if step < 8 && rng.IntN(3) > 0 {
		op.kind = opAdd
	}
	if op.kind == opAdd && idIndex >= 8 {
		op.id = ""
	}
	return op
}

func applyActual(q *SchedulingQueue, op randomOp) (string, bool, error) {
	switch op.kind {
	case opAdd:
		return "", false, q.Add(op.id, op.priority, op.now)
	case opPop:
		entry, ok, err := q.Pop(op.now)
		if entry != nil {
			return entry.ID, true, err
		}
		return "", ok, err
	case opDone:
		return "", false, q.Done(op.id, op.outcome, op.failBits, op.now)
	case opEvent:
		return "", false, q.Event(op.eventMask, op.now)
	case opAdvance:
		return "", false, q.Advance(op.now)
	case opRemove:
		return "", false, q.Remove(op.id)
	default:
		panic("unknown operation")
	}
}

func TestRandomAgainstNaiveModel(t *testing.T) {
	for iteration := 0; iteration < 2000; iteration++ {
		rng := rand.New(rand.NewPCG(uint64(iteration+1), 77))
		model := newNaiveModel(1+rng.Int64N(8), 8+rng.Int64N(40), 20+rng.Int64N(80))
		queue := mustNewQueue(t, model.base, model.max, model.retention)
		var trace []string

		for step := 0; step < 80; step++ {
			op := makeRandomOp(rng, model, step)
			actualID, actualOK, actualErr := applyActual(queue, op)
			modelID, modelOK, modelErr := model.apply(op)
			line := fmt.Sprintf("step=%d op=%s input={now:%d id:%q prio:%d outcome:%d fb:%d ev:%d} output={id:%q ok:%t err:%v} model={id:%q ok:%t err:%v} basis=%s",
				step, opName(op.kind), op.now, op.id, op.priority, op.outcome,
				op.failBits, op.eventMask, actualID, actualOK, actualErr, modelID, modelOK, modelErr,
				formatNaive(model))
			trace = append(trace, line)
			if !errors.Is(actualErr, modelErr) {
				for _, recorded := range trace {
					t.Log(recorded)
				}
				t.Fatalf("iteration %d step %d: error = %v, model error = %v", iteration, step, actualErr, modelErr)
			}
			if actualID != modelID || actualOK != modelOK {
				for _, recorded := range trace {
					t.Log(recorded)
				}
				t.Fatalf("iteration %d step %d: pop = %q,%t; model = %q,%t", iteration, step, actualID, actualOK, modelID, modelOK)
			}
			if modelErr == nil {
				assertQueuesMatch(t, queue, model, trace)
			}
		}
		t.Logf("seed=%d inputs/outputs/basis=%s", iteration+1, trace)
	}
}

func assertQueuesMatch(t *testing.T, q *SchedulingQueue, model *naiveModel, trace []string) {
	t.Helper()
	if len(q.pods) != len(model.pods) {
		t.Logf("%v", trace)
		t.Fatalf("pod count = %d, model = %d", len(q.pods), len(model.pods))
	}
	for id, expected := range model.pods {
		actual, exists := q.pods[id]
		if !exists {
			t.Logf("%v", trace)
			t.Fatalf("pod %q missing from actual queue", id)
		}
		if actual.priority != expected.priority || actual.addedAt != expected.addedAt ||
			actual.attempts != expected.attempts || actual.state != expected.state ||
			int64(actual.failBits) != expected.failBits || actual.expiresAt != expected.expires ||
			actual.parkedAt != expected.parked || actual.popSeq != expected.popSeq {
			t.Logf("%v", trace)
			t.Fatalf("pod %q = %+v, model = %+v", id, actual, expected)
		}
	}
	sizes := q.Sizes()
	expectedSizes := Sizes{
		Active:        countNaive(model, stateActive),
		Backoff:       countNaive(model, stateBackoff),
		Unschedulable: countNaive(model, stateUnschedulable),
		InFlight:      countNaive(model, stateInFlight),
	}
	if sizes != expectedSizes {
		t.Logf("%v", trace)
		t.Fatalf("sizes = %+v, model = %+v", sizes, expectedSizes)
	}
}

func opName(kind opKind) string {
	return []string{"Add", "Pop", "Done", "Event", "Advance", "Remove"}[kind]
}

func countNaive(model *naiveModel, want podState) int {
	count := 0
	for _, entry := range model.pods {
		if entry.state == want {
			count++
		}
	}
	return count
}

func formatNaive(model *naiveModel) string {
	return fmt.Sprintf("naive now=%d seq=%d pods=%d", model.now, model.seq, len(model.pods))
}
