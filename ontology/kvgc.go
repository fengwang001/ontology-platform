package ontology

import (
	"errors"
	"sync"
)

var (
	ErrInvalidArgument  = errors.New("invalid argument")
	ErrExpired          = errors.New("timestamp is before the safe point")
	ErrDuplicate        = errors.New("duplicate record")
	ErrFull             = errors.New("record limit reached")
	ErrRolledBack       = errors.New("safe point cannot roll back")
	ErrSnapshotBlocked  = errors.New("safe point is blocked by a snapshot")
	ErrSnapshotNotFound = errors.New("snapshot not found")
)

type RecordType byte

const (
	TypePut      RecordType = 'P'
	TypeDelete   RecordType = 'D'
	TypeLock     RecordType = 'L'
	TypeRollback RecordType = 'R'
)

const (
	minTimestamp   int64 = 1
	maxTimestamp   int64 = 1_000_000_000_000_000
	maxRecordLimit       = 1_000_000
)

type record struct {
	typ   RecordType
	ts    int64
	value []byte
}

type keyRecords struct {
	records []record
}

type KVGC struct {
	mu sync.Mutex

	limit     int
	safePoint int64
	total     int
	keys      map[string]*keyRecords
	keyOrder  keyTree

	snapshots      map[int64]int64
	nextSnapshotID int64

	cursor    []byte
	hasCursor bool

	accessCounts map[string]int64
}

func NewKVGC(limit int) *KVGC {
	if limit < 1 || limit > maxRecordLimit {
		panic(ErrInvalidArgument)
	}
	return &KVGC{
		limit:        limit,
		keys:         make(map[string]*keyRecords),
		snapshots:    make(map[int64]int64),
		accessCounts: make(map[string]int64),
	}
}

func (k *KVGC) Write(key []byte, typ RecordType, ts int64, value []byte) error {
	if len(key) == 0 || !validRecordType(typ) || ts < minTimestamp || ts > maxTimestamp ||
		(typ != TypePut && len(value) > 0) {
		return ErrInvalidArgument
	}

	k.mu.Lock()
	defer k.mu.Unlock()

	if ts <= k.safePoint {
		return ErrExpired
	}

	keyString := string(key)
	state := k.keys[keyString]
	if state != nil {
		if _, found := findRecord(state.records, ts); found {
			return ErrDuplicate
		}
	}
	if k.total >= k.limit {
		return ErrFull
	}

	copiedValue := append([]byte(nil), value...)
	newRecord := record{typ: typ, ts: ts, value: copiedValue}
	if state == nil {
		state = &keyRecords{}
		k.keys[keyString] = state
		k.keyOrder.insert(key)
	}

	insertAt, _ := findRecord(state.records, ts)
	state.records = append(state.records, record{})
	copy(state.records[insertAt+1:], state.records[insertAt:])
	state.records[insertAt] = newRecord
	k.total++
	return nil
}

func (k *KVGC) Get(key []byte, ts int64) ([]byte, bool, error) {
	if len(key) == 0 || ts < minTimestamp || ts > maxTimestamp {
		return nil, false, ErrInvalidArgument
	}

	k.mu.Lock()
	defer k.mu.Unlock()

	if ts < k.safePoint {
		return nil, false, ErrExpired
	}

	state := k.keys[string(key)]
	if state == nil {
		return nil, false, nil
	}
	for index := len(state.records) - 1; index >= 0; index-- {
		current := state.records[index]
		if current.ts > ts {
			continue
		}
		if current.typ == TypePut {
			if len(current.value) == 0 {
				return []byte{}, true, nil
			}
			return append([]byte(nil), current.value...), true, nil
		}
		if current.typ == TypeDelete {
			return nil, false, nil
		}
	}
	return nil, false, nil
}

