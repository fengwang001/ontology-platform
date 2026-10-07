package compensate

import (
	"fmt"
	"sort"
)

// Checker is an optional business validation hook (cascaded validation).
type Checker interface {
	Check(g *Graph, op SubOp) error
}

// Engine executes actions against a graph with compensation support.
type Engine struct {
	g       *Graph
	checker Checker
	tracer  Tracer

	regMu  chan struct{}
	regSeq uint64
}

// undoEntry is one registered inverse, with a globally unique registration
// number. Entries live only on the per-action stack; no global history of
// compensated ops is ever traversed.
type undoEntry struct {
	regNum  uint64
	opIndex int
	undo    func() error
}

// NewEngine constructs an engine bound to a graph.
func NewEngine(g *Graph) *Engine {
	return &Engine{g: g, regMu: make(chan struct{}, 1)}
}

// WithChecker attaches a cascaded validation hook.
func (e *Engine) WithChecker(c Checker) *Engine { e.checker = c; return e }

// WithTracer attaches a step-by-step tracer.
func (e *Engine) WithTracer(t Tracer) *Engine { e.tracer = t; return e }

func (e *Engine) trace(ev TraceEvent) {
	if e.tracer != nil {
		e.tracer.Event(ev)
	}
}

// nextRegNum issues a unique registration number drawn from a monotonic
// counter, so concurrent flows never duplicate or omit registration ids.
func (e *Engine) nextRegNum() uint64 {
	e.regMu <- struct{}{}
	n := e.regSeq
	e.regSeq++
	<-e.regMu
	return n
}

// Execute runs one action, compensating prior effects on later failure.
func (e *Engine) Execute(a Action) ActionResult {
	res := ActionResult{ActionName: a.Name, Primary: ReasonNone, FailedOpIndex: -1}

	// ---- Phase 0: collect resources and involved instances (no mutation) ----
	involved := map[InstanceID]struct{}{}
	tokenSet := map[string]struct{}{}
	for _, op := range a.Ops {
		for _, id := range op.involvedInstances() {
			involved[id] = struct{}{}
		}
		for _, t := range op.resources() {
			tokenSet[t] = struct{}{}
		}
		if op.Kind == OpCreateLink || op.Kind == OpDeleteLink {
			tokenSet[tokenObj(op.Link.Source)] = struct{}{}
			tokenSet[tokenObj(op.Link.Target)] = struct{}{}
		}
	}

	// Preflight priority 1: contamination. The check happens before ANY
	// sub-operation (and before lock acquisition), so a rejected action can
	// never leave partially effective side-effects.
	var polluted []InstanceID
	for id := range involved {
		if idx, ok := e.g.earliestPollution(id); ok {
			polluted = append(polluted, id)
			res.Primary = HigherReason(res.Primary, ReasonContaminated)
			res.Reject = &Failure{OpIndex: idx, Class: ReasonContaminated,
				Detail: "instance polluted: " + string(id)}
		}
	}
	if res.Primary == ReasonContaminated {
		sort.Slice(polluted, func(i, j int) bool { return polluted[i] < polluted[j] })
		e.trace(TraceEvent{ActionName: a.Name, Phase: "action", Kind: "preflight",
			Outcome: "reject", Detail: fmt.Sprintf("contaminated instances=%v", polluted)})
		e.trace(verdictEvent(a.Name, res))
		return res
	}

	// Preflight priority 3: acquire ALL resources up front in one global
	// sorted order. Contention is decided before any observable change;
	// versions/clocks of a rejected action are never touched.
	tokens := make([]string, 0, len(tokenSet))
	for t := range tokenSet {
		tokens = append(tokens, t)
	}
	if !e.g.tryAcquire(tokens) {
		res.Primary = ReasonContention
		res.Reject = &Failure{Class: ReasonContention,
			Detail: "resources busy at action start"}
		e.trace(TraceEvent{ActionName: a.Name, Phase: "action", Kind: "preflight",
			Outcome: "reject", Detail: "contention before first effect"})
		e.trace(verdictEvent(a.Name, res))
		return res
	}
	defer e.g.release(tokens)

	// ---- Phase 1: forward execution, effect + inverse registration indivisible ----
	var stack []undoEntry
	failedAt := -1
	var failClass ReasonClass
	var failDetail string

	for i, op := range a.Ops {
		entry, err, panicked := e.applyAndRegister(a, i, op)
		if err != nil {
			failedAt = i
			failClass = ReasonBusinessReject
			if !panicked {
				failClass, failDetail = classify(err)
			} else {
				failDetail = err.Error()
			}
			e.trace(TraceEvent{ActionName: a.Name, OpIndex: i, Phase: "apply",
				Kind: opKindName(op.Kind), Outcome: outcomeFor(panicked), Detail: failDetail})
			break
		}
		stack = append(stack, entry)
		e.trace(TraceEvent{ActionName: a.Name, OpIndex: i, Phase: "apply",
			Kind: opKindName(op.Kind), Outcome: "effect",
			Detail: fmt.Sprintf("reg#%d", entry.regNum)})
	}

	if failedAt < 0 {
		res.Committed = true
		e.trace(verdictEvent(a.Name, res))
		return res
	}

	// ---- Phase 2: strict reverse-order compensation; never abort ----
	res.FailedOpIndex = failedAt
	res.Primary = failClass
	res.Reject = &Failure{OpIndex: failedAt, Class: failClass, Detail: failDetail}
	res.CompFailures = e.compensate(a, stack)

	if len(res.CompFailures) > 0 {
		res.Primary = HigherReason(res.Primary, ReasonCompensationFailed)
	}
	e.trace(verdictEvent(a.Name, res))
	return res
}

