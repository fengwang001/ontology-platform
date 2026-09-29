package vm

// MaxCallDepth is the number of allowed nested CALL frames (root counts as 1).
const MaxCallDepth = 4

// Fixed instruction fees.
const (
	FeePush   int64 = 1
	FeeAdd    int64 = 2
	FeeLoad   int64 = 3
	FeeStore  int64 = 4
	FeeCall   int64 = 5
	FeeReturn int64 = 1
	FeeFail   int64 = 1
	FeeExpand int64 = 2 // flat fee besides the memory-expansion delta
)

// CALL leaves a status flag for the callee on the caller's stack.
const (
	successFlag int64 = 1
	failFlag    int64 = 0
)

// FrameReport records per-frame fuel accounting for audit.
type FrameReport struct {
	Program        string
	Depth          int
	Initial        int64
	Consumed       int64
	SentToChildren int64 // net fuel transferred to callees: sum(forwarded-returned)
	Returned       int64
	Reason         FailReason
	Children       []FrameReport
}

// Result is the outcome of one top-level execution.
type Result struct {
	Success   bool
	Reason    FailReason
	Remaining int64
	Root      FrameReport
}

// view is one frame's storage layer. Reads fall through to the parent (and
// finally to the committed snapshot); writes stay local so a failed frame and
// all of its descendants can be discarded together while the parent keeps any
// writes made by successful children.
type view struct {
	parent *view
	base   map[int64]int64 // committed snapshot, only used by the root layer
	local  map[int64]int64
}

func (v *view) get(key int64) int64 {
	for l := v; l != nil; l = l.parent {
		if val, ok := l.local[key]; ok {
			return val
		}
		if l.base != nil {
			return l.base[key]
		}
	}
	return 0
}

// merge pushes all writes of this layer (children included) into the parent.
func (v *view) merge() {
	for k, val := range v.local {
		v.parent.local[k] = val
	}
}

// Executor runs programs against a shared store.
type Executor struct {
	store    *Store
	programs Programs
}

// NewExecutor builds an executor after static validation.
func NewExecutor(store *Store, programs Programs) (*Executor, error) {
	if err := programs.Validate(); err != nil {
		return nil, err
	}
	return &Executor{store: store, programs: programs}, nil
}

type frame struct {
	program     int
	programName string
	depth       int
	stack       []int64
	memWords    int64
	view        *view
	fuel        int64 // remaining in this frame
	initial     int64
	consumed    int64 // own consumption: own fees + memory deltas
	sentToChild int64 // net fuel handed to callees
	children    []FrameReport
}

// memoryCost is the cumulative cost to hold n words: 3n + n*n/512.
func memoryCost(n int64) int64 {
	return 3*n + n*n/512
}

// charge deducts fee; the caller turns a false result into FailOutOfFuel.
func (f *frame) charge(fee int64) bool {
	if fee > f.fuel {
		return false
	}
	f.fuel -= fee
	f.consumed += fee
	return true
}

// exhaust burns the frame's residual fuel and reports out-of-fuel. This keeps
// the frame identity exact: Initial = Consumed + SentToChildren + Returned.
func (f *frame) exhaust() FailReason {
	f.consumed += f.fuel
	f.fuel = 0
	return FailOutOfFuel
}

// Run executes program target with the given fuel budget.
func (e *Executor) Run(target int, fuel int64) (*Result, error) {
	if target < 0 || target >= len(e.programs) {
		return nil, &StaticError{Msg: "run target does not exist"}
	}
	if fuel < 0 {
		return nil, &StaticError{Msg: "negative initial fuel"}
	}

	// Serialize whole executions; readers never see a half-applied run.
	base := e.store.begin()

	root := &frame{
		program: target,
		depth:   1,
		fuel:    fuel,
		initial: fuel,
		view:    &view{base: base, local: make(map[int64]int64)},
	}
	reason := e.exec(root)

	res := &Result{
		Success:   reason == FailNone,
		Reason:    reason,
		Remaining: root.fuel,
		Root:      root.report(reason),
	}

	if reason == FailNone {
		committed := base
		for k, val := range root.view.local {
			committed[k] = val
		}
		e.store.commit(committed)
	} else {
		// Top-level failure: the whole execution is undone.
		e.store.rollback()
	}
	return res, nil
}

