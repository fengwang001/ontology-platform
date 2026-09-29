package ontology_test

import (
	"math"
	"math/rand/v2"
	"testing"

	"ontology/bitpack"
	"ontology/dict"
	"ontology/zone"
)

func TestBitpack(t *testing.T) {
	widths := []uint8{1, 2, 7, 8, 9, 31, 32, 33, 63, 64}
	sizes := []int{0, 1, 2, 3, 7, 8, 9, 15, 63, 64, 65, 100, 127, 128, 129, 1000}
	rng := rand.New(rand.NewPCG(1, 2))
	for _, w := range widths {
		for _, n := range sizes {
			vals := make([]uint64, n)
			max := uint64(math.MaxUint64)
			if w < 64 {
				max = 1<<w - 1
			}
			for i := range vals {
				switch i % 4 {
				case 0:
				case 1:
					vals[i] = max
				case 2:
					vals[i] = max >> 1
				default:
					if max == math.MaxUint64 {
						vals[i] = rng.Uint64()
					} else {
						vals[i] = rng.Uint64N(max + 1)
					}
				}
			}
			data, err := bitpack.Pack(vals, w)
			if err != nil {
				t.Fatalf("Pack w=%d n=%d: %v", w, n, err)
			}
			if len(data) != bitpack.PackedLen(n, w) {
				t.Fatalf("PackedLen w=%d n=%d", w, n)
			}
			if n > 0 {
				if used := (n * int(w)) % 8; used != 0 {
					data[len(data)-1] |= byte(0xFF << used) // 污染末尾补位
				}
			}
			back, err := bitpack.Unpack(data, n, w)
			if err != nil {
				t.Fatalf("Unpack w=%d n=%d: %v", w, n, err)
			}
			for i, v := range vals {
				if back[i] != v {
					t.Fatalf("w=%d n=%d idx=%d got %d want %d", w, n, i, back[i], v)
				}
			}
			if len(data) > 0 {
				if _, err := bitpack.Unpack(data[:len(data)-1], n, w); err == nil {
					t.Fatalf("short buffer accepted w=%d n=%d", w, n)
				}
			}
		}
	}
	for _, v := range []int64{0, 1, -1, math.MaxInt64, math.MinInt64, 123456789, -123456789} {
		if got := bitpack.ZigDecode(bitpack.ZigEncode(v)); got != v {
			t.Fatalf("zigzag %d -> %d", v, got)
		}
	}
	if bitpack.WidthFor(math.MaxUint64) != 64 || bitpack.WidthFor(0) != 1 ||
		bitpack.WidthFor(256) != 9 {
		t.Fatal("WidthFor wrong")
	}
	for _, w := range []uint8{0, 65} {
		if _, err := bitpack.Pack(nil, w); err != bitpack.ErrBadWidth {
			t.Fatalf("bad width %d: %v", w, err)
		}
	}
	if _, err := bitpack.Pack([]uint64{2}, 1); err != bitpack.ErrBadWidth {
		t.Fatal("overflow accepted")
	}
}

