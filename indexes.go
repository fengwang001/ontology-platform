package ontology

import "container/heap"

type reuseEntry struct {
	authID  int
	expires int64
}

type reuseIndex []reuseEntry

func (h reuseIndex) Len() int { return len(h) }

func (h reuseIndex) Less(i, j int) bool {
	if h[i].expires != h[j].expires {
		return h[i].expires > h[j].expires
	}
	return h[i].authID < h[j].authID
}

func (h reuseIndex) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *reuseIndex) Push(value any) {
	*h = append(*h, value.(reuseEntry))
}

func (h *reuseIndex) Pop() any {
	old := *h
	entry := old[len(old)-1]
	*h = old[:len(old)-1]
	return entry
}

func (m *StateMachine) findReusableAuthorization(account, identifier string, now int64) int {
	byIdentifier := m.validIndexes[account]
	if byIdentifier == nil {
		return 0
	}
	index := byIdentifier[identifier]
	if index == nil {
		return 0
	}

	for index.Len() > 0 {
		entry := (*index)[0]
		auth := m.auths[entry.authID-1]
		m.lookupExamined++
		if auth.status != StatusValid || auth.expires <= now ||
			auth.account != account || auth.identifier != identifier {
			heap.Pop(index)
			continue
		}
		return entry.authID
	}
	return 0
}

func (m *StateMachine) insertValid(auth *authRecord) {
	byIdentifier := m.validIndexes[auth.account]
	if byIdentifier == nil {
		byIdentifier = make(map[string]*reuseIndex)
		m.validIndexes[auth.account] = byIdentifier
	}
	index := byIdentifier[auth.identifier]
	if index == nil {
		index = &reuseIndex{}
		byIdentifier[auth.identifier] = index
	}
	heap.Push(index, reuseEntry{authID: auth.id, expires: auth.expires})
}

func (m *StateMachine) removeValid(auth *authRecord) {
	byIdentifier := m.validIndexes[auth.account]
	if byIdentifier == nil {
		return
	}
	index := byIdentifier[auth.identifier]
	if index == nil {
		return
	}
	for i, entry := range *index {
		if entry.authID == auth.id {
			heap.Remove(index, i)
			break
		}
	}
}

func (m *StateMachine) addPending(account string, authID int) {
	byAccount := m.pendingSet[account]
	if byAccount == nil {
		byAccount = make(map[int]struct{})
		m.pendingSet[account] = byAccount
	}
	byAccount[authID] = struct{}{}
}

func (m *StateMachine) removePending(account string, authID int) {
	if byAccount := m.pendingSet[account]; byAccount != nil {
		delete(byAccount, authID)
	}
}

func (m *StateMachine) pendingCount(account string, now int64) int {
	byAccount := m.pendingSet[account]
	count := 0
	for authID := range byAccount {
		auth := m.auths[authID-1]
		if effectiveStatus(auth, now) == StatusPending {
			count++
		} else {
			delete(byAccount, authID)
		}
	}
	return count
}

func (m *StateMachine) activeFailureCount(account, identifier string, now int64) int {
	queue := m.failureQueue(account, identifier)
	for queue.head < len(queue.times) && queue.times[queue.head]+m.cfg.FailureWindow <= now {
		queue.head++
	}
	if queue.head > 0 {
		remaining := append([]int64(nil), queue.times[queue.head:]...)
		queue.times = remaining
		queue.head = 0
	}
	return len(queue.times)
}

func (m *StateMachine) appendFailure(account, identifier string, now int64) {
	queue := m.failureQueue(account, identifier)
	queue.times = append(queue.times, now)
}

func (m *StateMachine) failureQueue(account, identifier string) *failureQueue {
	byIdentifier := m.failures[account]
	if byIdentifier == nil {
		byIdentifier = make(map[string]*failureQueue)
		m.failures[account] = byIdentifier
	}
	queue := byIdentifier[identifier]
	if queue == nil {
		queue = &failureQueue{}
		byIdentifier[identifier] = queue
	}
	return queue
}
