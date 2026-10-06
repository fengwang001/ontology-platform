package store

import (
	"strconv"
	"testing"
)

func makeLargeLagStore(b *testing.B, tableSize, lag int64) *Store {
	b.Helper()
	records := make([]Record, 0, tableSize+lag)
	table := NewTable()
	for i := int64(0); i < tableSize; i++ {
		primary := "p" + string(rune(i))
		secondary := "s" + string(rune(i))
		record := Record{
			LSN:       i + 1,
			Operation: OpPut,
			Primary:   primary,
			Secondary: strPtr(secondary),
		}
		records = append(records, record)
		if err := applyRecordToTable(table, record); err != nil {
			b.Fatalf("apply setup record: %v", err)
		}
	}
	lastBase := int64(len(records))
	churn := "churn"
	for i := int64(0); i < lag; i++ {
		record := Record{
			LSN:       lastBase + i + 1,
			Operation: OpPut,
			Primary:   churn,
			Secondary: strPtr("churn" + string(rune(i))),
		}
		if i > 0 {
			record.OldSecondary = strPtr("churn" + string(rune(i-1)))
		}
		records = append(records, record)
	}
	if lag > 0 {
		table.Put(Row{Primary: churn, Secondary: strPtr("churn" + string(rune(lag-1)))})
	}
	log := newWriteLogWithRecords(records)
	index := NewSecondaryIndex()
	for secondary, entry := range replayIndex(records, lastBase) {
		index.entries[secondary] = entry
	}
	index.watermark = lastBase
	return newStore(table, log, index, nil)
}

func BenchmarkUniqueConflictWithLargeLag(b *testing.B) {
	for _, lag := range []int64{16, 256, 4096} {
		s := makeLargeLagStore(b, 4096, lag)
		b.Run("lag="+strconv.FormatInt(lag, 10), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, err := s.Put(Row{Primary: "other", Secondary: strPtr("s0")}); err != ErrUniqueConflict {
					b.Fatalf("err = %v, want conflict", err)
				}
			}
		})
	}
}

func BenchmarkReplayOneRecordScaledTable(b *testing.B) {
	for _, size := range []int64{128, 2048, 32768} {
		s := makeLargeLagStore(b, size, 1)
		records := s.log.Records()
		baseEntries := replayIndex(records, size)
		record, ok := s.log.At(size + 1)
		if !ok {
			b.Fatalf("missing replay record %d", size+1)
		}
		b.Run("size="+strconv.FormatInt(size, 10), func(b *testing.B) {
			for b.Loop() {
				b.StopTimer()
				index := NewSecondaryIndex()
				for secondary, entry := range baseEntries {
					index.entries[secondary] = entry
				}
				index.watermark = size
				b.StartTimer()
				if err := index.Apply(record); err != nil {
					b.Fatalf("catch-up: %v", err)
				}
			}
		})
	}
}
