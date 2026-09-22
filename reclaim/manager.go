package reclaim

import (
	"container/heap"
	"sync"

	"ontology/snapshot"
	"ontology/txid"
)

// Reclaimable is one chain on which the reclaimer wants the store to drop
// every version older than Keeper.
type Reclaimable struct {
	Key    string
	Keeper txid.TxID
}

// Manager owns the water mark and the candidate heap.
//
// It is safe for concurrent use. The manager never touches version bodies;
// the store performs the actual chain surgery and reports outcomes back.
type Manager struct {
	mu        sync.Mutex
	registry  *snapshot.Registry
	pending   heap
	waterMark txid.TxID

	// examined counts versions actually inspected during Reclaim runs.
	// It is deliberately non-exported; tests read it via ExaminedCount.
	examined int
}

// NewManager builds a manager over the given snapshot registry.
func NewManager(reg *snapshot.Registry) *Manager {
	return &Manager{registry: reg}
}

// AddCandidate records that Shadowed on key was just shadowed by Cover.
// A zero Shadowed (first append to a key) adds nothing.
func (m *Manager) AddCandidate(key string, shadowed, cover txid.TxID) {
	if shadowed == txid.Zero {
		return
	}
	m.mu.Lock()
	heap.Push(&m.pending, candidate{key: key, shadowed: shadowed, cover: cover})
	m.mu.Unlock()
}

// WaterMark returns the current reclaim water mark: the largest transaction
// number below which every committed transaction is behind every open
// snapshot. It only ever moves upward.
func (m *Manager) WaterMark() txid.TxID {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.waterMark
}

// ExaminedCount returns the cumulative number of candidates inspected by
// Reclaim. Inspected, not reclaimed: blocked candidates count too.
func (m *Manager) ExaminedCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.examined
}

// ResetExamined zeroes the inspection counter (test support).
func (m *Manager) ResetExamined() {
	m.mu.Lock()
	m.examined = 0
	m.mu.Unlock()
}

// advanceWaterLocked moves the water mark to horizon-1 without ever
// decreasing it. With no open snapshot the horizon is the maximum TxID;
// the water mark then reaches max-1 (everything committed is behind it).
func (m *Manager) advanceWaterLocked(horizon txid.TxID) {
	var target txid.TxID
	if horizon > 0 {
		target = horizon - 1
	}
	if target > m.waterMark {
		m.waterMark = target
	}
}

// Reclaim returns chains safe to collect right now.
//
// Only heap entries whose cover is strictly behind the oldest open snapshot
// point are popped. Covers visible to every snapshot are returned for
// removal; covers still listed in some snapshot's active set are pushed back
// and stop the scan (the heap is cover-ordered, so nothing behind them could
// be ready either). The scan never visits untouched keys.
func (m *Manager) Reclaim() []Reclaimable {
	m.mu.Lock()
	defer m.mu.Unlock()

	horizon := m.registry.Horizon()
	m.advanceWaterLocked(horizon)

	var ready []Reclaimable
	var blocked []candidate
	for m.pending.Len() > 0 {
		top := m.pending[0]
		if !(top.cover < horizon) {
			break
		}
		heap.Pop(&m.pending)
		m.examined++
		if m.registry.GloballyVisible(top.cover) {
			ready = append(ready, Reclaimable{Key: top.key, Keeper: top.cover})
			continue
		}
		blocked = append(blocked, top)
	}
	for _, c := range blocked {
		heap.Push(&m.pending, c)
	}
	return ready
}