// applyAndRegister is the indivisible processing unit required by the spec.
// The graph primitive performs an atomic check-then-commit, and the inverse
// closure is built from the pre-mutation snapshot and pushed onto the
// per-action stack in the same call frame, before the frame returns. There is
// no observable intermediate state "effective but unregistered" or
// "registered but not effective".
func (e *Engine) applyAndRegister(a Action, i int, op SubOp) (entry undoEntry, err error, panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			err = &BusinessError{Msg: fmt.Sprintf("apply panic: %v", r)}
			panicked = true
		}
	}()

	reg := e.nextRegNum()
	var undo func() error

	switch op.Kind {
	case OpSetAttrs:
		if fp := faultPoint(a.Inject, i, true); fp == FaultApplyPanic {
			panic("injected apply panic")
		} else if fp == FaultApplyFail {
			return undoEntry{}, &BusinessError{Msg: "injected apply failure"}, false
		}
		old, oldVer, gerr := e.g.setAttrsIfExists(op.Object, op.Attrs)
		if gerr != nil {
			return undoEntry{}, gerr, false
		}
		undo = e.makeUndo(a, i, func() error {
			return e.g.restoreAttrs(op.Object, old, oldVer)
		})

	case OpCreateLink:
		if fp := faultPoint(a.Inject, i, true); fp == FaultApplyPanic {
			panic("injected apply panic")
		} else if fp == FaultApplyFail {
			return undoEntry{}, &BusinessError{Msg: "injected apply failure"}, false
		}
		if gerr := e.g.createLinkIfAbsent(op.Link); gerr != nil {
			return undoEntry{}, gerr, false
		}
		undo = e.makeUndo(a, i, func() error { return e.g.removeCreatedLink(op.Link.ID) })

	case OpDeleteLink:
		if fp := faultPoint(a.Inject, i, true); fp == FaultApplyPanic {
			panic("injected apply panic")
		} else if fp == FaultApplyFail {
			return undoEntry{}, &BusinessError{Msg: "injected apply failure"}, false
		}
		old, gerr := e.g.deleteLinkIfExists(op.Link.ID)
		if gerr != nil {
			return undoEntry{}, gerr, false
		}
		undo = e.makeUndo(a, i, func() error { return e.g.restoreDeletedLink(old) })

	case OpValidate:
		if fp := faultPoint(a.Inject, i, true); fp == FaultApplyPanic {
			panic("injected apply panic")
		} else if fp == FaultApplyFail {
			return undoEntry{}, &BusinessError{Msg: "injected apply failure"}, false
		}
		if !e.g.objectExists(op.Object) {
			return undoEntry{}, &BusinessError{Msg: "validate: object not found: " + string(op.Object)}, false
		}
		if e.checker != nil {
			if cerr := e.checker.Check(e.g, op); cerr != nil {
				return undoEntry{}, cerr, false
			}
		}
		// A read-only hook changes no graph state; its inverse is a no-op, but
		// registration is still performed so effect/register stay paired.
		undo = e.makeUndo(a, i, func() error { return nil })

	default:
		return undoEntry{}, &BusinessError{Msg: fmt.Sprintf("unknown op kind %d", op.Kind)}, false
	}

	return undoEntry{regNum: reg, opIndex: i, undo: undo}, nil, false
}

