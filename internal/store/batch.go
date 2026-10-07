package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Instance is the externally visible state of one ontology object.
type Instance struct {
	ID         string
	Version    int64
	Properties map[string]string
}

// Op is one member update of a batch.
type Op struct {
	ObjectID   string
	Properties map[string]string
}

// Class is the externally reported disposition of a batch. The two
// interrupted-recovery classes are observationally indistinguishable from
// the corresponding normal classes with respect to instance state.
type Class string

const (
	ClassCommitted       Class = "committed"
	ClassAborted         Class = "aborted"
	ClassRecoveredCommit Class = "recovered_committed"
	ClassRecoveredAbort  Class = "recovered_aborted"
)

// Outcome is the durable terminal result of a batch.
type Outcome struct {
	BatchID string
	Class   Class
}

// RecoveryStep is one recorded decision made during recovery.
type RecoveryStep struct {
	Barrier  string
	Evidence string
	Decision string
}

// RecoveryReport records the deterministic classification of one recovery
// pass, including evidence and a bound on records inspected.
type RecoveryReport struct {
	BatchID        string
	Class          Class
	RecordsScanned int
	Steps          []RecoveryStep
}

// JournalEntry is one line of the replayable audit journal.
type JournalEntry struct {
	Seq     int
	Phase   string
	BatchID string
	Detail  string
}

// Store is an instance store supporting atomic batch updates with
// crash recovery.
type Store struct {
	mu     sync.Mutex
	engine Engine
	hook   CrashHook

	live     bool
	inflight string
	objects  map[string]string
	journal  []JournalEntry
}

// Option configures a Store.
type Option func(*config)

type config struct {
	hook CrashHook
}

// WithCrashHook installs a fault-injection hook invoked at each barrier.
func WithCrashHook(h CrashHook) Option {
	return func(c *config) { c.hook = h }
}

// Open opens a store over the given engine and recovers any interrupted
// batch. It is idempotent: repeated Open calls converge to the same state
// and the same classification.
func Open(ctx context.Context, eng Engine, opts ...Option) (*Store, []RecoveryReport, error) {
	cfg := config{}
	for _, o := range opts {
		o(&cfg)
	}
	s := &Store{
		engine:  eng,
		hook:    cfg.hook,
		live:    true,
		objects: make(map[string]string),
	}
	s.loadJournal()
	report, err := s.recover(ctx)
	if err != nil {
		return nil, nil, err
	}
	var reports []RecoveryReport
	if report != nil {
		reports = []RecoveryReport{*report}
	}
	return s, reports, nil
}

// FatalCrashError marks the store unusable after an injected crash.
type FatalCrashError struct{ Barrier string }

func (e *FatalCrashError) Error() string {
	return "store: process interruption simulated at " + e.Barrier
}

func (s *Store) crashf(barrier string) error {
	s.live = false
	return &FatalCrashError{Barrier: barrier}
}

// barrier performs one durable Put and then announces the durability
// point to the fault hook. If the hook injects a crash, the store dies
// exactly at that barrier and the caller must not continue.
func (s *Store) barrier(barrier string, key string, value []byte) error {
	if err := s.engine.Put(key, value); err != nil {
		return err
	}
	if s.hook != nil {
		if err := s.hook(barrier); err != nil {
			var ce *CrashError
			if errors.As(err, &ce) {
				return s.crashf(ce.Barrier)
			}
			return err
		}
	}
	return nil
}

func (s *Store) journalAppend(phase, batchID, detail string) error {
	e := journalRecord{
		Seq:     len(s.journal) + 1,
		Phase:   phase,
		BatchID: batchID,
		Detail:  detail,
	}
	s.journal = append(s.journal, JournalEntry{Seq: e.Seq, Phase: phase, BatchID: batchID, Detail: detail})
	b, err := encode(e)
	if err != nil {
		return err
	}
	return s.engine.Put(keyJournalEntry(e.Seq), b)
}

func (s *Store) loadJournal() {
	l, ok := s.engine.(Lister)
	if !ok {
		return
	}
	for _, k := range l.ListKeys("j/") {
		b, err := s.engine.Get(k)
		if err != nil {
			continue
		}
		e, err := decode[journalRecord](b)
		if err != nil {
			continue
		}
		s.journal = append(s.journal, JournalEntry{Seq: e.Seq, Phase: e.Phase, BatchID: e.BatchID, Detail: e.Detail})
	}
	sort.Slice(s.journal, func(i, j int) bool { return s.journal[i].Seq < s.journal[j].Seq })
}

func (s *Store) checkLive() error {
	if !s.live {
		return errors.New("store: instance is dead after simulated crash; reopen required")
	}
	return nil
}

func (s *Store) guardObject(id string) error {
	if _, uncertain := s.objects[id]; uncertain {
		return ErrUncertain
	}
	return nil
}

