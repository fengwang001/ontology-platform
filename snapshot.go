package ontology

import (
	"errors"
	"math"
	"sync"
)

const (
	maxKey            = 1_000_000_000
	maxCommitKeys     = 64
	maxBucketCount    = 1024
	maxBucketCapacity = 16
)

var (
	ErrInvalidConfig      = errors.New("invalid snapshot conflict table configuration")
	ErrUnknownTransaction = errors.New("unknown transaction")
	ErrTooManyCommitKeys  = errors.New("commit write set has more than 64 keys")
	ErrInvalidCommitKey   = errors.New("commit key is outside [0, 10^9]")
)

type CommitKind int

const (
	CommitRejected CommitKind = iota
	CommitOk
	CommitConflict
	CommitWatermark
)

type CommitResult struct {
	Kind      CommitKind
	Timestamp int64
	Reason    RejectReason
}

type RejectReason int

const (
	RejectNone RejectReason = iota
	RejectUnknownTransaction
	RejectTooManyKeys
	RejectInvalidKey
)

type tableEntry struct {
	key       int64
	timestamp int64
}

type SnapshotCommitManager struct {
	mu            sync.Mutex
	buckets       [][]tableEntry
	capacity      int
	active        map[int64]struct{}
	nextTimestamp int64
	watermark     int64
	probeCount    int64
}

func NewSnapshotCommitManager(buckets, capacity int) (*SnapshotCommitManager, error) {
	if buckets < 1 || buckets > maxBucketCount || capacity < 1 || capacity > maxBucketCapacity {
		return nil, ErrInvalidConfig
	}

	return &SnapshotCommitManager{
		buckets:  make([][]tableEntry, buckets),
		capacity: capacity,
		active:   make(map[int64]struct{}),
	}, nil
}

func (m *SnapshotCommitManager) Begin() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.nextTimestamp++
	m.active[m.nextTimestamp] = struct{}{}
	return m.nextTimestamp
}

func (m *SnapshotCommitManager) Commit(sequence int64, keys []int64) CommitResult {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, active := m.active[sequence]; !active {
		return CommitResult{Kind: CommitRejected, Reason: RejectUnknownTransaction}
	}

	if len(keys) > maxCommitKeys {
		return CommitResult{Kind: CommitRejected, Reason: RejectTooManyKeys}
	}

	writeSet := sortedUniqueKeys(keys)
	for _, key := range writeSet {
		if key < 0 || key > maxKey {
			return CommitResult{Kind: CommitRejected, Reason: RejectInvalidKey}
		}
	}

	delete(m.active, sequence)

	if len(writeSet) == 0 {
		return CommitResult{Kind: CommitOk}
	}

	for _, key := range writeSet {
		bucket := m.bucket(key)
		for _, entry := range m.buckets[bucket] {
			m.probeCount++
			if entry.key == key && entry.timestamp > sequence {
				return CommitResult{Kind: CommitConflict}
			}
		}
	}

	if m.watermark > sequence {
		return CommitResult{Kind: CommitWatermark}
	}

	commitTimestamp := m.nextTimestamp + 1
	m.nextTimestamp = commitTimestamp

	oldestActive := int64(math.MaxInt64)
	for active := range m.active {
		if active < oldestActive {
			oldestActive = active
		}
	}

	for _, key := range writeSet {
		bucketID := m.bucket(key)
		bucket := m.buckets[bucketID]

		existing := -1
		for index := range bucket {
			m.probeCount++
			if bucket[index].key == key {
				existing = index
				break
			}
		}

		if existing >= 0 {
			bucket[existing].timestamp = commitTimestamp
			continue
		}

		if len(bucket) == m.capacity {
			cleared := bucket[:0]
			for _, entry := range bucket {
				if entry.timestamp > oldestActive {
					cleared = append(cleared, entry)
				}
			}
			bucket = cleared
		}

		if len(bucket) == m.capacity {
			victim := 0
			for index := 1; index < len(bucket); index++ {
				if bucket[index].timestamp < bucket[victim].timestamp ||
					(bucket[index].timestamp == bucket[victim].timestamp && bucket[index].key < bucket[victim].key) {
					victim = index
				}
			}

			if bucket[victim].timestamp > m.watermark {
				m.watermark = bucket[victim].timestamp
			}
			bucket = append(bucket[:victim], bucket[victim+1:]...)
		}

		m.buckets[bucketID] = append(bucket, tableEntry{key: key, timestamp: commitTimestamp})
	}

	return CommitResult{Kind: CommitOk, Timestamp: commitTimestamp}
}

func (m *SnapshotCommitManager) Abort(sequence int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, active := m.active[sequence]; !active {
		return ErrUnknownTransaction
	}

	delete(m.active, sequence)
	return nil
}

func (m *SnapshotCommitManager) bucket(key int64) int {
	return int(key % int64(len(m.buckets)))
}

func sortedUniqueKeys(keys []int64) []int64 {
	seen := make(map[int64]struct{}, len(keys))
	unique := make([]int64, 0, len(keys))
	for _, key := range keys {
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, key)
	}

	for index := 1; index < len(unique); index++ {
		key := unique[index]
		insertAt := index
		for insertAt > 0 && unique[insertAt-1] > key {
			unique[insertAt] = unique[insertAt-1]
			insertAt--
		}
		unique[insertAt] = key
	}

	return unique
}
