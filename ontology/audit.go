package ontology

// Relaxation declares the scope over which a replacement implementation
// relaxes rejection relative to the generation it replaces. A replacement
// that flips any rejected input into an accepted input MUST carry a
// non-nil Relaxation; registration is rejected otherwise.
//
// The declared scope is given by Covers, a predicate over inputs. If the new
// implementation differs from the old one (either flips rejection into
// acceptance, or produces a different output) on any input for which Covers
// returns false, that is an undeclared difference: registration is not
// prevented for cases the static probes cannot distinguish, but the
// discrepancy is detectable afterwards by Audit and is recorded permanently.
type Relaxation struct {
	// Description is human-readable provenance for the declaration.
	Description string
	// Covers reports whether the declared relaxation scope includes input.
	Covers func(input any) bool
}

// DifferenceKind classifies how a new generation differs from an old one.
type DifferenceKind int

const (
	DifferenceRejectionRelaxed DifferenceKind = iota
	DifferenceRejectionTightened
	DifferenceOutputChanged
)

func outputsEqual(a any, aErr error, b any, bErr error) bool {
	if (aErr == nil) != (bErr == nil) {
		return false
	}
	if aErr != nil {
		return aErr.Error() == bErr.Error()
	}
	return a == b
}

// checkRelaxationRequired enforces the "relaxation must be declared at
// registration time" rule over the supplied probe corpus.
func checkRelaxationRequired(action *Action, old *generation, new_ Implementation, relax *Relaxation, probes []any) error {
	for _, in := range probes {
		if !old.impl.Pre(in) && new_.Pre(in) && relax == nil {
			return &RegistrationError{
				Action: old.action, Type: old.owner, Reason: reasonRelaxRequired,
			}
		}
	}
	return nil
}

// AuditFinding is one post-hoc detected discrepancy.
type AuditFinding struct {
	Action ActionID
	Type   TypeID
	OldSeq uint64
	NewSeq uint64
	Input  any
	Kind   DifferenceKind
	// Declared reports whether the replacement's relaxation declaration
	// claimed to cover this input.
	Declared bool
}

// AuditReport lists every undeclared difference discovered.
type AuditReport struct {
	FindingCount int
	Findings     []AuditFinding
}

// Audit compares every adjacent generation pair in the registry over probes
// and reports differences outside the declared relaxation scope. It never
// mutates registry state and never blocks; it exists precisely so that
// behavior beyond the declared scope remains discoverable after the fact.
func (r *Registry) Audit(probes []any) *AuditReport {
	r.mu.Lock()
	actionGen := map[ActionID]*Action{}
	for k, v := range r.actions {
		actionGen[k] = v
	}
	type pair struct {
		action ActionID
		typ    TypeID
		old    *generation
		new    *generation
	}
	var pairs []pair
	for aid, m := range r.table {
		for tid, s := range m {
			for i := 1; i < len(s.history); i++ {
				pairs = append(pairs, pair{aid, tid, s.history[i-1], s.history[i]})
			}
		}
	}
	r.mu.Unlock()

	rep := &AuditReport{}
	for _, p := range pairs {
		for _, in := range probes {
			kind, differs := classifyDifference(actionGen[p.action], p.old.impl, p.new.impl, in)
			if !differs {
				continue
			}
			declared := p.new.relax != nil && p.new.relax.Covers != nil && p.new.relax.Covers(in)
			if !declared {
				rep.Findings = append(rep.Findings, AuditFinding{
					Action: p.action, Type: p.typ,
					OldSeq: p.old.seq, NewSeq: p.new.seq,
					Input: in, Kind: kind, Declared: false,
				})
			}
		}
	}
	rep.FindingCount = len(rep.Findings)
	return rep
}

// classifyDifference reports how (if at all) new differs from old on input.
func classifyDifference(action *Action, old, new_ Implementation, in any) (DifferenceKind, bool) {
	oldAccept := old.Pre(in)
	newAccept := new_.Pre(in)
	switch {
	case !oldAccept && newAccept:
		return DifferenceRejectionRelaxed, true
	case oldAccept && !newAccept:
		return DifferenceRejectionTightened, true
	case !oldAccept && !newAccept:
		return 0, false
	}
	oldOut, oldErr := old.Execute(&ExecContext{}, in)
	newOut, newErr := new_.Execute(&ExecContext{}, in)
	if !outputsEqual(oldOut, oldErr, newOut, newErr) {
		return DifferenceOutputChanged, true
	}
	return 0, false
}