func TestZone(t *testing.T) {
	// 位图：行 1 为 NULL。
	present := []uint64{0b1101} // 行0,2,3 存在，行1 NULL
	s := zone.FromValues([]int64{10, 999, 0, 20}, present)
	if s.NullCount != 1 || s.Min != 0 || s.Max != 20 {
		t.Fatalf("stats: %+v", s)
	}
	allNull := zone.FromValues([]int64{1, 2}, []uint64{0})
	if allNull.HasMin || allNull.HasMax || allNull.NullCount != 2 {
		t.Fatalf("all-null stats: %+v", allNull)
	}
	cases := []struct {
		f    zone.Filter
		keep bool
	}{
		{zone.Filter{{Op: zone.OpEq, V: 20}}, true},   // =max 不能排除
		{zone.Filter{{Op: zone.OpEq, V: 0}}, true},    // =min
		{zone.Filter{{Op: zone.OpEq, V: 21}}, false},  // >max
		{zone.Filter{{Op: zone.OpEq, V: -1}}, false},  // <min
		{zone.Filter{{Op: zone.OpGt, V: 0}}, true},    // >min 保留
		{zone.Filter{{Op: zone.OpGt, V: 20}}, false},  // >max 排除
		{zone.Filter{{Op: zone.OpLt, V: 20}}, true},    // <max 保留
		{zone.Filter{{Op: zone.OpLt, V: 0}}, false},    // <min 排除
		{zone.Filter{{Op: zone.OpIn, In: []int64{100, 0}}}, true},
		{zone.Filter{{Op: zone.OpIn, In: []int64{-5, 21}}}, false},
	}
	for i, c := range cases {
		if got := c.f.Keeps(s); got != c.keep {
			t.Fatalf("case %d keeps=%v want %v", i, got, c.keep)
		}
	}
	// IS NULL / IS NOT NULL 对全空组与普通组。
	if (zone.Filter{{Op: zone.OpIsNull}}).Keeps(allNull) != true {
		t.Fatal("isnull allnull")
	}
	if (zone.Filter{{Op: zone.OpGe, V: 0}}).Keeps(allNull) != false {
		t.Fatal("numeric predicate must skip all-null group")
	}
	// NULL 行：数值谓词全 false，只 IS NULL 命中。
	numF := zone.Filter{{Op: zone.OpEq, V: 999}, {Op: zone.OpGt, V: -1 << 62},
		{Op: zone.OpLe, V: 1 << 62}, {Op: zone.OpIn, In: []int64{999}}}
	for _, f := range []zone.Filter{
		{{Op: zone.OpEq, V: 0}}, {{Op: zone.OpGt, V: -1 << 62}},
		{{Op: zone.OpIn, In: []int64{0}}}, numF,
	} {
		if f.Match(0, false) {
			t.Fatalf("null matched numeric predicate %+v", f)
		}
	}
	if !(zone.Filter{{Op: zone.OpIsNull}}).Match(0, false) {
		t.Fatal("null must match IS NULL")
	}
	if (zone.Filter{{Op: zone.OpIsNull}}).Match(0, true) {
		t.Fatal("present must not match IS NULL")
	}
	if !(zone.Filter{{Op: zone.OpIsNotNull}}).Match(0, true) {
		t.Fatal("present must match IS NOT NULL")
	}
	// AND 组合与贴边边界：x>=10 AND x<=20。
	rngF := zone.Filter{{Op: zone.OpGe, V: 10}, {Op: zone.OpLe, V: 20}}
	for _, c := range []struct {
		v    int64
		want bool
	}{{9, false}, {10, true}, {20, true}, {21, false}} {
		if rngF.Match(c.v, true) != c.want {
			t.Fatalf("range %d", c.v)
		}
	}
	if rngF.Keeps(s) != true {
		t.Fatal("range keeps")
	}
}

func TestDict(t *testing.T) {
	// int64 路径：含 0、负数、重复值。
	vals := []int64{0, 0, -7, 1 << 62, -7, 0}
	m, codes, err := dict.Build(vals, 0)
	if err != nil {
		t.Fatal(err)
	}
	if m.Len() != 3 {
		t.Fatalf("card=%d", m.Len())
	}
	back, err := m.Decode(codes)
	if err != nil || len(back) != len(vals) {
		t.Fatalf("decode: %v", err)
	}
	for i, v := range vals {
		if back[i] != v {
			t.Fatalf("idx=%d got %d want %d", i, back[i], v)
		}
	}
	if c, ok := m.Code(-7); !ok || c != 1 {
		t.Fatalf("code -7 = %d,%v", c, ok)
	}
	if _, ok := m.Code(999); ok {
		t.Fatal("missing value found")
	}
	if _, err := m.Lookup(99); err != dict.ErrBadCode {
		t.Fatalf("bad code: %v", err)
	}
	// 字符串路径：空串是合法存在值，与“不存在”可判定（三态的字典层基础）。
	sm, _, err := dict.Build([]string{"", "x", ""}, 0)
	if err != nil {
		t.Fatal(err)
	}
	c, ok := sm.Code("")
	if !ok || c != 0 {
		t.Fatalf("empty string code=%d ok=%v", c, ok)
	}
	if _, ok := sm.Code("absent"); ok {
		t.Fatal("absent string found")
	}
	// 基数上限：超限报错且不返回半成品。
	if _, _, err := dict.Build([]int64{1, 2, 3}, 2); err != dict.ErrDictLimit {
		t.Fatalf("limit: %v", err)
	}
	// 码字位宽可配合 bitpack 往返。
	packed, err := bitpack.Pack(codes, bitpack.WidthFor(uint64(m.Len()-1)))
	if err != nil {
		t.Fatal(err)
	}
	c2, err := bitpack.Unpack(packed, len(codes), bitpack.WidthFor(uint64(m.Len()-1)))
	if err != nil {
		t.Fatal(err)
	}
	b2, err := m.Decode(c2)
	if err != nil {
		t.Fatal(err)
	}
	for i, v := range vals {
		if b2[i] != v {
			t.Fatalf("packed dict idx=%d", i)
		}
	}
}
