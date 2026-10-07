package ontology_test

import (
	"math/rand"

	ont "ontology/ontology"
)

// Shared, deterministic mask/write-mask function families. Both the
// optimized engine and the naive reference receive the SAME function
// instances, so any divergence is caused by adjudication, never by
// differing mask code.
func maskFamilies() map[string]ont.MaskFunc {
	return map[string]ont.MaskFunc{
		"okZeroString": func(_ ont.Subject, _ ont.RawValue) (ont.RawValue, error) { return "masked", nil },
		"okZeroInt":    func(_ ont.Subject, _ ont.RawValue) (ont.RawValue, error) { return int64(-1), nil },
		"okZeroFloat":  func(_ ont.Subject, _ ont.RawValue) (ont.RawValue, error) { return float64(-1), nil },
		"okZeroBool":   func(_ ont.Subject, _ ont.RawValue) (ont.RawValue, error) { return true, nil },
		"echoString": func(_ ont.Subject, raw ont.RawValue) (ont.RawValue, error) {
			s, _ := raw.(string)
			return "M(" + s + ")", nil
		},
		"badKind": func(_ ont.Subject, _ ont.RawValue) (ont.RawValue, error) { return int64(999), nil },
		"badFloatNaN": func(_ ont.Subject, _ ont.RawValue) (ont.RawValue, error) {
			return float64(func() float64 { return 0 }()), nil // valid; kept for balance
		},
		"errMask": func(_ ont.Subject, _ ont.RawValue) (ont.RawValue, error) {
			return nil, errSentinel{}
		},
	}
}

type errSentinel struct{}

func (errSentinel) Error() string { return "mask failure" }

func writeMaskFamilies() map[string]ont.WriteMaskFunc {
	return map[string]ont.WriteMaskFunc{
		"identityInt":    func(_ ont.Subject, v ont.RawValue) (ont.RawValue, error) { return v, nil },
		"clampInt":       func(_ ont.Subject, v ont.RawValue) (ont.RawValue, error) { return int64(100), nil },
		"identityString": func(_ ont.Subject, v ont.RawValue) (ont.RawValue, error) { return v, nil },
		"tagString":      func(_ ont.Subject, v ont.RawValue) (ont.RawValue, error) { return v.(string) + "!", nil },
		"badKind":        func(_ ont.Subject, _ ont.RawValue) (ont.RawValue, error) { return true, nil },
	}
}

var subjects = []ont.Subject{
	{ID: "alice", Groups: []string{"g1"}},
	{ID: "bob", Groups: []string{"g2"}},
	{ID: "carol", Groups: []string{"g1", "g2"}},
	{ID: "dave"},
	{ID: AdminID, Groups: []string{"admin"}},
}

const AdminID = "admin"

func pickSelector(rng *rand.Rand) ont.SubjectSelector {
	switch rng.Intn(5) {
	case 0:
		// MatchAll would also match the omnipotent admin subject used
		// for raw-state snapshots, so generated rules use concrete
		// non-admin principals/groups instead.
		return ont.SubjectSelector{Users: []string{subjects[rng.Intn(4)].ID}}
	case 1:
		return ont.SubjectSelector{Users: []string{subjects[rng.Intn(4)].ID}}
	case 2:
		return ont.SubjectSelector{Groups: []string{"g1"}}
	case 3:
		return ont.SubjectSelector{Groups: []string{"g2"}}
	default:
		u := subjects[rng.Intn(4)]
		return ont.SubjectSelector{Users: []string{u.ID}, Groups: u.Groups}
	}
}

func randomValue(rng *rand.Rand, dt ont.DataType) ont.RawValue {
	switch dt {
	case ont.TypeInt:
		return int64(rng.Intn(20) - 10)
	case ont.TypeFloat:
		return float64(rng.Intn(20) - 10)
	case ont.TypeString:
		return []string{"a", "b", "secret", "x", ""}[rng.Intn(5)]
	default:
		return rng.Intn(2) == 0
	}
}

