package ontology

type TableEntryView struct {
	Key       int64
	Timestamp int64
}

type StateSnapshot struct {
	NextTimestamp int64
	Watermark     int64
	Buckets       [][]TableEntryView
	Active        []int64
	ProbeCount    int64
}

func (m *SnapshotCommitManager) Snapshot() StateSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()

	buckets := make([][]TableEntryView, len(m.buckets))
	for bucketID := range m.buckets {
		buckets[bucketID] = make([]TableEntryView, len(m.buckets[bucketID]))
		for index, entry := range m.buckets[bucketID] {
			buckets[bucketID][index] = TableEntryView{Key: entry.key, Timestamp: entry.timestamp}
		}
		for index := 1; index < len(buckets[bucketID]); index++ {
			entry := buckets[bucketID][index]
			insertAt := index
			for insertAt > 0 && buckets[bucketID][insertAt-1].Key > entry.Key {
				buckets[bucketID][insertAt] = buckets[bucketID][insertAt-1]
				insertAt--
			}
			buckets[bucketID][insertAt] = entry
		}
	}

	active := make([]int64, 0, len(m.active))
	for sequence := range m.active {
		active = append(active, sequence)
	}
	for index := 1; index < len(active); index++ {
		sequence := active[index]
		insertAt := index
		for insertAt > 0 && active[insertAt-1] > sequence {
			active[insertAt] = active[insertAt-1]
			insertAt--
		}
		active[insertAt] = sequence
	}

	return StateSnapshot{
		NextTimestamp: m.nextTimestamp,
		Watermark:     m.watermark,
		Buckets:       buckets,
		Active:        active,
		ProbeCount:    m.probeCount,
	}
}

func (m *SnapshotCommitManager) resetProbeCount() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()

	probes := m.probeCount
	m.probeCount = 0
	return probes
}
