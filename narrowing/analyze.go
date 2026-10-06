package narrowing

import "math"

// env is an immutable flow environment: variable name to narrowed type.
// A nil *env is the unreachable environment (some variable narrowed to
// never); it never participates in joins.
type env struct {
	vars map[string]Type
}

// setVar returns a new environment with one variable updated, or nil if
// the variable is narrowed to never.
func setVar(e *env, name string, typ Type) *env {
	if typ.IsNever() {
		return nil
	}
	next := make(map[string]Type, len(e.vars))
	for k, v := range e.vars {
		next[k] = v
	}
	next[name] = typ
	return &env{vars: next}
}

// unionEnv joins two environments variable-wise; unreachable
// environments do not participate.
func unionEnv(a, b *env) *env {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	next := make(map[string]Type, len(a.vars))
	for k, v := range a.vars {
		next[k] = UnionOf(v, b.vars[k])
	}
	return &env{vars: next}
}

type analyzer struct {
	declared   map[string]Type
	order      map[StatementID]int
	assignType map[StatementID]Type
	errs       []*AnalysisError
	seq        int
	pre        map[StatementID]map[string]Type
	post       map[StatementID]map[string]Type
}

// Analyze runs the flow-sensitive narrowing analysis over a structured
// program. On any error the whole analysis fails and the classified
// highest-priority error is returned; otherwise an immutable, freely
// shareable Result is produced.
func Analyze(decls []Decl, body []Stmt) (*Result, error) {
	a := &analyzer{
		declared:   make(map[string]Type, len(decls)),
		order:      make(map[StatementID]int),
		assignType: make(map[StatementID]Type),
		pre:        make(map[StatementID]map[string]Type),
		post:       make(map[StatementID]map[string]Type),
	}
	a.validateDecls(decls)
	a.validateBlock(body)
	if len(a.errs) > 0 {
		// Invalid-argument errors dominate every other class, so the
		// flow analysis can be skipped entirely.
		return nil, a.best()
	}

	initial := &env{vars: make(map[string]Type, len(a.declared))}
	for name, typ := range a.declared {
		initial.vars[name] = typ
	}
	a.execBlock(body, initial)
	if len(a.errs) > 0 {
		return nil, a.best()
	}
	return &Result{
		declared: a.declared,
		pre:      a.pre,
		post:     a.post,
	}, nil
}

// best returns the highest-priority collected error.
func (a *analyzer) best() *AnalysisError {
	best := a.errs[0]
	for _, e := range a.errs[1:] {
		if errLess(e, best) {
			best = e
		}
	}
	return best
}

func (a *analyzer) report(class ErrorClass, id StatementID, varName, msg string) {
	a.seq++
	a.errs = append(a.errs, &AnalysisError{
		Class:  class,
		StmtID: id,
		Var:    varName,
		Msg:    msg,
		order:  a.order[id],
		seq:    a.seq,
	})
}

// validateDecls checks declarations: unique names and well-formed types.
func (a *analyzer) validateDecls(decls []Decl) {
	for _, d := range decls {
		if _, dup := a.declared[d.Name]; dup {
			a.report(ErrInvalidArg, "", d.Name, "duplicate variable declaration")
			continue
		}
		typ, err := d.Type.normalize()
		if err != nil {
			a.report(ErrInvalidArg, "", d.Name, "malformed declared type: "+err.(*AnalysisError).Msg)
			continue
		}
		a.declared[d.Name] = typ
	}
}

// validateBlock statically checks a statement list: unique statement
// identifiers, declared variable references and well-formed types. It
// also assigns pre-order program positions used for error tie-breaking.
func (a *analyzer) validateBlock(stmts []Stmt) {
	for _, s := range stmts {
		if _, dup := a.order[s.StmtID()]; dup {
			a.report(ErrInvalidArg, s.StmtID(), "", "duplicate statement identifier")
		}
		a.order[s.StmtID()] = len(a.order)
		switch s := s.(type) {
		case *Assign:
			if _, ok := a.declared[s.Var]; !ok {
				a.report(ErrInvalidArg, s.ID, s.Var, "assignment to undeclared variable")
			}
			typ, err := s.Type.normalize()
			if err != nil {
				a.report(ErrInvalidArg, s.ID, s.Var, "malformed assigned type: "+err.(*AnalysisError).Msg)
			} else {
				a.assignType[s.ID] = typ
			}
		case *If:
			a.validateCond(s.Cond, s.ID)
			a.validateBlock(s.Then)
			a.validateBlock(s.Else)
		case *Return:
		}
	}
}

