// Package spill implements transactional row buffering with bounded memory.
//
// Transactions accumulate rows in memory. When the total in-memory row count
// would exceed the configured limit, the largest open transaction (smallest
// transaction id breaking ties) has its in-memory rows packed into a spill
// block on the spill store. On commit the transaction's blocks are replayed in
// ascending block order followed by its remaining in-memory rows, so the
// downstream sees committed transactions in append order.
package spill

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Row is a single appended transaction row.
type Row = string

// Logger receives one line per operation step. The nil logger discards output.
type Logger interface {
	Printf(format string, args ...any)
}

// Sentinel errors. Each rejection category is distinguishable via errors.Is.
var (
	// ErrInvalidConfig is returned by New when a configuration value is invalid,
	// or when an operation is invoked with invalid fixed arguments (nil sink).
	ErrInvalidConfig = errors.New("spill: invalid configuration")
	// ErrInvalidTxn is returned when a transaction id is invalid (non-positive).
	ErrInvalidTxn = errors.New("spill: invalid transaction id")
	// ErrTxnNotFound is returned when the transaction does not exist or has ended.
	ErrTxnNotFound = errors.New("spill: transaction not found")
	// ErrTxnExists is returned when Begin is called with a duplicate transaction id.
	ErrTxnExists = errors.New("spill: transaction already exists")
	// ErrInvalidRows is returned when Append receives no rows or an empty row.
	ErrInvalidRows = errors.New("spill: invalid rows")
	// ErrStoreFull is returned when spilling needs a new block but the spill store
	// already holds MaxSpillBlocks blocks.
	ErrStoreFull = errors.New("spill: spill store is full")
	// ErrInvariant is returned by Check when an internal invariant is violated.
	ErrInvariant = errors.New("spill: invariant violation")
)

// Config configures a Manager.
type Config struct {
	// MaxMemoryRows bounds the number of rows held in memory at any instant.
	MaxMemoryRows int
	// MaxSpillBlocks bounds the total number of spill blocks in the store.
	MaxSpillBlocks int
	// Log receives per-step diagnostics; nil discards them.
	Log Logger
}

// Block describes a spill block as visible through QueryBlocks.
type Block struct {
	// ID is the unique, never-reused block number.
	ID int64
	// TxnID owns the block.
	TxnID int64
	// Seq is the 0-based position of the block within the owning transaction.
	Seq int
	// Rows holds the packed rows in append order.
	Rows []Row
}

// TxnSnapshot describes an open transaction as visible through QueryTxns.
type TxnSnapshot struct {
	TxnID      int64
	MemoryRows int
	BlockCount int
	TotalRows  int
}

// Manager is the concurrency-safe spill manager.
type Manager struct {
	mu sync.RWMutex

	cfg Config

	// open are the transactions that have begun but not ended.
	open map[int64]*txnState

	// blocks are all live spill blocks keyed by block id.
	blocks map[int64]*Block

	// nextBlockID is the never-reused monotonic block counter.
	nextBlockID int64

	// inFlight holds txns whose commit output is currently being sent to the
	// sink; they no longer appear in open but still legally own blocks.
	inFlight map[int64]*txnState

	// committed holds rows already handed to the downstream, in commit order.
	committed []Row
}

type txnState struct {
	id         int64
	memory     []Row
	blockIDs   []int64
	totalRows  int
	committing bool
}

type discardLogger struct{}

func (discardLogger) Printf(string, ...any) {}

// New creates a Manager from cfg.
func New(cfg Config) (*Manager, error) {
	if cfg.MaxMemoryRows <= 0 {
		return nil, fmt.Errorf("%w: MaxMemoryRows must be positive, got %d", ErrInvalidConfig, cfg.MaxMemoryRows)
	}
	if cfg.MaxSpillBlocks <= 0 {
		return nil, fmt.Errorf("%w: MaxSpillBlocks must be positive, got %d", ErrInvalidConfig, cfg.MaxSpillBlocks)
	}
	if cfg.Log == nil {
		cfg.Log = discardLogger{}
	}
	return &Manager{
		cfg:         cfg,
		open:        make(map[int64]*txnState),
		blocks:      make(map[int64]*Block),
		nextBlockID: 1,
		inFlight:    make(map[int64]*txnState),
	}, nil
}

