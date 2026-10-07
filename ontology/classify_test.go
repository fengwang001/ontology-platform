package ontology

import "testing"

func intField(allowMissing bool, hasDefault bool, c Constraint) *FieldDef {
	return &FieldDef{
		Type:         NewIntType(),
		Constraint:   c,
		AllowMissing: allowMissing,
		HasDefault:   hasDefault,
		Default:      NewValue(int64(0)),
	}
}

func TestClassifyUniqueCategories(t *testing.T) {
	open := AnyValue()
	wide := NewRangeConstraint(0, 100, true, true)
	tight := NewRangeConstraint(0, 10, true, true)
	looser := NewRangeConstraint(-10, 1000, true, true)
	overlap := NewRangeConstraint(50, 200, true, true) // 与 wide 不可比

	cases := []struct {
		name string
		fc   FieldChange
		want ChangeCategory
	}{
		{"add with default", FieldChange{Name: "a", New: intField(false, true, open)}, CatAddWithDefault},
		{"add without default", FieldChange{Name: "a", New: intField(true, false, open)}, CatAddWithoutDefault},
		{"tighten", FieldChange{Name: "a", Old: intField(false, true, wide), New: intField(false, true, tight)}, CatTighten},
		{"loosen", FieldChange{Name: "a", Old: intField(false, true, tight), New: intField(false, true, looser)}, CatLoosen},
		{"type change", FieldChange{
			Name: "a",
			Old:  intField(false, true, open),
			New:  &FieldDef{Type: NewStringType(), Constraint: AnyValue(), HasDefault: true, Default: NewValue("0")},
		}, CatTypeChange},
		{"remove", FieldChange{Name: "a", Old: intField(false, true, open), New: nil}, CatRemove},
		{"mixed incomparable", FieldChange{Name: "a", Old: intField(false, true, wide), New: intField(false, true, overlap)}, CatMixed},
		{"metadata only", FieldChange{Name: "a", Old: intField(false, true, wide), New: intField(true, false, wide)}, CatNone},
	}

	// 分类必须唯一：同一输入重复分类结果一致，且与全部其他类别互斥。
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got1 := Classify(tc.fc)
			got2 := Classify(tc.fc)
			if got1 != tc.want || got2 != tc.want {
				t.Fatalf("Classify = %v, want %v (repeatable=%v)", got1, tc.want, got1 == got2)
			}
			for other := CatAddWithDefault; other <= CatMixed; other++ {
				if other != tc.want && other == got1 {
					t.Fatalf("change %q simultaneously classified as %v and %v", tc.name, tc.want, other)
				}
			}
		})
	}
}

func TestEnumTightenLoosen(t *testing.T) {
	old := NewEnumConstraint([]string{"a", "b", "c"})
	subset := NewEnumConstraint([]string{"a", "b"})
	superset := NewEnumConstraint([]string{"a", "b", "c", "d"})
	shifted := NewEnumConstraint([]string{"a", "x"})

	if got := Classify(FieldChange{Name: "s",
		Old: &FieldDef{Type: NewStringType(), Constraint: old, HasDefault: true, Default: NewValue("a")},
		New: &FieldDef{Type: NewStringType(), Constraint: subset, HasDefault: true, Default: NewValue("a")}}); got != CatTighten {
		t.Fatalf("enum subset = %v, want tighten", got)
	}
	if got := Classify(FieldChange{Name: "s",
		Old: &FieldDef{Type: NewStringType(), Constraint: old, HasDefault: true, Default: NewValue("a")},
		New: &FieldDef{Type: NewStringType(), Constraint: superset, HasDefault: true, Default: NewValue("a")}}); got != CatLoosen {
		t.Fatalf("enum superset = %v, want loosen", got)
	}
	if got := Classify(FieldChange{Name: "s",
		Old: &FieldDef{Type: NewStringType(), Constraint: old, HasDefault: true, Default: NewValue("a")},
		New: &FieldDef{Type: NewStringType(), Constraint: shifted, HasDefault: true, Default: NewValue("a")}}); got != CatMixed {
		t.Fatalf("enum shift = %v, want mixed", got)
	}
}
