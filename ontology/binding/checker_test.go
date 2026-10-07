package binding

import "testing"

func enumField(obj, name string, nullable bool, vals ...string) *FieldDef {
	vs := make([]Value, len(vals))
	for i, v := range vals {
		vs[i] = Value{Raw: v}
	}
	return &FieldDef{ObjectType: obj, Name: name, Type: FieldType{Kind: KindString}, Nullable: nullable, Allowed: vs}
}

func pairs(kv ...string) []ValuePair {
	if len(kv)%2 != 0 {
		panic("pairs must be even")
	}
	out := make([]ValuePair, 0, len(kv)/2)
	for i := 0; i < len(kv); i += 2 {
		out = append(out, ValuePair{Left: Value{Raw: kv[i]}, Right: Value{Raw: kv[i+1]}})
	}
	return out
}

func specFor(dir Direction, mapping []ValuePair, mapMissing bool) BindingSpec {
	return BindingSpec{
		LinkType:   "L",
		LeftObject: "A", LeftField: "f",
		RightObject: "B", RightField: "g",
		Direction: dir,
		Correspondence: Correspondence{
			Mapping: mapping,
			Missing: MissingPolicy{MapMissing: mapMissing},
		},
	}
}

// 双向绑定：双射 / 漏映射 / 碰撞 / 仅单向 的全部唯一性边界。
func TestCheckBidirectionalUniquenessBoundaries(t *testing.T) {
	cases := []struct {
		name    string
		left    []string
		right   []string
		mapping []ValuePair
		want    Verdict
	}{
		{"bijection", []string{"a", "b"}, []string{"x", "y"}, pairs("a", "x", "b", "y"), VerdictCompatible},
		{"partial_forward_missing", []string{"a", "b"}, []string{"x", "y"}, pairs("a", "x"), VerdictNonBijective},
		{"forward_total_reverse_collision", []string{"a", "b"}, []string{"x", "y"}, pairs("a", "x", "b", "x"), VerdictOneWayOnly},
		{"intersection_only", []string{"a", "b"}, []string{"a", "c"}, nil, VerdictNonBijective},
		{"right_larger", []string{"a"}, []string{"x", "y"}, pairs("a", "x"), VerdictOneWayOnly},
		{"left_larger", []string{"a", "b"}, []string{"x"}, pairs("a", "x"), VerdictOneWayOnly},
		{"both_empty_bijection", nil, nil, nil, VerdictCompatible},
		{"empty_left_nonempty_right", nil, []string{"x"}, nil, VerdictOneWayOnly},
		{"duplicate_collision_and_extra", []string{"a", "b", "c"}, []string{"x"}, pairs("a", "x"), VerdictOneWayOnly},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			left := enumField("A", "f", false, tc.left...)
			right := enumField("B", "g", false, tc.right...)
			got := Check(left, right, specFor(Bidirectional, tc.mapping, false))
			if got.Verdict != tc.want {
				t.Fatalf("want %s, got %s (%s); basis=%+v", tc.want, got.Verdict, got.Reason, got.Basis)
			}
		})
	}
}

// 单向绑定：前向全函数即兼容；反向即使成立也不作要求；非全函数不兼容。
func TestCheckUnidirectionalBoundaries(t *testing.T) {
	cases := []struct {
		name    string
		dir     Direction
		left    []string
		right   []string
		mapping []ValuePair
		wantLR  Verdict
		wantRL  Verdict
	}{
		{"surjection_lr", LeftToRight, []string{"a", "b"}, []string{"x"}, pairs("a", "x", "b", "x"), VerdictCompatible, VerdictNonBijective},
		{"non_surjective_rl_valid", RightToLeft, []string{"x"}, []string{"a", "b"}, pairs("x", "a", "x", "b"), VerdictNonBijective, VerdictCompatible},
		{"non_surjective_rl", RightToLeft, []string{"a", "b"}, []string{"x", "y"}, pairs("a", "x"), VerdictNonBijective, VerdictNonBijective},
		{"partial_lr", LeftToRight, []string{"a", "b"}, []string{"x", "y"}, pairs("a", "x"), VerdictNonBijective, VerdictNonBijective},
		{"bijection_either", LeftToRight, []string{"a"}, []string{"x"}, pairs("a", "x"), VerdictCompatible, VerdictCompatible},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			left := enumField("A", "f", false, tc.left...)
			right := enumField("B", "g", false, tc.right...)
			got := Check(left, right, specFor(tc.dir, tc.mapping, false))
			want := tc.wantLR
			if tc.dir == RightToLeft {
				want = tc.wantRL
			}
			if got.Verdict != want {
				t.Fatalf("want %s, got %s (%s)", want, got.Verdict, got.Reason)
			}
		})
	}
}

