package percolator

import "fmt"

// WriteType is the kind of pending or committed write.
type WriteType int

const (
	Put WriteType = iota
	Delete
	Rollback
)

func (t WriteType) String() string {
	switch t {
	case Put:
		return "Put"
	case Delete:
		return "Delete"
	case Rollback:
		return "Rollback"
	default:
		return fmt.Sprintf("WriteType(%d)", int(t))
	}
}

// Mutation is one key of a transaction's write set.
type Mutation struct {
	Key   string
	Type  WriteType // Put or Delete
	Value string    // used only when Type == Put
}

// Version is a committed write on a key: (commitTs, startTs, type, value).
type Version struct {
	CommitTs uint64
	StartTs  uint64
	Type     WriteType
	Value    string
}

// Lock is the at-most-one prewrite lock on a key.
type Lock struct {
	StartTs uint64
	Primary string
	Expire  uint64
	Type    WriteType
	Value   string
}

// TxnStatus is the resolution outcome of a primary key.
type TxnStatus int

const (
	StatusLive TxnStatus = iota
	StatusCommitted
	StatusRolledBack
)

func (s TxnStatus) String() string {
	switch s {
	case StatusLive:
		return "live"
	case StatusCommitted:
		return "committed"
	case StatusRolledBack:
		return "rolled_back"
	default:
		return fmt.Sprintf("TxnStatus(%d)", int(s))
	}
}

const maxTTL = 1_000_000_000
const maxNow = 1_000_000_000_000_000
