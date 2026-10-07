package ontology

import "testing"

func enumField(id string, nullable bool, values ...Value) FieldDef {
	return FieldDef{ID: id, Type: values[0].Type, Nullable: nullable, Enum: values}
}

func strs(id string, nullable bool, ss ...string) FieldDef {
	vals := make([]Value, len(ss))
	for i, s := range ss {
		vals[i] = StrValue(s)
	}
	return enumField(id, nullable, vals...)
}

func identityDecl(dir Direction, missing MissingPolicyKind) BindingDecl {
	return BindingDecl{
		LinkTypeID:     "lt",
		Left:           FieldRef{"A", "fa"},
		Right:          FieldRef{"B", "fb"},
		Direction:      dir,
		Correspondence: Correspondence{Kind: Identity},
		Missing:        MissingPolicy{Kind: missing},
	}
}

func explicitDecl(dir Direction, pairs ...ValuePair) BindingDecl {
	d := identityDecl(dir, MissingForbidden)
	d.Correspondence = Correspondence{Kind: Explicit, Pairs: pairs}
	return d
}

// 双向声明下对应关系唯一性的全部边界组合。
func TestCheckTwoWayUniquenessBoundaries(t *testing.T) {
	cases := []struct {
		name     string
		decl     BindingDecl
		left     FieldDef
		right    FieldDef
		wantKind IncompatKind
		wantL2R  bool
		wantR2L  bool
	}{
		{
			name:     "identity bijection over equal enums",
			decl:     identityDecl(TwoWay, MissingForbidden),
			left:     strs("fa", false, "a", "b"),
			right:    strs("fb", false, "a", "b"),
			wantKind: Compatible, wantL2R: true, wantR2L: true,
		},
		{
			name:     "left enum is strict subset: only left-to-right holds",
			decl:     identityDecl(TwoWay, MissingForbidden),
			left:     strs("fa", false, "a"),
			right:    strs("fb", false, "a", "b"),
			wantKind: IncompatDeclaredTwoWayOnlyOneWay, wantL2R: true, wantR2L: false,
		},
		{
			name:     "right enum is strict subset: only right-to-left holds",
			decl:     identityDecl(TwoWay, MissingForbidden),
			left:     strs("fa", false, "a", "b"),
			right:    strs("fb", false, "a"),
			wantKind: IncompatDeclaredTwoWayOnlyOneWay, wantL2R: false, wantR2L: true,
		},
		{
			name: "explicit many-to-one is not injective",
			decl: explicitDecl(TwoWay,
				ValuePair{StrValue("a"), StrValue("x")},
				ValuePair{StrValue("b"), StrValue("x")},
			),
			left:     strs("fa", false, "a", "b"),
			right:    strs("fb", false, "x", "y"),
			wantKind: IncompatDeclaredTwoWayOnlyOneWay, wantL2R: true, wantR2L: false,
		},
		{
			name: "explicit mapping leaves a left value unmapped",
			decl: explicitDecl(TwoWay,
				ValuePair{StrValue("a"), StrValue("x")},
			),
			left:  strs("fa", false, "a", "b"),
			right: strs("fb", false, "x"),
			// 正向有左值无对应，但反向 x 唯一对应 a。
			wantKind: IncompatDeclaredTwoWayOnlyOneWay, wantL2R: false, wantR2L: true,
		},
		{
			name: "explicit conflicting targets for same left value",
			decl: explicitDecl(TwoWay,
				ValuePair{StrValue("a"), StrValue("x")},
				ValuePair{StrValue("a"), StrValue("y")},
			),
			left:  strs("fa", false, "a"),
			right: strs("fb", false, "x", "y"),
			// 正向不是函数，但反向每个右值都有唯一左值对应。
			wantKind: IncompatDeclaredTwoWayOnlyOneWay, wantL2R: false, wantR2L: true,
		},
		{
			name:     "non-empty intersection without bijection is still incompatible",
			decl:     identityDecl(TwoWay, MissingForbidden),
			left:     strs("fa", false, "a", "b"),
			right:    strs("fb", false, "b", "c"),
			wantKind: IncompatNotBijective, wantL2R: false, wantR2L: false,
		},
		{
			name: "explicit bijection across different types",
			decl: explicitDecl(TwoWay,
				ValuePair{IntValue(1), StrValue("one")},
				ValuePair{IntValue(2), StrValue("two")},
			),
			left:     enumField("fa", false, IntValue(1), IntValue(2)),
			right:    strs("fb", false, "one", "two"),
			wantKind: Compatible, wantL2R: true, wantR2L: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := CheckBinding(tc.decl, &tc.left, &tc.right)
			if res.Kind != tc.wantKind {
				t.Fatalf("kind = %v, want %v (detail: %s, notes: %v)", res.Kind, tc.wantKind, res.Detail, res.Basis.Notes)
			}
			if res.LeftToRightOK != tc.wantL2R || res.RightToLeftOK != tc.wantR2L {
				t.Fatalf("directions = (%v, %v), want (%v, %v)", res.LeftToRightOK, res.RightToLeftOK, tc.wantL2R, tc.wantR2L)
			}
		})
	}
}