// 缺失取值参与唯一性判断的全部 nullable × mapMissing 组合。
func TestCheckMissingCombinations(t *testing.T) {
	type key struct {
		leftNull, rightNull, mapMissing bool
		dir                             Direction
	}
	want := map[key]Verdict{}

	dirs := []Direction{Bidirectional, LeftToRight, RightToLeft}
	for _, ln := range []bool{false, true} {
		for _, rn := range []bool{false, true} {
			for _, mm := range []bool{false, true} {
				for _, dir := range dirs {
					left := enumField("A", "f", ln, "a")
					right := enumField("B", "g", rn, "a")
					got := Check(left, right, specFor(dir, nil, mm))
					var expect Verdict
					switch {
					case !ln && !rn:
						// 双方均无缺失取值：恒等双射，与 mapMissing 无关。
						expect = VerdictCompatible
					case ln && rn && mm:
						// 缺失 ⇄ 缺失 被声明确立。
						expect = VerdictCompatible
					case ln && rn && !mm:
						// 缺失被当作独立取值但无对应：双向不成立；
						// 前向、反向都因缺失漏映射而不成立。
						expect = VerdictNonBijective
					case ln != rn:
						// 只有一侧可缺失，缺失在对端无空间可落。
						if dir == Bidirectional {
							if ln {
								expect = VerdictOneWayOnly // 右→左全（右无缺失），左→右漏缺失
							} else {
								expect = VerdictOneWayOnly // 左→右全，右→左漏缺失
							}
						} else if dir == LeftToRight {
							if ln {
								expect = VerdictNonBijective
							} else {
								expect = VerdictCompatible
							}
						} else {
							if rn {
								expect = VerdictNonBijective
							} else {
								expect = VerdictCompatible
							}
						}
					}
					want[key{ln, rn, mm, dir}] = got.Verdict
					if got.Verdict != expect {
						t.Errorf("ln=%v rn=%v mm=%v dir=%s: want %s got %s (%s)",
							ln, rn, mm, dir, expect, got.Verdict, got.Reason)
					}
				}
			}
		}
	}
}

// 仅有交集而非双射，绝不允许放行。
func TestIntersectionIsNotCompatibility(t *testing.T) {
	left := enumField("A", "f", false, "1", "2")
	right := enumField("B", "g", false, "2", "3")
	got := Check(left, right, specFor(Bidirectional, nil, false))
	if got.Verdict == VerdictCompatible {
		t.Fatalf("non-empty intersection must not be treated as bijection: %+v", got)
	}
	if len(got.Basis.UnmappedLeft) == 0 || len(got.Basis.RightWithoutImage) == 0 {
		t.Fatalf("evidence must list unmatched values: %+v", got.Basis)
	}
}

// 异种类型：未声明可比较 => 无法判定；声明可比较 + 显式双射 => 兼容。
func TestIncomparableTypes(t *testing.T) {
	left := &FieldDef{ObjectType: "A", Name: "f", Type: FieldType{Kind: KindString},
		Allowed: []Value{{Raw: "a"}}}
	right := &FieldDef{ObjectType: "B", Name: "g", Type: FieldType{Kind: KindInt},
		Allowed: []Value{{Raw: int64(1)}}}
	got := Check(left, right, specFor(Bidirectional, nil, false))
	if got.Verdict != VerdictIncomparableTypes {
		t.Fatalf("want incomparable, got %s", got.Verdict)
	}

	// 声明可比较后，必须给出显式映射；显式双射成立。
	left.Type.ComparableWith = []TypeKind{KindInt}
	spec := specFor(Bidirectional, []ValuePair{
		{Left: Value{Raw: "a"}, Right: Value{Raw: int64(1)}},
	}, false)
	got2 := Check(left, right, spec)
	if got2.Verdict != VerdictCompatible {
		t.Fatalf("declared comparable kinds with explicit bijection should pass: %s (%s)", got2.Verdict, got2.Reason)
	}
}
