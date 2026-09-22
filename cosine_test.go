package ontology

import (
	"errors"
	"math"
	"testing"
)

func TestCosineZeroNormError(t *testing.T) {
	nonZero := Vector{{0, 1}}
	allZero := Vector{{0, 0}, {5, 0}}

	cases := []struct {
		name string
		a, b Vector
		want int
	}{
		{"两者皆空", nil, nil, 3},
		{"两者皆全零", allZero, allZero, 3},
		{"第一个为空", nil, nonZero, 1},
		{"第二个为空", nonZero, nil, 2},
		{"第一个全零", allZero, nonZero, 1},
		{"第二个全零", nonZero, allZero, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _, err := Cosine(c.a, c.b)
			var zn *ZeroNormError
			if !errors.As(err, &zn) {
				t.Fatalf("期望 *ZeroNormError，得到 %v（值 %v）", err, got)
			}
			if zn.Which != c.want {
				t.Fatalf("ZeroNormError.Which = %d, want %d", zn.Which, c.want)
			}
			if math.IsNaN(got) {
				t.Fatal("不应把 NaN 交给调用方")
			}
		})
	}
}

func TestCosineIdenticalIsExactlyOne(t *testing.T) {
	// 模平方为 3，sqrt(3)^2 存在舍入，朴素算法会给 1.0000000000000002。
	a := Vector{{0, 1}, {1, 1}, {2, 1}}
	got, _, err := Cosine(a, a)
	if err != nil {
		t.Fatalf("Cosine 出错: %v", err)
	}
	if got != 1 || math.Float64bits(got) != math.Float64bits(1) {
		t.Fatalf("相同向量余弦必须精确为 1，得到 %v", got)
	}

	// 内容相同但为不同切片，同样精确为 1。
	b := Vector{{0, 1}, {1, 1}, {2, 1}}
	got, _, err = Cosine(a, b)
	if err != nil {
		t.Fatalf("Cosine 出错: %v", err)
	}
	if got != 1 {
		t.Fatalf("内容相同的向量余弦必须精确为 1，得到 %v", got)
	}
}

func TestCosineKnownValue(t *testing.T) {
	a := Vector{{0, 3}, {1, 4}}
	b := Vector{{0, 4}, {1, 3}}
	got, _, err := Cosine(a, b)
	if err != nil {
		t.Fatalf("Cosine 出错: %v", err)
	}
	if want := 24.0 / 25.0; got != want {
		t.Fatalf("got %v, want %v", got, want)
	}

	// 正交向量余弦为 0。
	c := Vector{{0, 1}}
	d := Vector{{1, 1}}
	got, _, err = Cosine(c, d)
	if err != nil {
		t.Fatalf("Cosine 出错: %v", err)
	}
	if got != 0 {
		t.Fatalf("正交向量余弦应为 0，得到 %v", got)
	}

	// 反向向量余弦为 -1。
	e := Vector{{0, 1}, {1, -2}}
	f := Vector{{0, -1}, {1, 2}}
	got, _, err = Cosine(e, f)
	if err != nil {
		t.Fatalf("Cosine 出错: %v", err)
	}
	if got != -1 {
		t.Fatalf("反向向量余弦应为 -1，得到 %v", got)
	}
}

func TestCosineClampedToUnitRange(t *testing.T) {
	// 构造多组近乎平行的向量，结果必须落在 [-1, 1]。
	seed := uint64(99)
	next := func() float64 {
		seed = seed*6364136223846793005 + 1442695040888963407
		return float64(seed>>33) / float64(1<<31)
	}
	for round := 0; round < 500; round++ {
		var a, b Vector
		for i := uint32(0); i < 6; i++ {
			base := next() * 100
			a = append(a, Element{i, base})
			// b 与 a 仅有微小扰动，极易因浮点误差越界。
			b = append(b, Element{i, base*(1+1e-16*(next()-0.5))})
		}
		got, _, err := Cosine(a, b)
		if err != nil {
			t.Fatalf("第 %d 轮 Cosine 出错: %v", round, err)
		}
		if got < -1 || got > 1 {
			t.Fatalf("第 %d 轮结果越界: %v", round, got)
		}
	}
}

func TestCosineNonFiniteError(t *testing.T) {
	cases := []struct {
		name string
		a, b Vector
	}{
		{"点积为无穷", Vector{{0, math.Inf(1)}}, Vector{{0, 1}}},
		{"点积为NaN", Vector{{0, math.Inf(1)}, {1, math.Inf(1)}},
			Vector{{0, 1}, {1, -1}}},
		{"模溢出", Vector{{0, 1e308}, {1, 1e308}}, Vector{{0, 1}, {1, 1}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _, err := Cosine(c.a, c.b)
			var nf *NonFiniteError
			if !errors.As(err, &nf) {
				t.Fatalf("期望 *NonFiniteError，得到 %v（值 %v）", err, got)
			}
			if math.IsNaN(got) {
				t.Fatal("不应把 NaN 交给调用方")
			}
		})
	}
}

func TestCosineRepeatDeterministic(t *testing.T) {
	a := Vector{{0, 1e16}, {2, 1}, {5, -1e16}, {8, 3.5}}
	b := Vector{{0, 1}, {2, -2}, {5, 1}, {8, 0.5}}
	first, stFirst, err := Cosine(a, b)
	if err != nil {
		t.Fatalf("Cosine 出错: %v", err)
	}
	for i := 0; i < 1000; i++ {
		got, st, err := Cosine(a, b)
		if err != nil {
			t.Fatalf("第 %d 次 Cosine 出错: %v", i, err)
		}
		if math.Float64bits(got) != math.Float64bits(first) || st != stFirst {
			t.Fatalf("第 %d 次结果不一致", i)
		}
	}
}
