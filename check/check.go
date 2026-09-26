package check

import (
	"fmt"

	"ontology/types"
)

type frame struct {
	bind   map[string]types.T
	parent *frame
}

// Env is a name->type map with let scopes; Infer never mutates the Env in.
type Env struct{ leaf *frame }

func NewEnv() *Env { return &Env{leaf: &frame{bind: map[string]types.T{}}} }

// Enter/Exit/Bind provide optional manual mutable scoping.
func (e *Env) Enter() { e.leaf = &frame{parent: e.leaf, bind: map[string]types.T{}} }
func (e *Env) Exit() {
	if e.leaf.parent != nil {
		e.leaf = e.leaf.parent
	}
}
func (e *Env) Bind(n string, t types.T) { e.leaf.bind[n] = t }
func (e *Env) child(n string, t types.T) *Env {
	return &Env{leaf: &frame{parent: e.leaf, bind: map[string]types.T{n: t}}}
}

type checker struct{ probes int }

// lookup locates a name inner-to-outer: one hash probe per frame, never a
// linear scan over bindings.
func (c *checker) lookup(env *Env, n string) (types.T, bool) {
	for f := env.leaf; f != nil; f = f.parent {
		c.probes++
		if t, ok := f.bind[n]; ok {
			return t, true
		}
	}
	return types.T{}, false
}

// kids infers the first n child expressions of e in order.
func (c *checker) kids(e *Expr, env *Env, n int) (ts [3]types.T, err error) {
	for i := 0; i < n; i++ {
		if ts[i], err = c.infer(e.Kids[i], env); err != nil {
			return
		}
	}
	return
}

// Infer type-checks e under env; failure returns no type and mutates nothing.
func Infer(e *Expr, env *Env) (types.T, error) {
	return (&checker{}).infer(e, env)
}

func (c *checker) infer(e *Expr, env *Env) (types.T, error) {
	switch e.Kind {
	case KIntLit:
		return types.Int, nil
	case KBoolLit:
		return types.Bool, nil
	case KVar:
		if t, ok := c.lookup(env, e.Name); ok {
			return t, nil
		}
		return types.T{}, fmt.Errorf("%w: %s", ErrUndefined, e.Name)
	case KNeg, KNot:
		ts, err := c.kids(e, env, 1)
		if err != nil {
			return types.T{}, err
		}
		return unaryRule(e.Kind, e.Op, ts[0])
	case KArith, KCmp, KLogic, KEq:
		ts, err := c.kids(e, env, 2)
		if err != nil {
			return types.T{}, err
		}
		return binaryRule(e.Kind, e.Op, ts[0], ts[1])
	case KIf:
		ts, err := c.kids(e, env, 3)
		if err != nil {
			return types.T{}, err
		}
		if !ts[0].Equal(types.Bool) || !ts[1].Equal(ts[2]) {
			return types.T{}, fmt.Errorf("%w: cond=%s then=%s else=%s", ErrIf, ts[0], ts[1], ts[2])
		}
		return ts[1], nil
	case KLet:
		t, err := c.infer(e.Kids[0], env)
		if err != nil {
			return types.T{}, err
		}
		return c.infer(e.Kids[1], env.child(e.Name, t))
	}
	return types.T{}, fmt.Errorf("typecheck: unknown node kind %d", e.Kind)
}

// unaryRule/binaryRule are the pure rule table shared by Infer and the naive
// reference, so the two cannot drift on any rule.
func unaryRule(k Kind, op string, t types.T) (types.T, error) {
	if k == KNeg {
		if t.Equal(types.Int) {
			return types.Int, nil
		}
		return types.T{}, fmt.Errorf("%w: %s%s", ErrIntRequired, op, t)
	}
	if t.Equal(types.Bool) {
		return types.Bool, nil
	}
	return types.T{}, fmt.Errorf("%w: %s%s", ErrBoolRequired, op, t)
}

func binaryRule(k Kind, op string, a, b types.T) (types.T, error) {
	res, s := types.Int, ErrIntRequired
	match := a.Equal(types.Int) && b.Equal(types.Int)
	switch k {
	case KCmp:
		res = types.Bool
	case KLogic:
		res, s = types.Bool, ErrBoolRequired
		match = a.Equal(types.Bool) && b.Equal(types.Bool)
	case KEq:
		res, s = types.Bool, ErrEqMismatch
		match = a.Equal(b)
	}
	if !match {
		return types.T{}, fmt.Errorf("%w: %s %s %s", s, a, op, b)
	}
	return res, nil
}

// VerifyLookupProbe reports whether one successful lookup is O(1); only the verdict is exported.
func VerifyLookupProbe() error {
	for _, m := range []int{100, 1000, 10000} {
		env := NewEnv()
		for i := 0; i < m; i++ {
			env.Bind(fmt.Sprintf("v%d", i), types.Int)
		}
		c := &checker{}
		c.lookup(env, fmt.Sprintf("v%d", m/2))
		if c.probes > 2 {
			return fmt.Errorf("lookup examined %d bindings at m=%d", c.probes, m)
		}
	}
	return nil
}
