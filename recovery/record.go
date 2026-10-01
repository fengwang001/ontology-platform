// Package recovery implements an ARIES-style recovery analysis phase
// calculator: it rebuilds the dirty page table (DPT) and the active
// transaction table (ATT) from appended log records, and reports the
// redo start point and the set of failed transactions.
package recovery

// LSN is the log sequence number, strictly increasing across the log.
type LSN int64

// TxnID identifies a transaction.
type TxnID int

// PageID identifies a page.
type PageID int

// RecordKind is the type of a log record.
type RecordKind int

const (
	KindUpdate    RecordKind = iota // Update(txn, page)
	KindCommit                      // Commit(txn)
	KindAbort                       // Abort(txn)
	KindEnd                         // End(txn)
	KindBeginCkpt                   // BeginCkpt
	KindEndCkpt                     // EndCkpt(begin, dpt, att)
	KindPageFlush                   // PageFlush(page)
)

func (k RecordKind) String() string {
	switch k {
	case KindUpdate:
		return "Update"
	case KindCommit:
		return "Commit"
	case KindAbort:
		return "Abort"
	case KindEnd:
		return "End"
	case KindBeginCkpt:
		return "BeginCkpt"
	case KindEndCkpt:
		return "EndCkpt"
	case KindPageFlush:
		return "PageFlush"
	}
	return "Unknown"
}

// TxnStatus is the transaction status stored in the ATT.
type TxnStatus int

const (
	StatusRunning   TxnStatus = iota // running
	StatusCommitted                  // committed
	StatusAborting                   // aborting (rolling back)
)

func (s TxnStatus) String() string {
	switch s {
	case StatusRunning:
		return "Running"
	case StatusCommitted:
		return "Committed"
	case StatusAborting:
		return "Aborting"
	}
	return "Unknown"
}

// ATTEntry is an active transaction table entry: status + last LSN.
type ATTEntry struct {
	Status  TxnStatus
	LastLSN LSN
}

// Record is a single log record; fields are used according to Kind.
type Record struct {
	LSN  LSN
	Kind RecordKind
	Txn  TxnID  // Update/Commit/Abort/End
	Page PageID // Update/PageFlush

	Begin LSN                // EndCkpt: LSN of the matching BeginCkpt
	DPT   map[PageID]LSN     // EndCkpt: dirty page table snapshot (page -> recLSN)
	ATT   map[TxnID]ATTEntry // EndCkpt: active transaction table snapshot
}

// Update builds an Update(txn, page) record.
func Update(lsn LSN, txn TxnID, page PageID) Record {
	return Record{LSN: lsn, Kind: KindUpdate, Txn: txn, Page: page}
}

// Commit builds a Commit(txn) record.
func Commit(lsn LSN, txn TxnID) Record {
	return Record{LSN: lsn, Kind: KindCommit, Txn: txn}
}

// Abort builds an Abort(txn) record.
func Abort(lsn LSN, txn TxnID) Record {
	return Record{LSN: lsn, Kind: KindAbort, Txn: txn}
}

// End builds an End(txn) record.
func End(lsn LSN, txn TxnID) Record {
	return Record{LSN: lsn, Kind: KindEnd, Txn: txn}
}

// BeginCkpt builds a BeginCkpt record.
func BeginCkpt(lsn LSN) Record {
	return Record{LSN: lsn, Kind: KindBeginCkpt}
}

// EndCkpt builds an EndCkpt(begin, dpt, att) record.
// The dpt/att snapshots are defensively copied.
func EndCkpt(lsn LSN, begin LSN, dpt map[PageID]LSN, att map[TxnID]ATTEntry) Record {
	dptCopy := make(map[PageID]LSN, len(dpt))
	for p, r := range dpt {
		dptCopy[p] = r
	}
	attCopy := make(map[TxnID]ATTEntry, len(att))
	for t, e := range att {
		attCopy[t] = e
	}
	return Record{LSN: lsn, Kind: KindEndCkpt, Begin: begin, DPT: dptCopy, ATT: attCopy}
}

// PageFlush builds a PageFlush(page) record.
func PageFlush(lsn LSN, page PageID) Record {
	return Record{LSN: lsn, Kind: KindPageFlush, Page: page}
}

// referencesTxn reports whether the record references a transaction
// (used by Append validation).
func (r Record) referencesTxn() bool {
	switch r.Kind {
	case KindUpdate, KindCommit, KindAbort, KindEnd:
		return true
	}
	return false
}