func (s *Store) readInstance(id string) (*Instance, error) {
	b, err := s.engine.Get(keyInstance(id))
	if errors.Is(err, ErrNotFound) {
		return &Instance{ID: id, Version: 0, Properties: map[string]string{}}, nil
	}
	if err != nil {
		return nil, err
	}
	r, err := decode[instanceRecord](b)
	if err != nil {
		return nil, err
	}
	if r.Properties == nil {
		r.Properties = map[string]string{}
	}
	return &Instance{ID: r.ID, Version: r.Version, Properties: r.Properties}, nil
}

func (s *Store) writeInstance(inst *Instance) error {
	b, err := encode(instanceRecord{Kind: "instance", ID: inst.ID, Version: inst.Version, Properties: inst.Properties})
	if err != nil {
		return err
	}
	return s.engine.Put(keyInstance(inst.ID), b)
}

// Get reads one instance. Reads of objects participating in an
// undetermined batch are rejected with ErrUncertain.
func (s *Store) Get(_ context.Context, id string) (*Instance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkLive(); err != nil {
		return nil, err
	}
	if err := s.guardObject(id); err != nil {
		return nil, err
	}
	return s.readInstance(id)
}

// Put writes one instance outside of a batch. Writes to objects
// participating in an undetermined batch are rejected with ErrUncertain.
func (s *Store) Put(_ context.Context, inst *Instance) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkLive(); err != nil {
		return err
	}
	if inst == nil || inst.ID == "" {
		return errors.New("store: instance id required")
	}
	if err := s.guardObject(inst.ID); err != nil {
		return err
	}
	if inst.Properties == nil {
		inst.Properties = map[string]string{}
	}
	return s.writeInstance(inst)
}

// Batch executes one atomic batch update. Versions of all touched
// instances advance by exactly one on commit; on abort they are unchanged.
func (s *Store) Batch(ctx context.Context, batchID string, ops []Op) (Outcome, error) {
	outcome, _, err := s.Prepare(ctx, batchID, ops)
	if err != nil {
		return Outcome{}, err
	}
	return s.CommitPrepared(ctx, outcome.BatchID)
}

// Prepare stages a batch (phase 1) without crossing the commit point.
// Callers must follow with exactly one of CommitPrepared or Abort.
func (s *Store) Prepare(ctx context.Context, batchID string, ops []Op) (Outcome, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkLive(); err != nil {
		return Outcome{}, 0, err
	}
	if batchID == "" {
		return Outcome{}, 0, errors.New("store: batch id required")
	}
	if s.inflight != "" {
		return Outcome{}, 0, ErrBusy
	}
	if err := s.prepare(batchID, ops); err != nil {
		return Outcome{}, 0, err
	}
	if err := ctx.Err(); err != nil {
		return Outcome{}, 0, err
	}
	return Outcome{BatchID: batchID}, len(ops), nil
}

// CommitPrepared crosses the unique commit point for a prepared batch.
func (s *Store) CommitPrepared(ctx context.Context, batchID string) (Outcome, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkLive(); err != nil {
		return Outcome{}, err
	}
	count, ok := s.preparedCount(batchID)
	if !ok {
		return Outcome{}, fmt.Errorf("store: batch %q is not prepared", batchID)
	}
	if err := s.commit(ctx, batchID, count); err != nil {
		return Outcome{}, err
	}
	return Outcome{BatchID: batchID, Class: ClassCommitted}, nil
}

// AbortPrepared is the normal pre-commit abort entry point.
func (s *Store) AbortPrepared(ctx context.Context, batchID string, ops []Op) (Outcome, error) {
	if _, _, err := s.Prepare(ctx, batchID, ops); err != nil {
		return Outcome{}, err
	}
	return s.Abort(ctx, batchID)
}

func (s *Store) preparedCount(batchID string) (int, bool) {
	if s.inflight != batchID {
		return 0, false
	}
	n := 0
	for _, b := range s.objects {
		if b == batchID {
			n++
		}
	}
	return n, true
}

// Abort aborts a batch before its commit point, producing the normal
// aborted terminal state (instances byte-identical to pre-batch state).
func (s *Store) Abort(ctx context.Context, batchID string) (Outcome, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkLive(); err != nil {
		return Outcome{}, err
	}
	if s.inflight != batchID {
		return Outcome{}, fmt.Errorf("store: batch %q not in progress", batchID)
	}
	count := len(s.objects)
	if err := s.rollback(ctx, batchID, count, false); err != nil {
		return Outcome{}, err
	}
	return Outcome{BatchID: batchID, Class: ClassAborted}, nil
}

// Journal returns the latest audit entry. The durable journal keeps only
// the most recent entry (it is replay evidence for the last interruption;
// recovery itself is driven solely by batch-owned records).
func (s *Store) Journal() []JournalEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]JournalEntry, len(s.journal))
	copy(out, s.journal)
	return out
}