// Begin registers a new open transaction.
func (m *Manager) Begin(txnID int64) error {
	if txnID <= 0 {
		m.cfg.Log.Printf("begin txn=%d rejected: %v", txnID, ErrInvalidTxn)
		return fmt.Errorf("%w: txn id must be positive, got %d", ErrInvalidTxn, txnID)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.open[txnID]; ok {
		m.cfg.Log.Printf("begin txn=%d rejected: %v", txnID, ErrTxnExists)
		return fmt.Errorf("%w: txn %d", ErrTxnExists, txnID)
	}
	m.open[txnID] = &txnState{id: txnID}
	m.cfg.Log.Printf("begin txn=%d ok: memoryRows=%d blocks=%d", txnID, m.memoryRowsLocked(), len(m.blocks))
	return nil
}

// Append adds rows to an open transaction, spilling other transactions to the
// spill store when total in-memory rows exceed the limit.
func (m *Manager) Append(txnID int64, rows ...Row) error {
	if txnID <= 0 {
		m.cfg.Log.Printf("append txn=%d rejected: %v", txnID, ErrInvalidTxn)
		return fmt.Errorf("%w: txn id must be positive, got %d", ErrInvalidTxn, txnID)
	}
	if len(rows) == 0 {
		m.cfg.Log.Printf("append txn=%d rejected: %v (no rows)", txnID, ErrInvalidRows)
		return fmt.Errorf("%w: at least one row is required", ErrInvalidRows)
	}
	copied := make([]Row, len(rows))
	for i, row := range rows {
		if row == "" {
			m.cfg.Log.Printf("append txn=%d rejected: %v (row %d empty)", txnID, ErrInvalidRows, i)
			return fmt.Errorf("%w: row %d is empty", ErrInvalidRows, i)
		}
		copied[i] = row
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	txn, ok := m.open[txnID]
	if !ok {
		m.cfg.Log.Printf("append txn=%d rows=%d rejected: %v", txnID, len(rows), ErrTxnNotFound)
		return fmt.Errorf("%w: txn %d", ErrTxnNotFound, txnID)
	}

	// Validate the whole operation against the block budget before mutating any
	// state: a rejected append must leave no trace.
	need := m.spillNeedLocked(txn, len(copied))
	free := m.cfg.MaxSpillBlocks - len(m.blocks)
	if need > free {
		m.cfg.Log.Printf(
			"append txn=%d rows=%d rejected: %v (needBlocks=%d freeBlocks=%d/%d memoryRows=%d)",
			txnID, len(copied), ErrStoreFull, need, free, m.cfg.MaxSpillBlocks, m.memoryRowsLocked(),
		)
		return fmt.Errorf("%w: append needs %d block(s), only %d free of %d",
			ErrStoreFull, need, free, m.cfg.MaxSpillBlocks)
	}

	m.cfg.Log.Printf("append txn=%d rows=%d: before memoryRows=%d", txnID, len(copied), m.memoryRowsLocked())
	txn.memory = append(txn.memory, copied...)
	txn.totalRows += len(copied)

	for m.memoryRowsLocked() > m.cfg.MaxMemoryRows {
		victim := m.selectVictimLocked()
		bid := m.spillLocked(victim)
		m.cfg.Log.Printf(
			"append txn=%d: victim=txn%d reason=max-memory,min-id%s spilledRows=%d block=%d; after memoryRows=%d blocks=%d",
			txnID, victim.id, victimSuffix(victim, txn), len(m.blocks[bid].Rows), bid, m.memoryRowsLocked(), len(m.blocks),
		)
	}
	m.cfg.Log.Printf("append txn=%d ok: memoryRows=%d blocks=%d", txnID, m.memoryRowsLocked(), len(m.blocks))
	return nil
}

// Commit replays the transaction's rows to sink in append order (spill blocks
// ascending by block number, then remaining in-memory rows) and ends it.
// Rows are published to the committed log only after the sink accepts them all.
func (m *Manager) Commit(ctx context.Context, txnID int64, sink func(Row) error) error {
	if txnID <= 0 {
		m.cfg.Log.Printf("commit txn=%d rejected: %v", txnID, ErrInvalidTxn)
		return fmt.Errorf("%w: txn id must be positive, got %d", ErrInvalidTxn, txnID)
	}
	if sink == nil {
		return fmt.Errorf("%w: sink must not be nil", ErrInvalidConfig)
	}

	// Remove the txn from the open map up front (it is ending; a concurrent
	// Rollback/Append/Commit must report not-found), retain its blocks while
	// the sink runs, then delete the blocks and publish the log. The blocks
	// remain owned for Check's purposes via inFlight.
	m.mu.Lock()
	txn, ok := m.open[txnID]
	if !ok {
		m.mu.Unlock()
		m.cfg.Log.Printf("commit txn=%d rejected: %v", txnID, ErrTxnNotFound)
		return fmt.Errorf("%w: txn %d", ErrTxnNotFound, txnID)
	}
	if txn.committing {
		m.mu.Unlock()
		m.cfg.Log.Printf("commit txn=%d rejected: commit already in progress", txnID)
		return fmt.Errorf("%w: txn %d is already committing", ErrInvariant, txnID)
	}
	output := make([]Row, 0, txn.totalRows)
	blockRows := 0
	for _, bid := range txn.blockIDs {
		rows := m.blocks[bid].Rows
		blockRows += len(rows)
		output = append(output, rows...)
	}
	output = append(output, txn.memory...)
	blockIDs := txn.blockIDs
	txn.committing = true
	delete(m.open, txnID)
	m.inFlight[txnID] = txn
	m.mu.Unlock()

	m.cfg.Log.Printf("commit txn=%d: replay blocks=%d ids=%v blockRows=%d memoryRows=%d total=%d",
		txnID, len(blockIDs), blockIDs, blockRows, len(output)-blockRows, len(output))

	for _, row := range output {
		if err := ctx.Err(); err != nil {
			m.abortCommitLocked(txn)
			return err
		}
		if err := sink(row); err != nil {
			m.abortCommitLocked(txn)
			return err
		}
	}

	m.mu.Lock()
	cur, ok := m.inFlight[txnID]
	if !ok || cur != txn {
		m.mu.Unlock()
		return fmt.Errorf("%w: txn %d changed during commit", ErrInvariant, txnID)
	}
	for _, bid := range blockIDs {
		delete(m.blocks, bid)
	}
	delete(m.inFlight, txnID)
	m.committed = append(m.committed, output...)
	m.mu.Unlock()

	m.cfg.Log.Printf("commit txn=%d ok: outputRows=%d deletedBlocks=%d", txnID, len(output), len(blockIDs))
	return nil
}

// Rollback discards the transaction without producing any output and frees all
// of its spill blocks.
func (m *Manager) Rollback(txnID int64) error {
	if txnID <= 0 {
		m.cfg.Log.Printf("rollback txn=%d rejected: %v", txnID, ErrInvalidTxn)
		return fmt.Errorf("%w: txn id must be positive, got %d", ErrInvalidTxn, txnID)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	txn, ok := m.open[txnID]
	if !ok {
		m.cfg.Log.Printf("rollback txn=%d rejected: %v", txnID, ErrTxnNotFound)
		return fmt.Errorf("%w: txn %d", ErrTxnNotFound, txnID)
	}
	for _, bid := range txn.blockIDs {
		delete(m.blocks, bid)
	}
	delete(m.open, txnID)
	m.cfg.Log.Printf("rollback txn=%d ok: droppedMemoryRows=%d droppedBlocks=%d; after memoryRows=%d blocks=%d",
		txnID, len(txn.memory), len(txn.blockIDs), m.memoryRowsLocked(), len(m.blocks))
	return nil
}

// abortCommitLocked restores the open state after a sink/context failure so
// the transaction stays usable (commit is retryable).
func (m *Manager) abortCommitLocked(txn *txnState) {
	m.mu.Lock()
	txn.committing = false
	if _, ok := m.inFlight[txn.id]; ok {
		delete(m.inFlight, txn.id)
		m.open[txn.id] = txn
	}
	m.mu.Unlock()
}

// LogSnapshot returns a copy of committed rows in commit/append order.
func (m *Manager) LogSnapshot() []Row {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Row, len(m.committed))
	copy(out, m.committed)
	return out
}

// QueryBlocks returns all live spill blocks in ascending block-number order.
func (m *Manager) QueryBlocks() []Block {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]int64, 0, len(m.blocks))
	for id := range m.blocks {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	out := make([]Block, 0, len(ids))
	for _, id := range ids {
		b := m.blocks[id]
		rows := make([]Row, len(b.Rows))
		copy(rows, b.Rows)
		out = append(out, Block{ID: b.ID, TxnID: b.TxnID, Seq: b.Seq, Rows: rows})
	}
	return out
}

// QueryTxns returns snapshots of all open transactions in ascending txn id.
func (m *Manager) QueryTxns() []TxnSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]int64, 0, len(m.open))
	for id := range m.open {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	out := make([]TxnSnapshot, 0, len(ids))
	for _, id := range ids {
		t := m.open[id]
		out = append(out, TxnSnapshot{
			TxnID:      id,
			MemoryRows: len(t.memory),
			BlockCount: len(t.blockIDs),
			TotalRows:  t.totalRows,
		})
	}
	return out
}

