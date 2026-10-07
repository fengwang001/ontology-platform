// Package action is the action engine. It owns object-type schemas, action
// definitions, nested invocation, hook dispatch and the serial executor
// that totally orders outermost action calls.
//
// Transaction boundary: every outermost Execute opens exactly one txn.Txn.
// Nested Invoke calls run their action body with the same Context, so all
// writes land in one overlay and commit or roll back as a single unit.
//
// Error precedence (only the first hit is reported):
//  1. invalid argument (unknown type/instance, payload type mismatch)
//  2. pre-hook failure
//  3. aggregated post-hook failures
//
// Any failure poisons the transaction: subsequent writes and nested
// invocations return the same error and the whole transaction rolls back.
package action

import (
	"fmt"
	"sync/atomic"

	oerr "ontology/errors"
	"ontology/hooks"
	"ontology/txn"
)

// PropType is a schema property type.
type PropType int

const (
	String PropType = iota
	Int
	Float
	Bool
)

// ObjectType describes the schema of one object type.
type ObjectType struct {
	Name  string
	Props map[string]PropType
}

// Ctx is the execution surface available to an action body. It is an
// interface so that an independent model implementation can run the same
// action bodies for differential testing.
type Ctx interface {
	Create(typeName, id string, props map[string]any) error
	Update(typeName, id string, props map[string]any) error
	Delete(typeName, id string) error
	Invoke(name string, params map[string]any) error
	Get(typeName, id string) (txn.Value, bool)
}

// Definition is a registered action. Run executes inside the caller's
// transaction boundary and may invoke further actions via Ctx.Invoke.
type Definition struct {
	Name string
	Run  func(ctx Ctx, params map[string]any) error
}

// Result is the outcome of one outermost action call.
type Result struct {
	Seq    int64 // global FIFO submission order
	TxnID  int64
	Action string
	Err    error // normalized *oerr.Error, nil on success
}

// Engine ties together the store, hook registry, schemas, action
// definitions and the serial executor.
type Engine struct {
	store *txn.Store
	reg   *hooks.Registry
	rec   *hooks.Recorder
	types map[string]ObjectType
	defs  map[string]Definition

	txnSeq atomic.Int64
	jobSeq atomic.Int64
	jobs   chan job
}

type job struct {
	seq    int64
	name   string
	params map[string]any
	reply  chan Result
}

// NewEngine builds an engine over a fresh committed store. The executor
// worker starts immediately; call Close to stop it.
func NewEngine(reg *hooks.Registry, rec *hooks.Recorder) *Engine {
	e := &Engine{
		store: txn.NewStore(),
		reg:   reg,
		rec:   rec,
		types: map[string]ObjectType{},
		defs:  map[string]Definition{},
		jobs:  make(chan job),
	}
	go e.serve()
	return e
}

// Close shuts the executor worker down after draining queued jobs.
func (e *Engine) Close() {
	close(e.jobs)
}

// RegisterType adds an object-type schema.
func (e *Engine) RegisterType(t ObjectType) {
	e.types[t.Name] = t
}

// RegisterAction adds an action definition.
func (e *Engine) RegisterAction(d Definition) {
	e.defs[d.Name] = d
}

// Get reads a committed instance (for tests and demos).
func (e *Engine) Get(typeName, id string) (txn.Value, bool) {
	return e.store.Get(typeName, id)
}

// CountCommitted counts committed live instances of a type.
func (e *Engine) CountCommitted(typeName string) int {
	return e.store.CountCommitted(typeName)
}

// serve is the single executor worker. Processing jobs one at a time from
// a FIFO channel yields a global serial order equal to submission order;
// two outermost transactions can never interleave.
func (e *Engine) serve() {
	for j := range e.jobs {
		j.reply <- e.run(j)
	}
}

// Submit enqueues an outermost action call and returns a channel that
// receives exactly one Result.
func (e *Engine) Submit(name string, params map[string]any) <-chan Result {
	j := job{
		seq:    e.jobSeq.Add(1),
		name:   name,
		params: params,
		reply:  make(chan Result, 1),
	}
	e.jobs <- j
	return j.reply
}

// Execute runs an outermost action call synchronously.
func (e *Engine) Execute(name string, params map[string]any) Result {
	return <-e.Submit(name, params)
}

// run executes one outermost action call inside a single transaction.
func (e *Engine) run(j job) Result {
	res := Result{Seq: j.seq, Action: j.name}
	def, ok := e.defs[j.name]
	if !ok {
		res.Err = oerr.InvalidArgument(j.name, "unknown action definition")
		return res
	}
	tx := e.store.Begin()
	res.TxnID = e.txnSeq.Add(1)
	ctx := &Context{
		engine: e,
		tx:     tx,
		action: j.name,
		disp:   hooks.NewDispatcher(e.reg, e.rec, res.TxnID, j.name),
	}
	err := def.Run(ctx, j.params)
	if ctx.failed != nil {
		err = ctx.failed
	}
	if err != nil {
		tx.Rollback()
		res.Err = normalize(j.name, err)
		return res
	}
	failures := ctx.disp.FirePostAll(tx.TouchedTypes(), tx.Snapshot())
	if len(failures) > 0 {
		tx.Rollback()
		res.Err = oerr.PostHook(j.name, failures)
		return res
	}
	tx.Commit()
	return res
}

