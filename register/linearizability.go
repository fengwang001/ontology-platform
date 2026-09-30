package register

import "sort"

type searchState struct {
	placed uint32
	value  int
}

func findWitness(ops []Operation) (bool, []int, string) {
	completedCount := 0
	for _, op := range ops {
		if op.Completed {
			completedCount++
		}
	}

	path := make([]int, 0, len(ops))
	failed := make(map[searchState]struct{})
	if witness := search(ops, 0, 0, completedCount, path, failed); witness != nil {
		return true, witness, "found a total order compatible with real-time order and register semantics"
	}
	return false, nil, "exhausted all eligible operation orders without satisfying the observed register results"
}

func search(ops []Operation, placed uint32, currentValue, completedCount int, path []int, failed map[searchState]struct{}) []int {
	if completedCount == 0 {
		witness := make([]int, len(path))
		copy(witness, path)
		return witness
	}

	state := searchState{placed: placed, value: currentValue}
	if _, ok := failed[state]; ok {
		return nil
	}

	candidates := eligibleCandidates(ops, placed)
	for _, index := range candidates {
		nextValue, ok := applyOperation(ops[index], currentValue)
		if !ok {
			continue
		}

		nextPlaced := placed | (uint32(1) << index)
		path = append(path, ops[index].ID)
		nextCompleted := completedCount
		if ops[index].Completed {
			nextCompleted--
		}

		if witness := search(ops, nextPlaced, nextValue, nextCompleted, path, failed); witness != nil {
			return witness
		}
		path = path[:len(path)-1]
	}

	failed[state] = struct{}{}
	return nil
}

func eligibleCandidates(ops []Operation, placed uint32) []int {
	candidates := make([]int, 0)
	for candidateIndex, candidate := range ops {
		if placed&(uint32(1)<<candidateIndex) != 0 {
			continue
		}
		if !candidate.Completed && candidate.Kind == KindRead {
			continue
		}

		eligible := true
		for blockerIndex, blocker := range ops {
			if blockerIndex == candidateIndex || !blocker.Completed {
				continue
			}
			if placed&(uint32(1)<<blockerIndex) != 0 {
				continue
			}
			if blocker.ReturnTime < candidate.InvokeTime {
				eligible = false
				break
			}
		}
		if eligible {
			candidates = append(candidates, candidateIndex)
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		left := ops[candidates[i]]
		right := ops[candidates[j]]
		if left.InvokeTime != right.InvokeTime {
			return left.InvokeTime < right.InvokeTime
		}
		return left.ID < right.ID
	})
	return candidates
}

func applyOperation(op Operation, currentValue int) (int, bool) {
	switch op.Kind {
	case KindWrite:
		return op.Value, true
	case KindRead:
		if !op.Completed || currentValue != op.ReadValue {
			return currentValue, false
		}
		return currentValue, true
	case KindCAS:
		matches := currentValue == op.Expected
		if op.Completed {
			if matches != op.CASSucceeded {
				return currentValue, false
			}
		} else if !matches {
			return currentValue, false
		}

		if matches {
			return op.New, true
		}
		return currentValue, true
	default:
		return currentValue, false
	}
}

func exhaustiveLinearizable(ops []Operation) bool {
	mandatory := make([]int, 0)
	optional := make([]int, 0)
	for index, op := range ops {
		switch {
		case op.Completed:
			mandatory = append(mandatory, index)
		case op.Kind == KindWrite || op.Kind == KindCAS:
			optional = append(optional, index)
		}
	}

	for mask := 0; mask < 1<<len(optional); mask++ {
		order := append([]int(nil), mandatory...)
		for index, opIndex := range optional {
			if mask&(1<<index) != 0 {
				order = append(order, opIndex)
			}
		}
		if permutationSucceeds(ops, order, 0, 0) {
			return true
		}
	}
	return false
}

func permutationSucceeds(ops []Operation, order []int, start, currentValue int) bool {
	if start == len(order) {
		return true
	}

	for index := start; index < len(order); index++ {
		candidate := ops[order[index]]
		eligible := true
		for blockerPosition := start; blockerPosition < len(order); blockerPosition++ {
			blocker := ops[order[blockerPosition]]
			if blockerPosition == index || !blocker.Completed {
				continue
			}
			if blocker.ReturnTime < candidate.InvokeTime {
				eligible = false
				break
			}
		}
		if !eligible {
			continue
		}

		nextValue, ok := applyOperation(candidate, currentValue)
		if !ok {
			continue
		}

		order[start], order[index] = order[index], order[start]
		if permutationSucceeds(ops, order, start+1, nextValue) {
			order[start], order[index] = order[index], order[start]
			return true
		}
		order[start], order[index] = order[index], order[start]
	}
	return false
}