// MemoryRows returns the current total number of in-memory rows across all
// open transactions.
func (m *Manager) MemoryRows() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.memoryRowsLocked()
}

// BlockCount returns the current number of live spill blocks.
func (m *Manager) BlockCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.blocks)
}

// Check verifies internal invariants: memory cap, block ownership by open
// transactions, block-id monotonicity and per-transaction sequence contiguity.
func (m *Manager) Check() error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var problems []string

	totalMemory := 0
	for _, t := range m.open {
		totalMemory += len(t.memory)
	}
	for _, t := range m.inFlight {
		totalMemory += len(t.memory)
	}
	owners := make(map[int64]*txnState, len(m.open)+len(m.inFlight))
	for _, t := range m.open {
		owners[t.id] = t
	}
	for _, t := range m.inFlight {
		owners[t.id] = t
	}
	for _, t := range owners {
		owned := 0
		for _, bid := range t.blockIDs {
			b, ok := m.blocks[bid]
			if !ok {
				problems = append(problems, fmt.Sprintf("txn %d references missing block %d", t.id, bid))
				continue
			}
			if b.TxnID != t.id {
				problems = append(problems, fmt.Sprintf("block %d owned by txn %d, referenced by txn %d", bid, b.TxnID, t.id))
			}
			if b.Seq != owned {
				problems = append(problems, fmt.Sprintf("txn %d block %d seq=%d want %d", t.id, bid, b.Seq, owned))
			}
			owned++
		}
	}
	if totalMemory > m.cfg.MaxMemoryRows {
		problems = append(problems, fmt.Sprintf("memory rows %d exceed limit %d", totalMemory, m.cfg.MaxMemoryRows))
	}
	if len(m.blocks) > m.cfg.MaxSpillBlocks {
		problems = append(problems, fmt.Sprintf("blocks %d exceed limit %d", len(m.blocks), m.cfg.MaxSpillBlocks))
	}
	for id, b := range m.blocks {
		if id != b.ID {
			problems = append(problems, fmt.Sprintf("block map key %d != block id %d", id, b.ID))
		}
		if b.ID >= m.nextBlockID {
			problems = append(problems, fmt.Sprintf("block id %d not below nextBlockID %d (id reuse)", b.ID, m.nextBlockID))
		}
		if _, ok := owners[b.TxnID]; !ok {
			problems = append(problems, fmt.Sprintf("block %d belongs to ended/missing txn %d", b.ID, b.TxnID))
		}
		if len(b.Rows) == 0 {
			problems = append(problems, fmt.Sprintf("block %d is empty", b.ID))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("%w: %s", ErrInvariant, strings.Join(problems, "; "))
	}
	return nil
}