func (a *analyzer) validateCond(c Cond, id StatementID) {
	checkVar := func(name string) {
		if _, ok := a.declared[name]; !ok {
			a.report(ErrInvalidArg, id, name, "condition references undeclared variable")
		}
	}
	checkLit := func(lit Literal) {
		if lit.Kind == LitNumber && math.IsNaN(lit.Num) {
			a.report(ErrInvalidArg, id, "", "NaN literal in condition")
		}
	}
	switch c := c.(type) {
	case TypeOf:
		checkVar(c.Var)
	case EqNull:
		checkVar(c.Var)
	case EqUndefined:
		checkVar(c.Var)
	case EqLiteral:
		checkVar(c.Var)
		checkLit(c.Lit)
	case LooseEqNull:
		checkVar(c.Var)
	case Truthy:
		checkVar(c.Var)
	case PropEq:
		checkVar(c.Var)
		checkLit(c.Lit)
	case Not:
		a.validateCond(c.C, id)
	case And:
		a.validateCond(c.L, id)
		a.validateCond(c.R, id)
	case Or:
		a.validateCond(c.L, id)
		a.validateCond(c.R, id)
	}
}

// execBlock executes a statement list and records pre/post environments
// for every statement. It returns the fall-through environment, or nil
// when every path terminated or became unreachable.
func (a *analyzer) execBlock(stmts []Stmt, e *env) *env {
	for _, s := range stmts {
		a.pre[s.StmtID()] = snapshot(e)
		e = a.execStmt(s, e)
		a.post[s.StmtID()] = snapshot(e)
	}
	return e
}

func snapshot(e *env) map[string]Type {
	if e == nil {
		return nil
	}
	return e.vars
}

func (a *analyzer) execStmt(s Stmt, e *env) *env {
	switch s := s.(type) {
	case *Assign:
		if e == nil {
			return nil // unreachable: no flow-dependent checks
		}
		typ := a.assignType[s.ID]
		decl := a.declared[s.Var]
		if !decl.ContainsType(typ) {
			a.report(ErrNotAssignable, s.ID, s.Var,
				"type "+typ.String()+" is not assignable to declared type "+decl.String())
			return e
		}
		return setVar(e, s.Var, typ)
	case *Return:
		return nil
	case *If:
		// Note: even when e is nil (unreachable) the branches are
		// executed so that every nested program point is recorded as
		// unreachable; evalCond propagates nil environments.
		tEnv, fEnv := a.evalCond(s.Cond, e, s.ID)
		thenOut := a.execBlock(s.Then, tEnv)
		elseOut := a.execBlock(s.Else, fEnv)
		return unionEnv(thenOut, elseOut)
	}
	return e
}

// evalCond splits an environment into the true and false environments
// of a condition.
func (a *analyzer) evalCond(c Cond, e *env, id StatementID) (*env, *env) {
	if e == nil {
		return nil, nil
	}
	switch c := c.(type) {
	case Not:
		t, f := a.evalCond(c.C, e, id)
		return f, t
	case And:
		lt, lf := a.evalCond(c.L, e, id)
		rt, rf := a.evalCond(c.R, lt, id)
		return rt, unionEnv(lf, rf)
	case Or:
		lt, lf := a.evalCond(c.L, e, id)
		rt, rf := a.evalCond(c.R, lf, id)
		return unionEnv(lt, rt), rf
	}
	t, f := a.evalLeaf(c, e, id)
	return t, f
}

