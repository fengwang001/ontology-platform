package ontology

import "sort"

type modelEntry struct {
	key       int64
	timestamp int64
}

type naiveManager struct {
	buckets   int
	capacity  int
	timestamp int64
	watermark int64
	active    map[int64]struct{}
	table     [][]modelEntry
	history   map[int64]int64
}

func newNaiveManager(buckets, capacity int) *naiveManager {
	return &naiveManager{
		buckets:  buckets,
		capacity: capacity,
		active:   make(map[int64]struct{}),
		table:    make([][]modelEntry, buckets),
		history:  make(map[int64]int64),
	}
}

func (m *naiveManager) begin() int64 {
	m.timestamp++
	m.active[m.timestamp] = struct{}{}
	return m.timestamp
}

func (m *naiveManager) commit(transaction int64, keys []int64) CommitResult {
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
	if len(writeSet) == 0 {
		delete(m.active, transaction)
		return CommitResult{Committed: true, Reason: ReasonCommitted}
	}

	bucketIndices := make(map[int64]int)
	for _, key := range writeSet {
		bucketIndices[key] = int(key % int64(m.buckets))
		for _, entry := range m.table[bucketIndices[key]] {
			if entry.key == key && entry.timestamp > transaction {
				return CommitResult{Reason: ReasonConflict}
			}
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
		bucketIndex := bucketIndices[key]
		bucket := m.table[bucketIndex]
		index := -1
		for position, entry := range bucket {
			if entry.key == key {
				index = position
				break
			}
		}
		if index >= 0 {
			bucket[index].timestamp = commitTimestamp
			m.table[bucketIndex] = bucket
			continue
		}

		if len(bucket) == m.capacity {
			if len(m.active) > 0 {
				retained := bucket[:0]
				for _, entry := range bucket {
					if entry.timestamp > oldestActive {
						retained = append(retained, entry)
					}
				}
				bucket = retained
			} else {
				bucket = nil
			}
		}

		if len(bucket) == m.capacity {
			victim := 0
			for position := 1; position < len(bucket); position++ {
				if bucket[position].timestamp < bucket[victim].timestamp ||
					(bucket[position].timestamp == bucket[victim].timestamp &&
						bucket[position].key < bucket[victim].key) {
					victim = position
				}
			}
			if bucket[victim].timestamp > m.watermark {
				m.watermark = bucket[victim].timestamp
			}
			bucket = append(bucket[:victim], bucket[victim+1:]...)
		}

		m.table[bucketIndex] = append(bucket, modelEntry{key: key, timestamp: commitTimestamp})
		m.history[key] = commitTimestamp
	}

	return CommitResult{Committed: true, Timestamp: commitTimestamp, Reason: ReasonCommitted}
}

func (m *naiveManager) abort(transaction int64) bool {
	if _, active := m.active[transaction]; !active {
		return false
	}
	delete(m.active, transaction)
	return true
}

func (m *naiveManager) snapshot() Snapshot {
	active := make([]int64, 0, len(m.active))
	for transaction := range m.active {
		active = append(active, transaction)
	}
	sort.Slice(active, func(i, j int) bool { return active[i] < active[j] })

	entries := make([]Entry, 0)
	for _, bucket := range m.table {
		for _, entry := range bucket {
			entries = append(entries, Entry{Key: entry.key, Timestamp: entry.timestamp})
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Key < entries[j].Key })

	return Snapshot{
		TimestampCounter: m.timestamp,
		Watermark:        m.watermark,
		Active:           active,
		Entries:          entries,
	}
}