// genPolicies builds a random, independently-valid policy set plus a
// fixed omnipotent admin policy used to snapshot raw state.
func genPolicies(rng *rand.Rand, ot *ont.ObjectType, decls []ont.PropertyDecl) (ont.RowPolicySet, ont.PropPolicySet) {
	rmode := []ont.MergeMode{ont.AllowOverrides, ont.DenyOverrides}[rng.Intn(2)]
	pmode := []ont.MergeMode{ont.AllowOverrides, ont.DenyOverrides}[rng.Intn(2)]

	rowRules := []ont.RowRule{
		{ID: "admin-row", Selector: ont.SubjectSelector{Groups: []string{"admin"}}, Effect: ont.EffectAllow},
	}
	nRow := 6 + rng.Intn(8)
	for i := 0; i < nRow; i++ {
		r := ont.RowRule{
			ID:       "row-" + itoa9(int64(i)),
			Selector: pickSelector(rng),
			Effect:   []ont.Effect{ont.EffectAllow, ont.EffectDeny}[rng.Intn(2)],
		}
		if rng.Intn(2) == 0 {
			p := decls[rng.Intn(len(decls))]
			r.Predicates = []ont.Predicate{{
				Property: p.Name,
				Op:       []ont.CmpOp{ont.CmpEq, ont.CmpNe, ont.CmpLt, ont.CmpGe}[rng.Intn(4)],
				Value:    randomValue(rng, p.Type),
			}}
		}
		rowRules = append(rowRules, r)
	}

	propRules := []ont.PropRule{}
	// admin raw read/write for every property
	for _, d := range decls {
		propRules = append(propRules, ont.PropRule{
			ID: "admin-" + d.Name, Property: d.Name,
			Selector: ont.SubjectSelector{Groups: []string{"admin"}},
			HasRead:  true, Readable: true, HasWrite: true, Writable: true,
		})
	}
	masks := maskFamilies()
	wmasks := writeMaskFamilies()
	rid := 0
	for _, d := range decls {
		n := 1 + rng.Intn(3)
		for j := 0; j < n; j++ {
			base := ont.PropRule{
				ID:       "prop-" + itoa9(int64(rid)),
				Property: d.Name,
				Selector: pickSelector(rng),
			}
			rid++
			kind := rng.Intn(6)
			switch kind {
			case 0: // plain read allow
				base.HasRead, base.Readable = true, true
			case 1: // plain read deny
				base.HasRead, base.Readable = true, false
			case 2: // plain write allow/deny
				base.HasWrite = true
				base.Writable = rng.Intn(2) == 0
			case 3: // read+write plain
				base.HasRead, base.Readable = true, true
				base.HasWrite, base.Writable = true, rng.Intn(2) == 0
			case 4: // masked read; choose a family compatible sometimes
				fam := []string{"okZeroString", "okZeroInt", "okZeroFloat", "okZeroBool", "echoString", "badKind", "errMask"}[rng.Intn(7)]
				base.Mask = masks[fam]
			case 5: // write mask
				var fam string
				switch d.Type {
				case ont.TypeInt:
					fam = []string{"identityInt", "clampInt", "badKind"}[rng.Intn(3)]
				case ont.TypeString:
					fam = []string{"identityString", "tagString", "badKind"}[rng.Intn(3)]
				case ont.TypeFloat:
					fam = "badKind"
				default:
					fam = "badKind"
				}
				base.WriteMask = wmasks[fam]
			}
			propRules = append(propRules, base)
		}
	}
	return ont.RowPolicySet{Mode: rmode, Rules: rowRules},
		ont.PropPolicySet{Mode: pmode, Rules: propRules}
}

func itoa9(i int64) string {
	// fixed-width-ish ids; uniqueness is what matters
	if i < 10 {
		return "0" + string(rune('0'+i))
	}
	return string(rune('0'+i/10)) + string(rune('0'+i%10))
}
