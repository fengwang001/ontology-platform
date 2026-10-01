package aries

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"
)

func TestAnalyzeRandomLogsAgainstNaiveReplay(t *testing.T) {
	for seed := int64(1); seed <= 1000; seed++ {
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			records := generateRandomLog(seed)

			calculator := NewCalculator()
			for _, record := range records {
				mustAppend(t, calculator, record)
			}

			got := calculator.Analyze()
			want, basis := naiveAnalyze(records)
			t.Logf("input=%v output=%+v basis=%s", records, got, basis)

			if !slices.EqualFunc(got.DirtyPages, want.DirtyPages, func(left, right DirtyPage) bool {
				return left == right
			}) {
				t.Fatalf("dirty pages = %+v, want %+v", got.DirtyPages, want.DirtyPages)
			}
			if !slices.EqualFunc(got.ActiveTransactions, want.ActiveTransactions, func(left, right ActiveTransaction) bool {
				return left == right
			}) {
				t.Fatalf("active transactions = %+v, want %+v", got.ActiveTransactions, want.ActiveTransactions)
			}
			if !slices.Equal(got.FailedTxns, want.FailedTxns) {
				t.Fatalf("failed transactions = %v, want %v", got.FailedTxns, want.FailedTxns)
			}
			if (got.RedoLSN == nil) != (want.RedoLSN == nil) {
				t.Fatalf("redo LSN present = %v, want %v", got.RedoLSN, want.RedoLSN)
			}
			if got.RedoLSN != nil && *got.RedoLSN != *want.RedoLSN {
				t.Fatalf("redo LSN = %d, want %d", *got.RedoLSN, *want.RedoLSN)
			}
		})
	}
}

type randomGenerator struct {
	rng        *rand.Rand
	lsn        int64
	nextTxn    int
	records    []Record
	dirty      map[string]int64
	txns       map[string]TxnState
	checkpoint *randomCheckpoint
}

type randomCheckpoint struct {
	beginLSN int64
	dirty    map[string]int64
	txns     map[string]TxnState
}

func generateRandomLog(seed int64) []Record {
	generator := &randomGenerator{
		rng:   rand.New(rand.NewPCG(uint64(seed), uint64(seed+1000))),
		dirty: make(map[string]int64),
		txns:  make(map[string]TxnState),
	}

	for range generator.rng.IntN(30) + 1 {
		generator.nextLSN()
		if generator.checkpoint != nil {
			generator.generateDuringCheckpoint()
		} else {
			generator.generateNormalRecord()
		}
	}

	return generator.records
}

func (g *randomGenerator) nextLSN() {
	g.lsn++
}

func (g *randomGenerator) generateDuringCheckpoint() {
	flushedSnapshotPages := []string{}
	for page := range g.checkpoint.dirty {
		if _, stillDirty := g.dirty[page]; stillDirty {
			flushedSnapshotPages = append(flushedSnapshotPages, page)
		}
	}
	slices.Sort(flushedSnapshotPages)

	switch g.rng.IntN(10) {
	case 0, 1:
		if len(flushedSnapshotPages) > 0 {
			page := flushedSnapshotPages[g.rng.IntN(len(flushedSnapshotPages))]
			delete(g.dirty, page)
			g.records = append(g.records, Record{LSN: g.lsn, Type: RecordPageFlush, Page: page})
			return
		}
		g.generateUpdate()
	case 2, 3:
		g.records = append(g.records, Record{
			LSN:                g.lsn,
			Type:               RecordEndCkpt,
			BeginLSN:           g.checkpoint.beginLSN,
			DirtyPages:         cloneInt64Map(g.checkpoint.dirty),
			ActiveTransactions: cloneTxnStateMap(g.checkpoint.txns),
		})
		g.checkpoint = nil
	default:
		g.generateUpdate()
	}
}

func (g *randomGenerator) generateNormalRecord() {
	switch g.rng.IntN(10) {
	case 0, 1:
		g.beginCheckpoint()
	case 2, 3:
		if len(g.dirty) > 0 {
			pages := mapKeys(g.dirty)
			page := pages[g.rng.IntN(len(pages))]
			delete(g.dirty, page)
			g.records = append(g.records, Record{LSN: g.lsn, Type: RecordPageFlush, Page: page})
			return
		}
		g.generateUpdate()
	case 4:
		if txn := g.chooseActiveTxn(); txn != "" {
			state := g.txns[txn]
			state.LastLSN = g.lsn
			if g.rng.IntN(2) == 0 {
				state.Status = StatusCommitted
				g.records = append(g.records, Record{LSN: g.lsn, Type: RecordCommit, Txn: txn})
			} else {
				state.Status = StatusAborting
				g.records = append(g.records, Record{LSN: g.lsn, Type: RecordAbort, Txn: txn})
			}
			g.txns[txn] = state
			return
		}
		g.generateUpdate()
	case 5:
		txns := []string{}
		for txn, state := range g.txns {
			if state.Status == StatusCommitted || state.Status == StatusAborting {
				txns = append(txns, txn)
			}
		}
		slices.Sort(txns)
		if len(txns) > 0 {
			txn := txns[g.rng.IntN(len(txns))]
			delete(g.txns, txn)
			g.records = append(g.records, Record{LSN: g.lsn, Type: RecordEnd, Txn: txn})
			return
		}
		g.generateUpdate()
	default:
		g.generateUpdate()
	}
}

