package aries

import "sync"

type Calculator struct {
	mu             sync.RWMutex
	records        []Record
	maxLSN         int64
	hasMaxLSN      bool
	begins         map[int64]struct{}
	completedBegin map[int64]struct{}
	seenTxns       map[string]struct{}
	endedTxns      map[string]struct{}
}

func NewCalculator() *Calculator {
	return &Calculator{
		begins:         make(map[int64]struct{}),
		completedBegin: make(map[int64]struct{}),
		seenTxns:       make(map[string]struct{}),
		endedTxns:      make(map[string]struct{}),
	}
}

func (c *Calculator) Append(record Record) error {
	if err := validateRecord(record); err != nil {
		return err
	}

	record = cloneRecord(record)

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.hasMaxLSN && record.LSN <= c.maxLSN {
		return ErrNonIncreasingLSN
	}

	if record.Type == RecordEndCkpt {
		if _, ok := c.begins[record.BeginLSN]; !ok {
			return ErrBeginCheckpointNotFound
		}
		if _, ok := c.completedBegin[record.BeginLSN]; ok {
			return ErrCheckpointAlreadyCompleted
		}
	}

	if recordReferencesEndedTxn(record, c.endedTxns) {
		return ErrEndedTransaction
	}

	if isTerminalRecord(record.Type) {
		if _, ok := c.seenTxns[record.Txn]; !ok {
			return ErrUnknownTransaction
		}
	}

	c.records = append(c.records, record)
	c.maxLSN = record.LSN
	c.hasMaxLSN = true

	switch record.Type {
	case RecordUpdate:
		c.seenTxns[record.Txn] = struct{}{}
	case RecordCommit, RecordAbort:
		c.seenTxns[record.Txn] = struct{}{}
	case RecordEnd:
		c.seenTxns[record.Txn] = struct{}{}
		c.endedTxns[record.Txn] = struct{}{}
	case RecordBeginCkpt:
		c.begins[record.LSN] = struct{}{}
	case RecordEndCkpt:
		c.completedBegin[record.BeginLSN] = struct{}{}
		for txn := range record.ActiveTransactions {
			c.seenTxns[txn] = struct{}{}
		}
	}

	return nil
}

func (c *Calculator) Analyze() Analysis {
	c.mu.RLock()
	records := make([]Record, len(c.records))
	for i, record := range c.records {
		records[i] = cloneRecord(record)
	}
	c.mu.RUnlock()

	return Analyze(records)
}
