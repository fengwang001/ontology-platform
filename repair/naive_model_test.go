package repair

import (
	"fmt"
	"math/rand"
	"os"
	"sort"
	"testing"
)

type naiveTicket struct {
	id, tenant                                                             int64
	trade, building                                                        string
	level                                                                  Level
	submitted, levelStart, dispatched, confirmed, responseDue, completeDue int
	status                                                                 TicketStatus
	assignee                                                               int64
	rejections                                                             int
	rejectedBy                                                             map[int64]bool
}

type naiveWorker struct {
	id                       int64
	trades, buildings        map[string]bool
	capacity                 int
	urgent, inactive         bool
	registered, lastComplete int
	active                   map[int64]*naiveTicket
}

type naiveEvent struct {
	at             int
	ticket, worker int64
	kind           EventKind
	from, to       Level
}

type naiveModel struct {
	now                    int
	workers                map[int64]*naiveWorker
	tickets                map[int64]*naiveTicket
	events                 []naiveEvent
	nextTicket, nextWorker int64
	rejectLimit            int
	limits                 [4]Limits
}

func newNaiveModel(config Config) *naiveModel {
	return &naiveModel{
		workers:     make(map[int64]*naiveWorker),
		tickets:     make(map[int64]*naiveTicket),
		nextTicket:  1,
		nextWorker:  1,
		rejectLimit: config.RejectLimit,
		limits:      config.Limits,
	}
}

type naiveAction struct {
	name                   string
	now                    int
	ticket, worker, tenant int64
	trade, building        string
	level                  Level
	capacity               int
	urgent                 bool
}

func (m *naiveModel) reject(work *naiveTicket, worker *naiveWorker, now int) {
	delete(worker.active, work.id)
	work.status = StatusQueued
	work.assignee = 0
	work.dispatched = 0
	work.responseDue = 0
	if !work.rejectedBy[worker.id] {
		work.rejectedBy[worker.id] = true
		work.rejections++
	}
	m.events = append(m.events, naiveEvent{at: now, ticket: work.id, worker: worker.id, kind: EventRejected})
	from := work.level
	if work.level != Urgent && work.rejections >= m.rejectLimit {
		work.level++
		work.levelStart = now
		work.rejections = 0
		m.events = append(m.events, naiveEvent{at: now, ticket: work.id, worker: worker.id, kind: EventUpgraded, from: from, to: work.level})
	}
}

