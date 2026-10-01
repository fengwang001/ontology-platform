// Package pbftlog implements the request log of a PBFT replica within one
// fixed view. It collects pre-prepare, prepare and commit messages keyed by
// (view, sequence, digest), evaluates the two certificate levels (prepared
// and committed-local) and executes requests in strictly ascending sequence
// order.
package pbftlog

import "sync"

// Kind identifies one of the three PBFT message types handled by a replica.
type Kind int

const (
	// PrePrepare is the primary's pre-prepare message.
	PrePrepare Kind = iota
	// Prepare is a backup's prepare message.
	Prepare
	// Commit is any replica's commit message.
	Commit
)

func (k Kind) String() string {
	switch k {
	case PrePrepare:
		return "PrePrepare"
	case Prepare:
		return "Prepare"
	case Commit:
		return "Commit"
	default:
		return "Unknown"
	}
}

// Message is a single pre-prepare/prepare/commit record. Digest is a
// non-empty string; From is the sender replica id in [0, N).
type Message struct {
	View   int
	Seq    int
	Digest string
	From   int
}

// ExecutedEntry is one (sequence, digest) pair executed by Execute.
type ExecutedEntry struct {
	Seq    int
	Digest string
}

// Log is the fixed-view request log of one replica. Its methods are safe for
// concurrent use: every call is linearized under a single mutex.
type Log struct {
	mu       sync.Mutex
	f        int
	n        int
	view     int
	primary  int
	limit    int
	executed int
	entries  map[int]*entry
}

type entry struct {
	prePrepareDigest string
	hasPrePrepare    bool
	prepares         map[int]string // sender -> digest, backups only
	commits          map[int]string // sender -> digest, any replica
}

// New creates a Log. f is the Byzantine fault bound (N = 3f+1), view is the
// fixed non-negative view and limit L is the in-flight window size.
func New(f int, view int, limit int) (*Log, error) {
	if f < 1 {
		return nil, ErrInvalidFaultBound
	}
	if limit < 1 {
		return nil, ErrInvalidLimit
	}
	if view < 0 {
		return nil, ErrInvalidView
	}
	n := 3*f + 1
	return &Log{
		f:       f,
		n:       n,
		view:    view,
		primary: view % n,
		limit:   limit,
		entries: make(map[int]*entry),
	}, nil
}

// N returns the total number of replicas.
func (l *Log) N() int { return l.n }

// View returns the fixed view.
func (l *Log) View() int { return l.view }

// Primary returns the primary replica id.
func (l *Log) Primary() int { return l.primary }

// Limit returns the window size L.
func (l *Log) Limit() int { return l.limit }

// Executed returns the highest executed sequence number.
func (l *Log) Executed() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.executed
}

// Handle records one message. A nil error means accepted (a duplicate with the
// same digest is a legal no-op). Rejected messages leave all state untouched.
func (l *Log) Handle(kind Kind, m Message) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Validation follows the mandated priority order exactly; the first
	// failing check is the only one reported.
	if m.From < 0 || m.From >= l.n {
		return ErrSenderOutOfRange
	}
	if m.Digest == "" {
		return ErrEmptyDigest
	}
	if m.View != l.view {
		return ErrWrongView
	}
	if m.Seq <= l.executed || m.Seq > l.executed+l.limit {
		return ErrSeqOutOfWindow
	}
	switch kind {
	case PrePrepare:
		if m.From != l.primary {
			return ErrPrePrepareFromBackup
		}
	case Prepare:
		if m.From == l.primary {
			return ErrPrepareFromPrimary
		}
	}

	// Every check passed: only now may state be touched.
	e := l.entries[m.Seq]
	if e == nil {
		e = &entry{
			prepares: make(map[int]string),
			commits:  make(map[int]string),
		}
		l.entries[m.Seq] = e
	}

	switch kind {
	case PrePrepare:
		if e.hasPrePrepare && e.prePrepareDigest != m.Digest {
			return ErrConflictingPrePrepare
		}
		e.hasPrePrepare = true
		e.prePrepareDigest = m.Digest
	case Prepare:
		if d, ok := e.prepares[m.From]; ok && d != m.Digest {
			return ErrConflictingPrepare
		}
		e.prepares[m.From] = m.Digest
	case Commit:
		if d, ok := e.commits[m.From]; ok && d != m.Digest {
			return ErrConflictingCommit
		}
		e.commits[m.From] = m.Digest
	}
	return nil
}

// Prepared reports whether Prepared(seq, digest) holds: a matching
// pre-prepare plus matching prepares from at least 2f distinct non-primary
// replicas.
func (l *Log) Prepared(seq int, digest string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entries[seq]
	return e != nil && l.preparedLocked(e, digest)
}

// CommittedLocal reports whether CommittedLocal(seq, digest) holds:
// Prepared(seq, digest) plus matching commits from at least 2f+1 distinct
// replicas (the primary included).
func (l *Log) CommittedLocal(seq int, digest string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entries[seq]
	return e != nil && l.committedLocalLocked(e, digest)
}

func (l *Log) preparedLocked(e *entry, digest string) bool {
	if !e.hasPrePrepare || e.prePrepareDigest != digest {
		return false
	}
	return countDigest(e.prepares, digest) >= 2*l.f
}

func (l *Log) committedLocalLocked(e *entry, digest string) bool {
	if !l.preparedLocked(e, digest) {
		return false
	}
	return countDigest(e.commits, digest) >= 2*l.f+1
}

func countDigest(votes map[int]string, digest string) int {
	count := 0
	for _, d := range votes {
		if d == digest {
			count++
		}
	}
	return count
}

// Execute advances executed over every contiguous committed-local sequence
// starting at executed+1 and returns the newly executed (seq, digest) pairs.
// Each sequence executes exactly once; the returned slice is freshly
// allocated and never aliases internal state.
func (l *Log) Execute() []ExecutedEntry {
	l.mu.Lock()
	defer l.mu.Unlock()

	done := make([]ExecutedEntry, 0)
	for seq := l.executed + 1; ; seq++ {
		e := l.entries[seq]
		// At most one digest can become prepared: prepared requires the
		// unique pre-prepare digest.
		if e == nil || !e.hasPrePrepare ||
			!l.committedLocalLocked(e, e.prePrepareDigest) {
			break
		}
		done = append(done, ExecutedEntry{Seq: seq, Digest: e.prePrepareDigest})
		l.executed = seq
		delete(l.entries, seq)
	}
	return done
}
