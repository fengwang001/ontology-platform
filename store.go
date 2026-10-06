package store

import "sync"

type Store struct {
	mu        sync.Mutex
	table     *Table
	log       *WriteLog
	index     *SecondaryIndex
	crashHook func(lsn int64) bool
}

func New() *Store {
	return newStore(NewTable(), NewWriteLog(), NewSecondaryIndex(), nil)
}

func newStore(table *Table, log *WriteLog, index *SecondaryIndex, crashHook func(lsn int64) bool) *Store {
	return &Store{
		table:     table,
		log:       log,
		index:     index,
		crashHook: crashHook,
	}
}

func (s *Store) Get(primary string) (Row, error) {
	if primary == "" {
		return Row{}, ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.table.Get(primary)
	if !ok {
		return Row{}, ErrPrimaryNotFound
	}
	return row, nil
}

func (s *Store) Put(row Row) (lsn int64, err error) {
	if row.Primary == "" {
		return 0, ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, exists := s.table.Get(row.Primary)
	if row.Secondary != nil {
		owner, occupied := s.table.Owner(*row.Secondary)
		if occupied && owner != row.Primary {
			return 0, ErrUniqueConflict
		}
	}

	lsn = s.log.LastLSN() + 1
	var oldSecondary *string
	if exists {
		oldSecondary = existing.Secondary
	}
	record := Record{
		LSN:          lsn,
		Operation:    OpPut,
		Primary:      row.Primary,
		Secondary:    cloneStringPtr(row.Secondary),
		OldSecondary: oldSecondary,
	}
	if err := s.log.Append(record); err != nil {
		return 0, err
	}
	s.table.Put(row)
	s.applyIfCurrent(record)
	return lsn, nil
}

func (s *Store) Delete(primary string) (lsn int64, err error) {
	if primary == "" {
		return 0, ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, exists := s.table.Get(primary)
	if !exists {
		return 0, ErrPrimaryNotFound
	}
	lsn = s.log.LastLSN() + 1
	record := Record{
		LSN:          lsn,
		Operation:    OpDelete,
		Primary:      primary,
		OldSecondary: existing.Secondary,
	}
	if err := s.log.Append(record); err != nil {
		return 0, err
	}
	s.table.Delete(primary)
	s.applyIfCurrent(record)
	return lsn, nil
}

func (s *Store) Find(secondary string) (primary string, lsn int64, err error) {
	if secondary == "" {
		return "", 0, ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.index.Watermark() != s.log.LastLSN() {
		return "", 0, ErrIndexBehind
	}
	entry, ok := s.index.Lookup(secondary)
	if !ok {
		return "", 0, nil
	}
	return entry.Primary, s.index.Watermark(), nil
}

func (s *Store) CatchUp(maxRecords int) (advancedTo int64, err error) {
	if maxRecords < 0 {
		return 0, ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	target := s.log.LastLSN()
	start := s.index.Watermark() + 1
	remaining := maxRecords
	if target-start+1 > int64(remaining) {
		target = start + int64(remaining) - 1
	}
	for lsn := start; lsn <= target; lsn++ {
		if !s.log.Has(lsn) {
			return s.index.Watermark(), ErrLogGap
		}
	}
	for lsn := start; lsn <= target; lsn++ {
		record, _ := s.log.At(lsn)
		if err := s.index.Apply(record); err != nil {
			return s.index.Watermark(), err
		}
		if s.crashHook != nil && s.crashHook(lsn) {
			return s.index.Watermark(), ErrSimulatedCrash
		}
		s.index.SetWatermark(lsn)
	}
	return s.index.Watermark(), nil
}

func (s *Store) Watermark() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.index.Watermark()
}

func (s *Store) LastLSN() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.log.LastLSN()
}

func (s *Store) IndexCaughtUp() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.index.Watermark() == s.log.LastLSN()
}

func (s *Store) applyIfCurrent(record Record) {
	if s.index.Watermark() != record.LSN-1 {
		return
	}
	if err := s.index.Apply(record); err != nil {
		return
	}
	s.index.SetWatermark(record.LSN)
}

func (s *Store) Check() (CheckReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.index.Watermark() != s.log.LastLSN() {
		return CheckReport{}, ErrIndexBehind
	}
	return s.index.Check(s.table), nil
}
