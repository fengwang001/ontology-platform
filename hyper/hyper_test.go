package hyper

import (
	"errors"
	"testing"

	"ontology/vec"
)

func TestSignatureRule(t *testing.T) {
	f := NewFamily(2, 1, 3, 42)
	cases := []struct {
		name string
		x    []float64
		want uint64
	}{
		// 第 0 位法向量：直接构造族并按定义核对零向量签名。
		{"zero", []float64{0, 0}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := f.Signature(0, c.x)
			if err != nil || got != c.want {
				t.Fatalf("sig=%d err=%v want %d", got, err, c.want)
			}
		})
	}
	// 签名必须与「内积>0 置位、≤0 清位」定义逐位一致。
	x := []float64{0.3, -1.2}
	var want uint64
	for i, n := range f.Normals()[0] {
		d, _ := vec.Dot(x, n)
		if d > 0 {
			want |= 1 << uint(i)
		}
	}
	got, _ := f.Signature(0, x)
	if got != want {
		t.Fatalf("sig=%d want %d", got, want)
	}
}

func TestReproducible(t *testing.T) {
	type rep struct {
		name  string
		seedA int64
		seedB int64
		same  bool
	}
	cases := []rep{
		{"same-seed", 7, 7, true},
		{"diff-seed", 7, 8, false},
	}
	dim, L, b := 5, 3, 7
	data := makeData(dim, 20)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fa := NewFamily(dim, L, b, c.seedA)
			fb := NewFamily(dim, L, b, c.seedB)
			for _, v := range data {
				sa, _ := fa.IndexSignatures(v)
				sb, _ := fb.IndexSignatures(v)
				eq := true
				for k := range sa {
					if sa[k] != sb[k] {
						eq = false
					}
				}
				if eq != c.same {
					t.Fatalf("sigs equal=%v want %v", eq, c.same)
				}
			}
		})
	}
}

func TestHashCountAndDim(t *testing.T) {
	dim, L, b, n := 4, 3, 7, 11
	f := NewFamily(dim, L, b, 1)
	for _, v := range makeData(dim, n) {
		if _, err := f.IndexSignatures(v); err != nil {
			t.Fatal(err)
		}
	}
	if got := f.HashOps(); got != int64(n*L*b) {
		t.Fatalf("hashOps=%d want %d", got, n*L*b)
	}
	if _, err := f.Signature(0, make([]float64, dim+1)); !errors.Is(err, vec.ErrDim) {
		t.Fatalf("dim err=%v", err)
	}
}

func makeData(dim, n int) [][]float64 {
	out := make([][]float64, n)
	for i := range out {
		v := make([]float64, dim)
		for d := range v {
			v[d] = float64(i) - float64(d)*0.5
		}
		out[i] = v
	}
	return out
}
