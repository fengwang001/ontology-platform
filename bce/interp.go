package bce

// interp.go is the independent naive model: it actually executes a
// program with concrete inputs and records which checks fail. A failed
// check traps (execution stops), matching the semantics the analyzer
// assumes. Used by tests to cross-validate soundness: a removed check
// must never fail in any execution.

// RunOutcome is the result of one concrete execution.
type RunOutcome struct {
	FailedCheck int  // ID of the check that trapped, -1 if none
	Steps       int  // executed instructions
	Halted      bool // true if the run terminated (return or step cap)
}

// Run executes p with the given input values. Variables not in inputs
// start at 0. maxSteps caps execution so arbitrary loops terminate.
func Run(p *Program, inputs map[string]int, maxSteps int) RunOutcome {
	out := RunOutcome{FailedCheck: -1}
	vars := map[string]int{}
	for _, v := range p.Vars {
		vars[v] = inputs[v]
	}
	lens := map[string]int{}
	for _, a := range p.Arrays {
		lens[a] = p.Lengths[a]
	}
	val := func(o Operand) int {
		if o.IsConst {
			return o.Const
		}
		return vars[o.Var]
	}
	block := p.Block(p.Entry)
	steps := 0
	for block != nil && steps < maxSteps {
		for _, in := range block.Instrs {
			steps++
			if steps >= maxSteps {
				out.Steps = steps
				out.Halted = true
				return out
			}
			switch in.Kind {
			case OpAssignConst:
				vars[in.Dst] = in.C
			case OpAssign:
				vars[in.Dst] = vars[in.Src]
			case OpAssignAdd:
				vars[in.Dst] = vars[in.Src] + in.C
			case OpNewArray:
				lens[in.Arr] = in.C
			case OpAssignArray:
				lens[in.Arr] = lens[in.Src]
			case OpCheck:
				idx := val(in.Idx)
				if idx < 0 || idx >= lens[in.Arr] {
					out.FailedCheck = in.ID
					out.Steps = steps
					out.Halted = true
					return out
				}
			}
		}
		t := block.Terms[0]
		switch t.Kind {
		case TJump:
			block = p.Block(t.Target)
		case TBranch:
			ok, _ := evalCmp(t.Op, val(t.Left), val(t.Right))
			if ok {
				block = p.Block(t.Then)
			} else {
				block = p.Block(t.Else)
			}
		case TReturn:
			block = nil
		}
	}
	out.Steps = steps
	out.Halted = true
	return out
}
