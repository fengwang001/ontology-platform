package store

import "sort"

type WriteLog struct {
	records map[int64]Record
	last    int64
}

func NewWriteLog() *WriteLog {
	return &WriteLog{records: make(map[int64]Record)}
}

func newWriteLogWithRecords(records []Record) *WriteLog {
	log := &WriteLog{records: make(map[int64]Record, len(records))}
	for _, record := range records {
		log.records[record.LSN] = cloneRecord(record)
		if record.LSN > log.last {
			log.last = record.LSN
		}
	}
	return log
}

func (l *WriteLog) Append(record Record) error {
	if record.LSN != l.last+1 {
		return ErrLogGap
	}
	l.records[record.LSN] = cloneRecord(record)
	l.last = record.LSN
	return nil
}

func (l *WriteLog) At(lsn int64) (Record, bool) {
	record, ok := l.records[lsn]
	return cloneRecord(record), ok
}

func (l *WriteLog) LastLSN() int64 {
	return l.last
}

func (l *WriteLog) Has(lsn int64) bool {
	_, ok := l.records[lsn]
	return ok
}

func (l *WriteLog) Records() []Record {
	result := make([]Record, 0, len(l.records))
	for _, record := range l.records {
		result = append(result, cloneRecord(record))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].LSN < result[j].LSN })
	return result
}

func (l *WriteLog) RecordsMap() map[int64]Record {
	result := make(map[int64]Record, len(l.records))
	for lsn, record := range l.records {
		result[lsn] = cloneRecord(record)
	}
	return result
}

func sortedRecords(records map[int64]Record) []Record {
	lsns := make([]int64, 0, len(records))
	for lsn := range records {
		lsns = append(lsns, lsn)
	}
	sort.Slice(lsns, func(i, j int) bool { return lsns[i] < lsns[j] })
	result := make([]Record, 0, len(lsns))
	for _, lsn := range lsns {
		result = append(result, cloneRecord(records[lsn]))
	}
	return result
}

func cloneRecord(record Record) Record {
	return Record{
		LSN:          record.LSN,
		Operation:    record.Operation,
		Primary:      record.Primary,
		Secondary:    cloneStringPtr(record.Secondary),
		OldSecondary: cloneStringPtr(record.OldSecondary),
	}
}
