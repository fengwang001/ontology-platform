package compat

import "testing"

func TestVersionCompare(t *testing.T) {
	cases := []struct {
		a, b Version
		want int
	}{
		{v(1, 0, 0), v(1, 0, 0), 0},
		{v(1, 0, 0), v(2, 0, 0), -1},
		{v(2, 0, 0), v(1, 9, 9), 1},
		{v(1, 2, 0), v(1, 10, 0), -1},
		{v(1, 2, 3), v(1, 2, 4), -1},
		{v(1, 2, 5), v(1, 2, 5), 0},
	}
	for _, c := range cases {
		if got := Compare(c.a, c.b); got != c.want {
			t.Errorf("Compare(%v, %v) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestRangeContainsBoundaries(t *testing.T) {
	r := Range{Min: v(1, 2, 0), Max: v(2, 0, 0)}
	cases := []struct {
		v    Version
		want bool
	}{
		{v(1, 2, 0), true},  // 恰好下边界
		{v(2, 0, 0), true},  // 恰好上边界
		{v(1, 1, 9), false}, // 下边界之外
		{v(2, 0, 1), false}, // 上边界之外
		{v(1, 5, 3), true},  // 内部
		{v(0, 9, 9), false},
		{v(3, 0, 0), false},
	}
	for _, c := range cases {
		if got := r.Contains(c.v); got != c.want {
			t.Errorf("Range[%v,%v].Contains(%v) = %v, want %v", r.Min, r.Max, c.v, got, c.want)
		}
	}
}

func TestTypeSpecContains(t *testing.T) {
	bounded := intType(FloatPtr(0), FloatPtr(10))
	cases := []struct {
		ts   TypeSpec
		val  any
		want bool
	}{
		{strType(), "x", true},
		{strType(), 1, false},
		{TypeSpec{Kind: KindBoolean}, true, true},
		{bounded, 5, true},
		{bounded, 5.0, true},
		{bounded, 5.5, false}, // 非整数
		{bounded, 0, true},    // 边界内
		{bounded, 10, true},
		{bounded, 11, false},
		{bounded, -1, false},
		{TypeSpec{Kind: KindFloat, Min: FloatPtr(0)}, 3.14, true},
		{TypeSpec{Kind: KindFloat, Min: FloatPtr(0)}, -0.1, false},
		{TypeSpec{Kind: KindEnum, Enum: []string{"a", "b"}}, "a", true},
		{TypeSpec{Kind: KindEnum, Enum: []string{"a", "b"}}, "c", false},
		{TypeSpec{Kind: KindEnum, Enum: []string{"a", "b"}}, 1, false},
	}
	for _, c := range cases {
		if got := c.ts.Contains(c.val); got != c.want {
			t.Errorf("%+v.Contains(%v) = %v, want %v", c.ts, c.val, got, c.want)
		}
	}
}

func TestSubsetOf(t *testing.T) {
	narrow := intType(FloatPtr(0), FloatPtr(10))
	wide := intType(FloatPtr(0), FloatPtr(100))
	unbounded := intType(nil, nil)
	cases := []struct {
		a, b TypeSpec
		want bool
	}{
		{narrow, wide, true},   // 收紧后的 ⊆ 收紧前的
		{wide, narrow, false},  // 收紧方向不可判定为子集
		{narrow, narrow, true}, // 自反
		{narrow, unbounded, true},
		{unbounded, narrow, false},
		{strType(), strType(), true},
		{strType(), intType(nil, nil), false},
		{intType(FloatPtr(0), FloatPtr(5)), TypeSpec{Kind: KindFloat, Min: FloatPtr(0), Max: FloatPtr(10)}, true}, // Integer ⊆ Float
		{TypeSpec{Kind: KindFloat}, intType(nil, nil), false},
		{TypeSpec{Kind: KindEnum, Enum: []string{"a"}}, TypeSpec{Kind: KindEnum, Enum: []string{"a", "b"}}, true},
		{TypeSpec{Kind: KindEnum, Enum: []string{"a", "c"}}, TypeSpec{Kind: KindEnum, Enum: []string{"a", "b"}}, false},
	}
	for _, c := range cases {
		if got := SubsetOf(c.a, c.b); got != c.want {
			t.Errorf("SubsetOf(%+v, %+v) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
