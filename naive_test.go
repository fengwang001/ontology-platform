package waitlist

import (
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"
	"testing"
)

type naiveEntry struct {
	id           int64
	passenger    string
	partySize    int
	priority     Priority
	registeredAt int64
	status       Status
	deadline     int64
}

type naiveModel struct {
	capacity  int
	confirmed int
	entries   map[int64]*naiveEntry
	byName    map[string]int64
	nextID    int64
}

func newNaiveModel(capacity, confirmed int) *naiveModel {
	return &naiveModel{capacity: capacity, confirmed: confirmed, entries: map[int64]*naiveEntry{}, byName: map[string]int64{}, nextID: 1}
}

func naivePending(m *naiveModel) int {
	total := 0
	for _, e := range m.entries {
		if e.status == StatusPending {
			total += e.partySize
		}
	}
	return total
}

func naiveAvailable(m *naiveModel) int {
	return m.capacity - m.confirmed - naivePending(m)
}

func naiveFulfill(m *naiveModel, at, window int64) {
	if naiveAvailable(m) <= 0 {
		return
	}
	var waiting []*naiveEntry
	for _, e := range m.entries {
		if e.status == StatusWaiting && e.registeredAt <= at {
			waiting = append(waiting, e)
		}
	}
	sort.Slice(waiting, func(i, j int) bool {
		a, b := waiting[i], waiting[j]
		if a.priority != b.priority {
			return a.priority > b.priority
		}
		if a.registeredAt != b.registeredAt {
			return a.registeredAt < b.registeredAt
		}
		return a.id < b.id
	})
	free := naiveAvailable(m)
	for _, e := range waiting {
		if free == 0 {
			break
		}
		if e.partySize <= free {
			e.status = StatusPending
			e.deadline = at + window
			free -= e.partySize
		}
	}
}

func naiveSettle(m *naiveModel, now, window int64) {
	for {
		at := int64(-1)
		for _, e := range m.entries {
			if e.status == StatusPending && (at < 0 || e.deadline < at) {
				at = e.deadline
			}
		}
		if at < 0 || at > now {
			return
		}
		for _, e := range m.entries {
			if e.status == StatusPending && e.deadline == at {
				e.status = StatusExpired
				delete(m.byName, e.passenger)
			}
		}
		naiveFulfill(m, at, window)
	}
}

func naivePreview(m *naiveModel, now, window int64, passenger string) (bool, int) {
	simulated := make(map[int64]*naiveEntry, len(m.entries))
	for id, entry := range m.entries {
		copyEntry := *entry
		simulated[id] = &copyEntry
	}
	for {
		at := int64(-1)
		for _, entry := range simulated {
			if entry.status == StatusPending && (at < 0 || entry.deadline < at) {
				at = entry.deadline
			}
		}
		if at < 0 || at > now {
			break
		}
		for _, entry := range simulated {
			if entry.status == StatusPending && entry.deadline == at {
				entry.status = StatusExpired
			}
		}
		simulation := &naiveModel{capacity: m.capacity, confirmed: m.confirmed, entries: simulated, byName: m.byName}
		naiveFulfill(simulation, at, window)
		m.capacity, m.confirmed = simulation.capacity, simulation.confirmed
	}
	duplicate := false
	waitingCount := 0
	for _, entry := range simulated {
		if entry.passenger == passenger && (entry.status == StatusWaiting || entry.status == StatusPending) {
			duplicate = true
		}
		if entry.status == StatusWaiting {
			waitingCount++
		}
	}
	return duplicate, waitingCount
}

var comparisonLog *strings.Builder

