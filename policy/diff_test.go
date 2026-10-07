package policy

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

func mapsEqual(a, b map[string]any) bool {
	return reflect.DeepEqual(a, b)
}

// errorKindOf extracts the engine error kind (0 for success).
func errorKindOf(err error) Kind {
	if err == nil {
		return 0
	}
	var pe *Error
	if errors.As(err, &pe) {
		return pe.Kind
	}
	return -1
}

func attrErrKinds(res *Result) map[string]Kind {
	out := map[string]Kind{}
	if res != nil {
		for _, ae := range res.AttributeErrors {
			out[ae.Attr] = ae.Err.Kind
		}
	}
	return out
}

// genCase builds a random policy set and instance exercising every phase:
// valid or dangling references, allow/deny conflicts, copy-derived chains and
// cycles, const rules that may violate ranges/enums, and raw values of all
// kinds. The generator is shared by both implementations, so comparison only
// concerns arbitration semantics.
func genCase(rng *rand.Rand) (PolicySet, Instance) {
	attrs := map[string]AttrType{
		"a0": {Kind: KindString, MaxLen: 8},
		"a1": {Kind: KindInt, Min: ptrF(0), Max: ptrF(10)},
		"a2": {Kind: KindString, Allowed: []any{"X", "Y", "Z"}},
		"a3": {Kind: KindFloat, Min: ptrF(0), Max: ptrF(1)},
		"a4": {Kind: KindBool},
	}
	names := make([]string, 0, len(attrs))
	for n := range attrs {
		names = append(names, n)
	}
	sort.Strings(names)

	values := map[string]any{
		"a0": rngString(rng, 6),
		"a1": rng.Intn(11),
		"a2": []string{"X", "Y", "Z"}[rng.Intn(3)],
		"a3": rng.Float64(),
		"a4": rng.Intn(2) == 0,
	}

	var vis []VisibilityPolicy
	var mask []MaskingPolicy
	subjects := []string{"s1", "s2"}
	nVis := rng.Intn(7)
	for i := 0; i < nVis; i++ {
		attr := names[rng.Intn(len(names))]
		if rng.Intn(10) == 0 {
			attr = "ghost" // dangling reference
		}
		p := VisibilityPolicy{
			ID:      fmt.Sprintf("v%02d", i),
			Object:  "T",
			Subject: subjects[rng.Intn(len(subjects))],
			Attr:    attr,
			Allow:   rng.Intn(2) == 0,
		}
		if rng.Intn(2) == 0 {
			cond := names[rng.Intn(len(names))]
			ops := []string{"eq", "ne", "lt", "ge"}
			var val any
			switch cond {
			case "a0":
				val = rngString(rng, 3)
			case "a1":
				val = rng.Intn(11)
			case "a2":
				val = []string{"X", "Y", "Z"}[rng.Intn(3)]
			case "a3":
				val = rng.Float64()
			case "a4":
				val = rng.Intn(2) == 0
			}
			p.Pred = &Predicate{CondAttr: cond, Op: ops[rng.Intn(len(ops))], Value: val}
		}
		vis = append(vis, p)
	}
	nMask := rng.Intn(7)
	for i := 0; i < nMask; i++ {
		attr := names[rng.Intn(len(names))]
		if rng.Intn(12) == 0 {
			attr = "phantom"
		}
		rule := randomRule(rng, names)
		mask = append(mask, MaskingPolicy{
			ID:       fmt.Sprintf("m%02d", i),
			Object:   "T",
			Subject:  subjects[rng.Intn(len(subjects))],
			Attr:     attr,
			Strength: Strength(1 + rng.Intn(3)),
			Rule:     rule,
		})
	}
	set := PolicySet{
		Types:      []ObjectType{{Name: "T", Attrs: attrs}},
		Visibility: vis,
		Masking:    mask,
	}
	inst := Instance{Type: "T", ID: "i", Values: values}
	return set, inst
}

func randomRule(rng *rand.Rand, names []string) DerivedRule {
	switch rng.Intn(5) {
	case 0:
		return DerivedRule{Kind: RuleRedact}
	case 1:
		return DerivedRule{Kind: RuleHash}
	case 2:
		return DerivedRule{Kind: RuleMask, KeepRunes: rng.Intn(4)}
	case 3:
		// constants often violate the target's type/range/enum
		switch v := rng.Intn(5); v {
		case 0:
			return DerivedRule{Kind: RuleConst, ConstVal: rngString(rng, 12)} // may exceed MaxLen
		case 1:
			return DerivedRule{Kind: RuleConst, ConstVal: 42 + rng.Intn(1000)} // may exceed range
		case 2:
			return DerivedRule{Kind: RuleConst, ConstVal: "Q"} // not in enum
		case 3:
			return DerivedRule{Kind: RuleConst, ConstVal: 7.5} // out of [0,1]
		default:
			return DerivedRule{Kind: RuleConst, ConstVal: "X"} // valid for a2/a0
		}
	default:
		return DerivedRule{Kind: RuleCopyDerived, SourceAttr: names[rng.Intn(len(names))]}
	}
}

func rngString(rng *rand.Rand, n int) string {
	letters := []rune("abcXYZ")
	b := make([]rune, n)
	for i := range b {
		b[i] = letters[rng.Intn(len(letters))]
	}
	return string(b)
}

func TestDifferentialAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20261007))
	const iterations = 4000
	for iter := 0; iter < iterations; iter++ {
		set, inst := genCase(rng)
		// Build succeeds structurally; dangling refs are evaluated lazily.
		r := NewRegistry()
		if err := r.Apply(set); err != nil {
			t.Fatalf("iter %d: apply: %v", iter, err)
		}
		for _, subject := range []string{"s1", "s2", "nobody"} {
			res, err := r.Render(cloneInstance(inst), subject)
			naive := NaiveRender(set, cloneInstance(inst), subject)
			ek := errorKindOf(err)
			if ek != naive.ErrorKind {
				t.Fatalf("iter %d subject %s: engine kind %v != naive %v (err=%v)", iter, subject, ek, naive.ErrorKind, err)
			}
			if ek == KindMaskingCycle && fmt.Sprint(errorCycleOf(err)) != fmt.Sprint(naive.ErrorCycle) {
				t.Fatalf("iter %d: cycle %v != naive %v", iter, errorCycleOf(err), naive.ErrorCycle)
			}
			if ek != 0 {
				continue
			}
			if !mapsEqual(res.Presented, naive.Presented) {
				t.Fatalf("iter %d subject %s:\nengine=%#v\nnaive =%#v\nset=%+v", iter, subject, res.Presented, naive.Presented, set)
			}
			if !reflect.DeepEqual(attrErrKinds(res), attrErrKindsNaive(naive)) {
				t.Fatalf("iter %d subject %s: attr errors engine=%v naive=%v", iter, subject, attrErrKinds(res), attrErrKindsNaive(naive))
			}
		}
	}
}

func errorCycleOf(err error) []string {
	var pe *Error
	if errors.As(err, &pe) {
		return pe.Cycle
	}
	return nil
}

func attrErrKindsNaive(o NaiveOutcome) map[string]Kind {
	out := map[string]Kind{}
	for _, ae := range o.AttributeErrors {
		out[ae.Attr] = ae.Err.Kind
	}
	return out
}

func cloneInstance(inst Instance) Instance {
	v := make(map[string]any, len(inst.Values))
	for k, val := range inst.Values {
		v[k] = val
	}
	return Instance{Type: inst.Type, ID: inst.ID, Values: v}
}
