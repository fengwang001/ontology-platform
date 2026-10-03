package resolver

import (
	"fmt"
	"math/rand"
	"testing"
)

var (
	diffTraits  = []string{"Show", "Eq", "Ord"}
	diffNullary = []string{"Int", "Char", "Bool"}
	diffUnary   = []string{"List", "Box"}
)

func randGround(rng *rand.Rand, fuel int) *Type {
	if fuel <= 0 || rng.Intn(3) == 0 {
		return Con(diffNullary[rng.Intn(len(diffNullary))])
	}
	if rng.Intn(2) == 0 {
		return Con(diffUnary[rng.Intn(len(diffUnary))], randGround(rng, fuel-1))
	}
	return Con("Pair", randGround(rng, fuel-1), randGround(rng, fuel-1))
}

// randPattern builds a type that may contain variables 0..nv-1.
func randPattern(rng *rand.Rand, fuel, nv int) *Type {
	if nv > 0 && rng.Intn(4) == 0 {
		return Var(rng.Intn(nv))
	}
	if fuel <= 0 || rng.Intn(3) == 0 {
		return Con(diffNullary[rng.Intn(len(diffNullary))])
	}
	if rng.Intn(2) == 0 {
		return Con(diffUnary[rng.Intn(len(diffUnary))], randPattern(rng, fuel-1, nv))
	}
	return Con("Pair", randPattern(rng, fuel-1, nv), randPattern(rng, fuel-1, nv))
}

type opKind int

const (
	opAdd opKind = iota
	opResolve
)

type operation struct {
	kind    opKind
	trait   string
	head    *Type
	ctx     []Constraint
	ty      *Type
	display string
}

func genOperation(rng *rand.Rand) operation {
	if rng.Intn(10) < 6 {
		trait := diffTraits[rng.Intn(len(diffTraits))]
		nv := rng.Intn(4)
		head := randPattern(rng, 3, nv)
		if nv > 0 && rng.Intn(10) == 0 {
			head = Var(rng.Intn(nv))
		}
		if nv > 0 && rng.Intn(6) == 0 {
			// Ambiguity-prone heads such as Pair<Int,a> vs Pair<a,Int>.
			g := Con(diffNullary[rng.Intn(len(diffNullary))])
			v := Var(rng.Intn(nv))
			if rng.Intn(2) == 0 {
				head = Con("Pair", g, v)
			} else {
				head = Con("Pair", v, g)
			}
		}
		var ctx []Constraint
		for i := 0; i < rng.Intn(3); i++ {
			ct := randPattern(rng, 2, nv)
			if rng.Intn(2) == 0 {
				ct = randGround(rng, 2)
			}
			ctx = append(ctx, Constraint{Trait: diffTraits[rng.Intn(len(diffTraits))], Type: ct})
		}
		if rng.Intn(20) == 0 {
			ctx = append(ctx, Constraint{Trait: "Show", Type: Var(7)})
		}
		return operation{
			kind:    opAdd,
			trait:   trait,
			head:    head,
			ctx:     ctx,
			display: fmt.Sprintf("AddInstance(%s, %s, %v)", trait, head, ctx),
		}
	}
	trait := diffTraits[rng.Intn(len(diffTraits))]
	ty := randGround(rng, 3)
	if rng.Intn(4) == 0 {
		// The shape that triggers overlapping-instance ambiguity.
		g := Con(diffNullary[rng.Intn(len(diffNullary))])
		ty = Con("Pair", g, g)
	}
	if rng.Intn(20) == 0 {
		ty = Con("List", Var(0))
	}
	return operation{
		kind:    opResolve,
		trait:   trait,
		ty:      ty,
		display: fmt.Sprintf("Resolve(%s, %s)", trait, ty),
	}
}

func renderResult(tree *Tree, f *Failure) string {
	if f != nil {
		return "FAIL " + f.String()
	}
	return fmt.Sprintf("OK %s h=%d", tree, tree.Height())
}