// evalLeaf evaluates a non-composite condition on one variable.
func (a *analyzer) evalLeaf(c Cond, e *env, id StatementID) (*env, *env) {
	var varName string
	switch c := c.(type) {
	case TypeOf:
		varName = c.Var
	case EqNull:
		varName = c.Var
	case EqUndefined:
		varName = c.Var
	case EqLiteral:
		varName = c.Var
	case LooseEqNull:
		varName = c.Var
	case Truthy:
		varName = c.Var
	case PropEq:
		varName = c.Var
	}
	current := e.vars[varName]
	var trueMembers, falseMembers []Member

	split := func(match func(Member) bool) {
		for _, m := range current.Members() {
			if match(m) {
				trueMembers = append(trueMembers, m)
			} else {
				falseMembers = append(falseMembers, m)
			}
		}
	}

	switch c := c.(type) {
	case TypeOf:
		split(func(m Member) bool { return matchesTypeOf(m, c.Kind) })
	case EqNull:
		split(func(m Member) bool { return m.kind == KindNull })
	case EqUndefined:
		split(func(m Member) bool { return m.kind == KindUndefined })
	case LooseEqNull:
		split(func(m Member) bool { return m.kind == KindNull || m.kind == KindUndefined })
	case EqLiteral:
		lit := literalMember(c.Lit)
		for _, m := range current.Members() {
			if m.key == lit.key {
				trueMembers = append(trueMembers, m)
				continue // false branch drops exactly this literal member
			}
			falseMembers = append(falseMembers, m)
			if atomicOfLiteral(m, lit) {
				// The atomic member narrows to the tested literal.
				trueMembers = append(trueMembers, lit)
			}
		}
	case Truthy:
		for _, m := range current.Members() {
			t, f := truthySplit(m)
			if t != nil {
				trueMembers = append(trueMembers, *t)
			}
			if f != nil {
				falseMembers = append(falseMembers, *f)
			}
		}
	case PropEq:
		lit := literalMember(c.Lit)
		ok := true
		for _, m := range current.Members() {
			if m.kind != KindObject {
				a.report(ErrPropNotAccessible, id, varName,
					"property "+c.Prop+" accessed on non-object member "+m.String())
				ok = false
				break
			}
			if _, has := m.props[c.Prop]; !has {
				a.report(ErrMissingDiscriminant, id, varName,
					"object member "+m.String()+" lacks discriminant property "+c.Prop)
				ok = false
				break
			}
		}
		if !ok {
			return e, e // analysis will fail; keep collecting errors
		}
		litType := fromMembers([]Member{lit})
		for _, m := range current.Members() {
			propType := m.props[c.Prop]
			if propType.ContainsMember(lit) {
				trueMembers = append(trueMembers, m)
			}
			if !propType.Equal(litType) {
				falseMembers = append(falseMembers, m)
			}
		}
	}
	return setVar(e, varName, fromMembers(trueMembers)),
		setVar(e, varName, fromMembers(falseMembers))
}

func matchesTypeOf(m Member, kind TypeOfKind) bool {
	switch kind {
	case TypeOfNumber:
		return m.kind == KindNumber || m.kind == KindNumberLiteral
	case TypeOfString:
		return m.kind == KindString || m.kind == KindStringLiteral
	case TypeOfBoolean:
		return m.kind == KindBooleanLiteral
	case TypeOfObject:
		return m.kind == KindObject || m.kind == KindNull
	case TypeOfUndefined:
		return m.kind == KindUndefined
	}
	return false
}

// literalMember converts a condition literal into a union member.
func literalMember(lit Literal) Member {
	switch lit.Kind {
	case LitNumber:
		return NumberLiteral(lit.Num).Members()[0]
	case LitString:
		return StringLiteral(lit.Str).Members()[0]
	case LitBoolean:
		return BooleanLiteral(lit.Bool).Members()[0]
	}
	return Member{}
}

// atomicOfLiteral reports whether m is the atomic member corresponding
// to the literal's class (number or string; booleans have no atomic
// member beyond the two literals).
func atomicOfLiteral(m Member, lit Member) bool {
	switch lit.kind {
	case KindNumberLiteral:
		return m.kind == KindNumber
	case KindStringLiteral:
		return m.kind == KindString
	}
	return false
}

// truthySplit computes the true-env and false-env contribution of one
// member under a truthiness test. A nil result means the member is
// absent from that environment.
func truthySplit(m Member) (t, f *Member) {
	zero := NumberLiteral(0).Members()[0]
	empty := StringLiteral("").Members()[0]
	switch m.kind {
	case KindNull, KindUndefined:
		return nil, &m
	case KindBooleanLiteral:
		if m.flag {
			return &m, nil
		}
		return nil, &m
	case KindNumber:
		return &m, &zero // atomic number narrows to the zero literal when falsy
	case KindString:
		return &m, &empty // atomic string narrows to the empty literal when falsy
	case KindNumberLiteral:
		if m.num == 0 {
			return nil, &m
		}
		return &m, nil
	case KindStringLiteral:
		if m.str == "" {
			return nil, &m
		}
		return &m, nil
	case KindObject:
		return &m, nil
	}
	return nil, nil
}
