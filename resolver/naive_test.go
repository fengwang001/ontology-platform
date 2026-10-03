package resolver

// This file holds an independent, cache-less reference implementation of
// the resolution rules. It is written directly from the specification and
// shares no logic with the production resolver, so the randomized
// differential test can cross-check the two implementations.

type nInstance struct {
	id      int
	trait   string
	head    *Type
	context []Constraint
}

type nResolver struct {
	d     int
	insts []nInstance
}

func nValidName(s string) bool {
	return len(s) >= 1 && len(s) <= 32
}

func nValidType(t *Type) bool {
	if t == nil {
		return false
	}
	if t.IsVar {
		return t.Var >= 0 && t.Var <= 7
	}
	if !nValidName(t.Name) || len(t.Args) > 4 {
		return false
	}
	for _, a := range t.Args {
		if !nValidType(a) {
			return false
		}
	}
	return true
}

func nGround(t *Type) bool {
	if t.IsVar {
		return false
	}
	for _, a := range t.Args {
		if !nGround(a) {
			return false
		}
	}
	return true
}

func nDepth(t *Type) int {
	if t.IsVar || len(t.Args) == 0 {
		return 1
	}
	m := 0
	for _, a := range t.Args {
		if d := nDepth(a); d > m {
			m = d
		}
	}
	return m + 1
}

func nEqual(a, b *Type) bool {
	if a.IsVar != b.IsVar {
		return false
	}
	if a.IsVar {
		return a.Var == b.Var
	}
	if a.Name != b.Name || len(a.Args) != len(b.Args) {
		return false
	}
	for i := range a.Args {
		if !nEqual(a.Args[i], b.Args[i]) {
			return false
		}
	}
	return true
}

func nCollectVars(t *Type, set map[int]bool) {
	if t.IsVar {
		set[t.Var] = true
		return
	}
	for _, a := range t.Args {
		nCollectVars(a, set)
	}
}

// nNorm renumbers variables by preorder of first occurrence.
func nNorm(t *Type, m map[int]int) *Type {
	if t.IsVar {
		id, ok := m[t.Var]
		if !ok {
			id = len(m)
			m[t.Var] = id
		}
		return Var(id)
	}
	args := make([]*Type, len(t.Args))
	for i, a := range t.Args {
		args[i] = nNorm(a, m)
	}
	return Con(t.Name, args...)
}

func nMatchInto(pat, tgt *Type, subst map[int]*Type) bool {
	if pat.IsVar {
		if bound, ok := subst[pat.Var]; ok {
			return nEqual(bound, tgt)
		}
		subst[pat.Var] = tgt
		return true
	}
	if tgt.IsVar {
		return false
	}
	if pat.Name != tgt.Name || len(pat.Args) != len(tgt.Args) {
		return false
	}
	for i := range pat.Args {
		if !nMatchInto(pat.Args[i], tgt.Args[i], subst) {
			return false
		}
	}
	return true
}

func nMatch(pat, tgt *Type) (map[int]*Type, bool) {
	subst := map[int]*Type{}
	if !nMatchInto(pat, tgt, subst) {
		return nil, false
	}
	return subst, true
}

// nConstify turns variables into distinct constants, using an offset
// different from the production implementation.
func nConstify(t *Type) *Type {
	if t.IsVar {
		return Var(t.Var + 5000)
	}
	args := make([]*Type, len(t.Args))
	for i, a := range t.Args {
		args[i] = nConstify(a)
	}
	return Con(t.Name, args...)
}

func nMoreSpec(a, b *Type) bool {
	_, ok := nMatch(b, nConstify(a))
	return ok
}

func nApply(t *Type, subst map[int]*Type) *Type {
	if t.IsVar {
		if v, ok := subst[t.Var]; ok {
			return v
		}
		return t
	}
	args := make([]*Type, len(t.Args))
	for i, a := range t.Args {
		args[i] = nApply(a, subst)
	}
	return Con(t.Name, args...)
}

func (n *nResolver) add(trait string, head *Type, ctx []Constraint) (int, ErrorKind, bool) {
	if !nValidName(trait) || !nValidType(head) || len(ctx) > 4 {
		return 0, ErrInvalidParam, false
	}
	headVars := map[int]bool{}
	nCollectVars(head, headVars)
	for _, c := range ctx {
		if !nValidName(c.Trait) || !nValidType(c.Type) {
			return 0, ErrInvalidParam, false
		}
		ctxVars := map[int]bool{}
		nCollectVars(c.Type, ctxVars)
		for v := range ctxVars {
			if !headVars[v] {
				return 0, ErrInvalidParam, false
			}
		}
	}
	if len(n.insts) >= 200 {
		return 0, ErrInstanceLimit, false
	}
	nh := nNorm(head, map[int]int{})
	for _, in := range n.insts {
		if in.trait == trait && nEqual(nNorm(in.head, map[int]int{}), nh) {
			return 0, ErrDuplicateInstance, false
		}
	}
	id := len(n.insts) + 1
	n.insts = append(n.insts, nInstance{id: id, trait: trait, head: head, context: ctx})
	return id, 0, true
}

func (n *nResolver) resolve(trait string, ty *Type) (*Tree, *Failure, ErrorKind, bool) {
	if !nValidName(trait) || !nValidType(ty) || !nGround(ty) || nDepth(ty) > 16 {
		return nil, nil, ErrInvalidParam, false
	}
	tree, f := n.resolveGoal(trait, ty, 1, nil)
	return tree, f, 0, true
}

func (n *nResolver) resolveGoal(trait string, ty *Type, level int, path []string) (*Tree, *Failure) {
	key := trait + "|" + ty.String()
	for _, p := range path {
		if p == key {
			return nil, &Failure{Kind: FailCycle, Trait: trait, Type: ty.String()}
		}
	}
	if level > n.d {
		return nil, &Failure{Kind: FailDepthExceeded, Trait: trait, Type: ty.String()}
	}
	var cands []nInstance
	var subs []map[int]*Type
	for _, in := range n.insts {
		if in.trait != trait {
			continue
		}
		if s, ok := nMatch(in.head, ty); ok {
			cands = append(cands, in)
			subs = append(subs, s)
		}
	}
	if len(cands) == 0 {
		return nil, &Failure{Kind: FailNoInstance, Trait: trait, Type: ty.String()}
	}
	best := -1
	ambiguous := false
	for i := range cands {
		dominates := true
		for j := range cands {
			if i != j && !nMoreSpec(cands[i].head, cands[j].head) {
				dominates = false
				break
			}
		}
		if dominates {
			if best != -1 {
				ambiguous = true
				break
			}
			best = i
		}
	}
	if ambiguous || best == -1 {
		return nil, &Failure{Kind: FailAmbiguous, Trait: trait, Type: ty.String()}
	}
	sel := cands[best]
	tree := &Tree{Trait: trait, Type: ty.String(), InstanceID: sel.id}
	childPath := append(append([]string{}, path...), key)
	for _, c := range sel.context {
		ct := nApply(c.Type, subs[best])
		child, f := n.resolveGoal(c.Trait, ct, level+1, childPath)
		if f != nil {
			return nil, f
		}
		tree.Children = append(tree.Children, child)
	}
	return tree, nil
}
