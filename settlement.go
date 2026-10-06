package waitlist

func (f *flight) settle(now, confirmationWindow int64) {
	for {
		next := f.pending.peek()
		if next == nil || next.deadline > now {
			return
		}
		expireAt := next.deadline
		expiredSize := 0
		for f.pending.peek() != nil && f.pending.peek().deadline == expireAt {
			entry := f.entries[f.pending.pop().id]
			f.journal = append(f.journal, settleChange{id: entry.ID, from: StatusPending, deadline: entry.Deadline, partySize: entry.PartySize})
			entry.Status = StatusExpired
			f.removeOpen(entry)
			expiredSize += entry.PartySize
		}
		f.pendingCount -= expiredSize
		f.fulfill(expireAt, confirmationWindow)
	}
}

func (f *flight) fulfill(releaseAt, confirmationWindow int64) {
	if f.available() <= 0 {
		return
	}
	type candidate struct {
		id   int64
		size int
	}
	candidates := make([]candidate, 0)
	visited := 0
	available := f.available()

	for priority := int(PriorityHigh); priority >= int(PriorityLow) && available > 0; priority-- {
		f.waiting.scan(releaseAt, Priority(priority), func(entry *Entry) bool {
			id := entry.ID
			visited++
			if entry.PartySize <= available {
				candidates = append(candidates, candidate{id: id, size: entry.PartySize})
				available -= entry.PartySize
			}
			return available == 0
		})
	}

	for _, selected := range candidates {
		entry := f.entries[selected.id]
		f.waiting.remove(entry.ID, entry.Priority)
		f.journal = append(f.journal, settleChange{id: entry.ID, from: StatusWaiting, deadline: entry.Deadline, partySize: selected.size})
		entry.Status = StatusPending
		entry.Deadline = releaseAt + confirmationWindow
		f.pending.push(entry.ID, entry.Deadline)
		f.pendingCount += selected.size
		f.waitingCount--
	}
}

func (f *flight) commitSettlement() {
	f.journal = nil
}
