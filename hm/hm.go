// Package hm implements a Hindley–Milner unification session with
// level-based generalization and the value restriction.
package hm

import (
	"errors"
	"sync"
)

// Type is a monomorphic type: either a variable or a constructor application.
type Type interface{ isType() }

// Var is a type variable identified by its session-global number.
type Var struct{ ID int }

// Con is a constructor application, e.g. Fn(a, b).
type Con struct {
	Name string
	Args []Type
}

func (Var) isType() {}
func (Con) isType() {}

// Pattern is a quantified type scheme. Quant[i] names the quantified
// variable Gi that appears in Body; an unquantified variable is Body.Var.
type Pattern struct {
	Quant []int
	Body  Type
}

// qvar is an internal pattern node naming quantified variable Gi.
type qvar struct{ idx int }

func (qvar) isType() {}

// Distinguishable rejection reasons.
var (
	ErrInvalidArgument     = errors.New("hm: invalid argument")
	ErrLevelUnderflow      = errors.New("hm: level already zero")
	ErrVariableLimit       = errors.New("hm: variable limit reached")
	ErrLevelLimit          = errors.New("hm: level limit reached")
	ErrEnvironmentFull     = errors.New("hm: pattern environment full")
	ErrNameExists          = errors.New("hm: name already bound")
	ErrNameNotFound        = errors.New("hm: name not found")
	ErrVariableBound       = errors.New("hm: variable bound to constructor")
	ErrOccursCheck         = errors.New("hm: occurs check failed")
	ErrConstructorMismatch = errors.New("hm: constructor mismatch")
)

const (
	maxLevel = 1000
	maxEnv   = 1000
)

// Session is a concurrent-safe HM inference session.
type Session struct {
	mu   sync.Mutex
	ctor map[string]int
	v    int
	l    int
	next int
	// level[i] is the current level of variable i (1-based).
	level []int
	// bind[i] is nil when variable i is unbound, otherwise its binding target.
	bind []Type
	// env holds patterns keyed by name, preserving insertion order.
	env      map[string]Pattern
	envOrder []string
}

// New creates a session from a name-to-arity constructor table and a
// variable cap v.
func New(ctors map[string]int, v int) (*Session, error) {
	if len(ctors) < 1 || len(ctors) > 16 || v < 1 || v > 1_000_000 {
		return nil, ErrInvalidArgument
	}
	for name, arity := range ctors {
		if !validName(name) || arity < 0 || arity > 4 {
			return nil, ErrInvalidArgument
		}
	}
	table := make(map[string]int, len(ctors))
	for name, arity := range ctors {
		table[name] = arity
	}
	return &Session{
		ctor: table,
		v:    v,
		env:  map[string]Pattern{},
	}, nil
}

// NewVar allocates a fresh variable at the current level.
func (s *Session) NewVar() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.next >= s.v {
		return 0, ErrVariableLimit
	}
	s.next++
	s.level = append(s.level, s.l)
	s.bind = append(s.bind, nil)
	return s.next, nil
}

// Enter raises the current level.
func (s *Session) Enter() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.l >= maxLevel {
		return ErrLevelLimit
	}
	s.l++
	return nil
}

// Leave lowers the current level.
func (s *Session) Leave() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.l == 0 {
		return ErrLevelUnderflow
	}
	s.l--
	return nil
}

// Level reports the level of a variable after pruning.
func (s *Session) Level(id int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id < 1 || id > s.next {
		return 0, ErrInvalidArgument
	}
	t := s.prune(Var{id})
	if _, ok := t.(Var); !ok {
		return 0, ErrVariableBound
	}
	return s.level[id-1], nil
}

// Resolve returns the fully resolved form of t.
func (s *Session) Resolve(t Type) (Type, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.validateType(t); err != nil {
		return nil, err
	}
	return s.resolve(t), nil
}

// Unify unifies two types atomically.
func (s *Session) Unify(a, b Type) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Validate both arguments completely, left first, before touching state.
	if err := s.validateType(a); err != nil {
		return err
	}
	if err := s.validateType(b); err != nil {
		return err
	}
	j := &journal{}
	if err := s.unify(j, a, b); err != nil {
		j.rollback(s)
		return err
	}
	return nil
}

// Bind generalizes t under the current level and stores the pattern.
func (s *Session) Bind(name string, t Type, expensive bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Invalid arguments take priority over the duplicate/capacity checks.
	if !validName(name) {
		return ErrInvalidArgument
	}
	if err := s.validateType(t); err != nil {
		return err
	}
	if _, ok := s.env[name]; ok {
		return ErrNameExists
	}
	if len(s.env) >= maxEnv {
		return ErrEnvironmentFull
	}
	r := s.resolve(t)
	quant := generalizeOrder(r, s.l, s.level)
	var body Type
	if expensive {
		// Value restriction: no quantification; widen every variable of the
		// original term (including alias chains) to L.
		s.lowerTermAliases(t, s.l)
		body = cloneType(r)
	} else {
		index := make(map[int]int, len(quant))
		for i, id := range quant {
			index[id] = i
		}
		body = abstractType(r, index)
	}
	p := Pattern{Quant: append([]int(nil), quant...), Body: body}
	s.env[name] = p
	s.envOrder = append(s.envOrder, name)
	return nil
}

