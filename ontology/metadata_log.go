package ontology

import (
	"errors"
	"sync"
)

// Sentinel errors identify rejected metadata log operations.
var (
	ErrInvalidArgument  = errors.New("invalid metadata log argument")
	ErrTransactionFull  = errors.New("metadata transaction is full")
	ErrEmptyTransaction = errors.New("cannot commit an empty metadata transaction")
)

// Block is a disk version and payload pair.
type Block struct {
	Ver     int
	Payload []byte
}

// RecoveryResult contains the recovered image and per-decision counters.
type RecoveryResult struct {
	Image        map[int]Block
	Applied      int
	Skipped      int
	Stale        int
	Checkpointed int
	Ignored      int
}

type recordKind int

const (
	writeRecord recordKind = iota
	revokeRecord
	commitRecord
	checkpointRecord
)

type record struct {
	kind    recordKind
	tid     int
	block   int
	payload []byte
	upto    int
}

// MetadataLog is a concurrency-safe append-only metadata transaction log.
type MetadataLog struct {
	mu             sync.RWMutex
	capacity       int
	committed      int
	openRecords    int
	lastCheckpoint int
	records        []record
}

func clonePayload(payload []byte) []byte {
	copied := make([]byte, len(payload))
	copy(copied, payload)
	return copied
}

// NewMetadataLog creates an append-only log with per-transaction record capacity.
func NewMetadataLog(capacity int) (*MetadataLog, error) {
	if capacity < 1 {
		return nil, ErrInvalidArgument
	}
	return &MetadataLog{capacity: capacity, records: make([]record, 0)}, nil
}

// Write appends a block write to the current open transaction.
func (l *MetadataLog) Write(block int, payload []byte) error {
	if block < 0 || len(payload) > 1024 {
		return ErrInvalidArgument
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.openRecords == l.capacity {
		return ErrTransactionFull
	}

	l.records = append(l.records, record{
		kind:    writeRecord,
		tid:     l.committed + 1,
		block:   block,
		payload: clonePayload(payload),
	})
	l.openRecords++
	return nil
}

// Revoke appends a block revocation to the current open transaction.
func (l *MetadataLog) Revoke(block int) error {
	if block < 0 {
		return ErrInvalidArgument
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.openRecords == l.capacity {
		return ErrTransactionFull
	}

	l.records = append(l.records, record{
		kind:  revokeRecord,
		tid:   l.committed + 1,
		block: block,
	})
	l.openRecords++
	return nil
}

// Commit commits and appends a commit record for the current transaction.
func (l *MetadataLog) Commit() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.openRecords == 0 {
		return ErrEmptyTransaction
	}

	l.records = append(l.records, record{
		kind: commitRecord,
		tid:  l.committed + 1,
	})
	l.committed++
	l.openRecords = 0
	return nil
}

// Checkpoint appends a declaration that transactions up to upto are durable.
func (l *MetadataLog) Checkpoint(upto int) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if upto < l.lastCheckpoint || upto > l.committed {
		return ErrInvalidArgument
	}

	l.records = append(l.records, record{
		kind: checkpointRecord,
		upto: upto,
	})
	l.lastCheckpoint = upto
	return nil
}

// Recover replays only the first n records without changing the log or disk.
func (l *MetadataLog) Recover(disk map[int]Block, n int) (RecoveryResult, error) {
	l.mu.RLock()
	if n < 0 || n > len(l.records) {
		l.mu.RUnlock()
		return RecoveryResult{}, ErrInvalidArgument
	}
	logRecords := append([]record(nil), l.records[:n]...)
	l.mu.RUnlock()

	committed := make(map[int]bool)
	checkpoint := 0
	for _, rec := range logRecords {
		switch rec.kind {
		case commitRecord:
			committed[rec.tid] = true
		case checkpointRecord:
			checkpoint = rec.upto
		}
	}

	revocations := make(map[int]int)
	lastActionByBlock := make(map[int]map[int]recordKind)
	result := RecoveryResult{}

	for _, rec := range logRecords {
		switch rec.kind {
		case writeRecord, revokeRecord:
			if !committed[rec.tid] {
				result.Ignored++
				continue
			}
			if lastActionByBlock[rec.tid] == nil {
				lastActionByBlock[rec.tid] = make(map[int]recordKind)
			}
			lastActionByBlock[rec.tid][rec.block] = rec.kind
		}
	}
	for tid, actions := range lastActionByBlock {
		for block, action := range actions {
			if action == revokeRecord && tid > revocations[block] {
				revocations[block] = tid
			}
		}
	}

	image := make(map[int]Block, len(disk))
	for block, value := range disk {
		payload := value.Payload
		if payload == nil {
			payload = []byte{}
		} else {
			payload = clonePayload(payload)
		}
		image[block] = Block{Ver: value.Ver, Payload: payload}
	}

	for _, rec := range logRecords {
		if rec.kind != writeRecord || !committed[rec.tid] {
			continue
		}

		block := rec.block
		switch {
		case rec.tid <= checkpoint:
			result.Checkpointed++
		case revocations[block] >= rec.tid:
			result.Skipped++
		case disk[block].Ver >= rec.tid:
			result.Stale++
		default:
			image[block] = Block{
				Ver:     rec.tid,
				Payload: clonePayload(rec.payload),
			}
			result.Applied++
		}
	}

	result.Image = image
	return result, nil
}

// Records returns the total number of appended records.
func (l *MetadataLog) Records() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.records)
}