func (g *randomGenerator) generateUpdate() {
	txn := g.chooseActiveTxn()
	if txn == "" || g.rng.IntN(3) == 0 {
		txn = fmt.Sprintf("T%d", g.nextTxn)
		g.nextTxn++
	}

	page := fmt.Sprintf("P%d", g.rng.IntN(4))
	g.records = append(g.records, Record{LSN: g.lsn, Type: RecordUpdate, Txn: txn, Page: page})

	state, ok := g.txns[txn]
	if !ok {
		g.txns[txn] = TxnState{Status: StatusRunning, LastLSN: g.lsn}
	} else {
		state.LastLSN = g.lsn
		g.txns[txn] = state
	}
	if _, ok := g.dirty[page]; !ok {
		g.dirty[page] = g.lsn
	}
}

func (g *randomGenerator) beginCheckpoint() {
	g.records = append(g.records, Record{LSN: g.lsn, Type: RecordBeginCkpt})
	g.checkpoint = &randomCheckpoint{
		beginLSN: g.lsn,
		dirty:    cloneInt64Map(g.dirty),
		txns:     cloneTxnStateMap(g.txns),
	}
}

func (g *randomGenerator) chooseActiveTxn() string {
	txns := mapKeys(g.txns)
	if len(txns) == 0 {
		return ""
	}
	return txns[g.rng.IntN(len(txns))]
}

func mapKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func cloneInt64Map(values map[string]int64) map[string]int64 {
	cloned := make(map[string]int64, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func cloneTxnStateMap(values map[string]TxnState) map[string]TxnState {
	cloned := make(map[string]TxnState, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func naiveAnalyze(records []Record) (Analysis, string) {
	beginAt := map[int64]int{}
	for index, record := range records {
		if record.Type == RecordBeginCkpt {
			beginAt[record.LSN] = index
		}
	}

	chosenBeginLSN := int64(0)
	chosenEndIndex := -1
	for index, record := range records {
		if record.Type != RecordEndCkpt {
			continue
		}
		beginIndex, ok := beginAt[record.BeginLSN]
		if ok && beginIndex < index && (chosenEndIndex < 0 || record.BeginLSN > chosenBeginLSN) {
			chosenBeginLSN = record.BeginLSN
			chosenEndIndex = index
		}
	}

	dirty := map[string]int64{}
	active := map[string]TxnState{}
	if chosenEndIndex >= 0 {
		for page, recLSN := range records[chosenEndIndex].DirtyPages {
			dirty[page] = recLSN
		}
		for txn, state := range records[chosenEndIndex].ActiveTransactions {
			active[txn] = state
		}
	}

	for _, record := range records {
		if chosenEndIndex >= 0 && record.LSN <= chosenBeginLSN {
			continue
		}
		if record.Type == RecordEndCkpt {
			continue
		}

		switch record.Type {
		case RecordUpdate:
			state, ok := active[record.Txn]
			if !ok {
				active[record.Txn] = TxnState{Status: StatusRunning, LastLSN: record.LSN}
			} else {
				state.LastLSN = record.LSN
				active[record.Txn] = state
			}
			if _, ok := dirty[record.Page]; !ok {
				dirty[record.Page] = record.LSN
			}
		case RecordCommit:
			state := active[record.Txn]
			state.Status = StatusCommitted
			state.LastLSN = record.LSN
			active[record.Txn] = state
		case RecordAbort:
			state := active[record.Txn]
			state.Status = StatusAborting
			state.LastLSN = record.LSN
			active[record.Txn] = state
		case RecordEnd:
			delete(active, record.Txn)
		case RecordPageFlush:
			delete(dirty, record.Page)
		}
	}

	result := buildAnalysis(dirty, active)
	basis := "no complete checkpoint; replay from empty tables"
	if chosenEndIndex >= 0 {
		basis = fmt.Sprintf("last complete checkpoint beginLSN=%d endLSN=%d", chosenBeginLSN, records[chosenEndIndex].LSN)
	}
	return result, basis
}
