package suppress

type suppressor struct {
	id            int
	directiveLine int
	sequence      int
	tag           string
	tagSpecific   bool
	kind          DirectiveKind
	reason        string
	used          bool
}

func (candidate *suppressor) identity() suppressorIdentity {
	return suppressorIdentity(candidate.id)
}

type suppressorKey struct {
	line int
	rule string
}

func analyze(snap snapshot) *Report {
	runner := newAnalysis(snap)
	runner.run()
	return runner.report()
}