// normalize maps any non-normalized error (e.g. a raw error returned by an
// action body) to KindPreHook, since it surfaced before commit.
func normalize(action string, err error) error {
	if _, ok := err.(*oerr.Error); ok {
		return err
	}
	return oerr.PreHook(action, fmt.Sprintf("action body failed: %v", err))
}

// Context is the execution context of one outermost action call, shared by
// every nested invocation within the same transaction boundary.
type Context struct {
	engine *Engine
	tx     *txn.Txn
	action string
	disp   *hooks.Dispatcher
	failed error // poisoned transaction: first normalized error
}

// View exposes the current in-transaction state to action bodies.
func (c *Context) View() txn.View {
	return c.tx.Snapshot()
}

// Get reads an instance as visible inside the transaction.
func (c *Context) Get(typeName, id string) (txn.Value, bool) {
	return c.tx.Get(typeName, id)
}

// Create stages a create write.
func (c *Context) Create(typeName, id string, props map[string]any) error {
	return c.write(opCreate, typeName, id, props)
}

// Update stages an update write, merged over the existing properties.
func (c *Context) Update(typeName, id string, props map[string]any) error {
	return c.write(opUpdate, typeName, id, props)
}

// Delete stages a delete write.
func (c *Context) Delete(typeName, id string) error {
	return c.write(opDelete, typeName, id, nil)
}

// Invoke calls another (or the same) action definition within the same
// transaction boundary. Its writes join the caller's transaction; post
// hooks never fire for the inner call itself.
func (c *Context) Invoke(name string, params map[string]any) error {
	if c.failed != nil {
		return c.failed
	}
	def, ok := c.engine.defs[name]
	if !ok {
		return c.poison(oerr.InvalidArgument(c.action, fmt.Sprintf("unknown action definition %q", name)))
	}
	if err := def.Run(c, params); err != nil {
		return c.poison(normalize(c.action, err))
	}
	return c.failed
}

type writeOp int

const (
	opCreate writeOp = iota
	opUpdate
	opDelete
)

// write validates, fires pre hooks and stages one write. The order is
// fixed: argument validation first, pre hooks second, apply last.
func (c *Context) write(op writeOp, typeName, id string, props map[string]any) error {
	if c.failed != nil {
		return c.failed
	}
	schema, ok := c.engine.types[typeName]
	if !ok {
		return c.poison(oerr.InvalidArgument(c.action, fmt.Sprintf("unknown object type %q", typeName)))
	}
	if err := checkProps(schema, props); err != nil {
		return c.poison(oerr.InvalidArgument(c.action, err.Error()))
	}
	existing, exists := c.tx.Get(typeName, id)
	switch op {
	case opCreate:
		if exists {
			return c.poison(oerr.InvalidArgument(c.action, fmt.Sprintf("instance %s/%s already exists", typeName, id)))
		}
	case opUpdate, opDelete:
		if !exists {
			return c.poison(oerr.InvalidArgument(c.action, fmt.Sprintf("target instance %s/%s does not exist", typeName, id)))
		}
	}
	if err := c.disp.FirePre(typeName, c.tx.Snapshot()); err != nil {
		return c.poison(err)
	}
	switch op {
	case opCreate:
		c.tx.Apply(typeName, id, cloneValue(props))
	case opUpdate:
		merged := cloneValue(existing)
		for k, v := range props {
			merged[k] = v
		}
		c.tx.Apply(typeName, id, merged)
	case opDelete:
		c.tx.Apply(typeName, id, nil)
	}
	return nil
}

// poison records the first normalized error and returns it. Once poisoned,
// every later write or nested invoke is a no-op returning the same error,
// so a failure aborts all not-yet-started work in every layer.
func (c *Context) poison(err error) error {
	if c.failed == nil {
		c.failed = err
	}
	return c.failed
}

// checkProps validates a write payload against the schema: every property
// must be declared and its Go type must match the declared PropType.
func checkProps(t ObjectType, props map[string]any) error {
	for name, v := range props {
		pt, ok := t.Props[name]
		if !ok {
			return fmt.Errorf("unknown property %q on type %q", name, t.Name)
		}
		if !typeMatches(pt, v) {
			return fmt.Errorf("property %q on type %q: value %v has wrong type", name, t.Name, v)
		}
	}
	return nil
}

func typeMatches(pt PropType, v any) bool {
	switch pt {
	case String:
		_, ok := v.(string)
		return ok
	case Int:
		switch v.(type) {
		case int, int8, int16, int32, int64:
			return true
		}
		return false
	case Float:
		_, ok := v.(float64)
		return ok
	case Bool:
		_, ok := v.(bool)
		return ok
	}
	return false
}

func cloneValue(props map[string]any) txn.Value {
	out := make(txn.Value, len(props))
	for k, v := range props {
		out[k] = v
	}
	return out
}