// makeUndo wraps an inverse with fault injection and panic conversion.
// A panic becomes an ordinary failure of that single compensation step.
func (e *Engine) makeUndo(a Action, i int, f func() error) func() error {
	return func() (err error) {
		defer func() {
			if r := recover(); r != nil {
				err = &BusinessError{Msg: fmt.Sprintf("undo panic: %v", r)}
			}
		}()
		switch faultPoint(a.Inject, i, false) {
		case FaultUndoPanic:
			panic("injected undo panic")
		case FaultUndoFail:
			return &BusinessError{Msg: "injected undo failure"}
		}
		return f()
	}
}

// compensate runs registered inverses LIFO. It never aborts: a failing or
// panicking inverse is recorded, the involved instances are marked polluted
// (earliest failing index wins), and every remaining inverse still runs.
func (e *Engine) compensate(a Action, stack []undoEntry) []CompFailure {
	var fails []CompFailure
	// blockedTokens are resources whose compensating inverse failed. Later
	// (earlier-op) inverses touching the same resource are attempted in the
	// sense of being processed, but must not mutate it: the graph state left
	// by the failed inverse is preserved exactly.
	blockedTokens := map[string]struct{}{}
	for j := len(stack) - 1; j >= 0; j-- {
		en := stack[j]
		op := a.Ops[en.opIndex]
		var detail, out string
		var ierr error
		if op.Kind != OpValidate {
			for _, t := range op.resources() {
				if _, bad := blockedTokens[t]; bad {
					detail = "skipped: resource held by earlier failed compensation"
					fails = append(fails, CompFailure{
						OpIndex: en.opIndex, Class: ReasonCompensationFailed, Detail: detail})
					for _, id := range op.involvedInstances() {
						e.g.markPolluted(id, en.opIndex)
					}
					e.trace(TraceEvent{ActionName: a.Name, OpIndex: en.opIndex, Phase: "undo",
						Kind: "inverse", Outcome: "fail", Detail: detail})
					goto next
				}
			}
		}
		ierr = en.undo()
		if ierr == nil {
			e.trace(TraceEvent{ActionName: a.Name, OpIndex: en.opIndex, Phase: "undo",
				Kind: "inverse", Outcome: "undo", Detail: fmt.Sprintf("reg#%d", en.regNum)})
			continue
		}
		detail = ierr.Error()
		out = "fail"
		if isPanicText(detail) {
			out = "panic"
		}
		fails = append(fails, CompFailure{
			OpIndex: en.opIndex, Class: ReasonCompensationFailed, Detail: detail})
		for _, t := range op.resources() {
			blockedTokens[t] = struct{}{}
		}
		for _, id := range a.Ops[en.opIndex].involvedInstances() {
			e.g.markPolluted(id, en.opIndex)
		}
		e.trace(TraceEvent{ActionName: a.Name, OpIndex: en.opIndex, Phase: "undo",
			Kind: "inverse", Outcome: out, Detail: detail})
	next:
	}
	return fails
}

// Repair clears the pollution marker after explicit manual repair.
func (e *Engine) Repair(id InstanceID) { e.g.Repair(id) }

func faultPoint(plan *FaultPlan, opIndex int, apply bool) FaultPoint {
	if plan == nil {
		return FaultNone
	}
	m := plan.Undo
	if apply {
		m = plan.Apply
	}
	if m != nil {
		if fp, ok := m[opIndex]; ok {
			return fp
		}
	}
	return FaultNone
}

func opKindName(k OpKind) string {
	switch k {
	case OpSetAttrs:
		return "set_attrs"
	case OpCreateLink:
		return "create_link"
	case OpDeleteLink:
		return "delete_link"
	case OpValidate:
		return "validate"
	default:
		return "unknown"
	}
}

func outcomeFor(panicked bool) string {
	if panicked {
		return "panic"
	}
	return "reject"
}

func isPanicText(s string) bool {
	return len(s) >= 11 && s[:11] == "undo panic:"
}

func verdictEvent(name string, res ActionResult) TraceEvent {
	committed := "rollback"
	if res.Committed {
		committed = "commit"
	}
	return TraceEvent{ActionName: name, Phase: "action", Kind: "verdict",
		Outcome: "verdict",
		Detail: fmt.Sprintf("%s primary=%s failedOp=%d compFailures=%d",
			committed, res.Primary, res.FailedOpIndex, len(res.CompFailures))}
}
