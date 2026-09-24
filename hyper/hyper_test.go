package hyper_test

import (
	"errors"
	"math"
	"testing"

	"ontology/hyper"
	"ontology/vec"
)

func TestVec(t *testing.T) {
	cases := []struct {
		name string
		a, b vec.Vec
		dim  int
		dot  float64
		dist float64
		err  error
	}{
		{name: "正交", a: vec.Vec{1, 0}, b: vec.Vec{0, 1}, dim: 2, dot: 0, dist: math.Sqrt2},
		{name: "一般", a: vec.Vec{1, 2, 3}, b: vec.Vec{4, 5, 6}, dim: 3, dot: 32, dist: math.Sqrt(27)},
		{name: "一维", a: vec.Vec{3}, b: vec.Vec{-1}, dim: 1, dot: -3, dist: 4},
		{name: "零向量", a: vec.Vec{0, 0}, b: vec.Vec{0, 0}, dim: 2, dot: 0, dist: 0},
		{name: "维度不符", a: vec.Vec{1, 2}, dim: 3, err: vec.ErrDim},
		{name: "含NaN", a: vec.Vec{1, math.NaN()}, dim: 2, err: vec.ErrNaN},
		{name: "含正Inf", a: vec.Vec{math.Inf(1)}, dim: 1, err: vec.ErrInf},
		{name: "含负Inf", a: vec.Vec{math.Inf(-1)}, dim: 1, err: vec.ErrInf},
	}
	for _, c := range cases {
		if err := vec.Check(c.a, c.dim); !errors.Is(err, c.err) {
			t.Errorf("%s: Check err=%v want %v", c.name, err, c.err)
		}
		if c.err != nil {
			continue
		}
		if d := vec.Dot(c.a, c.b); d != c.dot {
			t.Errorf("%s: Dot=%v want %v", c.name, d, c.dot)
		}
		if d := vec.Dist(c.a, c.b); d != c.dist {
			t.Errorf("%s: Dist=%v want %v", c.name, d, c.dist)
		}
	}
}

func TestFamily(t *testing.T) {
	const dim, bits, n = 16, 8, 500
	r := randSource(99)
	vs := make([]vec.Vec, n)
	for i := range vs {
		v := make(vec.Vec, dim)
		for j := range v {
			v[j] = r()
		}
		vs[i] = v
	}
	cases := []struct {
		name      string
		seedA     int64
		seedB     int64
		wantEqual bool
	}{
		{name: "同种子逐字节相同", seedA: 42, seedB: 42, wantEqual: true},
		{name: "不同种子有差异", seedA: 42, seedB: 43, wantEqual: false},
	}
	for _, c := range cases {
		fa, fb := hyper.New(dim, bits, c.seedA), hyper.New(dim, bits, c.seedB)
		equal := true
		for _, v := range vs {
			if fa.Signature(v) != fb.Signature(v) {
				equal = false
			}
		}
		if equal != c.wantEqual {
			t.Errorf("%s: equal=%v want %v", c.name, equal, c.wantEqual)
		}
	}
	if got := hyper.New(dim, bits, 1).Signature(make(vec.Vec, dim)); got != 0 {
		t.Errorf("零向量签名=%b 应为全 0", got)
	}
	// 前缀性质：同种子下 8 位签名是 12 位签名的低 8 位（候选随 b 单调的构造保证）。
	f8, f12 := hyper.New(dim, 8, 5), hyper.New(dim, 12, 5)
	for _, v := range vs {
		if f8.Signature(v) != f12.Signature(v)&0xff {
			t.Fatal("bits=8 签名应为 bits=12 的低 8 位")
		}
	}
	if f8.Hashes() != int64(n*8) || f12.Hashes() != int64(n*12) {
		t.Errorf("哈希计数 %d/%d 应为 %d/%d", f8.Hashes(), f12.Hashes(), n*8, n*12)
	}
}

// randSource 返回可复现的伪随机数生成器（避免测试间共享状态）。
func randSource(seed int64) func() float64 {
	x := uint64(seed)*2685821657736338717 + 1
	return func() float64 {
		x ^= x << 13
		x ^= x >> 7
		x ^= x << 17
		return float64(x%2000000)/1000000 - 1
	}
}
