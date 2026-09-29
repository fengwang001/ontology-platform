package stackvm

import (
	"context"
	"sort"
)

// Submit registers one or more programs and statically validates every one
// of them, together with all programs already present. An unknown opcode, a
// missing call target, or a negative memory-expansion operand anywhere in
// the combined program set rejects the whole batch atomically: nothing is
// registered and nothing has executed.
func (m *Machine) Submit(programs ...*Program) error {
	m.store.Mu.Lock()
	defer m.store.Mu.Unlock()

	merged := make(map[string]*Program, len(m.programs)+len(programs))
	for name, p := range m.programs {
		merged[name] = p
	}
	names := make([]string, 0, len(programs))
	for _, p := range programs {
		if p == nil {
			return &StaticError{Reason: "nil program"}
		}
		if _, dup := merged[p.Name]; dup {
			return &StaticError{Program: p.Name, Reason: "duplicate program name"}
		}
		merged[p.Name] = p
		names = append(names, p.Name)
	}

	if err := validateAll(merged); err != nil {
		return err
	}
	for _, name := range names {
		m.programs[name] = merged[name]
	}
	return nil
}

// MustSubmit is Submit that panics on a static error.
func (m *Machine) MustSubmit(programs ...*Program) {
	if err := m.Submit(programs...); err != nil {
		panic(err)
	}
}

func validateAll(all map[string]*Program) error {
	names := make([]string, 0, len(all))
	for name := range all {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		p := all[name]
		for pc, ins := range p.Code {
			switch ins.Op {
			case OpPush, OpAdd, OpSLoad, OpSStore, OpReturn, OpFail:
				// no static operand constraints
			case OpMExpand:
				if ins.A < 0 {
					return &StaticError{Program: name, PC: pc, Reason: ReasonNegativeWords}
				}
			case OpCall:
				if _, ok := all[ins.Target()]; !ok {
					return &StaticError{Program: name, PC: pc, Reason: ReasonMissingTarget}
				}
			default:
				return &StaticError{Program: name, PC: pc, Reason: ReasonUnknownOpcode}
			}
		}
	}
	return nil
}