func compareWithNaive(t *testing.T, s *System, m *naiveModel, key FlightKey, now int64, log *strings.Builder) {
	comparisonLog = log
	t.Helper()
	state, err := s.Snapshot(now, key)
	if err != nil {
		t.Fatal(err)
	}
	if state.Capacity != m.capacity || state.Confirmed != m.confirmed || state.PendingCount != naivePending(m) {
		naiveEntries := make(map[int64]naiveEntry, len(m.entries))
		for id, entry := range m.entries {
			naiveEntries[id] = *entry
		}
		t.Fatalf("counts differ: system=%+v naive cap=%d confirmed=%d pending=%d entries=%+v\n%s", state, m.capacity, m.confirmed, naivePending(m), naiveEntries, comparisonLog.String())
	}
	for id, want := range m.entries {
		got := state.Entries[id]
		if got.Status != want.status || got.Deadline != want.deadline || got.Priority != want.priority {
			t.Fatalf("entry %d differs: got=%+v want=%+v", id, got, want)
		}
	}
	var waiting []int64
	for _, e := range m.entries {
		if e.status == StatusWaiting {
			waiting = append(waiting, e.id)
		}
	}
	sort.Slice(waiting, func(i, j int) bool {
		a, b := m.entries[waiting[i]], m.entries[waiting[j]]
		if a.priority != b.priority {
			return a.priority > b.priority
		}
		if a.registeredAt != b.registeredAt {
			return a.registeredAt < b.registeredAt
		}
		return a.id < b.id
	})
	if fmt.Sprint(waiting) != fmt.Sprint(state.WaitingOrder) {
		t.Fatalf("waiting order differs: naive=%v system=%v\n%s", waiting, state.WaitingOrder, log.String())
	}
}

