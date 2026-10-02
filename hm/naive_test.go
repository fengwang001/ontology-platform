package hm

// naive is a deliberately simple reference implementation of the same
// specification: it uses an explicit substitution map, recomputes level
// adjustments over the whole term each time, and makes every mutating
// operation atomic by restoring a full state snapshot on failure.

type nqvar struct{ idx int }

func (nqvar) isType() {}

type npat struct {
	quant []int
	body  Type
}

type naive struct {
	ctor  map[string]int
	v     int
	l     int
	next  int
	level map[int]int
	sub   map[int]Type // explicit, never compressed
	env   map[string]npat
	order []string
}

func newNaive(ctors map[string]int, v int) *naive {
	return &naive{
		ctor:  cloneCtor(ctors),
		v:     v,
		level: map[int]int{},
		sub:   map[int]Type{},
		env:   map[string]npat{},
	}
}

func cloneCtor(m map[string]int) map[string]int {
	out := make(map[string]int, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

type nsnapshot struct {
	l     int
	next  int
	level map[int]int
	sub   map[int]Type
	env   map[string]npat
	order []string
}

func (n *naive) snapshot() nsnapshot {
	levels := make(map[int]int, len(n.level))
	for k, v := range n.level {
		levels[k] = v
	}
	subs := make(map[int]Type, len(n.sub))
	for k, v := range n.sub {
		subs[k] = v
	}
	envs := make(map[string]npat, len(n.env))
	for k, v := range n.env {
		envs[k] = v
	}
	return nsnapshot{n.l, n.next, levels, subs, envs, append([]string(nil), n.order...)}
}

func (n *naive) restore(s nsnapshot) {
	// Copy on restore: otherwise the retained snapshot and live maps alias
	// backing arrays and grow multiplicatively as mutations copy buckets.
	levels := make(map[int]int, len(s.level))
	for k, v := range s.level {
		levels[k] = v
	}
	subs := make(map[int]Type, len(s.sub))
	for k, v := range s.sub {
		subs[k] = v
	}
	envs := make(map[string]npat, len(s.env))
	for k, v := range s.env {
		envs[k] = v
	}
	n.l = s.l
	n.next = s.next
	n.level = levels
	n.sub = subs
	n.env = envs
	n.order = append([]string(nil), s.order...)
}

func (n *naive) validate(t Type) bool {
	switch x := t.(type) {
	case nil:
		return false
	case Var:
		return x.ID >= 1 && x.ID <= n.next
	case Con:
		arity, ok := n.ctor[x.Name]
		if !ok || arity != len(x.Args) {
			return false
		}
		for _, a := range x.Args {
			if !n.validate(a) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func (n *naive) newVar() (int, error) {
	if n.next >= n.v {
		return 0, ErrVariableLimit
	}
	n.next++
	n.level[n.next] = n.l
	return n.next, nil
}

func (n *naive) enter() error {
	if n.l >= maxLevel {
		return ErrLevelLimit
	}
	n.l++
	return nil
}

func (n *naive) leave() error {
	if n.l == 0 {
		return ErrLevelUnderflow
	}
	n.l--
	return nil
}

// prune follows explicit substitutions one step at a time.
func (n *naive) prune(t Type) Type {
	for {
		v, ok := t.(Var)
		if !ok {
			return t
		}
		b, ok := n.sub[v.ID]
		if !ok {
			return v
		}
		t = b
	}
}

func (n *naive) resolve(t Type) Type {
	switch x := n.prune(t).(type) {
	case Var:
		return x
	case Con:
		args := make([]Type, len(x.Args))
		for i, a := range x.Args {
			args[i] = n.resolve(a)
		}
		return Con{Name: x.Name, Args: args}
	}
	return nil
}

func (n *naive) levelOf(id int) (int, error) {
	if id < 1 || id > n.next {
		return 0, ErrInvalidArgument
	}
	t := n.prune(Var{id})
	if _, ok := t.(Var); !ok {
		return 0, ErrVariableBound
	}
	return n.level[t.(Var).ID], nil
}

func (n *naive) unify(a, b Type) error {
	save := n.snapshot()
	// Validate both arguments completely, left first, before unifying.
	if !n.validate(a) || !n.validate(b) {
		n.restore(save)
		return ErrInvalidArgument
	}
	if err := n.unifyRec(a, b); err != nil {
		n.restore(save)
		return err
	}
	return nil
}

func (n *naive) occurs(x int, t Type) bool {
	return n.occursSeen(x, t, map[int]bool{})
}

func (n *naive) occursSeen(x int, t Type, seen map[int]bool) bool {
	cur := t
	for {
		v, ok := cur.(Var)
		if !ok {
			for _, a := range cur.(Con).Args {
				if n.occursSeen(x, a, seen) {
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
		b, bound := n.sub[v.ID]
		if !bound {
			return false
		}
		cur = b
	}
}

// lowerAll recomputes level adjustment over the whole term from scratch.
func (n *naive) lowerAll(t Type, lvl int, seen map[int]bool) {
	cur := t
	for {
		cv, isVar := cur.(Var)
		if !isVar {
			for _, a := range cur.(Con).Args {
				n.lowerAll(a, lvl, seen)
			}
			return
		}
		if !seen[cv.ID] {
			seen[cv.ID] = true
			if n.level[cv.ID] > lvl {
				n.level[cv.ID] = lvl
			}
			b, bound := n.sub[cv.ID]
			if !bound {
				return
			}
			cur = b
			continue
		}
		return
	}
}

func (n *naive) unifyRec(a, b Type) error {
	pa := n.prune(a)
	pb := n.prune(b)
	va, aVar := pa.(Var)
	vb, bVar := pb.(Var)
	if aVar && bVar && va.ID == vb.ID {
		n.unifyChainLevels(a, b)
		return nil
	}
	switch {
	case aVar:
		n.unifyChainLevels(a, b)
		return n.bindVar(va.ID, pb)
	case bVar:
		n.unifyChainLevels(b, a)
		return n.bindVar(vb.ID, pa)
	default:
		ca, cb := pa.(Con), pb.(Con)
		if ca.Name != cb.Name {
			return ErrConstructorMismatch
		}
		for i := range ca.Args {
			if err := n.unifyRec(ca.Args[i], cb.Args[i]); err != nil {
				return err
			}
		}
		return nil
	}
}

func (n *naive) unifyChainLevels(a, b Type) {
	reachable := func(t Type) (map[int]bool, int) {
		set := map[int]bool{}
		min := 1 << 30
		var stack []int
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
			if n.level[id] < min {
				min = n.level[id]
			}
			if next, isVar := n.sub[id].(Var); isVar && !set[next.ID] {
				stack = append(stack, next.ID)
			}
			for other, bnd := range n.sub {
				if rv, ok := bnd.(Var); ok && rv.ID == id && !set[other] {
					stack = append(stack, other)
				}
			}
		}
		return set, min
	}
	sa, ma := reachable(a)
	sb, mb := reachable(b)
	minLevel := ma
	if mb < minLevel {
		minLevel = mb
	}
	for _, set := range []map[int]bool{sa, sb} {
		for id := range set {
			if n.level[id] > minLevel {
				n.level[id] = minLevel
			}
		}
	}
}

func (n *naive) bindVar(x int, t Type) error {
	if v, ok := t.(Var); ok {
		n.sub[x] = v
		return nil
	}
	if n.occurs(x, t) {
		return ErrOccursCheck
	}
	n.lowerAll(t, n.level[x], map[int]bool{})
	n.sub[x] = t
	return nil
}

func (n *naive) bind(name string, t Type, expensive bool) error {
	save := n.snapshot()
	if !validName(name) || !n.validate(t) {
		return ErrInvalidArgument
	}
	if _, ok := n.env[name]; ok {
		return ErrNameExists
	}
	if len(n.env) >= maxEnv {
		return ErrEnvironmentFull
	}
	r := n.resolve(t)
	quant := n.generalize(r)
	var body Type
	if expensive {
		n.lowerTermAliases(t, n.l)
		body = cloneType(r)
	} else {
		idx := map[int]int{}
		for i, id := range quant {
			idx[id] = i
		}
		body = n.abstract(r, idx)
	}
	n.env[name] = npat{quant: append([]int(nil), quant...), body: body}
	n.order = append(n.order, name)
	_ = save
	return nil
}

func (n *naive) lowerTermAliases(t Type, lvl int) {
	seen := map[int]bool{}
	var walk func(Type)
	walk = func(ty Type) {
		switch x := ty.(type) {
		case Var:
			if seen[x.ID] {
				return
			}
			seen[x.ID] = true
			if n.level[x.ID] > lvl {
				n.level[x.ID] = lvl
			}
			if b, ok := n.sub[x.ID]; ok {
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

func (n *naive) generalize(r Type) []int {
	var quant []int
	seen := map[int]bool{}
	var walk func(Type)
	walk = func(t Type) {
		switch x := t.(type) {
		case Var:
			if !seen[x.ID] && n.level[x.ID] > n.l {
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

func (n *naive) abstract(t Type, idx map[int]int) Type {
	switch x := t.(type) {
	case Var:
		if i, ok := idx[x.ID]; ok {
			return nqvar{idx: i}
		}
		return x
	case Con:
		args := make([]Type, len(x.Args))
		for i, a := range x.Args {
			args[i] = n.abstract(a, idx)
		}
		return Con{Name: x.Name, Args: args}
	default:
		return nil
	}
}

func (n *naive) instantiate(p npat, first int) Type {
	var walk func(Type) Type
	walk = func(t Type) Type {
		switch x := t.(type) {
		case nqvar:
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
	return walk(p.body)
}

func (n *naive) lookup(name string) (Type, error) {
	p, ok := n.env[name]
	if !ok {
		return nil, ErrNameNotFound
	}
	if n.next+len(p.quant) > n.v {
		return nil, ErrVariableLimit
	}
	first := n.next + 1
	for range p.quant {
		n.next++
		n.level[n.next] = n.l
	}
	return n.instantiate(p, first), nil
}
