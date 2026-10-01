package pbft

import (
	"errors"
	"sync"
)

type MessageKind int

const (
	PrePrepare MessageKind = iota + 1
	Prepare
	Commit
)

type Message struct {
	Kind   MessageKind
	View   int64
	Seq    int64
	Digest string
	From   int
}

type Execution struct {
	Seq    int64
	Digest string
}

var (
	ErrInvalidParameters     = errors.New("pbft: invalid parameters")
	ErrSenderOutOfRange      = errors.New("pbft: sender out of range")
	ErrEmptyDigest           = errors.New("pbft: empty digest")
	ErrWrongView             = errors.New("pbft: wrong view")
	ErrSeqOutOfWindow        = errors.New("pbft: sequence out of window")
	ErrPrePrepareFromBackup  = errors.New("pbft: pre-prepare must come from primary")
	ErrPrepareFromPrimary    = errors.New("pbft: prepare must not come from primary")
	ErrUnknownMessageKind    = errors.New("pbft: unknown message kind")
	ErrPrePrepareDigestClash = errors.New("pbft: conflicting pre-prepare digest")
	ErrPrepareDigestClash    = errors.New("pbft: conflicting prepare digest")
	ErrCommitDigestClash     = errors.New("pbft: conflicting commit digest")
)

type Log struct {
	mu       sync.Mutex
	f        int
	n        int
	view     int64
	limit    int64
	primary  int
	executed int64
	entries  map[int64]*seqEntry
}

type seqEntry struct {
	prePrepare    string
	hasPrePrepare bool
	prepares      map[int]string
	commits       map[int]string
}

func New(f int, view int64, windowSize int) (*Log, error) {
	if f < 1 || windowSize < 1 {
		return nil, ErrInvalidParameters
	}

	n := 3*f + 1
	return &Log{
		f:       f,
		n:       n,
		view:    view,
		limit:   int64(windowSize),
		primary: int(uint64(view) % uint64(n)),
		entries: make(map[int64]*seqEntry),
	}, nil
}

func (l *Log) Observe(msg Message) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if msg.From < 0 || msg.From >= l.n {
		return ErrSenderOutOfRange
	}
	if msg.Digest == "" {
		return ErrEmptyDigest
	}
	if msg.View != l.view {
		return ErrWrongView
	}
	if msg.Seq <= l.executed || msg.Seq > l.executed+l.limit {
		return ErrSeqOutOfWindow
	}

	switch msg.Kind {
	case PrePrepare:
		if msg.From != l.primary {
			return ErrPrePrepareFromBackup
		}
	case Prepare:
		if msg.From == l.primary {
			return ErrPrepareFromPrimary
		}
	case Commit:
	default:
		return ErrUnknownMessageKind
	}

	entry := l.entries[msg.Seq]
	if entry == nil {
		entry = &seqEntry{
			prepares: make(map[int]string),
			commits:  make(map[int]string),
		}
		l.entries[msg.Seq] = entry
	}

	switch msg.Kind {
	case PrePrepare:
		if entry.hasPrePrepare && entry.prePrepare != msg.Digest {
			return ErrPrePrepareDigestClash
		}
		entry.prePrepare = msg.Digest
		entry.hasPrePrepare = true
	case Prepare:
		if digest, ok := entry.prepares[msg.From]; ok && digest != msg.Digest {
			return ErrPrepareDigestClash
		}
		entry.prepares[msg.From] = msg.Digest
	case Commit:
		if digest, ok := entry.commits[msg.From]; ok && digest != msg.Digest {
			return ErrCommitDigestClash
		}
		entry.commits[msg.From] = msg.Digest
	}

	return nil
}

func (l *Log) Execute() []Execution {
	l.mu.Lock()
	defer l.mu.Unlock()

	var result []Execution
	for seq := l.executed + 1; ; seq++ {
		digest, ok := l.committedDigestLocked(seq)
		if !ok {
			break
		}

		result = append(result, Execution{Seq: seq, Digest: digest})
		delete(l.entries, seq)
		l.executed = seq
	}

	out := make([]Execution, len(result))
	copy(out, result)
	return out
}

func (l *Log) committedDigestLocked(seq int64) (string, bool) {
	entry := l.entries[seq]
	if entry == nil || !entry.hasPrePrepare {
		return "", false
	}

	digest := entry.prePrepare
	prepareCount := 0
	for from, prepareDigest := range entry.prepares {
		if from != l.primary && prepareDigest == digest {
			prepareCount++
		}
	}
	if prepareCount < 2*l.f {
		return "", false
	}

	commitCount := 0
	for _, commitDigest := range entry.commits {
		if commitDigest == digest {
			commitCount++
		}
	}
	if commitCount < 2*l.f+1 {
		return "", false
	}

	return digest, true
}