func TestRandomAgainstNaiveModel(t *testing.T) {
	const sequences, length, maxQueue, window = 60, 120, 10, int64(7)
	var log strings.Builder
	for seed := int64(1); seed <= sequences; seed++ {
		r := rand.New(rand.NewPCG(uint64(seed), 1454))
		key := FlightKey{FlightID: fmt.Sprintf("F%d", seed), Cabin: "Y"}
		confirmed := r.IntN(3)
		capacity := confirmed + r.IntN(3)
		if capacity == 0 {
			capacity = 1
		}
		system, err := NewSystem(Config{MaxQueueEntries: maxQueue, ConfirmationWindow: window})
		if err != nil {
			t.Fatal(err)
		}
		if err := system.AddFlight(0, key, capacity, confirmed); err != nil {
			t.Fatal(err)
		}
		model := newNaiveModel(capacity, confirmed)
		now := int64(0)
		names := []string{}
		fmt.Fprintf(&log, "sequence=%d capacity=%d confirmed=%d\n", seed, capacity, confirmed)

		for step := 0; step < length; step++ {
			now += int64(r.IntN(3))
			name := fmt.Sprintf("p%d", r.IntN(14))
			if !contains(names, name) {
				names = append(names, name)
			}
			size := 1 + r.IntN(9)
			priority := Priority(r.IntN(3))
			id := model.byName[name]
			entry := model.entries[id]
			kind := r.IntN(8)
			op := fmt.Sprintf("kind=%d t=%d passenger=%s id=%d size=%d priority=%d", kind, now, name, id, size, priority)
			var gotID int64
			var gotErr error
			switch kind {
			case 0, 1, 2:
				duplicate, waitingCount := naivePreview(model, now, window, name)
				wantCode := ErrorCode("")
				if duplicate {
					wantCode = ErrDuplicateEntry
				} else if waitingCount >= maxQueue {
					wantCode = ErrQueueFull
				}
				gotID, gotErr = system.Register(now, key, name, size, priority)
				if codeOf(gotErr) != wantCode {
					t.Fatalf("register mismatch: %s got=%v want=%s\n%s", op, gotErr, wantCode, log.String())
				}
				if gotErr == nil {
					naiveSettle(model, now, window)
					model.entries[gotID] = &naiveEntry{id: gotID, passenger: name, partySize: size, priority: priority, registeredAt: now, status: StatusWaiting}
					model.byName[name] = gotID
				}
			case 3:
				want := ErrEntryNotFound
				if id == 0 {
					want = ErrInvalidArgument
				}
				if entry != nil && (entry.status == StatusWaiting || entry.status == StatusPending) {
					if entry.status == StatusPending && entry.deadline <= now {
						want = ErrInvalidStatus
					} else {
						want = ""
					}
				} else if entry != nil {
					want = ErrInvalidStatus
				}
				gotErr = system.Withdraw(now, id)
				if codeOf(gotErr) != want {
					t.Fatalf("withdraw mismatch: %s got=%v want=%s\n%s", op, gotErr, want, log.String())
				}
				if gotErr == nil {
					naiveSettle(model, now, window)
					if entry.status == StatusWaiting {
						entry.status = StatusWithdrawn
						delete(model.byName, name)
					}
					if entry.status == StatusPending {
						entry.status = StatusWithdrawn
						delete(model.byName, name)
						naiveFulfill(model, now, window)
					}
				}
			case 4:
				want := ErrEntryNotFound
				if id == 0 {
					want = ErrInvalidArgument
				}
				if entry != nil && entry.status == StatusWaiting {
					want = ""
				} else if entry != nil {
					want = ErrInvalidStatus
				}
				gotErr = system.ChangePriority(now, id, priority)
				if codeOf(gotErr) != want {
					t.Fatalf("priority mismatch: %s got=%v want=%s\n%s", op, gotErr, want, log.String())
				}
				if gotErr == nil {
					naiveSettle(model, now, window)
					entry.priority = priority
				}
			case 5:
				want := ErrEntryNotFound
				if id == 0 {
					want = ErrInvalidArgument
				}
				if entry != nil && entry.status == StatusPending {
					if entry.deadline <= now {
						want = ErrInvalidStatus
					} else {
						want = ""
					}
				} else if entry != nil {
					want = ErrInvalidStatus
				}
				gotErr = system.Confirm(now, id)
				if codeOf(gotErr) != want {
					t.Fatalf("confirm mismatch: %s got=%v want=%s\n%s", op, gotErr, want, log.String())
				}
				if gotErr == nil {
					naiveSettle(model, now, window)
					entry.status = StatusConfirmed
					model.confirmed += entry.partySize
					delete(model.byName, name)
				}
			case 6:
				want := ErrEntryNotFound
				if id == 0 {
					want = ErrInvalidArgument
				}
				if entry != nil && entry.status == StatusConfirmed {
					want = ""
				} else if entry != nil {
					want = ErrInvalidStatus
				}
				gotErr = system.CancelConfirmed(now, id)
				if codeOf(gotErr) != want {
					t.Fatalf("cancel confirmed mismatch: %s got=%v want=%s\n%s", op, gotErr, want, log.String())
				}
				if gotErr == nil {
					naiveSettle(model, now, window)
					entry.status = StatusWithdrawn
					model.confirmed -= entry.partySize
					naiveFulfill(model, now, window)
				}
			default:
				naiveSettle(model, now, window)
				newCapacity := 1 + r.IntN(10)
				increased := newCapacity > model.capacity
				gotErr = system.AdjustCapacity(now, key, newCapacity)
				if gotErr != nil {
					t.Fatalf("capacity: %s %v", op, gotErr)
				}
				model.capacity = newCapacity
				if increased {
					naiveFulfill(model, now, window)
				}
			}
			fmt.Fprintf(&log, "step=%d %s -> id=%d err=%v; compared settled states\n", step, op, gotID, gotErr)
			if gotErr != nil {
				naiveSettle(model, now, window)
			}
			compareWithNaive(t, system, model, key, now, &log)
		}
	}
	t.Log(&log)
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func codeOf(err error) ErrorCode {
	if err == nil {
		return ""
	}
	callErr, ok := err.(*CallError)
	if !ok {
		return "unknown"
	}
	return callErr.Code
}
