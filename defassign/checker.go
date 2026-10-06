package defassign

// Package defassign is a definite-assignment checking subsystem for a
// compiler front end. It is split into four cooperating modules:
//
//   - program.go:   program-structure registration (Registry, Node)
//   - validate.go:  input validation and error categories
//   - cfg.go:       lowering of the structure tree to a control-flow graph
//   - flow.go:      path-state propagation (definite assignment, liveness)
//   - diagnose.go:  diagnostics and deterministic reporting
//
// Check is pure: it never mutates the Registry, so independent programs may
// be checked concurrently and repeated checks of one program produce
// byte-identical output.

// Check validates and analyzes the registered program. It returns the
// diagnostics sorted by position, or a non-nil *InputError and no
// diagnostics when the input is invalid.
func Check(reg *Registry) ([]Diagnostic, *InputError) {
	diags, _, err := CheckWithStats(reg)
	return diags, err
}

// CheckWithStats is Check plus instrumentation counters that make the
// complexity guarantees verifiable.
func CheckWithStats(reg *Registry) ([]Diagnostic, Stats, *InputError) {
	if ierr := Validate(reg); ierr != nil {
		return nil, Stats{}, ierr
	}
	g, entries := buildCFG(reg)
	fr := analyze(reg, g, entries)
	return collectDiagnostics(reg, fr), fr.stats, nil
}
