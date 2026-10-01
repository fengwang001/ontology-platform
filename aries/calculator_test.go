package aries

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

func lsn(value int64) *int64 {
	return &value
}

func mustAppend(t *testing.T, calculator *Calculator, record Record) {
	t.Helper()
	if err := calculator.Append(record); err != nil {
		t.Fatalf("Append(%+v) returned error %v", record, err)
	}
}

func assertAnalysisEqual(t *testing.T, got, want Analysis) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Analyze() = %+v, want %+v", got, want)
	}
}

func TestAnalyzeCheckpointFlushAndDirtyAgain(t *testing.T) {
	calculator := NewCalculator()
	records := []Record{
		{LSN: 1, Type: RecordUpdate, Txn: "T1", Page: "P1"},
		{LSN: 2, Type: RecordUpdate, Txn: "T2", Page: "P2"},
		{LSN: 3, Type: RecordBeginCkpt},
		{LSN: 4, Type: RecordPageFlush, Page: "P1"},
		{
			LSN:      5,
			Type:     RecordEndCkpt,
			BeginLSN: 3,
			DirtyPages: map[string]int64{
				"P1": 1,
				"P2": 2,
			},
			ActiveTransactions: map[string]TxnState{
				"T1": {Status: StatusRunning, LastLSN: 1},
				"T2": {Status: StatusRunning, LastLSN: 2},
			},
		},
		{LSN: 6, Type: RecordUpdate, Txn: "T1", Page: "P1"},
		{LSN: 7, Type: RecordCommit, Txn: "T2"},
		{LSN: 8, Type: RecordUpdate, Txn: "T3", Page: "P3"},
		{LSN: 9, Type: RecordAbort, Txn: "T3"},
	}
	for _, record := range records {
		mustAppend(t, calculator, record)
	}

	got := calculator.Analyze()
	want := Analysis{
		RedoLSN: lsn(2),
		DirtyPages: []DirtyPage{
			{Page: "P1", RecLSN: 6},
			{Page: "P2", RecLSN: 2},
			{Page: "P3", RecLSN: 8},
		},
		ActiveTransactions: []ActiveTransaction{
			{Txn: "T1", Status: StatusRunning, LastLSN: 6},
			{Txn: "T2", Status: StatusCommitted, LastLSN: 7},
			{Txn: "T3", Status: StatusAborting, LastLSN: 9},
		},
		FailedTxns: []string{"T1", "T3"},
	}
	assertAnalysisEqual(t, got, want)
}

func TestAnalyzeIgnoresIncompleteCheckpointAndUsesPreviousCompleteOne(t *testing.T) {
	calculator := NewCalculator()
	records := []Record{
		{LSN: 1, Type: RecordBeginCkpt},
		{LSN: 2, Type: RecordEndCkpt, BeginLSN: 1, DirtyPages: map[string]int64{}, ActiveTransactions: map[string]TxnState{}},
		{LSN: 3, Type: RecordBeginCkpt},
		{LSN: 4, Type: RecordUpdate, Txn: "T1", Page: "P1"},
	}
	for _, record := range records {
		mustAppend(t, calculator, record)
	}

	got := calculator.Analyze()
	want := Analysis{
		RedoLSN:    lsn(4),
		DirtyPages: []DirtyPage{{Page: "P1", RecLSN: 4}},
		ActiveTransactions: []ActiveTransaction{
			{Txn: "T1", Status: StatusRunning, LastLSN: 4},
		},
		FailedTxns: []string{"T1"},
	}
	assertAnalysisEqual(t, got, want)
}

func TestAnalyzeNoCheckpointAndEmptyDirtyPageTable(t *testing.T) {
	t.Run("no checkpoint", func(t *testing.T) {
		calculator := NewCalculator()
		mustAppend(t, calculator, Record{LSN: 1, Type: RecordUpdate, Txn: "T1", Page: "P1"})
		mustAppend(t, calculator, Record{LSN: 2, Type: RecordAbort, Txn: "T1"})

		got := calculator.Analyze()
		want := Analysis{
			RedoLSN: lsn(1),
			DirtyPages: []DirtyPage{
				{Page: "P1", RecLSN: 1},
			},
			ActiveTransactions: []ActiveTransaction{
				{Txn: "T1", Status: StatusAborting, LastLSN: 2},
			},
			FailedTxns: []string{"T1"},
		}
		assertAnalysisEqual(t, got, want)
	})

	t.Run("dirty table empty", func(t *testing.T) {
		calculator := NewCalculator()
		mustAppend(t, calculator, Record{LSN: 1, Type: RecordUpdate, Txn: "T1", Page: "P1"})
		mustAppend(t, calculator, Record{LSN: 2, Type: RecordPageFlush, Page: "P1"})
		mustAppend(t, calculator, Record{LSN: 3, Type: RecordCommit, Txn: "T1"})

		got := calculator.Analyze()
		want := Analysis{
			RedoLSN:    nil,
			DirtyPages: []DirtyPage{},
			ActiveTransactions: []ActiveTransaction{
				{Txn: "T1", Status: StatusCommitted, LastLSN: 3},
			},
			FailedTxns: []string{},
		}
		assertAnalysisEqual(t, got, want)
	})
}