func (m *naiveModel) run(action naiveAction) (string, bool) {
	if action.now < 0 || action.capacity < 0 || action.tenant < 0 || action.ticket < 0 || action.worker < 0 {
		return "ErrInvalidArgument", false
	}
	if (action.name == "confirm" || action.name == "reject" || action.name == "complete") && (action.ticket == 0 || action.worker == 0) {
		return "ErrInvalidArgument", false
	}
	if action.name == "cancel" && action.ticket == 0 {
		return "ErrInvalidArgument", false
	}
	if action.name == "deactivate" && action.worker == 0 {
		return "ErrInvalidArgument", false
	}
	if action.now < m.now {
		return "ErrClockBack", false
	}
	m.now = action.now
	m.advance(action.now)

	switch action.name {
	case "register":
		id := m.nextWorker
		m.nextWorker++
		m.workers[id] = &naiveWorker{
			id:         id,
			trades:     map[string]bool{action.trade: true},
			buildings:  map[string]bool{action.building: true},
			capacity:   action.capacity,
			urgent:     action.urgent,
			registered: int(id),
			active:     make(map[int64]*naiveTicket),
		}
		return fmt.Sprintf("worker:%d", id), true
	case "submit":
		id := m.nextTicket
		m.nextTicket++
		m.tickets[id] = &naiveTicket{
			id: id, tenant: action.tenant, trade: action.trade, building: action.building,
			level: action.level, submitted: action.now, levelStart: action.now,
			status: StatusQueued, rejectedBy: make(map[int64]bool),
		}
		return fmt.Sprintf("ticket:%d", id), true
	case "dispatch":
		for _, work := range m.sortedQueued() {
			worker, victim := m.choose(work)
			if worker == nil {
				continue
			}
			if victim != nil {
				delete(worker.active, victim.id)
				victim.status = StatusQueued
				victim.assignee = 0
				victim.dispatched = 0
				victim.responseDue = 0
				m.events = append(m.events, naiveEvent{at: action.now, ticket: victim.id, worker: worker.id, kind: EventPreempted})
			}
			work.status = StatusAssigned
			work.assignee = worker.id
			work.dispatched = action.now
			work.responseDue = action.now + m.limits[work.level].Response
			worker.active[work.id] = work
			m.events = append(m.events, naiveEvent{at: action.now, ticket: work.id, worker: worker.id, kind: EventAssigned})
			return fmt.Sprintf("assign:%d->%d", work.id, worker.id), true
		}
		return "ErrNoCandidate", false
	case "confirm", "reject":
		work := m.tickets[action.ticket]
		worker := m.workers[action.worker]
		if work == nil || worker == nil {
			return "ErrNotFound", false
		}
		if work.status != StatusAssigned || work.assignee != action.worker || action.now > work.responseDue {
			return "ErrInvalidState", false
		}
		if action.name == "confirm" {
			work.status = StatusConfirmed
			work.confirmed = action.now
			work.completeDue = action.now + m.limits[work.level].Completion
			m.events = append(m.events, naiveEvent{at: action.now, ticket: work.id, worker: worker.id, kind: EventConfirmed})
			return "confirmed", true
		}
		m.reject(work, worker, action.now)
		return "rejected", true
	case "complete":
		work := m.tickets[action.ticket]
		worker := m.workers[action.worker]
		if work == nil || worker == nil {
			return "ErrNotFound", false
		}
		if work.status != StatusConfirmed && work.status != StatusOverdue {
			return "ErrInvalidState", false
		}
		if work.assignee != action.worker {
			return "ErrPermission", false
		}
		work.status = StatusCompleted
		delete(worker.active, work.id)
		worker.lastComplete = action.now
		m.events = append(m.events, naiveEvent{at: action.now, ticket: work.id, worker: worker.id, kind: EventCompleted})
		return "completed", true
	case "cancel":
		work := m.tickets[action.ticket]
		if work == nil {
			return "ErrNotFound", false
		}
		if work.status == StatusConfirmed || work.status == StatusOverdue || work.status == StatusCompleted || work.status == StatusCanceled {
			return "ErrInvalidState", false
		}
		if work.tenant != action.tenant {
			return "ErrPermission", false
		}
		if work.status == StatusAssigned {
			delete(m.workers[work.assignee].active, work.id)
		}
		work.status = StatusCanceled
		work.assignee = 0
		work.dispatched = 0
		work.responseDue = 0
		m.events = append(m.events, naiveEvent{at: action.now, ticket: work.id, kind: EventCanceled})
		return "canceled", true
	case "deactivate":
		worker := m.workers[action.worker]
		if worker == nil {
			return "ErrNotFound", false
		}
		if worker.inactive {
			return "ErrInvalidState", false
		}
		worker.inactive = true
		held := make([]*naiveTicket, 0, len(worker.active))
		for _, work := range worker.active {
			held = append(held, work)
		}
		for _, work := range held {
			if work.status == StatusAssigned {
				work.status = StatusQueued
				work.assignee = 0
				work.dispatched = 0
				work.responseDue = 0
				delete(worker.active, work.id)
				m.events = append(m.events, naiveEvent{at: action.now, ticket: work.id, worker: worker.id, kind: EventRequeued})
			}
		}
		return "inactive", true
	}
	return "unknown", false
}

func (m *naiveModel) advance(now int) {
	for {
		var due *naiveTicket
		for _, work := range m.tickets {
			if work.status == StatusAssigned && work.responseDue+1 <= now &&
				(due == nil || work.responseDue < due.responseDue || work.responseDue == due.responseDue && work.id < due.id) {
				due = work
			}
		}
		if due != nil {
			m.events = append(m.events, naiveEvent{at: due.responseDue + 1, ticket: due.id, worker: due.assignee, kind: EventResponseOverdue})
			m.reject(due, m.workers[due.assignee], due.responseDue+1)
			continue
		}
		for _, work := range m.tickets {
			if work.status == StatusConfirmed && work.completeDue+1 <= now &&
				(due == nil || work.completeDue < due.completeDue || work.completeDue == due.completeDue && work.id < due.id) {
				due = work
			}
		}
		if due == nil {
			return
		}
		due.status = StatusOverdue
		m.events = append(m.events, naiveEvent{at: due.completeDue + 1, ticket: due.id, worker: due.assignee, kind: EventCompletionOverdue})
	}
}