func (f *frame) report(reason FailReason) FrameReport {
	return FrameReport{
		Program:        f.programName,
		Depth:          f.depth,
		Initial:        f.initial,
		Consumed:       f.consumed,
		SentToChildren: f.sentToChild,
		Returned:       f.fuel,
		Reason:         reason,
		Children:       f.children,
	}
}

// exec runs one frame and returns its failure reason (FailNone on success).
func (e *Executor) exec(f *frame) FailReason {
	code := e.programs[f.program].Code
	f.programName = e.programs[f.program].Name

	var pc int
	for pc < len(code) {
		ins := code[pc]

		// CALL is handled before its fee is charged so the fee accounting stays
		// in one place; the depth-exceeded case still pays the CALL fee.
		if ins.Op == OpCall {
			if !f.charge(FeeCall) {
				return f.exhaust()
			}
			if f.depth >= MaxCallDepth {
				// Child fails immediately: fee paid, nothing forwarded. Record
				// the synthetic instant-failure child; parent keeps going.
				f.children = append(f.children, FrameReport{
					Program: e.programs[int(ins.A)].Name,
					Depth:   f.depth + 1,
					Reason:  FailDepthExceeded,
				})
				f.stack = append(f.stack, failFlag)
				pc++
				continue
			}

			// Forward min(request, r - r/64) where r is post-fee fuel.
			requested := ins.B
			capped := f.fuel - f.fuel/64
			forward := requested
			if capped < forward {
				forward = capped
			}
			// Transfer the forwarded budget out of the parent now.
			f.fuel -= forward
			f.sentToChild += forward

			child := &frame{
				program: int(ins.A),
				depth:   f.depth + 1,
				fuel:    forward,
				initial: forward,
				view:    &view{parent: f.view, local: make(map[int64]int64)},
			}
			childReason := e.exec(child)
			f.children = append(f.children, child.report(childReason))

			switch childReason {
			case FailNone:
				// Success: child writes become visible, unused fuel comes back.
				child.view.merge()
				f.fuel += child.fuel
				f.sentToChild -= child.fuel
				f.stack = append(f.stack, successFlag)
			case FailOutOfFuel:
				// Child (or a descendant that never returned) exhausted its
				// forwarded budget: none comes back, writes are discarded,
				// parent keeps running with a failure flag.
				f.stack = append(f.stack, failFlag)
			default:
				// Any other failure: writes discarded, unused fuel refunded.
				f.fuel += child.fuel
				f.sentToChild -= child.fuel
				f.stack = append(f.stack, failFlag)
			}
			pc++
			continue
		}

		fee := int64(0)
		switch ins.Op {
		case OpPush:
			fee = FeePush
		case OpAdd:
			fee = FeeAdd
		case OpLoad:
			fee = FeeLoad
		case OpStore:
			fee = FeeStore
		case OpExpand:
			fee = FeeExpand
			if ins.Operand > f.memWords {
				fee += memoryCost(ins.Operand) - memoryCost(f.memWords)
			}
		case OpReturn:
			fee = FeeReturn
		case OpFail:
			fee = FeeFail
		}
		if !f.charge(fee) {
			return f.exhaust()
		}

		switch ins.Op {
		case OpPush:
			f.stack = append(f.stack, ins.Operand)
		case OpAdd:
			if len(f.stack) < 2 {
				return FailStackUnderflow
			}
			a := f.stack[len(f.stack)-1]
			b := f.stack[len(f.stack)-2]
			f.stack = f.stack[:len(f.stack)-2]
			f.stack = append(f.stack, b+a)
		case OpLoad:
			if len(f.stack) < 1 {
				return FailStackUnderflow
			}
			key := f.stack[len(f.stack)-1]
			f.stack = f.stack[:len(f.stack)-1]
			f.stack = append(f.stack, f.view.get(key))
		case OpStore:
			if len(f.stack) < 2 {
				return FailStackUnderflow
			}
			key := f.stack[len(f.stack)-1]
			val := f.stack[len(f.stack)-2]
			f.stack = f.stack[:len(f.stack)-2]
			f.view.local[key] = val
		case OpExpand:
			if ins.Operand > f.memWords {
				f.memWords = ins.Operand
			}
		case OpReturn:
			return FailNone
		case OpFail:
			return FailExplicit
		}
		pc++
	}
	// Falling off the end is an implicit successful return (no fee).
	return FailNone
}
