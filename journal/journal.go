// Package journal is an append-only, in-memory execution log shared by SAGA
// instances. Records are immutable once appended. It depends on no other
// package.
package journal

import (
	"errors"
	"sync"
)

// ErrJournalFull is returned before appending when the configured maximum
// number of records would be exceeded; no partial record is written.
var ErrJournalFull = errors.New("journal: maximum record count exceeded")

// Kind enumerates every record type that can appear in an instance log.
type Kind int

const (
	AttemptForward   Kind = iota + 1 // one physical forward invocation
	ForwardOK                        // forward succeeded, effect present
	ForwardUncertain                 // forward may have produced an effect
	ForwardFailed                    // forward definitively failed, no effect
	AttemptComp                      // one physical compensation invocation
	Compensated                      // compensation succeeded
	CompFailed                       // compensation failed after retries
	MarkForwardDone                  // terminal: whole forward phase succeeded
	MarkCompDone                     // terminal: compensation phase finished
)

// Record is one immutable log entry.
type Record struct {
	Seq      int64
	Instance string
	Kind     Kind
	Index    int    // step index, -1 for terminal markers
	Key      string // step idempotency key
	Attempt  int    // 0-based physical attempt within the step/direction
	ErrText  string // terminal error, empty on success
	Ts       int64  // injected clock, milliseconds
}

// Journal stores records per instance. MaxRecords<=0 means unlimited.
type Journal struct {
	mu        sync.Mutex
	perInst   map[string][]Record
	max       int
	nextSeq   int64
}

// New creates a Journal capped at maxRecords total records.
func New(maxRecords int) *Journal {
	return &Journal{perInst: map[string][]Record{}, max: maxRecords}
}

// Append validates capacity before writing and returns ErrJournalFull without
// mutating the log when full.
func (j *Journal) Append(r Record) (Record, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.max > 0 && int(j.nextSeq) >= j.max {
		return Record{}, ErrJournalFull
	}
	j.nextSeq++
	r.Seq = j.nextSeq
	j.perInst[r.Instance] = append(j.perInst[r.Instance], r)
	return r, nil
}

// Read returns a copy of the records of one instance, in append order.
func (j *Journal) Read(instance string) []Record {
	j.mu.Lock()
	defer j.mu.Unlock()
	src := j.perInst[instance]
	out := make([]Record, len(src))
	copy(out, src)
	return out
}

// Last reports whether instance has records and, if so, the final one.
func (j *Journal) Last(instance string) (Record, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	rs := j.perInst[instance]
	if len(rs) == 0 {
		return Record{}, false
	}
	return rs[len(rs)-1], true
}

// Len returns the number of records stored for an instance.
func (j *Journal) Len(instance string) int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.perInst[instance])
}
