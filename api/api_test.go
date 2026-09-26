package api_test

import (
	"math/rand"
	"slices"
	"testing"

	"ontology/api"
	"ontology/spv"
)

// assertCanonical 手动核验规范形，并与稠密全扫描交叉验证非零条目数。
func assertCanonical(t *testing.T, v *spv.Vec, m int) {
	t.Helper()
	idx, val := v.Snapshot()
	if !v.Canonical() || len(idx) != len(val) {
		t.Fatalf("canonical violated: len idx=%d val=%d", len(idx), len(val))
	}
	nnz := 0
	for k, ix := range idx {
		if ix < 0 || ix >= m || (k > 0 && ix <= idx[k-1]) || val[k] == 0 || v.Get(ix) != val[k] {
			t.Fatalf("bad entry k=%d idx=%v val=%v", k, idx, val)
		}
		nnz++
	}
	for k := 0; k < m; k++ {
		if v.Get(k) != 0 {
			nnz--
		}
	}
	if nnz != 0 {
		t.Fatalf("snapshot/dense nnz mismatch by %d", nnz)
	}
}

func TestCanonicalForm(t *testing.T) {
	// 表驱动：经 Build 构造的合法输入必须规范，含边界 m=1 与空向量。
	cases := []struct {
		m   int
		idx []int
		val []float64
	}{
		{10, []int{1, 3, 5}, []float64{2, 3, 5}},
		{10, []int{0, 1, 5}, []float64{4, 7, 6}},
		{1, []int{0}, []float64{-1.25}},
		{64, nil, nil},
		{6, []int{0, 2, 4}, []float64{-1.5, 2, -3}},
	}
	for _, c := range cases {
		v, err := api.Build(c.m, c.idx, c.val)
		if err != nil {
			t.Fatalf("Build(%v): %v", c.idx, err)
		}
		assertCanonical(t, v, c.m)
	}
	// 随机到达顺序：乱序写入、随机覆盖、随机零值删除，多档 m 循环。
	for _, m := range []int{1, 2, 5, 17, 100} {
		for seed := int64(0); seed < 20; seed++ {
			rng := rand.New(rand.NewSource(seed + int64(m)))
			v := spv.New(m)
			for round := 0; round < 3*m+2; round++ {
				ix, val := rng.Intn(m), float64(rng.Intn(7)-3)
				if err := v.Set(ix, val); err != nil {
					t.Fatalf("Set(%d,%v): %v", ix, val, err)
				}
				if round%5 == 0 {
					assertCanonical(t, v, m)
				}
			}
			assertCanonical(t, v, m)
		}
	}
	// 定点：乱序到达后覆盖不产生重复，零值删除后条目消失。
	v := spv.New(10)
	for k, ix := range []int{2, 7, 3} {
		if err := v.Set(ix, []float64{1, 2, 3}[k]); err != nil {
			t.Fatal(err)
		}
	}
	if err := v.Set(3, 9); err != nil || v.Get(3) != 9 {
		t.Fatalf("overwrite: err=%v get=%v", err, v.Get(3))
	}
	if err := v.Set(7, 0); err != nil || v.Get(7) != 0 {
		t.Fatalf("delete: err=%v get=%v", err, v.Get(7))
	}
	assertCanonical(t, v, 10)
	if err := api.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

func TestRejectedOpsNoTrace(t *testing.T) {
	// Set 路径：越界（负下标与上界）被拒，快照逐字节不变，之后仍可正常使用。
	for _, bad := range []int{-1, -100, 10, 1000} {
		v, _ := api.Build(10, []int{1, 3, 5}, []float64{2, 3, 5})
		i0, v0 := v.Snapshot()
		if err := v.Set(bad, 9); err != spv.ErrIndexOutOfRange {
			t.Fatalf("Set(%d): err=%v", bad, err)
		}
		i1, v1 := v.Snapshot()
		if !slices.Equal(i0, i1) || !slices.Equal(v0, v1) {
			t.Fatalf("Set(%d) rejected but state changed", bad)
		}
		if err := v.Set(3, 8); err != nil || v.Get(3) != 8 || !v.Canonical() {
			t.Fatal("vec unusable after rejection")
		}
	}
	// Build 路径：四类哨兵互不相同，失败一律返回 nil。
	cases := []struct {
		m    int
		idx  []int
		val  []float64
		want error
	}{
		{10, []int{10}, []float64{1}, spv.ErrIndexOutOfRange},
		{10, []int{-1}, []float64{1}, spv.ErrIndexOutOfRange},
		{10, []int{1}, []float64{0}, api.ErrZeroValue},
		{10, []int{1, 2}, []float64{1}, api.ErrLenMismatch},
		{10, []int{2, 1}, []float64{1, 2}, api.ErrIndexNotSorted},
		{10, []int{1, 1}, []float64{1, 2}, api.ErrIndexNotSorted},
	}
	seen := map[error]bool{}
	for _, c := range cases {
		v, err := api.Build(c.m, c.idx, c.val)
		if v != nil || err != c.want {
			t.Fatalf("Build(%v): v=%v err=%v want %v", c.idx, v, err, c.want)
		}
		seen[err] = true
	}
	if len(seen) != 4 {
		t.Fatalf("want 4 distinct sentinels, got %d", len(seen))
	}
	// 失败的 Build 不得影响此前已存在的向量；之后的合法 Build 仍成功。
	good, _ := api.Build(10, []int{1, 3, 5}, []float64{2, 3, 5})
	gi0, gv0 := good.Snapshot()
	if _, err := api.Build(10, []int{1, 1}, []float64{1, 2}); err == nil {
		t.Fatal("expected rejection")
	}
	gi1, gv1 := good.Snapshot()
	if !slices.Equal(gi0, gi1) || !slices.Equal(gv0, gv1) {
		t.Fatal("failed Build altered an unrelated vector")
	}
	if after, err := api.Build(10, []int{0, 9}, []float64{-1, 1}); err != nil || after.Get(9) != 1 {
		t.Fatalf("Build after rejection: %v", err)
	}
}
