package acc

import (
	"math"
	"testing"

	"ontology/row"
)

func TestMerge(t *testing.T) {
	inf := math.Inf(1)
	ninf := math.Inf(-1)
	mk := func(vals ...float64) State {
		var s State
		for _, v := range vals {
			s.Add(v)
		}
		return s
	}
	cases := []struct {
		name             string
		a, b, want       State
		sumBitsAssocWith []float64 // 若给出，a.Merge(b).Sum 须等于 mk(vals).Sum 的位模式
	}{
		{"empty identity", State{}, mk(3, 5), mk(3, 5), nil},
		{"count sum", mk(1, 2), mk(4), State{3, 7, 1, 4}, []float64{1, 2, 4}},
		{"min max extremes", mk(2, 8), mk(-3, 5), State{4, 12, -3, 8}, []float64{2, 8, -3, 5}},
		{"signed zero min", mk(math.Copysign(0, -1)), mk(0), State{2, 0, math.Copysign(0, -1), 0}, []float64{-0.0, 0}},
		{"inf", mk(inf, 1), mk(ninf), State{3, math.NaN(), ninf, inf}, []float64{inf, 1, ninf}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.a
			got.Merge(c.b)
			if got.Count != c.want.Count || got.Min != c.want.Min || got.Max != c.want.Max {
				t.Fatalf("got %+v want %+v", got, c.want)
			}
			if c.name != "inf" && math.Float64bits(got.Sum) != math.Float64bits(c.want.Sum) {
				t.Fatalf("sum bits got %x want %x", math.Float64bits(got.Sum), math.Float64bits(c.want.Sum))
			}
			if c.sumBitsAssocWith != nil {
				if math.Float64bits(got.Sum) != math.Float64bits(mk(c.sumBitsAssocWith...).Sum) {
					t.Fatalf("merge sum bits differ from single pass")
				}
			}
		})
	}
}

func TestAssociativeDomainBracketing(t *testing.T) {
	// 结合域（精确整数，|和|<=2^53）：任意括号划分都与单次累加逐位相同。
	vals := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 1 << 40, -(1 << 40), 100}
	var single State
	for _, v := range vals {
		single.Add(v)
	}
	splits := []int{1, 2, 3, 5, 7, 9, 11}
	for _, sp := range splits {
		l, r := mk(vals[:sp]...), mk(vals[sp:]...)
		var got State
		got.Merge(l)
		got.Merge(r)
		if math.Float64bits(got.Sum) != math.Float64bits(single.Sum) || got != single {
			t.Fatalf("split %d: got %+v want %+v", sp, got, single)
		}
	}
}

func TestEntryCodec(t *testing.T) {
	cases := []struct {
	key string
		st  State
	}{
		{"", State{0, 0, 0, 0}},
		{"k", State{2, 3.5, 1, 2.5}},
		{"中文 longer key", State{7, math.Inf(1), math.Inf(-1), math.Inf(1)}},
	}
	for _, c := range cases {
		b := EncodeEntry(c.key, c.st)
	gotKey, gotSt, err := DecodeEntry(b)
		if err != nil || gotKey != c.key {
			t.Fatalf("codec key %q err %v", gotKey, err)
		}
		if c.st.Count != 0 && gotSt != c.st {
			t.Fatalf("codec state got %+v want %+v", gotSt, c.st)
		}
	}
	// 表驱动行编解码往返
	for _, r := range []row.Row{{"", 0}, {"a", -1.25}, {"键", math.Inf(-1)}} {
		got, err := row.Decode(r.Encode())
		if err != nil || got != r {
			t.Fatalf("row codec got %+v want %+v err %v", got, r, err)
		}
	}
	if _, err := DecodeEntry([]byte{0, 1}); err == nil {
		t.Fatal("expected short error")
	}
}
