package recovery

import "sort"

// analyzeRecords runs the ARIES analysis phase over a complete record
// sequence. It is a pure function: identical inputs always produce
// identical results.
func analyzeRecords(records []Record) Result {
	// Index EndCkpt snapshots by the LSN of their BeginCkpt.
	endCkptByBegin := make(map[LSN]Record)
	for _, r := range records {
		if r.Kind == KindEndCkpt {
			endCkptByBegin[r.Begin] = r
		}
	}

	// Find the last BeginCkpt that has a matching EndCkpt. BeginCkpt
	// records without an EndCkpt are incomplete and ignored.
	startLSN := LSN(-1)
	haveCheckpoint := false
	var snapshot Record
	for i := len(records) - 1; i >= 0; i-- {
		r := records[i]
		if r.Kind != KindBeginCkpt {
			continue
		}
		if ec, ok := endCkptByBegin[r.LSN]; ok {
			startLSN = r.LSN
			snapshot = ec
			haveCheckpoint = true
			break
		}
	}

	dpt := make(map[PageID]LSN)
	att := make(map[TxnID]ATTEntry)

	// Initialize both tables from the chosen EndCkpt snapshot.
	if haveCheckpoint {
		for page, recLSN := range snapshot.DPT {
			dpt[page] = recLSN
		}
		for txn, entry := range snapshot.ATT {
			att[txn] = entry
		}
	}

	// Scan records with LSN greater than the chosen BeginCkpt in LSN
	// order. The EndCkpt record itself is not processed (its snapshot
	// was already applied); other EndCkpt/BeginCkpt records are no-ops.
	for _, r := range records {
		if haveCheckpoint && r.LSN <= startLSN {
			continue
		}
		switch r.Kind {
		case KindUpdate:
			if entry, ok := att[r.Txn]; ok {
				// Already active: only refresh last LSN, keep status.
				entry.LastLSN = r.LSN
				att[r.Txn] = entry
			} else {
				att[r.Txn] = ATTEntry{Status: StatusRunning, LastLSN: r.LSN}
			}
			if _, ok := dpt[r.Page]; !ok {
				dpt[r.Page] = r.LSN
			}
		case KindCommit:
			att[r.Txn] = ATTEntry{Status: StatusCommitted, LastLSN: r.LSN}
		case KindAbort:
			att[r.Txn] = ATTEntry{Status: StatusAborting, LastLSN: r.LSN}
		case KindEnd:
			delete(att, r.Txn)
		case KindPageFlush:
			delete(dpt, r.Page)
		case KindBeginCkpt, KindEndCkpt:
			// No effect on the tables during the scan.
		}
	}

	return buildResult(dpt, att)
}

// buildResult renders the tables into the sorted, deterministic Result.
func buildResult(dpt map[PageID]LSN, att map[TxnID]ATTEntry) Result {
	res := Result{
		DPT:    make([]DirtyPageEntry, 0, len(dpt)),
		ATT:    make([]ActiveTxnEntry, 0, len(att)),
		Failed: []TxnID{},
	}

	for page, recLSN := range dpt {
		res.DPT = append(res.DPT, DirtyPageEntry{Page: page, RecLSN: recLSN})
	}
	sort.Slice(res.DPT, func(i, j int) bool { return res.DPT[i].Page < res.DPT[j].Page })

	for txn, entry := range att {
		res.ATT = append(res.ATT, ActiveTxnEntry{Txn: txn, Entry: entry})
	}
	sort.Slice(res.ATT, func(i, j int) bool { return res.ATT[i].Txn < res.ATT[j].Txn })

	if len(res.DPT) > 0 {
		res.NeedRedo = true
		res.RedoLSN = res.DPT[0].RecLSN
		for _, e := range res.DPT[1:] {
			if e.RecLSN < res.RedoLSN {
				res.RedoLSN = e.RecLSN
			}
		}
	}

	for _, e := range res.ATT {
		if e.Entry.Status == StatusRunning || e.Entry.Status == StatusAborting {
			res.Failed = append(res.Failed, e.Txn)
		}
	}
	return res
}
