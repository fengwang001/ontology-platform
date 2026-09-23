package acc

import (
	"errors"
	"math"
	"testing"
)

func stateOf(vals ...float64) State {
	var s State
	for _, v := range vals {
		s.Add(v)
	}
	return s
}

func sameBits(a, b State) bool {
	return a.Count == b.Count &&
		math.Float64bits(a.Sum) == math.Float64bits(b.Sum) &&
		math.Float64bits(a.Min) == math.Float64bits(b.Min) &&
		math.Float64bits(a.Max) == math.Float64bits(b.Max)
}

func TestAdd(t *testing.T) {
	cases := []struct {
		name string
		vals []float64
		want State
	}{
		{"空", nil, State{}},
		{"单值", []float64{3}, State{1, 3, 3, 3}},
		{"多值", []float64{1, 2, 3}, State{3, 6, 1, 3}},
		{"负值", []float64{-5, 2, -1}, State{3, -4, -5, 2}},
		{"正Inf", []float64{1, math.Inf(1)}, State{2, math.Inf(1), 1, math.Inf(1)}},
		{"负Inf", []float64{1, math.Inf(-1)}, State{2, math.Inf(-1), math.Inf(-1), 1}},
		{"正负Inf", []float64{math.Inf(1), math.Inf(-1)}, State{2, math.NaN(), math.Inf(-1), math.Inf(1)}},
	}
	for _, c := range cases {
		got := stateOf(c.vals...)
		if got.Count != c.want.Count || got.Min != c.want.Min || got.Max != c.want.Max ||
			(math.Float64bits(got.Sum) != math.Float64bits(c.want.Sum) && !(math.IsNaN(got.Sum) && math.IsNaN(c.want.Sum))) {
			t.Errorf("%s: got %+v want %+v", c.name, got, c.want)
		}
	}
}

func TestMerge(t *testing.T) {
	cases := []struct {
		name string
		a, b State
		want State
	}{
		{"空并空", State{}, State{}, State{}},
		{"空并非空", State{}, stateOf(1, 2), stateOf(1, 2)},
		{"非空并空", stateOf(1, 2), State{}, stateOf(1, 2)},
		{"两段合并", stateOf(1, 2), stateOf(3, 4, 5), State{5, 15, 1, 5}},
		{"含Inf", stateOf(math.Inf(1)), stateOf(1), State{2, math.Inf(1), 1, math.Inf(1)}},
	}
	for _, c := range cases {
		got := c.a
		got.Merge(c.b)
		if !sameBits(got, c.want) {
			t.Errorf("%s: got %+v want %+v", c.name, got, c.want)
		}
	}
}

// TestMergeAssociative 验证整数值下四种聚合的合并均满足结合律：
// (a⊕b)⊕c == a⊕(b⊕c)，这是同一分区多次溢出后按任意分组顺序合并仍正确的基础。
func TestMergeAssociative(t *testing.T) {
	cases := []struct{ a, b, c []float64 }{
		{[]float64{1, 2}, []float64{3}, []float64{4, 5, 6}},
		{[]float64{-7, 100}, []float64{0}, []float64{42}},
		{[]float64{math.Inf(-1)}, []float64{5}, []float64{math.Inf(1)}},
		{[]float64{}, []float64{9}, []float64{}},
	}
	for i, c := range cases {
		left := stateOf(c.a...)
		left.Merge(stateOf(c.b...))
		left.Merge(stateOf(c.c...))
		right := stateOf(c.a...)
		bc := stateOf(c.b...)
		bc.Merge(stateOf(c.c...))
		right.Merge(bc)
		if !sameBits(left, right) {
			t.Errorf("用例%d: (a⊕b)⊕c=%+v != a⊕(b⊕c)=%+v", i, left, right)
		}
	}
}

func TestEncodeDecode(t *testing.T) {
	cases := []struct {
		key string
		st  State
	}{
		{"k", State{3, 6, 1, 3}},
		{"", State{1, -2.5, -2.5, -2.5}},
		{"长键长键长键", State{2, math.Inf(1), math.Inf(-1), math.Inf(1)}},
		{"zero", State{}},
	}
	for _, c := range cases {
		key, st, err := Decode(Encode(c.key, c.st))
		if err != nil || key != c.key || !sameBits(st, c.st) {
			t.Errorf("键%q: 往返失败 key=%q st=%+v err=%v", c.key, key, st, err)
		}
	}
}

func TestDecodeError(t *testing.T) {
	good := Encode("k", State{1, 1, 1, 1})
	cases := []struct {
		name string
		data []byte
	}{
		{"空输入", nil},
		{"键长前缀残缺", []byte{0x80}},
		{"键体残缺", good[:3]},
		{"状态残缺", good[:len(good)-1]},
		{"尾部多余", append(append([]byte{}, good...), 0)},
	}
	for _, c := range cases {
		if _, _, err := Decode(c.data); !errors.Is(err, ErrDecode) {
			t.Errorf("%s: 期望 ErrDecode, 得到 %v", c.name, err)
		}
	}
}
