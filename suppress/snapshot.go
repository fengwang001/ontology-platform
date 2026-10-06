package suppress

type acceptedDirective struct {
	line     int
	kind     DirectiveKind
	tags     []string
	reason   string
	sequence int
	tagIDs   []int
}

type snapshot struct {
	totalLines    int
	requireReason bool
	knownRules    map[string]struct{}
	diagnostics   []Diagnostic
	directives    []acceptedDirective
}
