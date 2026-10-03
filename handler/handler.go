// Package handler manages workflow instance state on top of an append-only
// history log and a bounded dedup table.
package handler

import (
	"errors"
	"fmt"
	"sync"

	"ontology/dedupe"
	"ontology/history"
)

// Sentinel errors, distinguishable with errors.Is.
var (
	ErrInvalidParam   = errors.New("handler: invalid parameter")
	ErrNotFound       = errors.New("handler: instance not found")
	ErrExists         = errors.New("handler: instance already exists")
	ErrClosed         = errors.New("handler: instance closed")
	ErrEmpty          = errors.New("handler: no queued update")
	ErrCorruptHistory = errors.New("handler: corrupt history")
)

// Limits accepted by Create and Update.
const (
	MaxCap   int64 = 1_000_000_000_000
	MaxDelta int64 = 1_000_000_000_000
)

// ResultKind classifies the outcome of an update.
type ResultKind int

const (
	Unknown   ResultKind = iota // uid not present in the dedup table
	Rejected                    // validation failed; normal result, not an error
	Accepted                    // validated and logged; carries Seq
	Completed                   // applied by Step; carries Value
	Aborted                     // still queued when the instance was closed
)

// Result is the registered outcome of one update uid.
type Result struct {
	Kind  ResultKind
	Seq   int64 // history seq of the U event, for Accepted/Completed/Aborted
	Value int64 // applied value after Step, for Completed
}

// config holds per-instance settings recorded at Create; not rebuilt by
// Recover.
type config struct {
	maxCap int64
	k      int
}

// queued is one accepted-but-not-yet-applied update.
type queued struct {
	delta  int64
	uSeq   int64
	result *Result
}

// instance is the in-memory runtime state of one workflow instance.
type instance struct {
	mu         sync.Mutex
	cfg        config
	log        *history.Log
	table      *dedupe.Table[*Result]
	applied    int64 // s: sum of applied deltas
	pendingSum int64 // sum of queued deltas; projection = applied + pendingSum
	queue      []queued
	closed     bool
}

// Handler owns all instances, their configs and their persistent logs.
type Handler struct {
	mu      sync.Mutex // guards insts and configs
	insts   map[string]*instance
	configs map[string]config
}

// New returns an empty handler.
func New() *Handler {
	return &Handler{
		insts:   make(map[string]*instance),
		configs: make(map[string]config),
	}
}

// Create registers a new instance with the given cap and dedup capacity k.
func (h *Handler) Create(inst []byte, maxCap int64, k int) error {
	if len(inst) == 0 {
		return fmt.Errorf("%w: empty instance id", ErrInvalidParam)
	}
	if maxCap < 0 || maxCap > MaxCap {
		return fmt.Errorf("%w: cap %d out of range", ErrInvalidParam, maxCap)
	}
	table, err := dedupe.New[*Result](k)
	if err != nil {
		return fmt.Errorf("%w: dedup capacity %d out of range", ErrInvalidParam, k)
	}
	key := string(inst)
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.insts[key]; ok {
		return ErrExists
	}
	cfg := config{maxCap: maxCap, k: k}
	h.insts[key] = &instance{cfg: cfg, log: history.New(), table: table}
	h.configs[key] = cfg
	return nil
}

// Update submits an update with a unique uid to an instance.
//
// Processing order: parameter validation, existence, dedup hit (returned
// as-is, even on a closed instance), closed check, projection validation.
// Accepted updates are logged as U and queued; Rejected ones only register
// in the dedup table. Both are normal results, not errors.
func (h *Handler) Update(inst []byte, uid string, delta int64) (Result, error) {
	if len(inst) == 0 {
		return Result{}, fmt.Errorf("%w: empty instance id", ErrInvalidParam)
	}
	if uid == "" {
		return Result{}, fmt.Errorf("%w: empty uid", ErrInvalidParam)
	}
	if delta < -MaxDelta || delta > MaxDelta {
		return Result{}, fmt.Errorf("%w: delta %d out of range", ErrInvalidParam, delta)
	}
	in, err := h.lookup(inst)
	if err != nil {
		return Result{}, err
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	if r, ok := in.table.Get(uid); ok {
		return *r, nil
	}
	if in.closed {
		return Result{}, ErrClosed
	}
	projection := in.applied + in.pendingSum
	if v := projection + delta; v < 0 || v > in.cfg.maxCap {
		res := &Result{Kind: Rejected}
		in.table.Add(uid, res)
		return *res, nil
	}
	seq, err := in.log.AppendUpdate(uid, delta)
	if err != nil {
		return Result{}, err
	}
	res := &Result{Kind: Accepted, Seq: seq}
	in.queue = append(in.queue, queued{delta: delta, uSeq: seq, result: res})
	in.pendingSum += delta
	in.table.Add(uid, res)
	return *res, nil
}

// lookup returns the live instance or ErrNotFound.
func (h *Handler) lookup(inst []byte) (*instance, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	in, ok := h.insts[string(inst)]
	if !ok {
		return nil, ErrNotFound
	}
	return in, nil
}