// 单向声明下只要求声明方向唯一可对应。
func TestCheckOneWayBoundaries(t *testing.T) {
	cases := []struct {
		name     string
		decl     BindingDecl
		left     FieldDef
		right    FieldDef
		wantKind IncompatKind
	}{
		{
			name:     "left-to-right holds even though reverse fails",
			decl:     identityDecl(LeftToRight, MissingForbidden),
			left:     strs("fa", false, "a"),
			right:    strs("fb", false, "a", "b"),
			wantKind: Compatible,
		},
		{
			name: "left-to-right tolerates many-to-one",
			decl: func() BindingDecl {
				d := explicitDecl(LeftToRight,
					ValuePair{StrValue("a"), StrValue("x")},
					ValuePair{StrValue("b"), StrValue("x")},
				)
				return d
			}(),
			left:     strs("fa", false, "a", "b"),
			right:    strs("fb", false, "x"),
			wantKind: Compatible,
		},
		{
			name:     "left-to-right fails when a left value has no counterpart",
			decl:     identityDecl(LeftToRight, MissingForbidden),
			left:     strs("fa", false, "a", "b"),
			right:    strs("fb", false, "a"),
			wantKind: IncompatNotBijective,
		},
		{
			name:     "right-to-left holds even though forward fails",
			decl:     identityDecl(RightToLeft, MissingForbidden),
			left:     strs("fa", false, "a", "b"),
			right:    strs("fb", false, "a"),
			wantKind: Compatible,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := CheckBinding(tc.decl, &tc.left, &tc.right)
			if res.Kind != tc.wantKind {
				t.Fatalf("kind = %v, want %v (detail: %s)", res.Kind, tc.wantKind, res.Detail)
			}
		})
	}
}

// 缺失取值作为独立取值参与对应关系判断的全部组合。
func TestCheckMissingValueCombinations(t *testing.T) {
	cases := []struct {
		name     string
		decl     BindingDecl
		left     FieldDef
		right    FieldDef
		wantKind IncompatKind
	}{
		{
			name:     "both nullable, missing-to-missing, equal enums",
			decl:     identityDecl(TwoWay, MissingToMissing),
			left:     strs("fa", true, "a"),
			right:    strs("fb", true, "a"),
			wantKind: Compatible,
		},
		{
			name:     "left nullable, right not: left missing has no counterpart",
			decl:     identityDecl(TwoWay, MissingToMissing),
			left:     strs("fa", true, "a"),
			right:    strs("fb", false, "a"),
			wantKind: IncompatDeclaredTwoWayOnlyOneWay,
		},
		{
			name:     "right nullable, left not: right missing has no counterpart",
			decl:     identityDecl(TwoWay, MissingToMissing),
			left:     strs("fa", false, "a"),
			right:    strs("fb", true, "a"),
			wantKind: IncompatDeclaredTwoWayOnlyOneWay,
		},
		{
			name:     "missing forbidden and neither side nullable",
			decl:     identityDecl(TwoWay, MissingForbidden),
			left:     strs("fa", false, "a"),
			right:    strs("fb", false, "a"),
			wantKind: Compatible,
		},
		{
			name:     "missing forbidden but field is nullable: missing participates and breaks uniqueness",
			decl:     identityDecl(TwoWay, MissingForbidden),
			left:     strs("fa", true, "a"),
			right:    strs("fb", true, "a"),
			wantKind: IncompatNotBijective,
		},
		{
			name: "missing-to-value with a free target value",
			decl: func() BindingDecl {
				d := identityDecl(TwoWay, MissingToValue)
				d.Missing.LeftMissingTo = StrValue("b")
				return d
			}(),
			left:     strs("fa", true, "a"),
			right:    strs("fb", false, "a", "b"),
			wantKind: Compatible,
		},
		{
			name: "missing-to-value colliding with an existing pair",
			decl: func() BindingDecl {
				d := identityDecl(TwoWay, MissingToValue)
				d.Missing.LeftMissingTo = StrValue("a")
				return d
			}(),
			left:     strs("fa", true, "a"),
			right:    strs("fb", false, "a"),
			wantKind: IncompatDeclaredTwoWayOnlyOneWay,
		},
		{
			name: "explicit pairs covering missing on both sides",
			decl: func() BindingDecl {
				d := explicitDecl(TwoWay,
					ValuePair{StrValue("a"), StrValue("x")},
					ValuePair{MissingValue(), MissingValue()},
				)
				d.Missing = MissingPolicy{Kind: MissingToMissing}
				return d
			}(),
			left:     strs("fa", true, "a"),
			right:    strs("fb", true, "x"),
			wantKind: Compatible,
		},
		{
			name: "explicit pairs forget missing: missing is still checked",
			decl: explicitDecl(TwoWay,
				ValuePair{StrValue("a"), StrValue("x")},
			),
			left:     strs("fa", true, "a"),
			right:    strs("fb", true, "x"),
			wantKind: IncompatNotBijective,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := CheckBinding(tc.decl, &tc.left, &tc.right)
			if res.Kind != tc.wantKind {
				t.Fatalf("kind = %v, want %v (detail: %s, notes: %v)", res.Kind, tc.wantKind, res.Detail, res.Basis.Notes)
			}
		})
	}
}