// Lookup instantiates a stored pattern.
func (s *Session) Lookup(name string) (Type, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.env[name]
	if !ok {
		return nil, ErrNameNotFound
	}
	if s.next+len(p.Quant) > s.v {
		return nil, ErrVariableLimit
	}
	first := s.next + 1
	for range p.Quant {
		s.next++
		s.level = append(s.level, s.l)
		s.bind = append(s.bind, nil)
	}
	return instantiate(p, first), nil
}

// ---- internals ---------------------------------------------------------

func validName(name string) bool {
	n := len(name)
	return n >= 1 && n <= 32
}

// validateType checks every constructor name/arity and every variable id.
func (s *Session) validateType(t Type) error {
	switch x := t.(type) {
	case Var:
		if x.ID < 1 || x.ID > s.next {
			return ErrInvalidArgument
		}
		return nil
	case Con:
		arity, ok := s.ctor[x.Name]
		if !ok || arity != len(x.Args) {
			return ErrInvalidArgument
		}
		for _, a := range x.Args {
			if a == nil {
				return ErrInvalidArgument
			}
			if err := s.validateType(a); err != nil {
				return err
			}
		}
		return nil
	default:
		return ErrInvalidArgument
	}
}

// prune follows the binding chain to an unbound variable or a Con.
func (s *Session) prune(t Type) Type {
	for {
		v, ok := t.(Var)
		if !ok {
			return t
		}
		b := s.bind[v.ID-1]
		if b == nil {
			return v
		}
		t = b
	}
}

// resolve returns the fully substituted form with all bound variables gone.
func (s *Session) resolve(t Type) Type {
	switch x := s.prune(t).(type) {
	case Var:
		return x
	case Con:
		args := make([]Type, len(x.Args))
		for i, a := range x.Args {
			args[i] = s.resolve(a)
		}
		return Con{Name: x.Name, Args: args}
	}
	return nil
}

// occurs reports whether variable x appears anywhere inside the pruned t.
func (s *Session) occurs(x int, t Type, seen map[int]bool) bool {
	cur := t
	for {
		v, ok := cur.(Var)
		if !ok {
			for _, a := range cur.(Con).Args {
				if s.occurs(x, a, seen) {
					return true
				}
			}
			return false
		}
		if v.ID == x {
			return true
		}
		if seen[v.ID] {
			return false
		}
		seen[v.ID] = true
		b := s.bind[v.ID-1]
		if b == nil {
			return false
		}
		cur = b
	}
}

// lowerIn lowers, to lvl, every unbound variable inside the pruned Con t
// whose level exceeds lvl, recording each change once.
func (s *Session) lowerIn(j *journal, t Type, lvl int, seen map[int]bool) {
	// Walk the term via direct bindings: every variable on a chain (including
	// chain aliases that continue into a constructor) is lowered, and a
	// constructor reached at the chain end is traversed.
	cur := t
	for {
		cv, isVar := cur.(Var)
		if !isVar {
			for _, a := range cur.(Con).Args {
				s.lowerIn(j, a, lvl, seen)
			}
			return
		}
		if !seen[cv.ID] {
			seen[cv.ID] = true
			if s.level[cv.ID-1] > lvl {
				j.levelChange(s, cv.ID, s.level[cv.ID-1])
				s.level[cv.ID-1] = lvl
			}
			b := s.bind[cv.ID-1]
			if b == nil {
				return
			}
			cur = b
			continue
		}
		return
	}
}

type changeKind int

const (
	chBind changeKind = iota
	chLevel
)

type change struct {
	kind changeKind
	id   int
	old  int // old level for chLevel
}

// lowerTermAliases lowers to lvl every variable reachable from t through
// variable bindings, including alias variables that remain observable.
func (s *Session) lowerTermAliases(t Type, lvl int) {
	seen := map[int]bool{}
	var walk func(Type)
	walk = func(ty Type) {
		switch x := ty.(type) {
		case Var:
			if seen[x.ID] {
				return
			}
			seen[x.ID] = true
			if s.level[x.ID-1] > lvl {
				s.level[x.ID-1] = lvl
			}
			if b := s.bind[x.ID-1]; b != nil {
				walk(b)
			}
		case Con:
			for _, a := range x.Args {
				walk(a)
			}
		}
	}
	walk(t)
}

type journal struct {
	changes []change
}

func (j *journal) bindVar(s *Session, id int) {
	j.changes = append(j.changes, change{kind: chBind, id: id})
}

func (j *journal) levelChange(s *Session, id, old int) {
	j.changes = append(j.changes, change{kind: chLevel, id: id, old: old})
}

func (j *journal) rollback(s *Session) {
	for i := len(j.changes) - 1; i >= 0; i-- {
		c := j.changes[i]
		switch c.kind {
		case chBind:
			s.bind[c.id-1] = nil
		case chLevel:
			s.level[c.id-1] = c.old
		}
	}
}