func (k *KVGC) OpenSnapshot(ts int64) (int64, error) {
	if ts < minTimestamp || ts > maxTimestamp {
		return 0, ErrInvalidArgument
	}

	k.mu.Lock()
	defer k.mu.Unlock()

	if ts < k.safePoint {
		return 0, ErrExpired
	}

	id := k.nextSnapshotID + 1
	k.nextSnapshotID = id
	k.snapshots[id] = ts
	return id, nil
}

func (k *KVGC) CloseSnapshot(id int64) error {
	k.mu.Lock()
	defer k.mu.Unlock()

	if _, ok := k.snapshots[id]; !ok {
		return ErrSnapshotNotFound
	}
	delete(k.snapshots, id)
	return nil
}

func (k *KVGC) SetSafePoint(sp int64) error {
	if sp < 0 || sp > maxTimestamp {
		return ErrInvalidArgument
	}

	k.mu.Lock()
	defer k.mu.Unlock()

	if sp < k.safePoint {
		return ErrRolledBack
	}

	if len(k.snapshots) > 0 {
		minimum := maxTimestamp
		for _, snapshotTS := range k.snapshots {
			if snapshotTS < minimum {
				minimum = snapshotTS
			}
		}
		if sp > minimum {
			return ErrSnapshotBlocked
		}
	}

	k.safePoint = sp
	k.cursor = nil
	k.hasCursor = false
	return nil
}

func (k *KVGC) GCStep(n int) (int, error) {
	if n < 1 {
		return 0, ErrInvalidArgument
	}

	k.mu.Lock()
	defer k.mu.Unlock()

	cursor := []byte(nil)
	if k.hasCursor {
		cursor = k.cursor
	}

	var selected [][]byte
	k.keyOrder.firstAfter(cursor, n, func(key []byte) bool {
		selected = append(selected, append([]byte(nil), key...))
		return true
	})

	for index, key := range selected {
		keyString := string(key)
		k.processKey(keyString, k.safePoint)
		if index == len(selected)-1 {
			k.cursor = key
			k.hasCursor = true
		}
	}
	return len(selected), nil
}

func validRecordType(typ RecordType) bool {
	switch typ {
	case TypePut, TypeDelete, TypeLock, TypeRollback:
		return true
	default:
		return false
	}
}

func findRecord(records []record, ts int64) (int, bool) {
	left, right := 0, len(records)
	for left < right {
		middle := left + (right-left)/2
		if records[middle].ts < ts {
			left = middle + 1
		} else {
			right = middle
		}
	}
	if left < len(records) && records[left].ts == ts {
		return left, true
	}
	return left, false
}

func (k *KVGC) processKey(key string, sp int64) {
	state := k.keys[key]
	if state == nil {
		return
	}

	visibleCount := 0
	for _, current := range state.records {
		if current.ts <= sp {
			visibleCount++
		}
	}
	k.accessCounts[key] += int64(visibleCount)

	withoutLockRollback := make([]record, 0, len(state.records))
	var boundary record
	hasBoundary := false
	for _, current := range state.records {
		if current.ts <= sp && (current.typ == TypeLock || current.typ == TypeRollback) {
			continue
		}
		withoutLockRollback = append(withoutLockRollback, current)
		if current.ts <= sp && (current.typ == TypePut || current.typ == TypeDelete) {
			if !hasBoundary || current.ts > boundary.ts {
				boundary = current
				hasBoundary = true
			}
		}
	}

	remaining := make([]record, 0, len(withoutLockRollback))
	for _, current := range withoutLockRollback {
		if current.ts > sp {
			remaining = append(remaining, current)
			continue
		}
		if hasBoundary && current.ts == boundary.ts && current.typ == boundary.typ &&
			boundary.typ == TypePut {
			remaining = append(remaining, current)
		}
	}

	removedCount := len(state.records) - len(remaining)
	if len(remaining) == 0 {
		delete(k.keys, key)
		k.keyOrder.delete([]byte(key))
	} else {
		state.records = remaining
	}
	k.total -= removedCount
}