func (m *Manager) memoryRowsLocked() int {
	total := 0
	for _, t := range m.open {
		total += len(t.memory)
	}
	for _, t := range m.inFlight {
		total += len(t.memory)
	}
	return total
}

// selectVictimLocked picks the open transaction with the most in-memory rows;
// ties are broken by the smallest transaction id. The appending transaction
// participates in the selection like every other open transaction.
func (m *Manager) selectVictimLocked() *txnState {
	var victim *txnState
	for _, t := range m.open {
		if len(t.memory) == 0 {
			continue
		}
		if victim == nil {
			victim = t
			continue
		}
		if len(t.memory) > len(victim.memory) ||
			(len(t.memory) == len(victim.memory) && t.id < victim.id) {
			victim = t
		}
	}
	return victim
}

func victimSuffix(victim, target *txnState) string {
	if victim == target {
		return " (self)"
	}
	return ""
}

// spillLocked packs all in-memory rows of t into a new block and clears them.
// The caller guarantees a free block slot.
func (m *Manager) spillLocked(t *txnState) int64 {
	id := m.nextBlockID
	m.nextBlockID++
	rows := make([]Row, len(t.memory))
	copy(rows, t.memory)
	m.blocks[id] = &Block{
		ID:    id,
		TxnID: t.id,
		Seq:   len(t.blockIDs),
		Rows:  rows,
	}
	t.blockIDs = append(t.blockIDs, id)
	t.memory = t.memory[:0]
	return id
}

// spillNeedLocked reports how many new blocks appending addRows to target will
// consume before memory fits the cap again. Each step removes the largest open
// txn (smallest id on ties), exactly mirroring selectVictimLocked. A spill
// packs the victim's entire in-memory set at once, so the removed size is the
// victim's full current size.
func (m *Manager) spillNeedLocked(target *txnState, addRows int) int {
	mem := m.memoryRowsLocked()
	need := 0

	// Snapshot per-txn in-memory sizes after the append.
	sizes := make(map[int64]int, len(m.open))
	for id, t := range m.open {
		sizes[id] = len(t.memory)
	}
	sizes[target.id] += addRows
	mem += addRows

	pick := func() (int64, int) {
		var vid int64
		best := 0
		for id, size := range sizes {
			if size <= 0 {
				continue
			}
			if best == 0 {
				best, vid = size, id
				continue
			}
			if size > best || (size == best && id < vid) {
				best, vid = size, id
			}
		}
		return vid, best
	}

	for mem > m.cfg.MaxMemoryRows {
		vid, best := pick()
		if best <= 0 {
			break
		}
		mem -= best
		sizes[vid] = 0
		need++
	}
	return need
}
