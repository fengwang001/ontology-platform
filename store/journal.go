package store

import "ontology/txid"

// Op identifies what one journaled write did to one key.
type Op uint8

const (
	opPut    Op = iota + 1 // value version
	opDelete               // tombstone version
)

// writeRec is one key-level version record inside a transaction.
type writeRec struct {
	Key   string
	Op    Op
	Value []byte
}

// txnRec is the journal record for one transaction.
type txnRec struct {
	Txn       txid.ID
	Writes    []writeRec
	Committed bool
}

// journal is a process-memory "durable" log. Surviving a simulated crash
// means: the volatile store is thrown away, but a journal handed to Recover
// remains. This models WAL durability without touching the real filesystem.
type journal struct {
	recs  map[txid.ID]*txnRec
	order []txid.ID
	maxID txid.ID
}

func newJournal() *journal {
	return &journal{recs: make(map[txid.ID]*txnRec)}
}

// Begin records that a transaction with id exists (allocation boundary).
func (j *journal) Begin(id txid.ID) {
	if _, ok := j.recs[id]; !ok {
		j.recs[id] = &txnRec{Txn: id}
		j.order = append(j.order, id)
		if j.maxID.Less(id) {
			j.maxID = id
		}
	}
}

// AppendWrite durably logs one key write for txn.
func (j *journal) AppendWrite(txn txid.ID, w writeRec) {
	rec := j.recs[txn]
	rec.Writes = append(rec.Writes, w)
}

// Commit durably marks the transaction committed. This is the atomic
// visibility decision: present => fully visible, absent => fully invisible.
func (j *journal) Commit(txn txid.ID) {
	j.recs[txn].Committed = true
}

// MaxID returns the highest begun transaction id, for source reconstruction.
func (j *journal) MaxID() txid.ID { return j.maxID }
