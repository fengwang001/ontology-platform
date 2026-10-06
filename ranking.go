package overload

type comparison int

const (
	compIncomparable comparison = iota
	compEqual
	compLeftBetter
	compRightBetter
)

type rankOutcome struct {
	selected int
	tie      TieRule
	basis    string
	front    []int
}

func rankCandidates(s snapshot, candidates []analyzedCandidate, call Call) rankOutcome {
	front := conversionFrontier(candidates)
	if len(front) == 1 {
		return rankOutcome{selected: front[0], tie: TieNone, basis: "unique candidate after position-wise conversion ranking", front: front}
	}

	nonVariadic := filterCandidates(front, candidates, func(candidate analyzedCandidate) bool {
		return !candidate.report.UsedVariadic
	})
	if len(nonVariadic) == 1 {
		return rankOutcome{selected: nonVariadic[0], tie: TieNoVariadic, basis: "candidate without variadic arguments wins first tie-break", front: front}
	}
	if len(nonVariadic) > 1 {
		front = nonVariadic
	}

	fewestDefaults := minDefaults(candidates, front)
	defaultFront := filterCandidates(front, candidates, func(candidate analyzedCandidate) bool {
		return candidate.report.DefaultsUsed == fewestDefaults
	})
	if len(defaultFront) == 1 {
		return rankOutcome{selected: defaultFront[0], tie: TieFewerDefaults, basis: "candidate using fewer default arguments wins second tie-break", front: front}
	}
	if len(defaultFront) > 1 {
		front = defaultFront
	}

	specialized := specializationFrontier(s, candidates, front, call)
	if len(specialized) == 1 {
		annotateSpecialization(s, candidates, specialized[0], front, call)
		return rankOutcome{selected: specialized[0], tie: TieMoreSpecialized, basis: "more specialized parameter types win third tie-break", front: specialized}
	}
	return rankOutcome{selected: -1, tie: TieNone, basis: "candidates remain incomparable after all tie-breaks", front: specialized}
}

func conversionFrontier(candidates []analyzedCandidate) []int {
	front := []int{}
	for i := range candidates {
		if !candidates[i].report.Applicable {
			continue
		}
		dominated := false
		for j := range candidates {
			if i == j || !candidates[j].report.Applicable {
				continue
			}
			if compareConversions(candidates[j].report, candidates[i].report) == compLeftBetter {
				dominated = true
				break
			}
		}
		if !dominated {
			front = append(front, i)
		}
	}
	return front
}

func compareConversions(left, right CandidateReport) comparison {
	n := len(left.Conversions)
	if len(right.Conversions) != n {
		return compIncomparable
	}
	leftBetter := false
	rightBetter := false
	for i := 0; i < n; i++ {
		lr := left.Conversions[i].Rank
		rr := right.Conversions[i].Rank
		if lr < rr {
			leftBetter = true
		}
		if lr > rr {
			rightBetter = true
		}
	}
	switch {
	case leftBetter && rightBetter:
		return compIncomparable
	case leftBetter:
		return compLeftBetter
	case rightBetter:
		return compRightBetter
	default:
		return compEqual
	}
}

func filterCandidates(indices []int, candidates []analyzedCandidate, keep func(analyzedCandidate) bool) []int {
	result := []int{}
	for _, index := range indices {
		if keep(candidates[index]) {
			result = append(result, index)
		}
	}
	return result
}

func minDefaults(candidates []analyzedCandidate, indices []int) int {
	best := candidates[indices[0]].report.DefaultsUsed
	for _, index := range indices[1:] {
		if candidates[index].report.DefaultsUsed < best {
			best = candidates[index].report.DefaultsUsed
		}
	}
	return best
}

func specializationFrontier(s snapshot, candidates []analyzedCandidate, indices []int, call Call) []int {
	front := []int{}
	for _, i := range indices {
		dominated := false
		for _, j := range indices {
			if i == j {
				continue
			}
			if compareSpecialization(s, candidates[j].report.Declaration, candidates[i].report.Declaration, call) == compLeftBetter {
				dominated = true
				break
			}
		}
		if !dominated {
			front = append(front, i)
		}
	}
	return front
}

func compareSpecialization(s snapshot, left, right Declaration, call Call) comparison {
	n := len(call.Args)
	if n == 0 {
		return compIncomparable
	}
	leftBetter := false
	rightBetter := false
	for position := 0; position < n; position++ {
		lp := parameterAt(left, position).Type
		rp := parameterAt(right, position).Type
		leftToRight := s.conv.convert(lp, rp).rank != RankNone
		rightToLeft := s.conv.convert(rp, lp).rank != RankNone
		switch {
		case leftToRight && !rightToLeft:
			leftBetter = true
		case rightToLeft && !leftToRight:
			rightBetter = true
		case !leftToRight && !rightToLeft:
			return compIncomparable
		}
	}
	switch {
	case leftBetter && rightBetter:
		return compIncomparable
	case leftBetter:
		return compLeftBetter
	case rightBetter:
		return compRightBetter
	default:
		return compEqual
	}
}

func annotateSpecialization(_ snapshot, candidates []analyzedCandidate, winner int, indices []int, call Call) {
	wins := []int{}
	for _, index := range indices {
		if index == winner {
			continue
		}
		for position := range call.Args {
			left := parameterAt(candidates[winner].report.Declaration, position).Type
			right := parameterAt(candidates[index].report.Declaration, position).Type
			if left != right {
				wins = append(wins, position)
			}
		}
	}
	candidates[winner].report.SpecializationWins = wins
}