func (m *naiveModel) sortedQueued() []*naiveTicket {
	result := make([]*naiveTicket, 0)
	for _, work := range m.tickets {
		if work.status == StatusQueued {
			result = append(result, work)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].level != result[j].level {
			return result[i].level > result[j].level
		}
		if result[i].submitted != result[j].submitted {
			return result[i].submitted < result[j].submitted
		}
		return result[i].id < result[j].id
	})
	return result
}

func (m *naiveModel) choose(target *naiveTicket) (*naiveWorker, *naiveTicket) {
	var best *naiveWorker
	for _, worker := range m.workers {
		if m.eligible(worker, target) && len(worker.active) < worker.capacity && (best == nil || m.better(worker, best)) {
			best = worker
		}
	}
	if best != nil {
		return best, nil
	}
	if target.level != Urgent {
		return nil, nil
	}
	var victim *naiveTicket
	for _, worker := range m.workers {
		if !m.eligible(worker, target) || len(worker.active) < worker.capacity {
			continue
		}
		candidate := m.victim(worker)
		if candidate != nil && (best == nil || m.better(worker, best)) {
			best = worker
			victim = candidate
		}
	}
	return best, victim
}

func (m *naiveModel) eligible(worker *naiveWorker, work *naiveTicket) bool {
	return !worker.inactive && worker.trades[work.trade] && worker.buildings[work.building] &&
		!work.rejectedBy[worker.id] && (work.level != Urgent || worker.urgent)
}

func (m *naiveModel) better(left, right *naiveWorker) bool {
	if len(left.active) != len(right.active) {
		return len(left.active) < len(right.active)
	}
	if left.lastComplete != right.lastComplete {
		return left.lastComplete < right.lastComplete
	}
	return left.registered < right.registered
}

func (m *naiveModel) victim(worker *naiveWorker) *naiveTicket {
	var result *naiveTicket
	for _, work := range worker.active {
		if work.status == StatusAssigned && work.level != Urgent &&
			(result == nil || work.dispatched > result.dispatched || work.dispatched == result.dispatched && work.id > result.id) {
			result = work
		}
	}
	return result
}

func TestNaiveRandomModelComparison(t *testing.T) {
	config := testConfig()
	verbose := os.Getenv("REPAIR_TEST_LOG") == "1"
	rng := rand.New(rand.NewSource(20261006))
	trades := []string{"plumbing", "electric", "gas"}
	buildings := []string{"A", "B", "C"}
	for iteration := 0; iteration < 80; iteration++ {
		real, _ := New(config)
		model := newNaiveModel(config)
		now := 0
		for step := 0; step < 60; step++ {
			action := randomAction(rng, now, trades, buildings, int(model.nextWorker-1), int(model.nextTicket-1))
			now = action.now
			got, gotOK := runReal(real, action)
			want, wantOK := model.run(action)
			if verbose {
				t.Logf("iter=%d step=%d input=%+v output=%q(%v) reference=%q(%v)", iteration, step, action, got, gotOK, want, wantOK)
			}
			if gotOK != wantOK || got != want {
				t.Fatalf("result mismatch iter=%d step=%d action=%+v got=%q(%v) want=%q(%v)", iteration, step, action, got, gotOK, want, wantOK)
			}
			compareModels(t, real, model, iteration, step)
		}
	}
}

