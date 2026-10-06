package overload

func resolveSnapshot(s snapshot, call Call) Report {
	analyzed := analyzeCandidates(s, call)
	candidates := make([]CandidateReport, len(analyzed))
	for i, candidate := range analyzed {
		candidates[i] = candidate.report
	}
	report := Report{Call: call, Candidates: candidates}
	applicable := applicableCandidates(analyzed)
	if len(applicable) == 0 {
		report.NoMatch = true
		report.Basis = "no candidate accepts all arguments"
		return report
	}
	outcome := rankCandidates(s, analyzed, call)
	report.TieRule = outcome.tie
	report.Basis = outcome.basis
	if outcome.selected >= 0 {
		declaration := analyzed[outcome.selected].report.Declaration
		report.Selected = &declaration
		report.Candidates = reportsFromAnalyzed(analyzed)
		return report
	}
	report.Ambiguous = true
	for _, index := range outcome.front {
		candidate := analyzed[index]
		report.AmbiguousCandidates = append(report.AmbiguousCandidates, candidate.report.Declaration)
	}
	report.Candidates = reportsFromAnalyzed(analyzed)
	return report
}

func reportsFromAnalyzed(candidates []analyzedCandidate) []CandidateReport {
	reports := make([]CandidateReport, len(candidates))
	for i, candidate := range candidates {
		reports[i] = candidate.report
	}
	return reports
}
