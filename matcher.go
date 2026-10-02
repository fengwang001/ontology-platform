package ontology

type executionState struct {
	pc  int
	pos int
}

type executionOutcome struct {
	kind  string
	start int
	end   int
	steps int64
}

func runProgram(prog *program, input []byte, limit int64, memoize bool, limitKind string) executionOutcome {
	return runProgramWithDispatchCount(prog, input, limit, memoize, limitKind, nil)
}

func runProgramWithDispatchCount(prog *program, input []byte, limit int64, memoize bool, limitKind string, dispatchCount *int64) executionOutcome {
	visited := make(map[executionState]struct{})
	stack := make([]executionState, 0, len(prog.code))
	var steps int64

	start := 0
	for start <= len(input) {
		state := executionState{pc: 0, pos: start}
		stack = stack[:0]

		for {
			if memoize {
				if _, ok := visited[state]; ok {
					if len(stack) == 0 {
						break
					}
					state = stack[len(stack)-1]
					stack = stack[:len(stack)-1]
					continue
				}
				visited[state] = struct{}{}
			}
			if dispatchCount != nil {
				*dispatchCount++
			}

			if steps == limit {
				return executionOutcome{kind: limitKind, steps: steps}
			}
			steps++

			inst := prog.code[state.pc]
			fail := func() bool {
				if len(stack) == 0 {
					return false
				}
				state = stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				return true
			}
			switch inst.op {
			case opChar:
				if state.pos >= len(input) || input[state.pos] != inst.byte {
					if !fail() {
						goto nextStart
					}
					continue
				}
				state.pc++
				state.pos++
			case opAny:
				if state.pos >= len(input) || input[state.pos] == '\n' {
					if !fail() {
						goto nextStart
					}
					continue
				}
				state.pc++
				state.pos++
			case opClass:
				if state.pos >= len(input) || !inst.mask[input[state.pos]] {
					if !fail() {
						goto nextStart
					}
					continue
				}
				state.pc++
				state.pos++
			case opStartAssert:
				if state.pos != 0 {
					if !fail() {
						goto nextStart
					}
					continue
				}
				state.pc++
			case opEndAssert:
				if state.pos != len(input) {
					if !fail() {
						goto nextStart
					}
					continue
				}
				state.pc++
			case opSplit:
				stack = append(stack, executionState{pc: inst.second, pos: state.pos})
				state.pc = inst.first
			case opJmp:
				state.pc = inst.first
			case opMatch:
				return executionOutcome{
					kind:  OutcomeMatch,
					start: start,
					end:   state.pos,
					steps: steps,
				}
			}
		}
	nextStart:
		start++
	}

	return executionOutcome{kind: OutcomeNoMatch, steps: steps}
}
