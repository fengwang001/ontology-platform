package ontology

import (
	"errors"
	"fmt"
	"sync"
)

// DefaultMaxDepth bounds nested action calls.
const DefaultMaxDepth = 8

// Engine executes actions as atomic transactions against an in-memory
// store and appends an immutable record per successful commit.
type Engine struct {
	store    *Store
	actions  map[string]*ActionType
	mu       sync.Mutex // serializes top-level executions and commits
	seq      uint64
	log      []Record
	MaxDepth int
}

// NewEngine returns an engine backed by a fresh in-memory store.
func NewEngine() *Engine {
	return &Engine{
		store:    NewStore(),
		actions:  map[string]*ActionType{},
		MaxDepth: DefaultMaxDepth,
	}
}

// Store exposes the underlying store for read queries and fault injection.
func (e *Engine) Store() *Store {
	return e.store
}

// Register adds an ActionType; names must be unique and Run is required.
func (e *Engine) Register(a *ActionType) error {
	if a.Name == "" {
		return errors.New("ontology: action name is required")
	}
	if a.Run == nil {
		return fmt.Errorf("ontology: action %q: Run is required", a.Name)
	}
	if _, ok := e.actions[a.Name]; ok {
		return fmt.Errorf("ontology: action %q already registered", a.Name)
	}
	e.actions[a.Name] = a
	return nil
}

// Execute runs one top-level action atomically. On success it commits and
// returns the immutable execution record; on any failure every modification
// is rolled back and no record (nor sequence number) is produced.
func (e *Engine) Execute(name string, params map[string]any) (Record, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.store.Inconsistent() {
		return Record{}, ErrInconsistent
	}
	tx := &Tx{
		engine:      e,
		store:       e.store,
		chain:       nil,
		affectedObj: map[string]bool{},
		affectedRel: map[string]bool{},
	}
	rec, err := e.runAction(tx, name, params)
	if err != nil {
		tx.rollback()
		return Record{}, err
	}
	e.seq++
	rec.Seq = e.seq
	rec.Objects = sortedKeys(tx.affectedObj)
	rec.Relations = sortedKeys(tx.affectedRel)
	rec.Params = deepCopy(rec.Params).(map[string]any)
	e.log = append(e.log, rec)
	return rec, nil
}

// runAction executes one (possibly nested) action inside tx.
func (e *Engine) runAction(tx *Tx, name string, params map[string]any) (Record, error) {
	at, ok := e.actions[name]
	if !ok {
		return Record{}, fmt.Errorf("ontology: unknown action %q", name)
	}
	chain := append(append([]string(nil), tx.chain...), name)
	for _, c := range tx.chain {
		if c == name {
			return Record{}, &RecursionError{Chain: chain}
		}
	}
	if len(tx.chain) >= e.MaxDepth {
		return Record{}, &DepthError{Chain: chain, Limit: e.MaxDepth}
	}
	norm, err := at.Validate(params)
	if err != nil {
		return Record{}, err
	}
	tx.chain = append(tx.chain, name)
	defer func() { tx.chain = tx.chain[:len(tx.chain)-1] }()
	if err := runHooks(at, tx, norm); err != nil {
		return Record{}, err
	}
	// Snapshot the normalized params before Run can mutate them.
	recParams := deepCopy(norm).(map[string]any)
	if err := at.Run(tx, norm); err != nil {
		return Record{}, fmt.Errorf("action %q: %w", name, err)
	}
	return Record{ActionType: name, Params: recParams}, nil
}

// runHooks runs the pre-hooks in declaration order. The store is snapshotted
// first so that any write a hook smuggles in is detected (via the version
// counter) and fully reverted before the action fails.
func runHooks(at *ActionType, tx *Tx, norm map[string]any) error {
	if len(at.Hooks) == 0 {
		return nil
	}
	snap := tx.store.snapshot()
	for i, h := range at.Hooks {
		before := tx.store.Version()
		if err := h(&HookCtx{tx: tx, index: i, params: norm}); err != nil {
			return &HookRejectedError{Action: at.Name, Index: i, Reason: err.Error()}
		}
		if tx.store.Version() != before {
			tx.store.restore(snap)
			return &HookViolationError{Action: at.Name, Index: i}
		}
	}
	return nil
}
