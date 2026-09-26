package vv

import (
	"math/rand"
	"testing"
)

// TestMergeReadsBounded 证明稀疏表示：actor id 空间为 m、两向量各 2 个非零
// 条目时，Merge 读取的条目数不随 m 增长（稠密数组整表扫描会读到 m 级）。
func TestMergeReadsBounded(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		a := Vector{0: 1, m - 1: 7}
		b := Vector{1: 2, m - 2: 9}
		Merge(a, b)
		if got := stats.reads.Load(); got > 8 {
			t.Fatalf("m=%d: merge read %d entries, want <= 8 (constant)", m, got)
		}
	}
}

// TestMergePure 验证 Merge 不修改输入。
func TestMergePure(t *testing.T) {
	a := Vector{0: 1, 2: 5}
	b := Vector{1: 3, 2: 4}
	Merge(a, b)
	if a[2] != 5 || len(a) != 2 || b[2] != 4 || len(b) != 2 {
		t.Fatalf("merge mutated inputs: a=%v b=%v", a, b)
	}
}

// TestMergeIsJoin 表驱动 + 随机循环：交换律、幂等、结果 >= 两个输入。
func TestMergeIsJoin(t *testing.T) {
	cases := [][2]Vector{
		{{0: 1}, {0: 1, 1: 2}},
		{{}, {}},
		{{5: 9}, {2: 1}},
		{{0: 3, 7: 1}, {0: 2, 7: 1, 8: 4}},
	}
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 50; i++ {
		cases = append(cases, [2]Vector{randVec(rng, 8), randVec(rng, 8)})
	}
	for i, c := range cases {
		a, b := c[0], c[1]
		ab, ba := Merge(a, b), Merge(b, a)
		if !equalVec(ab, ba) {
			t.Fatalf("case %d: not commutative: %v vs %v", i, ab, ba)
		}
		if aa := Merge(a, a); !equalVec(aa, a) {
			t.Fatalf("case %d: not idempotent: %v vs %v", i, aa, a)
		}
		if o := Compare(ab, a); o != Equal && o != Greater {
			t.Fatalf("case %d: merge not >= a: %v", i, o)
		}
		if o := Compare(ab, b); o != Equal && o != Greater {
			t.Fatalf("case %d: merge not >= b: %v", i, o)
		}
	}
}

// TestCompareTrichotomy 表驱动（含缺失视为 0）+ 随机循环对拍逐 key 定义。
func TestCompareTrichotomy(t *testing.T) {
	cases := []struct {
		a, b Vector
		want Order
	}{
		{Vector{0: 1}, Vector{0: 1}, Equal},
		{Vector{0: 1}, Vector{0: 1, 1: 2}, Less},
		{Vector{0: 1, 1: 2}, Vector{0: 1}, Greater},
		{Vector{0: 1}, Vector{1: 1, 2: 1}, Concurrent},
		{Vector{}, Vector{}, Equal},
		{Vector{}, Vector{3: 1}, Less},
		{Vector{4: 0}, Vector{}, Equal}, // 显式 0 等价于缺失
		{Vector{1: 5, 9: 1}, Vector{1: 5, 8: 2}, Concurrent},
	}
	for i, c := range cases {
		if got := Compare(c.a, c.b); got != c.want {
			t.Fatalf("case %d: Compare(%v,%v)=%v, want %v", i, c.a, c.b, got, c.want)
		}
		// 对称性：交换参数后 Less<->Greater，Equal/Concurrent 不变。
		wantRev := map[Order]Order{Less: Greater, Greater: Less, Equal: Equal, Concurrent: Concurrent}[c.want]
		if got := Compare(c.b, c.a); got != wantRev {
			t.Fatalf("case %d reversed: Compare=%v, want %v", i, got, wantRev)
		}
	}
	rng := rand.New(rand.NewSource(2))
	for i := 0; i < 200; i++ {
		a, b := randVec(rng, 6), randVec(rng, 6)
		less, greater := leq(a, b), leq(b, a)
		want := Concurrent
		switch {
		case less && greater:
			want = Equal
		case less:
			want = Less
		case greater:
			want = Greater
		}
		if got := Compare(a, b); got != want {
			t.Fatalf("iter %d: Compare(%v,%v)=%v, want %v", i, a, b, got, want)
		}
	}
}

// leq 按缺失视为 0 的逐 key <= 定义重算：只需遍历 a，
// 只在 b 出现的 key 上 a 为 0，恒满足 0 <= b[k]。
func leq(a, b Vector) bool {
	for k, va := range a {
		if va > b[k] {
			return false
		}
	}
	return true
}

func randVec(rng *rand.Rand, space int) Vector {
	v := Vector{}
	for i := 0; i < space; i++ {
		if rng.Intn(2) == 0 {
			v[rng.Intn(space)] = rng.Intn(5)
		}
	}
	return v
}

// equalVec 语义相等：缺失的 key 与显式 0 等价。
func equalVec(a, b Vector) bool {
	for k, va := range a {
		if va != b[k] {
			return false
		}
	}
	for k, vb := range b {
		if _, ok := a[k]; !ok && vb != 0 {
			return false
		}
	}
	return true
}
