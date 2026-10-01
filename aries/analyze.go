package aries

import "slices"

func Analyze(records []Record) Analysis {
	beginLSN, endLSN := latestCompleteCheckpoint(records)

	dirtyPages := make(map[string]int64)
	activeTxns := make(map[string]TxnState)

	hasCheckpoint := endLSN >= 0
	if hasCheckpoint {
		endRecord := records[endLSN]
		for page, recLSN := range endRecord.DirtyPages {
			dirtyPages[page] = recLSN
		}
		for txn, state := range endRecord.ActiveTransactions {
			activeTxns[txn] = state
		}
	}

	for _, record := range records {
		if record.Type == RecordEndCkpt {
			continue
		}
		if hasCheckpoint && record.LSN <= beginLSN {
			continue
		}

		switch record.Type {
		case RecordUpdate:
			state, ok := activeTxns[record.Txn]
			if !ok {
				activeTxns[record.Txn] = TxnState{Status: StatusRunning, LastLSN: record.LSN}
			} else {
				state.LastLSN = record.LSN
				activeTxns[record.Txn] = state
			}

			if _, ok := dirtyPages[record.Page]; !ok {
				dirtyPages[record.Page] = record.LSN
			}
		case RecordCommit:
			changeTxnStatus(activeTxns, record.Txn, StatusCommitted, record.LSN)
		case RecordAbort:
			changeTxnStatus(activeTxns, record.Txn, StatusAborting, record.LSN)
		case RecordEnd:
			delete(activeTxns, record.Txn)
		case RecordPageFlush:
			delete(dirtyPages, record.Page)
		}
	}

	return buildAnalysis(dirtyPages, activeTxns)
}

func latestCompleteCheckpoint(records []Record) (int64, int) {
	begins := make(map[int64]int)
	for index, record := range records {
		if record.Type == RecordBeginCkpt {
			begins[record.LSN] = index
		}
	}

	var chosenBegin int64
	var chosenEnd int
	var hasCheckpoint bool
	for index, record := range records {
		if record.Type != RecordEndCkpt {
			continue
		}
		if beginIndex, ok := begins[record.BeginLSN]; ok && beginIndex < index {
			if !hasCheckpoint || record.BeginLSN > chosenBegin {
				chosenBegin = record.BeginLSN
				chosenEnd = index
				hasCheckpoint = true
			}
		}
	}

	if !hasCheckpoint {
		return 0, -1
	}

	return chosenBegin, chosenEnd
}

func changeTxnStatus(activeTxns map[string]TxnState, txn string, status TxnStatus, lsn int64) {
	state, ok := activeTxns[txn]
	if !ok {
		state = TxnState{Status: status, LastLSN: lsn}
	} else {
		state.Status = status
		state.LastLSN = lsn
	}
	activeTxns[txn] = state
}

func buildAnalysis(dirtyPages map[string]int64, activeTxns map[string]TxnState) Analysis {
	pages := make([]DirtyPage, 0, len(dirtyPages))
	for page, recLSN := range dirtyPages {
		pages = append(pages, DirtyPage{Page: page, RecLSN: recLSN})
	}
	slices.SortFunc(pages, func(left, right DirtyPage) int {
		switch {
		case left.Page < right.Page:
			return -1
		case left.Page > right.Page:
			return 1
		default:
			return 0
		}
	})

	txns := make([]ActiveTransaction, 0, len(activeTxns))
	failed := make([]string, 0)
	for txn, state := range activeTxns {
		txns = append(txns, ActiveTransaction{
			Txn:     txn,
			Status:  state.Status,
			LastLSN: state.LastLSN,
		})
		if state.Status == StatusRunning || state.Status == StatusAborting {
			failed = append(failed, txn)
		}
	}
	slices.SortFunc(txns, func(left, right ActiveTransaction) int {
		switch {
		case left.Txn < right.Txn:
			return -1
		case left.Txn > right.Txn:
			return 1
		default:
			return 0
		}
	})
	slices.Sort(failed)

	var redoLSN *int64
	if len(pages) > 0 {
		minimum := pages[0].RecLSN
		for _, page := range pages[1:] {
			if page.RecLSN < minimum {
				minimum = page.RecLSN
			}
		}
		redoLSN = &minimum
	}

	return Analysis{
		RedoLSN:            redoLSN,
		DirtyPages:         pages,
		ActiveTransactions: txns,
		FailedTxns:         failed,
	}
}