func TestAppendRejectsInSpecifiedOrderAndKeepsLogAtomic(t *testing.T) {
	calculator := NewCalculator()
	mustAppend(t, calculator, Record{LSN: 1, Type: RecordUpdate, Txn: "T1", Page: "P1"})

	invalidRecords := []struct {
		record Record
		want   error
	}{
		{Record{LSN: 1, Type: RecordUpdate, Txn: "T1", Page: "P2"}, ErrNonIncreasingLSN},
		{Record{LSN: 2, Type: RecordEndCkpt, BeginLSN: 99}, ErrBeginCheckpointNotFound},
	}
	for _, item := range invalidRecords {
		if err := calculator.Append(item.record); !errors.Is(err, item.want) {
			t.Fatalf("Append(%+v) error = %v, want %v", item.record, err, item.want)
		}
	}

	mustAppend(t, calculator, Record{LSN: 3, Type: RecordBeginCkpt})
	mustAppend(t, calculator, Record{LSN: 4, Type: RecordEndCkpt, BeginLSN: 3})
	if err := calculator.Append(Record{LSN: 5, Type: RecordEndCkpt, BeginLSN: 3}); !errors.Is(err, ErrCheckpointAlreadyCompleted) {
		t.Fatalf("duplicate EndCkpt error = %v, want %v", err, ErrCheckpointAlreadyCompleted)
	}

	mustAppend(t, calculator, Record{LSN: 6, Type: RecordEnd, Txn: "T1"})
	if err := calculator.Append(Record{LSN: 7, Type: RecordUpdate, Txn: "T1", Page: "P9"}); !errors.Is(err, ErrEndedTransaction) {
		t.Fatalf("ended transaction error = %v, want %v", err, ErrEndedTransaction)
	}

	mustAppend(t, calculator, Record{LSN: 8, Type: RecordBeginCkpt})
	endedSnapshot := Record{
		LSN:                9,
		Type:               RecordEndCkpt,
		BeginLSN:           8,
		ActiveTransactions: map[string]TxnState{"T1": {Status: StatusCommitted, LastLSN: 6}},
	}
	if err := calculator.Append(endedSnapshot); !errors.Is(err, ErrEndedTransaction) {
		t.Fatalf("ended checkpoint transaction error = %v, want %v", err, ErrEndedTransaction)
	}

	if err := calculator.Append(Record{LSN: 10, Type: RecordCommit, Txn: "T9"}); !errors.Is(err, ErrUnknownTransaction) {
		t.Fatalf("unknown transaction error = %v, want %v", err, ErrUnknownTransaction)
	}

	got := calculator.Analyze()
	if got.RedoLSN != nil || len(got.DirtyPages) != 0 || len(got.ActiveTransactions) != 0 {
		t.Fatalf("rejected records changed the log, analysis = %+v", got)
	}
}

func TestCheckpointSnapshotMakesTransactionVisible(t *testing.T) {
	calculator := NewCalculator()
	mustAppend(t, calculator, Record{LSN: 1, Type: RecordBeginCkpt})
	mustAppend(t, calculator, Record{
		LSN:      2,
		Type:     RecordEndCkpt,
		BeginLSN: 1,
		ActiveTransactions: map[string]TxnState{
			"T1": {Status: StatusRunning, LastLSN: 1},
		},
	})
	mustAppend(t, calculator, Record{LSN: 3, Type: RecordCommit, Txn: "T1"})

	got := calculator.Analyze()
	want := Analysis{
		RedoLSN:    nil,
		DirtyPages: []DirtyPage{},
		ActiveTransactions: []ActiveTransaction{
			{Txn: "T1", Status: StatusCommitted, LastLSN: 3},
		},
		FailedTxns: []string{},
	}
	assertAnalysisEqual(t, got, want)
}

func TestConcurrentAppendAndAnalyze(t *testing.T) {
	calculator := NewCalculator()
	var waitGroup sync.WaitGroup

	for i := int64(1); i <= 100; i++ {
		waitGroup.Add(1)
		go func(lsn int64) {
			defer waitGroup.Done()
			_ = calculator.Append(Record{LSN: lsn, Type: RecordUpdate, Txn: "T1", Page: "P1"})
			_ = calculator.Analyze()
		}(i)
	}

	waitGroup.Wait()
	got := calculator.Analyze()
	if got.RedoLSN == nil || *got.RedoLSN != 1 {
		t.Fatalf("concurrent analysis = %+v", got)
	}
}