// Execute runs a previously submitted program with the given initial fuel.
// The whole execution is serialized with respect to every other execution on
// this machine, so it applies atomically to the shared storage. A top-level
// frame failure rolls back every storage write made by the execution; a
// failed child frame rolls back only itself and its descendants while the
// parent keeps running with a 0 pushed onto its stack.
func (m *Machine) Execute(ctx context.Context, name string, input []int64, fuel uint64) (*Receipt, error) {
	m.store.Mu.Lock()
	defer m.store.Mu.Unlock()

	prog, ok := m.programs[name]
	if !ok {
		return nil, &StaticError{Program: name, PC: -1, Reason: ReasonMissingTarget}
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	f := &frame{
		machine: m,
		program: prog,
		depth:   0,
		initial: fuel,
		gas:     fuel,
		ledger:  &ledger{},
		stack:   append([]int64(nil), input...),
	}
	output, cause, _ := f.run()

	receipt := &Receipt{
		Program:     name,
		Success:     cause == CauseNone,
		Cause:       cause,
		Output:      output,
		Frames:      f.ledger.items,
		InitialFuel: fuel,
	}
	for _, acc := range f.ledger.items {
		receipt.SpentFuel += acc.Spent
	}

	// Fuel conservation: initial fuel equals the sum of each frame's own
	// consumption plus the fuel left over at the root frame.
	var treeSpent uint64
	for _, acc := range f.ledger.items {
		treeSpent += acc.Spent
	}
	root := f.ledger.items[0]
	if root.Initial != treeSpent+root.Returned {
		panic("stackvm: fuel conservation violated across frames")
	}
	return receipt, nil
}

// Programs returns the sorted registered program names.
func (m *Machine) Programs() []string {
	m.store.Mu.Lock()
	defer m.store.Mu.Unlock()
	names := make([]string, 0, len(m.programs))
	for name := range m.programs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// frame is one call frame. Only the root frame owns accounts; descendants
// share it so the receipt ledger is in call-creation order.
type frame struct {
	machine   *Machine
	program   *Program
	depth     int
	initial   uint64
	gas       uint64
	spent     uint64
	highWater uint64
	stack     []int64
	ledger    *ledger
	journal   []journalEntry
}

// ledger collects frame accounts in call-creation order. Each frame reserves
// a placeholder slot when it starts and fills it when it ends; sharing one
// pointer avoids slice-growth aliasing across nested calls.
type ledger struct {
	items []FrameAccounting
}

// run executes the frame and returns its output stack, failure cause and
// returned fuel. On failure every storage write by this frame and by frames
// it called is rolled back.
func (f *frame) run() (output []int64, cause FailCause, returned uint64) {
	accIdx := len(f.ledger.items)
	f.ledger.items = append(f.ledger.items, FrameAccounting{})

	defer func() {
		if r := recover(); r != nil {
			if fe, ok := r.(*frameError); ok {
				cause = fe.cause
				output = nil
			} else {
				panic(r)
			}
		}

		if cause != CauseNone {
			// Undo this frame's own writes and every descendant frame's
			// writes (their journal entries were folded in by invoke).
			f.machine.store.undo(f.journal)
		}
		if cause == CauseFuelExhausted {
			// A fuel-exhausted frame burns its whole forward: none comes back.
			f.spent += f.gas
			f.gas = 0
			returned = 0
		} else {
			returned = f.gas
		}

		f.ledger.items[accIdx] = FrameAccounting{
			Program:  f.program.Name,
			Depth:    f.depth,
			Initial:  f.initial,
			Spent:    f.spent,
			Returned: returned,
			Cause:    cause,
		}
	}()

	code := f.program.Code
	for pc := 0; pc < len(code); pc++ {
		ins := code[pc]
		switch ins.Op {
		case OpPush:
			f.charge(FeePush)
			f.push(ins.A)

		case OpAdd:
			f.charge(FeeAdd)
			b := f.pop()
			a := f.pop()
			f.push(a + b)

		case OpSLoad:
			f.charge(FeeSLoad)
			key := f.pop()
			f.push(f.machine.store.read(key))

		case OpSStore:
			f.charge(FeeSStore)
			value := f.pop()
			key := f.pop()
			f.journal = append(f.journal, f.machine.store.write(key, value))

		case OpMExpand:
			f.charge(FeeMExpand)
			f.expand(uint64(ins.A))

		case OpCall:
			// The call instruction's own fee is always paid; an
			// over-depth call forwards nothing but still costs the fee.
			f.charge(FeeCall)
			success := f.invoke(ins.Target(), uint64(ins.A))
			if success {
				f.push(1)
			} else {
				f.push(0)
			}

		case OpReturn:
			f.charge(FeeReturn)
			cause = CauseNone
			output = append([]int64(nil), f.stack...)
			return

		case OpFail:
			f.charge(FeeFail)
			f.fail(CauseExplicitFail)
		}
	}

	// Falling off the end is a successful return of the whole stack.
	cause = CauseNone
	output = append([]int64(nil), f.stack...)
	return
}

// invoke executes a child frame and folds its fuel and storage result into
// the parent. It reports whether the child succeeded.
func (f *frame) invoke(target string, requested uint64) bool {
	if f.depth+1 > f.machine.maxDepth {
		// Depth-exceeded call: child fails immediately, nothing forwarded.
		f.ledger.items = append(f.ledger.items, FrameAccounting{
			Program: target,
			Depth:   f.depth + 1,
			Cause:   CauseDepthExceeded,
		})
		return false
	}

	forwarded := forwardGas(f.gas, requested)
	if forwarded == 0 {
		// Nothing can be forwarded: the child cannot even start and is
		// deemed fuel-exhausted with the whole (zero) forward consumed.
		f.ledger.items = append(f.ledger.items, FrameAccounting{
			Program: target,
			Depth:   f.depth + 1,
			Cause:   CauseFuelExhausted,
		})
		return false
	}

	f.gas -= forwarded
	child := &frame{
		machine: f.machine,
		program: f.machine.programs[target],
		depth:   f.depth + 1,
		initial: forwarded,
		gas:     forwarded,
		ledger:  f.ledger,
	}
	_, cause, returned := child.run()

	// The child's journal was already undone inside run on failure.
	f.journal = append(f.journal, child.journal...)

	if cause == CauseFuelExhausted {
		// Entire forward consumed: nothing is given back.
		return false
	}
	f.gas += returned
	return cause == CauseNone
}

// expand raises the memory high-water mark to at least words, charging only
// the difference between cumulative expansion costs.
func (f *frame) expand(words uint64) {
	if words <= f.highWater {
		return
	}
	delta := memoryCost(words) - memoryCost(f.highWater)
	if delta > f.gas {
		f.fail(CauseFuelExhausted)
	}
	f.gas -= delta
	f.spent += delta
	f.highWater = words
}

func (f *frame) charge(fee uint64) {
	if fee > f.gas {
		f.fail(CauseFuelExhausted)
	}
	f.gas -= fee
	f.spent += fee
}

func (f *frame) push(v int64) { f.stack = append(f.stack, v) }

func (f *frame) pop() int64 {
	if len(f.stack) == 0 {
		f.fail(CauseStackUnderflow)
	}
	v := f.stack[len(f.stack)-1]
	f.stack = f.stack[:len(f.stack)-1]
	return v
}

func (f *frame) fail(cause FailCause) {
	panic(&frameError{cause: cause})
}
