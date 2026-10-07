package ontology

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// snapshot deep-copies the store so the production and naive paths can each
// run from identical state.
func snapshotStore(s *Store) map[string]*record {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]*record, len(s.instances))
	for k, rec := range s.instances {
		out[k] = &record{
			instance:      rec.instance.clone(),
			version:       rec.version,
			lastWriteNano: rec.lastWriteNano,
		}
	}
	return out
}

func restoreStore(s *Store, snap map[string]*record) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.instances = snap
}

type diffWorld struct {
	a        *Adjudicator
	naive    *NaiveAdjudicator
	store    *Store
	rng      *rand.Rand
	props    []string
	subjects []string
	ids      []string
}

func buildDiffWorld(rng *rand.Rand, rowMode, propMode MergeMode, writeMode WriteMode) *diffWorld {
	props := []string{"pstr", "pint", "pbool"}
	ot := &ObjectType{
		Name: "Obj",
		Properties: []PropertyDef{
			{Name: "pstr", Type: DeclaredType{Kind: KindString, EnumValues: []string{"a", "b", "c"}}},
			{Name: "pint", Type: DeclaredType{Kind: KindInt, Min: ptrFloat(0), Max: ptrFloat(9)}},
			{Name: "pbool", Type: DeclaredType{Kind: KindBoolean}},
		},
	}
	catalog := NewPolicyCatalog()
	store := NewStore()
	audit := NewAuditLogger(nil)
	cfg := Config{
		RowMode: rowMode, PropertyMode: propMode,
		DefaultRow:   Effect(rngPick(rng, []string{string(EffectAllow), string(EffectDeny)})),
		DefaultRead:  Effect(rngPick(rng, []string{string(EffectAllow), string(EffectDeny)})),
		DefaultWrite: Effect(rngPick(rng, []string{string(EffectAllow), string(EffectDeny)})),
		WriteMode:    writeMode,
	}
	a := NewAdjudicator(cfg, catalog, store, audit)
	a.RegisterType(ot)

	subjects := []string{"s0", "s1", "s2", "s3"}
	ids := []string{"i0", "i1", "i2", "i3", "i4"}

	// Random row policies: each selects a random subject subset (empty means
	// everyone) and one random atom.
	for i := 0; i < rng.Intn(12)+1; i++ {
		p := RowPolicy{
			ID:         fmt.Sprintf("r%02d", i),
			ObjectType: "Obj",
			Subjects:   rngSubset(rng, subjects),
			Effect:     effect(rng),
		}
		switch rng.Intn(3) {
		case 0:
			p.Predicate = Predicate{Atoms: []Atom{{
				Property: "pstr", Op: rngCmp(rng, false), Str: rngPick(rng, []string{"a", "b", "c", "z"}),
			}}}
		case 1:
			p.Predicate = Predicate{Atoms: []Atom{{
				Property: "pint", Op: rngCmp(rng, true), Numeric: float64(rng.Intn(11)),
			}}}
		default:
			p.Predicate = Predicate{Atoms: []Atom{{
				Property: "pbool", Op: CmpOp(rngPick(rng, []string{string(OpEq), string(OpNe)})),
				Bool: rng.Intn(2) == 0,
			}}}
		}
		catalog.RegisterRowPolicy(p)
	}

	// Random property policies: independent read/write effects, sometimes a
	// mask. String masks map to either a legal enum value or an illegal one;
	// int masks sometimes leave the [0,9] range.
	for i := 0; i < rng.Intn(16)+1; i++ {
		pp := PropertyPolicy{
			ID:         fmt.Sprintf("p%02d", i),
			ObjectType: "Obj",
			Subjects:   rngSubset(rng, subjects),
			Property:   rngPick(rng, append(append([]string{}, props...), "*")),
		}
		if rng.Intn(2) == 0 {
			e := effect(rng)
			pp.Read = &e
		}
		if rng.Intn(2) == 0 {
			e := effect(rng)
			pp.Write = &e
		}
		switch pp.Property {
		case "pstr":
			if rng.Intn(2) == 0 {
				out := rngPick(rng, []string{"a", "b", "zzz"})
				pp.MaskName = "strmask"
				pp.Mask = func(Value) Value { return Value{Str: out} }
			}
		case "pint":
			if rng.Intn(2) == 0 {
				n := rng.Intn(14) - 2
				pp.MaskName = "intmask"
				pp.Mask = func(Value) Value { return Value{Int: int64(n)} }
			}
		}
		catalog.RegisterPropertyPolicy(pp)
	}

	for _, id := range ids {
		inst := Instance{Type: "Obj", ID: id, Values: map[string]Value{}, Present: map[string]bool{}}
		for _, name := range props {
			if rng.Intn(2) == 0 {
				inst.Values[name] = randomValue(rng, name)
				inst.Present[name] = true
			}
		}
		store.Put(inst)
	}
	return &diffWorld{
		a: a, naive: NewNaiveAdjudicator(a), store: store, rng: rng,
		props: props, subjects: subjects, ids: ids,
	}
}

