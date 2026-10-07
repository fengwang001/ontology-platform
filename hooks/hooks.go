// Package hooks registers pre/post validation hooks per object type and
// dispatches them at the correct points of a transaction's lifecycle.
package hooks

import (
	"fmt"
	"sync"

	"ontology/errors"
	"ontology/txn"
)

// Phase distinguishes pre-write from post-write (pre-commit) hooks.
type Phase int

const (
	Pre Phase = iota
	Post
)

// Context is handed to a hook when it fires.
type Context struct {
	TxnID    int64
	Action   string   // outermost action name
	TypeName string   // object type that triggered the hook
	View     txn.View // snapshot visible to the hook
}

// Func is a validation hook. Returning a non-nil error fails the hook.
type Func func(Context) error

// Record is one entry of the hook firing log.
type Record struct {
	Seq      int
	TxnID    int64
	Action   string
	Phase    Phase
	TypeName string
	HookName string
}

// Registry stores hooks per object type in registration order.
type Registry struct {
	pre  map[string][]namedHook
	post map[string][]namedHook
}

type namedHook struct {
	name string
	fn   Func
}

// NewRegistry returns an empty hook registry.
func NewRegistry() *Registry {
	return &Registry{
		pre:  map[string][]namedHook{},
		post: map[string][]namedHook{},
	}
}

// RegisterPre appends a pre-write hook for typeName.
func (r *Registry) RegisterPre(typeName, name string, fn Func) {
	r.pre[typeName] = append(r.pre[typeName], namedHook{name, fn})
}

// RegisterPost appends a post-write (pre-commit) hook for typeName.
func (r *Registry) RegisterPost(typeName, name string, fn Func) {
	r.post[typeName] = append(r.post[typeName], namedHook{name, fn})
}

// Recorder is a goroutine-safe, append-only global hook firing log. The
// order of records is the exact firing order across all transactions.
type Recorder struct {
	mu      sync.Mutex
	records []Record
}

// NewRecorder returns an empty recorder.
func NewRecorder() *Recorder {
	return &Recorder{}
}

func (rc *Recorder) append(r Record) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	r.Seq = len(rc.records)
	rc.records = append(rc.records, r)
}

// Records returns a copy of the firing log.
func (rc *Recorder) Records() []Record {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	out := make([]Record, len(rc.records))
	copy(out, rc.records)
	return out
}

// Dispatcher drives hook firing for exactly one outermost transaction.
// Pre hooks fire at most once per object type per transaction, immediately
// before the first write to an instance of that type; post hooks fire
// exactly once, at the end of the outermost action, before commit.
type Dispatcher struct {
	reg      *Registry
	rec      *Recorder
	txnID    int64
	action   string
	preFired map[string]bool
}

// NewDispatcher creates a dispatcher for one transaction.
func NewDispatcher(reg *Registry, rec *Recorder, txnID int64, action string) *Dispatcher {
	return &Dispatcher{reg: reg, rec: rec, txnID: txnID, action: action, preFired: map[string]bool{}}
}

// FirePre runs the pre hooks of typeName if they have not fired yet in
// this transaction. The view is the state before the triggering write,
// including all writes already applied by outer/nested actions. The first
// failing hook aborts immediately; later hooks of the same type do not run.
func (d *Dispatcher) FirePre(typeName string, view txn.View) error {
	if d.preFired[typeName] {
		return nil
	}
	d.preFired[typeName] = true
	for _, h := range d.reg.pre[typeName] {
		d.rec.append(Record{TxnID: d.txnID, Action: d.action, Phase: Pre, TypeName: typeName, HookName: h.name})
		if err := h.fn(Context{TxnID: d.txnID, Action: d.action, TypeName: typeName, View: view}); err != nil {
			return errors.PreHook(d.action, fmt.Sprintf("pre-hook %s/%s: %v", typeName, h.name, err))
		}
	}
	return nil
}

// FirePostAll runs every post hook of every touched type, in first-write
// type order and registration order within a type. It never short-circuits:
// all failures are collected into one aggregated report.
func (d *Dispatcher) FirePostAll(types []string, view txn.View) []errors.HookFailure {
	var failures []errors.HookFailure
	for _, typeName := range types {
		for _, h := range d.reg.post[typeName] {
			d.rec.append(Record{TxnID: d.txnID, Action: d.action, Phase: Post, TypeName: typeName, HookName: h.name})
			if err := h.fn(Context{TxnID: d.txnID, Action: d.action, TypeName: typeName, View: view}); err != nil {
				failures = append(failures, errors.HookFailure{
					TypeName: typeName,
					HookName: h.name,
					Message:  err.Error(),
				})
			}
		}
	}
	return failures
}
