package ontology

// Arbiter is the instance-version arbitration module. It contains no state of
// its own: callers hold the Store's global lock, which is the single
// serialization point, and consult these pure token decisions. A rejected
// credential never advances any version number.
type Arbiter struct{}

// NewArbiter builds the arbiter.
func NewArbiter() *Arbiter { return &Arbiter{} }

// Decision is the outcome of a token check.
type Decision struct {
	Accept        bool
	Current       int64
	Deleted       bool
	EverCommitted bool
	Reason        ConflictReason
}

// CheckWrite validates a write credential against the current record state.
//   - expected==0: create; only succeeds when nothing ever committed.
//   - expected>0 : update; only succeeds on a live record at that exact version.
func (a *Arbiter) CheckWrite(cur int64, deleted, ever bool, expected int64) Decision {
	d := Decision{Current: cur, Deleted: deleted, EverCommitted: ever}
	if expected == 0 {
		if ever {
			d.Reason = ReasonStaleVersion
			return d
		}
		d.Accept = true
		return d
	}
	if !ever || deleted {
		d.Reason = ReasonWriteOnMissing
		return d
	}
	if expected != cur {
		d.Reason = ReasonStaleVersion
		return d
	}
	d.Accept = true
	return d
}

// CheckDelete validates a delete credential against the current record state.
//   - never committed  => NotFound (caller distinguishes this first).
//   - tombstone        => VersionConflict/AlreadyDeleted.
//   - expected==0      => accepts any live version.
//   - expected>0       => accepts only the exact live version.
func (a *Arbiter) CheckDelete(cur int64, deleted, ever bool, expected int64) Decision {
	d := Decision{Current: cur, Deleted: deleted, EverCommitted: ever}
	if !ever {
		return d // caller maps this to ErrNotFound
	}
	if deleted {
		d.Reason = ReasonAlreadyDeleted
		return d
	}
	if expected != 0 && expected != cur {
		d.Reason = ReasonStaleVersion
		return d
	}
	d.Accept = true
	return d
}
