package ontology

import (
	"errors"
	"sort"
	"sync"
)

type Status string

const (
	OPEN      Status = "OPEN"
	PREPARED  Status = "PREPARED"
	COMMITTED Status = "COMMITTED"
	ABORTED   Status = "ABORTED"
)

var (
	ErrInvalidArgument    = errors.New("invalid argument")
	ErrCheckpointOrder    = errors.New("checkpoint out of order")
	ErrStaleNotification  = errors.New("stale notification")
	ErrFutureNotification = errors.New("future notification")
	ErrRestoreRollback    = errors.New("restore checkpoint before last notification")
	ErrRestoreFuture      = errors.New("restore checkpoint after last barrier")
	ErrNotFound           = errors.New("transaction not found")
)

type Name struct {
	Subtask    int
	Checkpoint int
}

type TxnInfo struct {
	Status  Status
	Epoch   int
	Records int
	Life    int
}

type Stats struct {
	CommittedRecords int
	AbortedRecords   int
	OpenRecords      int
	PreparedRecords  int
	Probes           int
}

type RestoreResult struct {
	Committed []Name
	Aborted   []Name
	Probes    int
}

type transaction struct {
	status  Status
	epoch   int
	records int
	life    int
}

type Registry struct {
	mu sync.RWMutex

	parallelism int
	missLimit   int
	lastBarrier int
	lastNotify  int
	life        int
	txns        map[Name]transaction
	prepared    []Name

	committedRecords int
	abortedRecords   int
	probes           int
	visited          int
}

func New(parallelism, missLimit int) (*Registry, error) {
	if parallelism < 1 || parallelism > 64 || missLimit < 1 || missLimit > 1000 {
		return nil, ErrInvalidArgument
	}
	return &Registry{
		parallelism: parallelism,
		missLimit:   missLimit,
		txns:        make(map[Name]transaction),
	}, nil
}

func (r *Registry) Write(subtask, records int) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if subtask < 0 || subtask >= r.parallelism || records < 1 || records > 1_000_000 {
		return ErrInvalidArgument
	}

	name := Name{Subtask: subtask, Checkpoint: r.lastBarrier + 1}
	current, exists := r.txns[name]
	if exists && current.status == OPEN && current.life == r.life {
		current.records += records
		r.txns[name] = current
		return nil
	}

	if exists && current.status != COMMITTED && current.status != ABORTED && current.life != r.life {
		current.status = ABORTED
		r.txns[name] = current
		r.abortedRecords += current.records
	}

	epoch := 0
	if exists {
		epoch = current.epoch + 1
	}
	r.txns[name] = transaction{
		status:  OPEN,
		epoch:   epoch,
		records: records,
		life:    r.life,
	}
	return nil
}

func (r *Registry) Barrier(checkpoint int) ([]Name, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if checkpoint < 1 {
		return nil, ErrInvalidArgument
	}

	if checkpoint != r.lastBarrier+1 {
		return nil, ErrCheckpointOrder
	}

	prepared := make([]Name, 0, r.parallelism)
	for subtask := 0; subtask < r.parallelism; subtask++ {
		name := Name{Subtask: subtask, Checkpoint: checkpoint}
		txn := r.txns[name]
		if txn.status == OPEN && txn.life == r.life {
			txn.status = PREPARED
			r.txns[name] = txn
			prepared = append(prepared, name)
			r.prepared = append(r.prepared, name)
		}
	}
	r.lastBarrier = checkpoint
	return prepared, nil
}

func (r *Registry) Complete(checkpoint int) ([]Name, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if checkpoint < 1 {
		return nil, ErrInvalidArgument
	}

	if checkpoint <= r.lastNotify {
		return nil, ErrStaleNotification
	}
	if checkpoint > r.lastBarrier {
		return nil, ErrFutureNotification
	}

	committed := r.commitPrepared(checkpoint)
	r.lastNotify = checkpoint
	return committed, nil
}

func (r *Registry) Restore(checkpoint, parallelism int) (RestoreResult, error) {
	if checkpoint < 0 || parallelism < 1 || parallelism > 64 {
		return RestoreResult{}, ErrInvalidArgument
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if checkpoint < r.lastNotify {
		return RestoreResult{}, ErrRestoreRollback
	}
	if checkpoint > r.lastBarrier {
		return RestoreResult{}, ErrRestoreFuture
	}

	result := RestoreResult{
		Committed: make([]Name, 0),
		Aborted:   make([]Name, 0),
	}
	result.Committed = r.commitPrepared(checkpoint)

	oldParallelism := r.parallelism
	if parallelism > oldParallelism {
		oldParallelism = parallelism
	}
	for subtask := 0; subtask < oldParallelism; subtask++ {
		misses := 0
		nextCheckpoint := checkpoint + 1
		for misses < r.missLimit {
			name := Name{Subtask: subtask, Checkpoint: nextCheckpoint}
			nextCheckpoint++
			r.probes++
			result.Probes++

			txn := r.txns[name]
			if txn.status == OPEN || txn.status == PREPARED {
				txn.status = ABORTED
				r.txns[name] = txn
				r.abortedRecords += txn.records
				result.Aborted = append(result.Aborted, name)
				misses = 0
			} else {
				misses++
			}
		}
	}

	r.prepared = nil
	r.parallelism = parallelism
	r.lastBarrier = checkpoint
	r.lastNotify = checkpoint
	r.life++
	return result, nil
}

func (r *Registry) Txn(subtask, checkpoint int) (TxnInfo, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if subtask < 0 || subtask >= 64 || checkpoint < 1 {
		return TxnInfo{}, ErrInvalidArgument
	}

	txn, exists := r.txns[Name{Subtask: subtask, Checkpoint: checkpoint}]
	if !exists {
		return TxnInfo{}, ErrNotFound
	}
	return TxnInfo{
		Status:  txn.status,
		Epoch:   txn.epoch,
		Records: txn.records,
		Life:    txn.life,
	}, nil
}

func (r *Registry) Pending() []Name {
	r.mu.RLock()
	defer r.mu.RUnlock()

	pending := make([]Name, 0)
	for name, txn := range r.txns {
		if txn.status == OPEN || txn.status == PREPARED {
			pending = append(pending, name)
		}
	}
	sort.Slice(pending, func(i, j int) bool {
		if pending[i].Subtask != pending[j].Subtask {
			return pending[i].Subtask < pending[j].Subtask
		}
		return pending[i].Checkpoint < pending[j].Checkpoint
	})
	return pending
}

func (r *Registry) Stats() Stats {
	r.mu.RLock()
	defer r.mu.RUnlock()

	stats := Stats{
		CommittedRecords: r.committedRecords,
		AbortedRecords:   r.abortedRecords,
		Probes:           r.probes,
	}
	for _, txn := range r.txns {
		switch txn.status {
		case OPEN:
			stats.OpenRecords += txn.records
		case PREPARED:
			stats.PreparedRecords += txn.records
		}
	}
	return stats
}

func (r *Registry) commitPrepared(checkpoint int) []Name {
	committed := make([]Name, 0)
	end := 0
	for end < len(r.prepared) {
		name := r.prepared[end]
		r.visited++
		if name.Checkpoint > checkpoint {
			break
		}

		txn := r.txns[name]
		txn.status = COMMITTED
		r.txns[name] = txn
		r.committedRecords += txn.records
		committed = append(committed, name)
		end++
	}
	r.prepared = r.prepared[end:]
	return committed
}
