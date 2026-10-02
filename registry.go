package ontology

const (
	maxFunctions  = 256
	maxNameBytes  = 32
	maxParameters = 8
	maxVariables  = 32
	maxStatements = 64
	maxCallArgs   = 8
)

type function struct {
	name       string
	params     int
	variables  int
	statements []Statement
	siteIDs    []int
	summary    Summary
	sites      []AllocationSite
}

func validVariable(index, variables int) bool {
	return index >= 0 && index < variables
}

func validateStatements(statements []Statement, params, variables int) error {
	if len(statements) > maxStatements {
		return ErrInvalidArgument
	}

	for _, stmt := range statements {
		switch stmt.Kind {
		case NewStmt:
			if !validVariable(stmt.D, variables) {
				return ErrInvalidArgument
			}
		case CopyStmt, StoreStmt, LoadStmt:
			if !validVariable(stmt.D, variables) || !validVariable(stmt.S, variables) {
				return ErrInvalidArgument
			}
		case RetStmt, GlobalStmt:
			if !validVariable(stmt.S, variables) || stmt.D == -1 {
				return ErrInvalidArgument
			}
		case CallStmt:
			if stmt.D != -1 && !validVariable(stmt.D, variables) {
				return ErrInvalidArgument
			}
			if len(stmt.Args) > maxCallArgs {
				return ErrInvalidArgument
			}
			for _, arg := range stmt.Args {
				if !validVariable(arg, variables) {
					return ErrInvalidArgument
				}
			}
		default:
			return ErrInvalidArgument
		}

		if stmt.Kind != CallStmt && stmt.D == -1 {
			return ErrInvalidArgument
		}
	}

	_ = params
	return nil
}

func cloneStatements(statements []Statement) []Statement {
	cloned := make([]Statement, len(statements))
	for i, stmt := range statements {
		cloned[i] = stmt
		if stmt.Args != nil {
			cloned[i].Args = append([]int(nil), stmt.Args...)
		}
	}
	return cloned
}

func (r *Registry) Register(name string, k, v int, statements []Statement) error {
	if len(name) == 0 || len(name) > maxNameBytes || k < 0 || k > maxParameters ||
		v < k || v > maxVariables || len(statements) > maxStatements {
		return ErrInvalidArgument
	}
	if err := validateStatements(statements, k, v); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.functions[name]; exists {
		return ErrNameRegistered
	}
	if len(r.functions) >= maxFunctions {
		return ErrFunctionLimit
	}

	callees := map[string]*function{name: {params: k}}
	for _, stmt := range statements {
		if stmt.Kind != CallStmt {
			continue
		}
		callee := callees[stmt.G]
		if callee == nil && stmt.G != name {
			callee = r.functions[stmt.G]
		}
		if callee == nil {
			return ErrUnknownCallee
		}
		if len(stmt.Args) != callee.params {
			return ErrArgumentCount
		}
		callees[stmt.G] = callee
	}

	fn := &function{
		name:       name,
		params:     k,
		variables:  v,
		statements: cloneStatements(statements),
	}
	for _, stmt := range fn.statements {
		if stmt.Kind == NewStmt {
			r.siteCount++
			fn.siteIDs = append(fn.siteIDs, r.siteCount)
		}
	}

	fn.summary, fn.sites = analyzeFunction(fn, r.functions, r)
	r.functions[name] = fn
	r.order = append(r.order, fn)
	return nil
}

func (r *Registry) lookup(name string) (*function, error) {
	fn := r.functions[name]
	if fn == nil {
		return nil, ErrFunctionNotFound
	}
	return fn, nil
}

func (r *Registry) Summary(name string) (Summary, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	fn, err := r.lookup(name)
	if err != nil {
		return Summary{}, err
	}
	return cloneSummary(fn.summary), nil
}

func (r *Registry) Sites(name string) ([]AllocationSite, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	fn, err := r.lookup(name)
	if err != nil {
		return nil, err
	}
	return append([]AllocationSite(nil), fn.sites...), nil
}
