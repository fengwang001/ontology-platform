package mv2pl

import "math/bits"

// keySet is a bitmask over key numbers (K <= 64), always consumed from
// the smallest key upwards so that cascades are deterministic.
type keySet uint64

func (s *keySet) add(k int) { *s |= 1 << uint(k) }

func (s *keySet) takeMin() (int, bool) {
	if *s == 0 {
		return 0, false
	}
	k := bits.TrailingZeros64(uint64(*s))
	*s &= ^(1 << uint(k))
	return k, true
}

// grantable reports whether txn may be granted mode on key right now,
// considering only locks already granted to other transactions.
func (m *Manager) grantable(key, txnID int, mode Mode) bool {
	for _, g := range m.granted[key] {
		if g.txn != txnID && !compatible(g.mode, mode) {
			return false
		}
	}
	return true
}

// setGrant records that txn holds mode on key, replacing any lock the
// same transaction already holds on that key.
func (m *Manager) setGrant(key, txnID int, mode Mode) {
	gs := m.granted[key]
	for i := range gs {
		if gs[i].txn == txnID {
			gs[i].mode = mode
			return
		}
	}
	m.granted[key] = append(gs, grant{txn: txnID, mode: mode})
}

// removeGrant drops every lock txn holds on key.
func (m *Manager) removeGrant(key, txnID int) {
	gs := m.granted[key]
	out := gs[:0]
	for _, g := range gs {
		if g.txn != txnID {
			out = append(out, g)
		}
	}
	m.granted[key] = out
}

// insertConversion places a C conversion request after the last
// conversion request already queued on key and before all ordinary
// requests.
func insertConversion(queue []request, req request) []request {
	pos := 0
	for pos < len(queue) && queue[pos].conv {
		pos++
	}
	queue = append(queue, request{})
	copy(queue[pos+1:], queue[pos:])
	queue[pos] = req
	return queue
}

// drain repeatedly takes the smallest pending key and grants queued
// requests on it from the head of the queue while they are compatible
// with the granted locks (strict FIFO: the first incompatible head
// stops the key). Grants that complete a certification commit or
// release further locks merge their keys back into the pending set.
func (m *Manager) drain(pending *keySet, events *[]Event) {
	for {
		key, ok := pending.takeMin()
		if !ok {
			return
		}
		for len(m.queues[key]) > 0 {
			req := m.queues[key][0]
			if !m.grantable(key, req.txn, req.mode) {
				break
			}
			m.queues[key] = m.queues[key][1:]
			tx := m.txns[req.txn]
			if req.conv {
				m.setGrant(key, req.txn, C)
				tx.locks[key] = C
				*events = append(*events, Event{Kind: EventGrant, Txn: req.txn, Key: key, Mode: C})
				if readyToCertify(tx) {
					m.finishCommit(req.txn, tx, pending, events)
				}
				continue
			}
			m.setGrant(key, req.txn, req.mode)
			tx.locks[key] = req.mode
			if req.mode == S {
				*events = append(*events, Event{Kind: EventGrant, Txn: req.txn, Key: key, Mode: S, Value: m.committed[key]})
			} else {
				tx.buffer[key] = req.val
				*events = append(*events, Event{Kind: EventGrant, Txn: req.txn, Key: key, Mode: X, Value: req.val})
			}
			tx.state = Active
		}
	}
}

// readyToCertify reports whether tx holds a C lock on every key it wrote.
func readyToCertify(tx *txn) bool {
	for key := range tx.buffer {
		if tx.locks[key] != C {
			return false
		}
	}
	return true
}

// finishCommit publishes the buffered writes of tx in ascending key
// order, emits the commit event, releases every lock of tx and moves it
// to the committed state. Released keys are merged into pending; the
// caller keeps draining.
func (m *Manager) finishCommit(txnID int, tx *txn, pending *keySet, events *[]Event) {
	commit := Event{Kind: EventCommit, Txn: txnID}
	for key := 0; key < m.keys; key++ {
		value, written := tx.buffer[key]
		if !written {
			continue
		}
		m.committed[key] = value
		commit.Writes = append(commit.Writes, WriteEntry{Key: key, Value: value})
	}
	*events = append(*events, commit)
	for key := range tx.locks {
		m.removeGrant(key, txnID)
		pending.add(key)
	}
	tx.locks = make(map[int]Mode)
	tx.buffer = make(map[int]int64)
	tx.state = Committed
}

// abortTxn removes every queued request of tx, releases all its locks,
// discards its buffer and moves it to the aborted state, then drains
// the keys touched by the removal/release.
func (m *Manager) abortTxn(txnID int, tx *txn, events *[]Event) {
	var pending keySet
	for key := 0; key < m.keys; key++ {
		queue := m.queues[key]
		kept := queue[:0]
		removed := false
		for _, req := range queue {
			if req.txn == txnID {
				removed = true
				continue
			}
			kept = append(kept, req)
		}
		if removed {
			m.queues[key] = kept
			pending.add(key)
		}
	}
	for key := range tx.locks {
		m.removeGrant(key, txnID)
		pending.add(key)
	}
	tx.locks = make(map[int]Mode)
	tx.buffer = make(map[int]int64)
	tx.state = Aborted
	m.drain(&pending, events)
}

// deadlocked builds the wait graph over every queued request of every
// key and reports whether txnID can reach itself. Each queued entry
// points to the holders of conflicting granted locks of other
// transactions on its key and to every other transaction's entry queued
// ahead of it on the same key.
func (m *Manager) deadlocked(txnID int) bool {
	edges := make(map[int][]int)
	add := func(from, to int) { edges[from] = append(edges[from], to) }
	for key := 0; key < m.keys; key++ {
		queue := m.queues[key]
		for i, req := range queue {
			for _, g := range m.granted[key] {
				if g.txn != req.txn && !compatible(g.mode, req.mode) {
					add(req.txn, g.txn)
				}
			}
			for j := 0; j < i; j++ {
				if queue[j].txn != req.txn {
					add(req.txn, queue[j].txn)
				}
			}
		}
	}
	seen := make(map[int]bool)
	stack := append([]int(nil), edges[txnID]...)
	for len(stack) > 0 {
		next := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if next == txnID {
			return true
		}
		if seen[next] {
			continue
		}
		seen[next] = true
		stack = append(stack, edges[next]...)
	}
	return false
}
