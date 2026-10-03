package ontology

import (
	"errors"
	"sort"
	"sync"
)

const (
	ReasonCommitted   = "committed"
	ReasonConflict    = "conflict"
	ReasonWatermark   = "watermark"
	ReasonInvalidTxn  = "invalid_transaction"
	ReasonTooManyKeys = "too_many_keys"
	ReasonInvalidKey  = "invalid_key"
)

type CommitResult struct {
	Committed bool
	Timestamp int64
	Reason    string
}

type Entry struct {
	Key       int64
	Timestamp int64
}

type Snapshot struct {
	TimestampCounter int64
	Watermark        int64
	Active           []int64
	Entries          []Entry
}

type CommitManager struct {
	mu        sync.Mutex
	buckets   int
	capacity  int
	timestamp int64
	watermark int64
	active    map[int64]struct{}
	table     []map[int64]int64
	probes    int
}

var ErrInvalidConfiguration = errors.New("invalid commit manager configuration")

func NewCommitManager(buckets, capacity int) (*CommitManager, error) {
	if buckets < 1 || buckets > 1024 || capacity < 1 || capacity > 16 {
		return nil, ErrInvalidConfiguration
	}

	table := make([]map[int64]int64, buckets)
	for index := range table {
		table[index] = make(map[int64]int64)
	}

	return &CommitManager{
		buckets:  buckets,
		capacity: capacity,
		active:   make(map[int64]struct{}),
		table:    table,
	}, nil
}

func (m *CommitManager) Begin() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.timestamp++
	m.active[m.timestamp] = struct{}{}
	return m.timestamp
}

func (m *CommitManager) Commit(transaction int64, keys []int64) CommitResult {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, active := m.active[transaction]; !active {
		return CommitResult{Reason: ReasonInvalidTxn}
	}
	if len(keys) > 64 {
		return CommitResult{Reason: ReasonTooManyKeys}
	}

	writeSet := uniqueSortedKeys(keys)
	for _, key := range writeSet {
		if key < 0 || key > 1_000_000_000 {
			return CommitResult{Reason: ReasonInvalidKey}
		}
	}

	return m.commitLocked(transaction, writeSet)
}

func (m *CommitManager) Abort(transaction int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, active := m.active[transaction]; !active {
		return false
	}
	delete(m.active, transaction)
	return true
}

func (m *CommitManager) commitLocked(transaction int64, writeSet []int64) CommitResult {
	if len(writeSet) == 0 {
		delete(m.active, transaction)
		return CommitResult{Committed: true, Timestamp: 0, Reason: ReasonCommitted}
	}

	m.probes = 0
	for _, key := range writeSet {
		bucket := m.table[key%int64(m.buckets)]
		m.probes += len(bucket)
		if timestamp, exists := bucket[key]; exists && timestamp > transaction {
			return CommitResult{Reason: ReasonConflict}
		}
	}

	if m.watermark > transaction {
		return CommitResult{Reason: ReasonWatermark}
	}

	commitTimestamp := m.timestamp + 1
	m.timestamp = commitTimestamp
	delete(m.active, transaction)

	oldestActive := int64(0)
	for activeTransaction := range m.active {
		if oldestActive == 0 || activeTransaction < oldestActive {
			oldestActive = activeTransaction
		}
	}

	for _, key := range writeSet {
		bucketIndex := key % int64(m.buckets)
		bucket := m.table[bucketIndex]
		if _, exists := bucket[key]; exists {
			bucket[key] = commitTimestamp
			continue
		}

		if len(bucket) == m.capacity {
			if len(m.active) > 0 {
				for storedKey, storedTimestamp := range bucket {
					m.probes++
					if storedTimestamp <= oldestActive {
						delete(bucket, storedKey)
					}
				}
			} else {
				m.probes += len(bucket)
				clear(bucket)
			}
		}

		if len(bucket) == m.capacity {
			victimKey := int64(-1)
			victimTimestamp := int64(0)
			for storedKey, storedTimestamp := range bucket {
				m.probes++
				if victimKey == -1 || storedTimestamp < victimTimestamp ||
					(storedTimestamp == victimTimestamp && storedKey < victimKey) {
					victimKey = storedKey
					victimTimestamp = storedTimestamp
				}
			}
			delete(bucket, victimKey)
			if victimTimestamp > m.watermark {
				m.watermark = victimTimestamp
			}
		}

		bucket[key] = commitTimestamp
	}

	return CommitResult{Committed: true, Timestamp: commitTimestamp, Reason: ReasonCommitted}
}

func (m *CommitManager) Snapshot() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()

	active := make([]int64, 0, len(m.active))
	for transaction := range m.active {
		active = append(active, transaction)
	}
	sort.Slice(active, func(i, j int) bool { return active[i] < active[j] })

	entries := make([]Entry, 0)
	for _, bucket := range m.table {
		for key, timestamp := range bucket {
			entries = append(entries, Entry{Key: key, Timestamp: timestamp})
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Key != entries[j].Key {
			return entries[i].Key < entries[j].Key
		}
		return entries[i].Timestamp < entries[j].Timestamp
	})

	return Snapshot{
		TimestampCounter: m.timestamp,
		Watermark:        m.watermark,
		Active:           active,
		Entries:          entries,
	}
}

func (m *CommitManager) probesSinceCommit() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.probes
}

func uniqueSortedKeys(keys []int64) []int64 {
	seen := make(map[int64]struct{}, len(keys))
	writeSet := make([]int64, 0, len(keys))
	for _, key := range keys {
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		writeSet = append(writeSet, key)
	}
	sort.Slice(writeSet, func(i, j int) bool { return writeSet[i] < writeSet[j] })
	return writeSet
}
