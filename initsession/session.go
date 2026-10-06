package initsession

import "sync"

// Session records package-level variable initialization units and
// function declarations in source order and answers initialization
// order queries.
//
// A Session is safe for concurrent use: registrations and solves may be
// issued by multiple goroutines. A solve observes one consistent
// snapshot of accepted operations and never sees a half-registered
// unit.
type Session struct {
	mu sync.RWMutex

	// predeclared names are ready before any initialization and may not
	// be redeclared.
	predeclared map[string]struct{}

	names     map[string]nameInfo
	units     []*unit
	functions []*fn
	decls     []decl

	// totalRefs counts deduplicated references stored in accepted
	// declarations; surfaced through Stats.
	totalRefs int
}

// New creates a Session with the given predeclared identifiers, which
// are considered ready without initialization and may not be
// redeclared.
func New(predeclared ...string) (*Session, error) {
	pre := make(map[string]struct{}, len(predeclared))
	for _, name := range predeclared {
		if !IsValidIdentifier(name) || IsBlank(name) {
			return nil, invalidf("invalid predeclared identifier %q", name)
		}
		pre[name] = struct{}{}
	}
	return &Session{
		predeclared: pre,
		names:       make(map[string]nameInfo),
	}, nil
}

// validateRefs checks a reference set: every entry must be a valid
// identifier and none may be the blank identifier. It returns the
// references deduplicated in first-occurrence order.
func validateRefs(refs []string) ([]string, error) {
	seen := make(map[string]struct{}, len(refs))
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		if !IsValidIdentifier(ref) {
			return nil, invalidf("invalid identifier %q in reference set", ref)
		}
		if IsBlank(ref) {
			return nil, invalidf("blank identifier %q must not be referenced", ref)
		}
		if _, ok := seen[ref]; ok {
			continue
		}
		seen[ref] = struct{}{}
		out = append(out, ref)
	}
	return out, nil
}

// AddVariableUnit registers one variable initialization unit: all
// variables on the left are initialized together, and refs are the
// identifiers directly mentioned by the initialization expression.
//
// On success it returns the zero based source-order index of the unit.
// A rejected registration occupies no source position and changes no
// state. Invalid arguments take priority over redeclaration errors.
func (s *Session) AddVariableUnit(variables []string, refs []string) (int, error) {
	if len(variables) == 0 {
		return -1, invalidf("variable unit has an empty left-hand side")
	}
	blankCount := 0
	for _, v := range variables {
		if !IsValidIdentifier(v) {
			return -1, invalidf("invalid variable identifier %q", v)
		}
		if IsBlank(v) {
			blankCount++
		}
	}
	uniqRefs, err := validateRefs(refs)
	if err != nil {
		return -1, err
	}

	// All argument validation is finished before the lock and before any
	// redeclaration check, so invalid arguments always win regardless of
	// whether a name also conflicts.
	s.mu.Lock()
	defer s.mu.Unlock()

	prospective := len(s.units)
	locals := make(map[string]struct{}, len(variables))
	for _, v := range variables {
		if IsBlank(v) {
			continue
		}
		if _, ok := locals[v]; ok {
			return prospective, redeclaredf(v, prospective, "")
		}
		locals[v] = struct{}{}
		if _, ok := s.names[v]; ok {
			return prospective, redeclaredf(v, prospective, "")
		}
		if _, ok := s.predeclared[v]; ok {
			return prospective, redeclaredf(v, prospective, "")
		}
	}

	u := &unit{
		index:     prospective,
		vars:      append([]string(nil), variables...),
		refs:      uniqRefs,
		blankVars: blankCount,
	}
	for _, v := range variables {
		if IsBlank(v) {
			continue
		}
		s.names[v] = nameInfo{kind: kindVariable, unitIndex: prospective}
	}
	s.units = append(s.units, u)
	s.decls = append(s.decls, decl{kind: declUnit, index: prospective})
	s.totalRefs += len(uniqRefs)
	return prospective, nil
}

// AddFunction registers a function declaration. Functions need no
// initialization, share the variable namespace, and must not be named
// "_".
func (s *Session) AddFunction(name string, refs []string) error {
	if !IsValidIdentifier(name) {
		return invalidf("invalid function name %q", name)
	}
	if IsBlank(name) {
		return invalidf("function name must not be the blank identifier %q", name)
	}
	uniqRefs, err := validateRefs(refs)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.names[name]; ok {
		return redeclaredf(name, -1, name)
	}
	if _, ok := s.predeclared[name]; ok {
		return redeclaredf(name, -1, name)
	}
	f := &fn{
		index: len(s.functions),
		name:  name,
		refs:  uniqRefs,
	}
	s.names[name] = nameInfo{kind: kindFunction, funcIndex: f.index}
	s.functions = append(s.functions, f)
	s.decls = append(s.decls, decl{kind: declFunction, index: f.index})
	s.totalRefs += len(uniqRefs)
	return nil
}

// Solve computes the deterministic initialization order together with
// each unit's transitive variable dependencies and work statistics.
// Solve does not modify the session.
func (s *Session) Solve() (*Result, Stats, error) {
	s.mu.RLock()
	snap := s.snapshotLocked()
	s.mu.RUnlock()
	return snap.solve()
}
