package ontology

import "testing"

func intField(name string, c Constraint) *FieldDef {
	return &FieldDef{Name: name, Type: IntType, Constraint: c, Nullable: true}
}

func TestClassifySixKindsUnique(t *testing.T) {
	cases := []struct {
		name string
		ch   FieldChange
		want ChangeKind
	}{
		{"add with default", FieldChange{Field: "a", New: &FieldDef{Name: "a", Type: IntType, HasDefault: true, Default: IntValue(0)}}, AddFieldWithDefault},
		{"add without default nullable", FieldChange{Field: "a", New: &FieldDef{Name: "a", Type: IntType, Nullable: true}}, AddFieldWithoutDefault},
		{"add without default required", FieldChange{Field: "a", New: &FieldDef{Name: "a", Type: IntType}}, AddFieldWithoutDefault},
		{"tighten", FieldChange{Field: "a", Old: intField("a", NumRange(0, 100)), New: intField("a", NumRange(10, 50))}, TightenConstraint},
		{"loosen", FieldChange{Field: "a", Old: intField("a", NumRange(10, 50)), New: intField("a", NumRange(0, 100))}, LoosenConstraint},
		{"change type", FieldChange{Field: "a", Old: intField("a", Constraint{}), New: &FieldDef{Name: "a", Type: FloatType, Nullable: true}}, ChangeType},
		{"remove", FieldChange{Field: "a", Old: intField("a", Constraint{})}, RemoveField},
	}
	seen := map[ChangeKind]bool{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Classify(tc.ch)
			if err != nil {
				t.Fatalf("Classify: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			seen[got] = true
		})
	}
	// 六个类别都必须被覆盖到。
	for k := AddFieldWithDefault; k <= RemoveField; k++ {
		if !seen[k] {
			t.Fatalf("kind %v not covered", k)
		}
	}
}

func TestClassifyTypeChangeDominatesConstraintChange(t *testing.T) {
	// 类型与约束同时变化时，必须唯一落入 ChangeType。
	ch := FieldChange{
		Field: "a",
		Old:   intField("a", NumRange(0, 100)),
		New:   &FieldDef{Name: "a", Type: FloatType, Constraint: NumRange(0, 10), Nullable: true},
	}
	got, err := Classify(ch)
	if err != nil || got != ChangeType {
		t.Fatalf("got %v, %v; want ChangeType", got, err)
	}
}

func TestClassifyIncomparableConstraintIsTighten(t *testing.T) {
	// 区间与枚举互不包含：保守归入收紧。
	ch := FieldChange{
		Field: "a",
		Old:   intField("a", NumRange(0, 100)),
		New:   intField("a", EnumConstraint(IntValue(1), IntValue(200))),
	}
	got, err := Classify(ch)
	if err != nil || got != TightenConstraint {
		t.Fatalf("got %v, %v; want TightenConstraint", got, err)
	}
}

func TestClassifyNoOp(t *testing.T) {
	same := intField("a", NumRange(0, 10))
	if _, err := Classify(FieldChange{Field: "a", Old: same, New: same}); err != ErrNoChange {
		t.Fatalf("want ErrNoChange, got %v", err)
	}
	if _, err := Classify(FieldChange{Field: "a"}); err != ErrNoChange {
		t.Fatalf("want ErrNoChange, got %v", err)
	}
}