func randomAction(rng *rand.Rand, now int, trades, buildings []string, workers, tickets int) naiveAction {
	now += rng.Intn(4)
	action := naiveAction{
		name:     []string{"register", "submit", "dispatch", "confirm", "reject", "complete", "cancel", "deactivate"}[rng.Intn(8)],
		now:      now,
		trade:    trades[rng.Intn(len(trades))],
		building: buildings[rng.Intn(len(buildings))],
		level:    Level(rng.Intn(4)),
		capacity: 1 + rng.Intn(2),
		urgent:   rng.Intn(2) == 0,
		tenant:   int64(1 + rng.Intn(3)),
	}
	if workers > 0 {
		action.worker = int64(1 + rng.Intn(workers))
	}
	if tickets > 0 {
		action.ticket = int64(1 + rng.Intn(tickets))
	}
	return action
}

func runReal(s *Service, action naiveAction) (string, bool) {
	switch action.name {
	case "register":
		worker, err := s.RegisterContractor(action.now, []string{action.trade}, []string{action.building}, action.capacity, action.urgent)
		return resultID("worker", worker.ID, err)
	case "submit":
		work, err := s.Submit(action.now, action.tenant, action.trade, action.building, action.level)
		return resultID("ticket", work.ID, err)
	case "dispatch":
		work, err := s.DispatchNext(action.now)
		if err != nil {
			return errorName(err), false
		}
		return fmt.Sprintf("assign:%d->%d", work.ID, work.Assignee), true
	case "confirm":
		_, err := s.Confirm(action.now, action.ticket, action.worker)
		return resultBool("confirmed", err)
	case "reject":
		_, err := s.Reject(action.now, action.ticket, action.worker)
		return resultBool("rejected", err)
	case "complete":
		_, err := s.Complete(action.now, action.ticket, action.worker)
		return resultBool("completed", err)
	case "cancel":
		_, err := s.Cancel(action.now, action.ticket, action.tenant)
		return resultBool("canceled", err)
	case "deactivate":
		_, err := s.DeactivateContractor(action.now, action.worker)
		return resultBool("inactive", err)
	}
	return "unknown", false
}

func resultID(prefix string, id int64, err error) (string, bool) {
	if err != nil {
		return errorName(err), false
	}
	return fmt.Sprintf("%s:%d", prefix, id), true
}

func resultBool(value string, err error) (string, bool) {
	if err != nil {
		return errorName(err), false
	}
	return value, true
}

func errorName(err error) string {
	switch err {
	case ErrInvalidArgument:
		return "ErrInvalidArgument"
	case ErrClockBack:
		return "ErrClockBack"
	case ErrNotFound:
		return "ErrNotFound"
	case ErrInvalidState:
		return "ErrInvalidState"
	case ErrNoCandidate:
		return "ErrNoCandidate"
	case ErrPermission:
		return "ErrPermission"
	default:
		return err.Error()
	}
}

func compareModels(t *testing.T, real *Service, model *naiveModel, iteration, step int) {
	t.Helper()
	for id, expected := range model.tickets {
		actual, err := real.TicketAt(model.now, id)
		if err != nil {
			t.Fatalf("ticket %d query at iter=%d step=%d: %v", id, iteration, step, err)
		}
		if actual.Level != expected.level || actual.Status != expected.status || actual.Assignee != expected.assignee ||
			actual.SubmittedAt != expected.submitted || actual.LevelStartedAt != expected.levelStart ||
			actual.Rejections != expected.rejections || actual.ResponseDue != expected.responseDue || actual.CompleteDue != expected.completeDue {
			t.Fatalf("ticket %d mismatch iter=%d step=%d actual=%+v expected=%+v", id, iteration, step, actual, expected)
		}
	}
	actualEvents := real.Events()
	if len(actualEvents) != len(model.events) {
		t.Fatalf("event count iter=%d step=%d actual=%d expected=%d actual=%+v", iteration, step, len(actualEvents), len(model.events), actualEvents)
	}
	for i, expected := range model.events {
		actual := actualEvents[i]
		if actual.At != expected.at || actual.TicketID != expected.ticket || actual.ContractorID != expected.worker ||
			actual.Kind != expected.kind || actual.FromLevel != expected.from || actual.ToLevel != expected.to {
			t.Fatalf("event %d mismatch iter=%d step=%d actual=%+v expected=%+v", i, iteration, step, actual, expected)
		}
	}
}
