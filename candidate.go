package overload

type analyzedCandidate struct {
	report CandidateReport
}

func analyzeCandidates(s snapshot, call Call) []analyzedCandidate {
	declarations := s.declarations[call.Name]
	candidates := make([]analyzedCandidate, 0, len(declarations))
	for _, declaration := range declarations {
		candidates = append(candidates, analyzeCandidate(s, declaration, call))
	}
	return candidates
}

func analyzeCandidate(s snapshot, declaration Declaration, call Call) analyzedCandidate {
	fixedCount := len(declaration.Params)
	variadic := fixedCount > 0 && declaration.Params[fixedCount-1].Variadic
	if variadic {
		fixedCount--
	}
	minCount := 0
	for i := range declaration.Params {
		if !declaration.Params[i].Default && !declaration.Params[i].Variadic {
			minCount++
		}
	}
	maxCount := fixedCount
	if variadic {
		maxCount = -1
	}
	cr := CandidateReport{Declaration: declaration, Conversions: []PositionRank{}}
	if len(call.Args) < minCount {
		cr.Rejection = CandidateRejection{Reason: RejectTooFewArgs, Message: "too few arguments"}
		return analyzedCandidate{report: cr}
	}
	if maxCount >= 0 && len(call.Args) > maxCount {
		cr.Rejection = CandidateRejection{Reason: RejectTooManyArgs, Message: "too many arguments"}
		return analyzedCandidate{report: cr}
	}
	cr.UsedVariadic = variadic && len(call.Args) > fixedCount
	if len(call.Args) < fixedCount {
		for i := len(call.Args); i < fixedCount; i++ {
			if declaration.Params[i].Default {
				cr.DefaultsUsed++
			}
		}
	}
	for position, argument := range call.Args {
		param := parameterAt(declaration, position)
		path := s.conv.convert(argument, param.Type)
		conversion := PositionRank{
			Position:         position,
			From:             argument,
			To:               param.Type,
			Rank:             path.rank,
			PromotionsBefore: path.beforeUD,
			PromotionsAfter:  path.afterUD,
			UsesUserDef:      path.rank == RankUser,
			ChainedUserDef:   path.chainedUD,
		}
		cr.Conversions = append(cr.Conversions, conversion)
		if path.rank == RankNone {
			reason := RejectUnconvertible
			message := "argument cannot be converted to parameter type"
			if path.chainedUD {
				reason = RejectChainedUserDef
				message = "two user-defined conversions would be chained"
			}
			cr.Rejection = CandidateRejection{
				Reason:   reason,
				Message:  message,
				Position: position,
				From:     argument,
				To:       param.Type,
			}
			return analyzedCandidate{report: cr}
		}
	}
	cr.Applicable = true
	return analyzedCandidate{report: cr}
}

func parameterAt(declaration Declaration, position int) Param {
	if position < len(declaration.Params) {
		param := declaration.Params[position]
		if !param.Variadic {
			return param
		}
	}
	return declaration.Params[len(declaration.Params)-1]
}

func applicableCandidates(candidates []analyzedCandidate) []analyzedCandidate {
	result := []analyzedCandidate{}
	for _, candidate := range candidates {
		if candidate.report.Applicable {
			result = append(result, candidate)
		}
	}
	return result
}
