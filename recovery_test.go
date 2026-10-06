package store

import "testing"

func replayIndex(records []Record, through int64) map[string]IndexEntry {
	entries := make(map[string]IndexEntry)
	for _, record := range records {
		if record.LSN > through {
			continue
		}
		if record.OldSecondary != nil {
			if entry, ok := entries[*record.OldSecondary]; ok && entry.Primary == record.Primary {
				delete(entries, *record.OldSecondary)
			}
		}
		if record.Operation == OpPut && record.Secondary != nil {
			entries[*record.Secondary] = IndexEntry{Primary: record.Primary, LSN: record.LSN}
		}
	}
	return entries
}

func recoveredStoreFromRecords(t *testing.T, records []Record, appliedPrefix, watermark int64) *Store {
	t.Helper()
	table := NewTable()
	recordMap := make(map[int64]Record, len(records))
	var expectedLSN int64 = 1
	for _, record := range records {
		if record.LSN != expectedLSN {
			t.Fatalf("test setup gap at %d", expectedLSN)
		}
		recordMap[record.LSN] = record
		requireNoError(t, applyRecordToTable(table, record))
		expectedLSN++
	}
	log := newWriteLogWithRecords(records)
	index := NewSecondaryIndex()
	for secondary, entry := range replayIndex(records, appliedPrefix) {
		index.entries[secondary] = entry
	}
	index.watermark = watermark
	return newStore(table, log, index, nil)
}

func requireIndexEntries(t *testing.T, s *Store, want map[string]string) {
	t.Helper()
	state := s.PersistedSnapshot()
	if len(state.Entries) != len(want) {
		t.Fatalf("index entries = %#v, want %#v", state.Entries, want)
	}
	for secondary, primary := range want {
		entry, ok := state.Entries[secondary]
		if !ok || entry.Primary != primary {
			t.Fatalf("index[%q] = %#v, want primary %q; all=%#v", secondary, entry, primary, state.Entries)
		}
	}
}

func recoveryTestRecords() []Record {
	return []Record{
		{LSN: 1, Operation: OpPut, Primary: "p1", Secondary: strPtr("a")},
		{LSN: 2, Operation: OpPut, Primary: "p2", Secondary: strPtr("b"), OldSecondary: strPtr("b")},
		{LSN: 3, Operation: OpPut, Primary: "p1", Secondary: strPtr("c"), OldSecondary: strPtr("a")},
		{LSN: 4, Operation: OpPut, Primary: "p2", Secondary: nil, OldSecondary: strPtr("b")},
		{LSN: 5, Operation: OpPut, Primary: "p3", Secondary: strPtr("b")},
		{LSN: 6, Operation: OpDelete, Primary: "p3", OldSecondary: strPtr("b")},
		{LSN: 7, Operation: OpPut, Primary: "p2", Secondary: strPtr("a")},
		{LSN: 8, Operation: OpPut, Primary: "p1", Secondary: nil, OldSecondary: strPtr("c")},
	}
}

func TestCatchUpEveryLeadingPrefixAndBatchBoundary(t *testing.T) {
	records := recoveryTestRecords()
	for appliedPrefix := int64(0); appliedPrefix <= int64(len(records)); appliedPrefix++ {
		for watermark := int64(0); watermark <= appliedPrefix; watermark++ {
			for batchSize := 1; batchSize <= len(records)+1; batchSize++ {
				s := recoveredStoreFromRecords(t, records, appliedPrefix, watermark)
				for {
					advanced, err := s.CatchUp(batchSize)
					requireNoError(t, err)
					if advanced == int64(len(records)) {
						break
					}
					_, _, err = s.Find("a")
					requireErrorIs(t, err, ErrIndexBehind)
					if _, err := s.Get("p1"); err != nil {
						t.Fatalf("get during catch-up: %v", err)
					}
				}
				requireIndexEntries(t, s, map[string]string{"a": "p2"})
				report, err := s.Check()
				requireNoError(t, err)
				if len(report.Mismatches) != 0 {
					t.Fatalf("applied=%d watermark=%d batch=%d mismatches=%#v", appliedPrefix, watermark, batchSize, report.Mismatches)
				}
			}
		}
	}
}

func TestCrashAfterEveryAppliedRecordThenResume(t *testing.T) {
	records := recoveryTestRecords()
	for watermark := int64(0); watermark < int64(len(records)); watermark++ {
		for batchSize := 1; batchSize <= 3; batchSize++ {
			for crashAt := watermark + 1; crashAt <= int64(len(records)); crashAt++ {
				s := recoveredStoreFromRecords(t, records, watermark, watermark)
				s.SetCrashHookForTest(func(lsn int64) bool { return lsn == crashAt })
				var err error
				for s.Watermark() < int64(len(records)) {
					_, err = s.CatchUp(batchSize)
					if err != nil {
						break
					}
				}
				if err == nil {
					t.Fatalf("crash at %d did not stop batch", crashAt)
				}
				crashed := s.PersistedSnapshot()
				resumed := New()
				requireNoError(t, resumed.Restart(crashed))
				for resumed.Watermark() < int64(len(records)) {
					_, err := resumed.CatchUp(batchSize)
					requireNoError(t, err)
				}
				requireIndexEntries(t, resumed, map[string]string{"a": "p2"})
			}
		}
	}
}

func TestIndexApplySameRecordIsIdempotent(t *testing.T) {
	index := NewSecondaryIndex()
	record := Record{
		LSN:          3,
		Operation:    OpPut,
		Primary:      "p1",
		Secondary:    strPtr("a"),
		OldSecondary: strPtr("b"),
	}
	requireNoError(t, index.Apply(record))
	index.watermark = 2
	requireNoError(t, index.Apply(record))
	entries := index.Entries()
	if len(entries) != 1 || entries["a"] != (IndexEntry{Primary: "p1", LSN: 3}) {
		t.Fatalf("entries after duplicate apply = %#v", entries)
	}
}
