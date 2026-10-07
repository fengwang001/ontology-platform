package ontology

import "testing"

func ptrFloat(f float64) *float64 { return &f }

func TestValueConforms(t *testing.T) {
	enumStr := DeclaredType{Kind: KindString, EnumValues: []string{"red", "green"}}
	rangedInt := DeclaredType{Kind: KindInt, Min: ptrFloat(0), Max: ptrFloat(10)}
	rangedFloat := DeclaredType{Kind: KindFloat, Min: ptrFloat(0.5)}

	cases := []struct {
		name string
		dt   DeclaredType
		v    Value
		want bool
	}{
		{"enum ok", enumStr, Value{Str: "red"}, true},
		{"enum bad", enumStr, Value{Str: "blue"}, false},
		{"enum empty bad", enumStr, Value{Str: ""}, false},
		{"int in range", rangedInt, Value{Int: 5}, true},
		{"int zero in range", rangedInt, Value{Int: 0}, true},
		{"int below", rangedInt, Value{Int: -1}, false},
		{"int above", rangedInt, Value{Int: 11}, false},
		{"float min", rangedFloat, Value{Real: 0.5}, true},
		{"float below", rangedFloat, Value{Real: 0.4}, false},
		{"bool always", DeclaredType{Kind: KindBoolean}, Value{Bool: false}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.v.Conforms(tc.dt); got != tc.want {
				t.Fatalf("Conforms() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPredicateRunsOnRawValues(t *testing.T) {
	dt := map[string]DeclaredType{
		"level": {Kind: KindInt, Min: ptrFloat(0)},
		"tag":   {Kind: KindString},
		"flag":  {Kind: KindBoolean},
	}
	pred := Predicate{Atoms: []Atom{
		{Property: "level", Op: OpGe, Numeric: 5},
		{Property: "tag", Op: OpEq, Str: "secret"},
	}}
	raw := map[string]Value{"level": {Int: 7}, "tag": {Str: "secret"}}
	if !pred.eval(raw, dt) {
		t.Fatal("expected predicate true on raw values")
	}
	// Missing property referenced by the predicate makes its atom false.
	if (Predicate{Atoms: []Atom{{Property: "ghost", Op: OpEq, Str: "x"}}}).
		eval(raw, dt) {
		t.Fatal("atom on missing property must be false")
	}
	if !(Predicate{Atoms: []Atom{{Property: "flag", Op: OpEq, Bool: false}}}).
		eval(map[string]Value{"flag": {Bool: false}}, dt) {
		t.Fatal("boolean equality on false must work")
	}
}

func TestMergeEffects(t *testing.T) {
	allow := EffectAllow
	deny := EffectDeny
	if got := mergeEffects([]Effect{allow, deny}, DenyOverrides, allow); got != deny {
		t.Fatalf("deny overrides: got %s", got)
	}
	if got := mergeEffects([]Effect{allow, deny}, AllowOverrides, deny); got != allow {
		t.Fatalf("allow overrides: got %s", got)
	}
	if got := mergeEffects(nil, DenyOverrides, deny); got != deny {
		t.Fatalf("no matches must use fallback, got %s", got)
	}
	if got := mergeEffects([]Effect{deny}, AllowOverrides, allow); got != deny {
		t.Fatalf("lone deny under allow-overrides must deny, got %s", got)
	}
}
