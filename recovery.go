package store

type PersistedState struct {
	Rows      map[string]Row
	Records   map[int64]Record
	Entries   map[string]IndexEntry
	Watermark int64
}

func (s *Store) PersistedSnapshot() PersistedState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return PersistedState{
		Rows:      s.table.Snapshot(),
		Records:   s.log.RecordsMap(),
		Entries:   s.index.Entries(),
		Watermark: s.index.Watermark(),
	}
}

func (s *Store) Restart(state PersistedState) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	table := NewTable()
	log := NewWriteLog()
	index := NewSecondaryIndex()
	var expectedLSN int64 = 1
	for _, record := range sortedRecords(state.Records) {
		if record.LSN != expectedLSN {
			return ErrLogGap
		}
		if record.Primary == "" {
			return ErrInvalidArgument
		}
		if err := log.Append(record); err != nil {
			return err
		}
		if err := applyRecordToTable(table, record); err != nil {
			return err
		}
		expectedLSN++
	}
	if state.Watermark < 0 || state.Watermark > log.LastLSN() {
		return ErrInvalidArgument
	}
	for secondary, entry := range state.Entries {
		if secondary == "" || entry.Primary == "" || entry.LSN <= 0 || entry.LSN > log.LastLSN() {
			return ErrInvalidArgument
		}
		index.entries[secondary] = entry
	}
	index.watermark = state.Watermark
	s.table = table
	s.log = log
	s.index = index
	return nil
}

func (s *Store) SetCrashHookForTest(hook func(lsn int64) bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.crashHook = hook
}

func applyRecordToTable(table *Table, record Record) error {
	switch record.Operation {
	case OpPut:
		table.Put(Row{Primary: record.Primary, Secondary: record.Secondary})
	case OpDelete:
		table.Delete(record.Primary)
	default:
		return ErrInvalidArgument
	}
	return nil
}
