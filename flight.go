package waitlist

import "sort"

type flight struct {
	key           FlightKey
	capacity      int
	confirmed     int
	pendingCount  int
	canceled      bool
	lastTime      int64
	entries       map[int64]*Entry
	passengerOpen map[string]int64
	waiting       *waitingLists
	pending       *pendingSet
	waitingCount  int
	journal       []settleChange
}

type settleChange struct {
	id        int64
	from      Status
	deadline  int64
	partySize int
}

func newFlight(key FlightKey, capacity, confirmed int, now int64) *flight {
	f := &flight{
		key:           key,
		capacity:      capacity,
		confirmed:     confirmed,
		lastTime:      now,
		entries:       make(map[int64]*Entry),
		passengerOpen: make(map[string]int64),
		waiting:       nil,
		pending:       newPendingSet(),
	}
	f.waiting = newWaitingLists(f.entries)
	return f
}

func (f *flight) available() int {
	return f.capacity - f.confirmed - f.pendingCount
}

func (f *flight) insertWaiting(entry *Entry) {
	f.waiting.appendEntry(entry.ID, entry.Priority)
	f.passengerOpen[entry.Passenger] = entry.ID
}

func (f *flight) removeOpen(entry *Entry) {
	if existing, ok := f.passengerOpen[entry.Passenger]; ok && existing == entry.ID {
		delete(f.passengerOpen, entry.Passenger)
	}
}

func (f *flight) previewOpenAndWaiting(now, window int64, passenger string) (bool, int) {
	type simulationEntry struct {
		id           int64
		passenger    string
		size         int
		priority     Priority
		registeredAt int64
		status       Status
		deadline     int64
	}
	entries := make(map[int64]*simulationEntry)
	pendingCount := f.pendingCount
	var pendingIDs []int64
	for id, source := range f.entries {
		entry := &simulationEntry{id: id, passenger: source.Passenger, size: source.PartySize, priority: source.Priority, registeredAt: source.RegisteredAt, status: source.Status, deadline: source.Deadline}
		entries[id] = entry
		if entry.status == StatusPending {
			pendingIDs = append(pendingIDs, id)
		}
	}
	for {
		earliest := int64(-1)
		for _, id := range pendingIDs {
			entry := entries[id]
			if entry.status == StatusPending && (earliest < 0 || entry.deadline < earliest) {
				earliest = entry.deadline
			}
		}
		if earliest < 0 || earliest > now {
			break
		}
		released := 0
		nextPending := pendingIDs[:0]
		for _, id := range pendingIDs {
			entry := entries[id]
			if entry.status == StatusPending && entry.deadline == earliest {
				entry.status = StatusExpired
				released += entry.size
			} else if entry.status == StatusPending {
				nextPending = append(nextPending, id)
			}
		}
		pendingCount -= released
		var waiting []*simulationEntry
		for _, entry := range entries {
			if entry.status == StatusWaiting && entry.registeredAt <= earliest {
				waiting = append(waiting, entry)
			}
		}
		sort.Slice(waiting, func(i, j int) bool {
			if waiting[i].priority != waiting[j].priority {
				return waiting[i].priority > waiting[j].priority
			}
			if waiting[i].registeredAt != waiting[j].registeredAt {
				return waiting[i].registeredAt < waiting[j].registeredAt
			}
			return waiting[i].id < waiting[j].id
		})
		available := f.capacity - f.confirmed - pendingCount
		for _, entry := range waiting {
			if available == 0 {
				break
			}
			if entry.size <= available {
				entry.status = StatusPending
				entry.deadline = earliest + window
				pendingCount += entry.size
				available -= entry.size
				nextPending = append(nextPending, entry.id)
			}
		}
		pendingIDs = nextPending
	}
	duplicate := false
	waitingCount := 0
	for _, entry := range entries {
		if entry.passenger == passenger && (entry.status == StatusWaiting || entry.status == StatusPending) {
			duplicate = true
		}
		if entry.status == StatusWaiting {
			waitingCount++
		}
	}
	return duplicate, waitingCount
}
