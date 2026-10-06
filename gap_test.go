package store

import "testing"

func TestLogGapAtEveryPositionDoesNotMutate(t *testing.T) {
	base := recoveryTestRecords()
	for missing := int64(1); missing <= int64(len(base)); missing++ {
		records := make(map[int64]Record)
		for _, record := range base {
			if record.LSN == missing {
				continue
			}
			records[record.LSN] = record
		}
		s := recoveredStoreFromRecords(t, nil, 0, 0)
		s.log = &WriteLog{records: records, last: int64(len(base))}
		s.table = NewTable()

		before := s.PersistedSnapshot()
		advanced, err := s.CatchUp(len(base))
		requireErrorIs(t, err, ErrLogGap)
		if advanced != 0 {
			t.Fatalf("missing=%d advanced=%d, want 0", missing, advanced)
		}
		after := s.PersistedSnapshot()
		if len(after.Entries) != len(before.Entries) || after.Watermark != before.Watermark {
			t.Fatalf("missing=%d state changed: before=%#v after=%#v", missing, before, after)
		}
	}
}