func TestRandomDifferential(t *testing.T) {
	const sequences = 2000
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)*7919 + 42))
		d := 1 + rng.Intn(8)
		main, err := NewResolver(d)
		if err != nil {
			t.Fatalf("seq=%d: NewResolver(%d): %v", seq, d, err)
		}
		naive := &nResolver{d: d}
		ops := 1 + rng.Intn(20)
		for op := 0; op < ops; op++ {
			o := genOperation(rng)
			switch o.kind {
			case opAdd:
				mid, merr := main.AddInstance(o.trait, o.head, o.ctx)
				nid, nkind, nok := naive.add(o.trait, o.head, o.ctx)
				mainOut, naiveOut := "", ""
				match := true
				if merr != nil {
					mainOut = merr.(*Error).Kind.String()
					naiveOut = "accepted"
					match = !nok && merr.(*Error).Kind == nkind
				} else {
					mainOut = fmt.Sprintf("id=%d", mid)
					naiveOut = "rejected"
					match = nok && mid == nid
				}
				if nok {
					naiveOut = fmt.Sprintf("id=%d", nid)
				} else if merr == nil {
					naiveOut = nkind.String()
				}
				t.Logf("seq=%d op=%d in=%s main=%s naive=%s verdict=%v",
					seq, op, o.display, mainOut, naiveOut, match)
				if !match {
					t.Fatalf("seq=%d op=%d: %s: main=%s naive=%s", seq, op, o.display, mainOut, naiveOut)
				}
			case opResolve:
				mtree, mf, merr := main.Resolve(o.trait, o.ty)
				ntree, nf, nkind, nok := naive.resolve(o.trait, o.ty)
				var mainOut, naiveOut string
				match := true
				if merr != nil {
					mainOut = "ERR " + merr.(*Error).Kind.String()
					match = !nok && merr.(*Error).Kind == nkind
					naiveOut = "accepted"
					if !nok {
						naiveOut = "ERR " + nkind.String()
					}
				} else {
					mainOut = renderResult(mtree, mf)
					match = nok
					naiveOut = "rejected"
					if nok {
						naiveOut = renderResult(ntree, nf)
						match = mainOut == naiveOut
					}
				}
				t.Logf("seq=%d op=%d in=%s main=%s naive=%s verdict=%v",
					seq, op, o.display, mainOut, naiveOut, match)
				if !match {
					t.Fatalf("seq=%d op=%d: %s:\nmain =%s\nnaive=%s", seq, op, o.display, mainOut, naiveOut)
				}
			}
			if err := main.selfCheck(); err != nil {
				t.Fatalf("seq=%d op=%d: selfCheck after %s: %v", seq, op, o.display, err)
			}
		}
		t.Logf("seq=%d done: d=%d ops=%d instances=%d cache=%d",
			seq, d, ops, len(main.instances), len(main.cache))
	}
}

func TestReplayDeterminism(t *testing.T) {
	for seq := 0; seq < 50; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)*104729 + 7))
		d := 1 + rng.Intn(8)
		var ops []operation
		for i := 0; i < 1+rng.Intn(20); i++ {
			ops = append(ops, genOperation(rng))
		}
		run := func() []string {
			r, err := NewResolver(d)
			if err != nil {
				t.Fatalf("NewResolver(%d): %v", d, err)
			}
			var out []string
			for _, o := range ops {
				switch o.kind {
				case opAdd:
					id, aerr := r.AddInstance(o.trait, o.head, o.ctx)
					if aerr != nil {
						out = append(out, "ERR "+aerr.(*Error).Kind.String())
					} else {
						out = append(out, fmt.Sprintf("id=%d", id))
					}
				case opResolve:
					tree, f, rerr := r.Resolve(o.trait, o.ty)
					if rerr != nil {
						out = append(out, "ERR "+rerr.(*Error).Kind.String())
					} else {
						out = append(out, renderResult(tree, f))
					}
				}
			}
			return out
		}
		first := run()
		second := run()
		if len(first) != len(second) {
			t.Fatalf("seq=%d: replay produced %d outputs, want %d", seq, len(second), len(first))
		}
		for i := range first {
			t.Logf("seq=%d op=%d in=%s first=%s second=%s", seq, i, ops[i].display, first[i], second[i])
			if first[i] != second[i] {
				t.Fatalf("seq=%d op=%d: replay mismatch: %q vs %q", seq, i, first[i], second[i])
			}
		}
	}
}