// 类型无法比较与字段删除的判定。
func TestCheckIncomparableAndDeleted(t *testing.T) {
	t.Run("identity across different types is undecidable", func(t *testing.T) {
		left := enumField("fa", false, IntValue(1))
		right := strs("fb", false, "1")
		res := CheckBinding(identityDecl(TwoWay, MissingForbidden), &left, &right)
		if res.Kind != IncompatIncomparableTypes {
			t.Fatalf("kind = %v, want %v", res.Kind, IncompatIncomparableTypes)
		}
	})
	t.Run("explicit mapping over unbounded domain is undecidable", func(t *testing.T) {
		left := FieldDef{ID: "fa", Type: TString} // 无界
		right := strs("fb", false, "a")
		decl := explicitDecl(TwoWay, ValuePair{StrValue("a"), StrValue("a")})
		res := CheckBinding(decl, &left, &right)
		if res.Kind != IncompatIncomparableTypes {
			t.Fatalf("kind = %v, want %v", res.Kind, IncompatIncomparableTypes)
		}
	})
	t.Run("unbounded identity both sides same type is bijective", func(t *testing.T) {
		left := FieldDef{ID: "fa", Type: TString}
		right := FieldDef{ID: "fb", Type: TString}
		res := CheckBinding(identityDecl(TwoWay, MissingForbidden), &left, &right)
		if res.Kind != Compatible {
			t.Fatalf("kind = %v, want %v", res.Kind, Compatible)
		}
	})
	t.Run("unbounded to finite fails forward coverage", func(t *testing.T) {
		left := FieldDef{ID: "fa", Type: TString}
		right := strs("fb", false, "a")
		res := CheckBinding(identityDecl(TwoWay, MissingForbidden), &left, &right)
		if res.Kind != IncompatDeclaredTwoWayOnlyOneWay || res.LeftToRightOK {
			t.Fatalf("kind = %v l2r=%v, want declared-two-way-only-one-way with l2r=false", res.Kind, res.LeftToRightOK)
		}
	})
	t.Run("deleted field takes priority over every other verdict", func(t *testing.T) {
		right := strs("fb", false, "a")
		// 即便类型本身无法比较，字段删除也必须优先判定。
		res := CheckBinding(identityDecl(TwoWay, MissingForbidden), nil, &right)
		if res.Kind != IncompatFieldDeleted {
			t.Fatalf("kind = %v, want %v", res.Kind, IncompatFieldDeleted)
		}
		res = CheckBinding(identityDecl(TwoWay, MissingForbidden), &right, nil)
		if res.Kind != IncompatFieldDeleted {
			t.Fatalf("kind = %v, want %v", res.Kind, IncompatFieldDeleted)
		}
	})
}
