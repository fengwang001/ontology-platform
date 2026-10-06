package suppress

type diagnosticEntry struct {
	diagnostic Diagnostic
	sequence   int
	suppressor *suppressor
}

type analysisState struct {
	snap           snapshot
	directives     []acceptedDirective
	diagnostics    []diagnosticEntry
	issues         []DirectiveIssue
	active         map[string]*activeRange
	points         map[suppressorKey]*suppressor
	files          map[string]*suppressor
	diagnosticAt   map[suppressorKey][]*diagnosticEntry
	allCandidates  map[suppressorKey]*suppressor
	candidates     map[suppressorIdentity]*suppressor
	nextID         int
	unclosed       map[int]struct{}
	reportedIssues map[reportedIssueKey]struct{}
}

func newAnalysis(snap snapshot) *analysisState {
	directives := append([]acceptedDirective(nil), snap.directives...)
	diagnostics := make([]diagnosticEntry, len(snap.diagnostics))
	for i := range snap.diagnostics {
		diagnostics[i] = diagnosticEntry{diagnostic: snap.diagnostics[i], sequence: i}
	}
	radixSortDirectives(directives)
	radixSortDiagnosticsByLine(diagnostics)

	state := &analysisState{
		snap:           snap,
		directives:     directives,
		diagnostics:    diagnostics,
		active:         make(map[string]*activeRange),
		points:         make(map[suppressorKey]*suppressor),
		files:          make(map[string]*suppressor),
		diagnosticAt:   make(map[suppressorKey][]*diagnosticEntry),
		allCandidates:  make(map[suppressorKey]*suppressor),
		candidates:     make(map[suppressorIdentity]*suppressor),
		nextID:         1,
		unclosed:       make(map[int]struct{}),
		reportedIssues: make(map[reportedIssueKey]struct{}),
	}
	for directiveIndex := range directives {
		state.directives[directiveIndex].tagIDs = make([]int, len(state.directives[directiveIndex].tags))
		for tagIndex := range state.directives[directiveIndex].tagIDs {
			state.directives[directiveIndex].tagIDs[tagIndex] = state.nextID
			state.nextID++
		}
	}
	for i := range diagnostics {
		key := suppressorKey{line: diagnostics[i].diagnostic.Line, rule: diagnostics[i].diagnostic.Rule}
		state.diagnosticAt[key] = append(state.diagnosticAt[key], &diagnostics[i])
	}
	return state
}
