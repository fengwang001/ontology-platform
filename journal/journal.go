// Package journal implements an append-only metadata log with undo
// records, checkpoints and on-disk version comparison.
//
// A log is a sequence of records: Write, Revoke, Commit and Checkpoint.
// Transactions are numbered from 1; the current open transaction has
// number committed+1. Recover replays a prefix of the log against a
// read-only disk image and produces a new image plus skip counters.
package journal

import (
	"errors"
	"sync"
)

// MaxPayload is the maximum payload size in bytes for a Write record.
const MaxPayload = 1024

var (
	// ErrInvalidArgument reports an illegal argument: negative block,
	// oversized payload, Recover n out of range, or a non-monotonic /
	// uncommitted Checkpoint upto.
	ErrInvalidArgument = errors.New("journal: invalid argument")
	// ErrTransactionFull reports that the open transaction already holds
	// Cap Write/Revoke records.
	ErrTransactionFull = errors.New("journal: transaction full")
	// ErrEmptyTransaction reports a Commit with no records in the open
	// transaction.
	ErrEmptyTransaction = errors.New("journal: empty transaction")
)

// Block is a disk/image block: a version and its payload.
type Block struct {
	Ver     int
	Payload []byte
}

// RecordKind identifies the four record types.
type RecordKind int

const (
	WriteRecord RecordKind = iota
	RevokeRecord
	CommitRecord
	CheckpointRecord
)

// Record is a single appended log record.
type Record struct {
	Kind    RecordKind
	Tid     int    // transaction number (Write, Revoke, Commit)
	Block   int    // block number (Write, Revoke)
	Payload []byte // payload copy (Write)
	Upto    int    // checkpoint watermark (Checkpoint)
}

// Result is the outcome of Recover: the replayed image and counters.
type Result struct {
	Image        map[int]Block
	Applied      int
	Skipped      int
	Stale        int
	Checkpointed int
	Ignored      int
}

// Journal is an append-only metadata log. All methods are safe for
// concurrent use; results equal some serial order of the calls.
type Journal struct {
	mu          sync.Mutex
	cap         int
	records     []Record
	committed   int
	openRecords int
	lastChkUpto int
}

// New creates a log whose transactions hold at most cap Write/Revoke
// records. cap < 1 is rejected with ErrInvalidArgument.
func New(capacity int) (*Journal, error) {
	if capacity < 1 {
		return nil, ErrInvalidArgument
	}
	return &Journal{cap: capacity}, nil
}

// Records returns the total number of log records, including Commit and
// Checkpoint records.
func (j *Journal) Records() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.records)
}

// Write appends Write(tid, block, payload) to the open transaction,
// copying payload.
func (j *Journal) Write(block int, payload []byte) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if block < 0 || len(payload) > MaxPayload {
		return ErrInvalidArgument
	}
	if j.openRecords >= j.cap {
		return ErrTransactionFull
	}
	cp := make([]byte, len(payload))
	copy(cp, payload)
	j.records = append(j.records, Record{
		Kind:    WriteRecord,
		Tid:     j.committed + 1,
		Block:   block,
		Payload: cp,
	})
	j.openRecords++
	return nil
}

// Revoke appends Revoke(tid, block) to the open transaction.
func (j *Journal) Revoke(block int) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if block < 0 {
		return ErrInvalidArgument
	}
	if j.openRecords >= j.cap {
		return ErrTransactionFull
	}
	j.records = append(j.records, Record{
		Kind:  RevokeRecord,
		Tid:   j.committed + 1,
		Block: block,
	})
	j.openRecords++
	return nil
}

// Commit commits the open transaction and appends a Commit record.
func (j *Journal) Commit() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.openRecords == 0 {
		return ErrEmptyTransaction
	}
	j.records = append(j.records, Record{
		Kind: CommitRecord,
		Tid:  j.committed + 1,
	})
	j.committed++
	j.openRecords = 0
	return nil
}

// Checkpoint appends Checkpoint(upto), declaring that transactions with
// tid <= upto have been written back to disk.
func (j *Journal) Checkpoint(upto int) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if upto < j.lastChkUpto || upto > j.committed {
		return ErrInvalidArgument
	}
	j.records = append(j.records, Record{
		Kind: CheckpointRecord,
		Upto: upto,
	})
	j.lastChkUpto = upto
	return nil
}

// Recover replays the first n log records against disk (read-only, nil
// means empty) and returns the resulting image and counters. Neither
// disk nor the log is modified.
func (j *Journal) Recover(disk map[int]Block, n int) (Result, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if n < 0 || n > len(j.records) {
		return Result{}, ErrInvalidArgument
	}
	prefix := j.records[:n]

	committed := make(map[int]bool)
	highWater := 0
	for _, r := range prefix {
		switch r.Kind {
		case CommitRecord:
			committed[r.Tid] = true
		case CheckpointRecord:
			highWater = r.Upto
		}
	}

	type txnBlock struct {
		tid   int
		block int
	}
	lastOp := make(map[txnBlock]RecordKind)
	for _, r := range prefix {
		if r.Kind == WriteRecord || r.Kind == RevokeRecord {
			lastOp[txnBlock{r.Tid, r.Block}] = r.Kind
		}
	}
	revoked := make(map[int]int)
	for tb, kind := range lastOp {
		if kind == RevokeRecord && committed[tb.tid] && tb.tid > revoked[tb.block] {
			revoked[tb.block] = tb.tid
		}
	}

	res := Result{Image: make(map[int]Block)}
	for _, r := range prefix {
		switch r.Kind {
		case WriteRecord:
			if !committed[r.Tid] {
				res.Ignored++
				continue
			}
			switch {
			case r.Tid <= highWater:
				res.Checkpointed++
			case revoked[r.Block] >= r.Tid:
				res.Skipped++
			case diskVer(disk, r.Block) >= r.Tid:
				res.Stale++
			default:
				cp := make([]byte, len(r.Payload))
				copy(cp, r.Payload)
				res.Image[r.Block] = Block{Ver: r.Tid, Payload: cp}
				res.Applied++
			}
		case RevokeRecord:
			if !committed[r.Tid] {
				res.Ignored++
			}
		}
	}
	return res, nil
}

func diskVer(disk map[int]Block, block int) int {
	if b, ok := disk[block]; ok {
		return b.Ver
	}
	return 0
}
