// Package ranking incrementally maintains positions, competition ranks, and
// dense ranks under insertion and deletion.
package ranking

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"sync"
)

var (
	// ErrEmptyID indicates that an element ID is the empty string.
	ErrEmptyID = errors.New("ranking: element id must not be empty")
	// ErrDuplicateID indicates that an inserted ID already exists.
	ErrDuplicateID = errors.New("ranking: element id already exists")
	// ErrIDNotFound indicates that a deleted ID does not exist.
	ErrIDNotFound = errors.New("ranking: element id not found")
)

// Entry is one element together with its complete ranking triple.
type Entry struct {
	ID        string
	Score     int
	Position  int
	Rank      int
	DenseRank int
}

// Snapshot is a point-in-time ordered copy of all maintained entries.
type Snapshot struct {
	Entries []Entry
}

// CheckReport describes whether an internal consistency check passed.
type CheckReport struct {
	OK     bool
	Reason string
}

type element struct {
	id    string
	score int
}

// Maintainer stores elements ordered by descending score and ascending ID.
// It is safe for concurrent use after creation.
type Maintainer struct {
	mu       sync.RWMutex
	elements []element
	indexes  map[string]int
}

// NewMaintainer creates an empty ranking maintainer.
func NewMaintainer() *Maintainer {
	return &Maintainer{indexes: make(map[string]int)}
}

// Insert adds an element and returns its ranking triple. Empty IDs and
// duplicate IDs are rejected without changing the maintained state.
func (m *Maintainer) Insert(id string, score int) (Entry, error) {
	if id == "" {
		return Entry{}, ErrEmptyID
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureIndexesLocked()

	if _, exists := m.indexes[id]; exists {
		return Entry{}, fmt.Errorf("%w: %q", ErrDuplicateID, id)
	}

	position := m.insertPosition(score, id)
	newElement := element{id: id, score: score}
	m.elements = slices.Insert(m.elements, position, newElement)
	m.rebuildIndexes()

	return m.entryAtLocked(position), nil
}

// Delete removes an element. Empty IDs and missing IDs are rejected without
// changing the maintained state.
func (m *Maintainer) Delete(id string) error {
	if id == "" {
		return ErrEmptyID
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureIndexesLocked()

	position, exists := m.indexes[id]
	if !exists {
		return fmt.Errorf("%w: %q", ErrIDNotFound, id)
	}

	m.elements = append(m.elements[:position], m.elements[position+1:]...)
	m.rebuildIndexes()
	return nil
}

// Get returns the current ranking triple for the requested ID.
func (m *Maintainer) Get(id string) (Entry, bool) {
	if id == "" {
		return Entry{}, false
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.indexes == nil {
		return Entry{}, false
	}

	position, exists := m.indexes[id]
	if !exists {
		return Entry{}, false
	}
	return m.entryAtLocked(position), true
}

// List returns a snapshot ordered by descending score and ascending ID.
func (m *Maintainer) List() Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return Snapshot{Entries: m.entriesLocked()}
}

// SelfCheck validates ordering, index integrity, positions, ranks, and dense
// ranks. It may run concurrently with other read operations.
func (m *Maintainer) SelfCheck() CheckReport {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.indexes == nil {
		return CheckReport{OK: true}
	}

	if len(m.elements) != len(m.indexes) {
		return CheckReport{Reason: "ordered elements and index map have different sizes"}
	}

	seenPositions := make(map[int]string, len(m.elements))
	for position, elem := range m.elements {
		if elem.id == "" {
			return CheckReport{Reason: fmt.Sprintf("empty id at position %d", position+1)}
		}
		mappedPosition, exists := m.indexes[elem.id]
		if !exists {
			return CheckReport{Reason: fmt.Sprintf("id %q missing from index", elem.id)}
		}
		if mappedPosition != position {
			return CheckReport{Reason: fmt.Sprintf("id %q maps to position %d, want %d", elem.id, mappedPosition+1, position+1)}
		}
		if _, duplicate := seenPositions[mappedPosition]; duplicate {
			return CheckReport{Reason: fmt.Sprintf("duplicate index position %d", mappedPosition+1)}
		}
		seenPositions[mappedPosition] = elem.id
	}

	for id, position := range m.indexes {
		if position < 0 || position >= len(m.elements) {
			return CheckReport{Reason: fmt.Sprintf("id %q has out-of-range position %d", id, position+1)}
		}
		if m.elements[position].id != id {
			return CheckReport{Reason: fmt.Sprintf("id %q index points to %q", id, m.elements[position].id)}
		}
	}

	if !sort.SliceIsSorted(m.elements, func(i, j int) bool {
		return less(m.elements[i], m.elements[j])
	}) {
		return CheckReport{Reason: "elements are not ordered by score descending and id ascending"}
	}

	expectedRank := 1
	expectedDenseRank := 1
	entries := m.entriesLocked()
	for position := range m.elements {
		entry := entries[position]
		if entry.Position != position+1 {
			return CheckReport{Reason: fmt.Sprintf("%q has position %d, want %d", entry.ID, entry.Position, position+1)}
		}
		if position == 0 || m.elements[position].score != m.elements[position-1].score {
			if position > 0 {
				expectedRank = position + 1
				expectedDenseRank++
			}
		}
		if entry.Rank != expectedRank {
			return CheckReport{Reason: fmt.Sprintf("%q has rank %d, want %d", entry.ID, entry.Rank, expectedRank)}
		}
		if entry.DenseRank != expectedDenseRank {
			return CheckReport{Reason: fmt.Sprintf("%q has dense rank %d, want %d", entry.ID, entry.DenseRank, expectedDenseRank)}
		}
	}

	return CheckReport{OK: true}
}

func (m *Maintainer) entriesLocked() []Entry {
	entries := make([]Entry, len(m.elements))
	rank := 1
	denseRank := 1
	for position, elem := range m.elements {
		if position > 0 && elem.score != m.elements[position-1].score {
			rank = position + 1
			denseRank++
		}
		entries[position] = Entry{
			ID:        elem.id,
			Score:     elem.score,
			Position:  position + 1,
			Rank:      rank,
			DenseRank: denseRank,
		}
	}
	return entries
}

func less(a, b element) bool {
	if a.score != b.score {
		return a.score > b.score
	}
	return a.id < b.id
}

func (m *Maintainer) insertPosition(score int, id string) int {
	target := element{id: id, score: score}
	return sort.Search(len(m.elements), func(i int) bool {
		return less(target, m.elements[i])
	})
}

func (m *Maintainer) rebuildIndexes() {
	clear(m.indexes)
	for position, elem := range m.elements {
		m.indexes[elem.id] = position
	}
}

func (m *Maintainer) ensureIndexesLocked() {
	if m.indexes == nil {
		m.indexes = make(map[string]int)
	}
}

func (m *Maintainer) entryAtLocked(position int) Entry {
	score := m.elements[position].score
	rank := position + 1
	for i := position - 1; i >= 0; i-- {
		if m.elements[i].score == score {
			rank = i + 1
			continue
		}
		break
	}

	denseRank := 1
	for i := 0; i < position; i++ {
		if m.elements[i].score != m.elements[i+1].score {
			denseRank++
		}
	}

	return Entry{
		ID:        m.elements[position].id,
		Score:     score,
		Position:  position + 1,
		Rank:      rank,
		DenseRank: denseRank,
	}
}
