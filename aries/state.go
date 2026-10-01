package aries

func validateRecord(record Record) error {
	switch record.Type {
	case RecordUpdate:
		if record.Txn == "" || record.Page == "" {
			return ErrInvalidRecord
		}
	case RecordCommit, RecordAbort, RecordEnd:
		if record.Txn == "" {
			return ErrInvalidRecord
		}
	case RecordBeginCkpt:
	case RecordPageFlush:
		if record.Page == "" {
			return ErrInvalidRecord
		}
	case RecordEndCkpt:
		for page, recLSN := range record.DirtyPages {
			if page == "" {
				return ErrInvalidRecord
			}
			_ = recLSN
		}
		for txn, state := range record.ActiveTransactions {
			if txn == "" || !validTxnStatus(state.Status) {
				return ErrInvalidRecord
			}
		}
	default:
		return ErrInvalidRecord
	}

	return nil
}

func validTxnStatus(status TxnStatus) bool {
	switch status {
	case StatusRunning, StatusCommitted, StatusAborting:
		return true
	default:
		return false
	}
}

func isTerminalRecord(recordType RecordType) bool {
	return recordType == RecordCommit || recordType == RecordAbort || recordType == RecordEnd
}

func recordReferencesEndedTxn(record Record, endedTxns map[string]struct{}) bool {
	if record.Txn != "" {
		if _, ok := endedTxns[record.Txn]; ok {
			return true
		}
	}

	if record.Type == RecordEndCkpt {
		for txn := range record.ActiveTransactions {
			if _, ok := endedTxns[txn]; ok {
				return true
			}
		}
	}

	return false
}

func cloneRecord(record Record) Record {
	if record.DirtyPages != nil {
		dirtyPages := make(map[string]int64, len(record.DirtyPages))
		for page, recLSN := range record.DirtyPages {
			dirtyPages[page] = recLSN
		}
		record.DirtyPages = dirtyPages
	}

	if record.ActiveTransactions != nil {
		activeTransactions := make(map[string]TxnState, len(record.ActiveTransactions))
		for txn, state := range record.ActiveTransactions {
			activeTransactions[txn] = state
		}
		record.ActiveTransactions = activeTransactions
	}

	return record
}