func (s *Session) unify(j *journal, a, b Type) error {
	pa := s.prune(a)
	pb := s.prune(b)
	va, aVar := pa.(Var)
	vb, bVar := pb.(Var)
	if aVar && bVar && va.ID == vb.ID {
		// Already one variable: unify the minimum levels reachable through
		// both operand chains (a later alias may carry a smaller level).
		s.unifyChainLevels(j, a, b)
		return nil
	}
	switch {
	case aVar:
		s.unifyChainLevels(j, a, b)
		return s.bindTo(j, va.ID, pb)
	case bVar:
		s.unifyChainLevels(j, b, a)
		return s.bindTo(j, vb.ID, pa)
	default:
		ca := pa.(Con)
		cb := pb.(Con)
		if ca.Name != cb.Name {
			return ErrConstructorMismatch
		}
		for i := range ca.Args {
			if err := s.unify(j, ca.Args[i], cb.Args[i]); err != nil {
				return err
			}
		}
		return nil
	}
}

// unifyChainLevels makes every unbound variable reachable from either
// operand through variable-variable bindings share the minimum level found
// on either reachable set. Changes are journaled for atomic rollback.
func (s *Session) unifyChainLevels(j *journal, a, b Type) {
	// Collect the bidirectional equivalence closure: a variable bound to
	// another shares that variable's level, so the minimum propagates across
	// the whole merged component.
	reachable := func(t Type) (map[int]bool, int) {
		set := map[int]bool{}
		min := 1 << 30
		stack := []int{}
		if v, ok := t.(Var); ok {
			stack = append(stack, v.ID)
		}
		for len(stack) > 0 {
			id := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if set[id] {
				continue
			}
			set[id] = true
			if s.level[id-1] < min {
				min = s.level[id-1]
			}
			if next, isVar := s.bind[id-1].(Var); isVar && !set[next.ID] {
				stack = append(stack, next.ID)
			}
			for other, bnd := range s.bind {
				if rv, ok := bnd.(Var); ok && rv.ID == id && !set[other+1] {
					stack = append(stack, other+1)
				}
			}
		}
		return set, min
	}
	sa, ma := reachable(a)
	sb, mb := reachable(b)
	min := ma
	if mb < min {
		min = mb
	}
	lower := func(set map[int]bool) {
		for id := range set {
			if s.level[id-1] > min {
				j.levelChange(s, id, s.level[id-1])
				s.level[id-1] = min
			}
		}
	}
	lower(sa)
	lower(sb)
}

// bindTo binds the distinct unbound variable x to the pruned term t.
// boundOperand is the original side carrying x; operand is the other side.
// bindTo binds the distinct unbound variable x to the pruned term t.
func (s *Session) bindTo(j *journal, x int, t Type) error {
	if v, ok := t.(Var); ok {
		// Variable-variable: levels were aligned by unifyChainLevels.
		j.bindVar(s, x)
		s.bind[x-1] = v
		return nil
	}
	// Variable-constructor: occurs check first, then level lowering.
	if s.occurs(x, t, map[int]bool{}) {
		return ErrOccursCheck
	}
	s.lowerIn(j, t, s.level[x-1], map[int]bool{})
	j.bindVar(s, x)
	s.bind[x-1] = t
	return nil
}

// generalizeOrder lists unbound variables of the fully resolved r whose
// level exceeds l, by pre-order, left-to-right, first occurrence.
func generalizeOrder(r Type, l int, levels []int) []int {
	var quant []int
	seen := map[int]bool{}
	var walk func(Type)
	walk = func(t Type) {
		switch x := t.(type) {
		case Var:
			if !seen[x.ID] && levels[x.ID-1] > l {
				seen[x.ID] = true
				quant = append(quant, x.ID)
			}
		case Con:
			for _, a := range x.Args {
				walk(a)
			}
		}
	}
	walk(r)
	return quant
}

func cloneType(t Type) Type {
	switch x := t.(type) {
	case Var:
		return x
	case Con:
		args := make([]Type, len(x.Args))
		for i, a := range x.Args {
			args[i] = cloneType(a)
		}
		return Con{Name: x.Name, Args: args}
	default:
		return nil
	}
}

// abstractType replaces quantified variables (mapped in index) by qvar.
func abstractType(t Type, index map[int]int) Type {
	switch x := t.(type) {
	case Var:
		if i, ok := index[x.ID]; ok {
			return qvar{idx: i}
		}
		return x
	case Con:
		args := make([]Type, len(x.Args))
		for i, a := range x.Args {
			args[i] = abstractType(a, index)
		}
		return Con{Name: x.Name, Args: args}
	default:
		return nil
	}
}

func instantiate(p Pattern, first int) Type {
	var walk func(Type) Type
	walk = func(t Type) Type {
		switch x := t.(type) {
		case qvar:
			return Var{ID: first + x.idx}
		case Var:
			return x
		case Con:
			args := make([]Type, len(x.Args))
			for i, a := range x.Args {
				args[i] = walk(a)
			}
			return Con{Name: x.Name, Args: args}
		default:
			return nil
		}
	}
	return walk(p.Body)
}