func randomValue(rng *rand.Rand, name string) Value {
	switch name {
	case "pstr":
		return Value{Str: rngPick(rng, []string{"a", "b", "c"})}
	case "pint":
		return Value{Int: int64(rng.Intn(10))}
	default:
		return Value{Bool: rng.Intn(2) == 0}
	}
}

func effect(rng *rand.Rand) Effect {
	return Effect(rngPick(rng, []string{string(EffectAllow), string(EffectDeny)}))
}

func rngPick(rng *rand.Rand, xs []string) string { return xs[rng.Intn(len(xs))] }

func rngCmp(rng *rand.Rand, numeric bool) CmpOp {
	if numeric {
		return CmpOp(rngPick(rng, []string{
			string(OpEq), string(OpNe), string(OpLt), string(OpLe),
			string(OpGt), string(OpGe),
		}))
	}
	return CmpOp(rngPick(rng, []string{string(OpEq), string(OpNe)}))
}

func rngSubset(rng *rand.Rand, xs []string) []string {
	var out []string
	for _, x := range xs {
		if rng.Intn(2) == 0 {
			out = append(out, x)
		}
	}
	return out
}

func (w *diffWorld) randomWrite() map[string]Value {
	values := map[string]Value{}
	for _, name := range w.props {
		if w.rng.Intn(2) == 0 {
			if w.rng.Intn(6) == 0 {
				// Occasionally an out-of-contract value.
				values[name] = Value{Str: "zzz", Int: 42}
			} else {
				values[name] = randomValue(w.rng, name)
			}
		}
	}
	if len(values) == 0 {
		values["pint"] = randomValue(w.rng, "pint")
	}
	if w.rng.Intn(8) == 0 {
		values["ghost"] = Value{Str: "x"}
	}
	return values
}

func sameError(x, y *DecisionError) bool {
	if x == nil || y == nil {
		return x == nil && y == nil
	}
	return x.Kind == y.Kind && x.Property == y.Property
}

func sameView(x, y *InstanceView) bool {
	if x == nil || y == nil {
		return x == nil && y == nil
	}
	if !reflect.DeepEqual(x.Status, y.Status) {
		return false
	}
	if !reflect.DeepEqual(x.Fields, y.Fields) {
		return false
	}
	if !reflect.DeepEqual(x.Basis, y.Basis) {
		return false
	}
	return true
}

func sameWriteResult(x, y *WriteResult) bool {
	if x == nil || y == nil {
		return x == nil && y == nil
	}
	sx := append([]string{}, x.Dropped...)
	sy := append([]string{}, y.Dropped...)
	sort.Strings(sx)
	sort.Strings(sy)
	return reflect.DeepEqual(x.Applied, y.Applied) &&
		reflect.DeepEqual(sx, sy) &&
		x.Version == y.Version &&
		reflect.DeepEqual(x.Basis, y.Basis)
}

func TestDifferentialAgainstNaiveReference(t *testing.T) {
	modes := []struct {
		row, prop MergeMode
		write     WriteMode
	}{
		{DenyOverrides, DenyOverrides, WriteReject},
		{AllowOverrides, AllowOverrides, WriteDrop},
		{DenyOverrides, AllowOverrides, WriteDrop},
		{AllowOverrides, DenyOverrides, WriteReject},
	}
	for seed := int64(1); seed <= 30; seed++ {
		m := modes[int(seed)%len(modes)]
		t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) {
			w := buildDiffWorld(rand.New(rand.NewSource(seed)), m.row, m.prop, m.write)
			for op := 0; op < 200; op++ {
				subject := w.subjects[w.rng.Intn(len(w.subjects))]
				id := w.ids[w.rng.Intn(len(w.ids))]
				if w.rng.Intn(8) == 0 {
					id = "missing-id"
				}
				if w.rng.Intn(2) == 0 {
					snap := snapshotStore(w.store)
					v1, e1 := w.a.Read(subject, "Obj", id)
					restoreStore(w.store, snap)
					v2, e2 := w.naive.Read(subject, "Obj", id)
					if !sameError(e1, e2) {
						t.Fatalf("op %d read error mismatch: %v vs %v", op, e1, e2)
					}
					if !sameView(v1, v2) {
						t.Fatalf("op %d read view mismatch:\n%+v\nvs\n%+v", op, v1, v2)
					}
				} else {
					values := w.randomWrite()
					snap := snapshotStore(w.store)
					r1, e1 := w.a.Write(subject, "Obj", id, values)
					restoreStore(w.store, snap)
					r2, e2 := w.naive.Write(subject, "Obj", id, values)
					if !sameError(e1, e2) {
						t.Fatalf("op %d write error mismatch: %+v vs %+v", op, e1, e2)
					}
					if !sameWriteResult(r1, r2) {
						t.Fatalf("op %d write result mismatch:\n%+v\nvs\n%+v", op, r1, r2)
					}
				}
			}
		})
	}
}
