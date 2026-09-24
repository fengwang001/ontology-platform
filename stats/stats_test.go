package stats

import (
	"errors"
	"math"
	"math/rand"
	"testing"
)

func batch(xs []float64) (int64, float64, float64) {
	if len(xs) == 0 {
		return 0, 0, 0
	}
	var sum float64
	for _, x := range xs {
		sum += x
	}
	mean := sum / float64(len(xs))
	var m2 float64
	for _, x := range xs {
		d := x - mean
		m2 += d * d
	}
	return int64(len(xs)), mean, m2
}

func close(a, b float64) bool {
	return math.Abs(a-b) <= 1e-9*math.Max(1, math.Max(math.Abs(a), math.Abs(b)))
}

// 不变量 1：随机操作序列后每个 Key 与批量重算一致。
func TestBatchRecomputeConsistency(t *testing.T) {
	r := rand.New(rand.NewSource(385))
	s := New()
	keys := []string{"a", "b", "c"}
	elems := map[string][]float64{}
	for i := 0; i < 3000; i++ {
		k := keys[r.Intn(len(keys))]
		if r.Intn(3) == 0 && len(elems[k]) > 0 {
			idx := r.Intn(len(elems[k]))
			x := elems[k][idx]
			if err := s.Apply(Op{Kind: OpRemove, Key: k, X: x}); err != nil {
				t.Fatal(err)
			}
			elems[k] = append(elems[k][:idx], elems[k][idx+1:]...)
		} else {
			x := float64(r.Intn(50)) * 0.5 // 制造重复值
			if err := s.Apply(Op{Kind: OpAdd, Key: k, X: x}); err != nil {
				t.Fatal(err)
			}
			elems[k] = append(elems[k], x)
		}
	}
	for _, k := range keys {
		n, mean, m2 := batch(elems[k])
		if v := s.View(k); v.N != n || !close(v.Mean, mean) || !close(v.M2, m2) {
			t.Errorf("key %s: got (%d,%g,%g), want (%d,%g,%g)", k, v.N, v.Mean, v.M2, n, mean, m2)
		}
	}
}

// 不变量 2：Remove 精确重算 M2（第三节(乙)：M2=14/3，方差=14/9）。
func TestRemoveRecomputesM2(t *testing.T) {
	s := New()
	for _, x := range []float64{1, 2, 3, 5} {
		if err := s.Apply(Op{Kind: OpAdd, Key: "g", X: x}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Apply(Op{Kind: OpRemove, Key: "g", X: 1}); err != nil {
		t.Fatal(err)
	}
	if v := s.View("g"); v.N != 3 || !close(v.Mean, 10.0/3) || !close(v.M2, 14.0/3) || !close(v.Variance, 14.0/9) {
		t.Errorf("got %+v", v)
	}
}

// 不变量 3：Merge 含交叉项（第三节(甲)：八步后 M2=208.8，方差=41.76）。
func TestMergeCrossTerm(t *testing.T) {
	s := New()
	ops := []Op{
		{Kind: OpAdd, Key: "g2", X: 10}, {Kind: OpAdd, Key: "g2", X: 20},
		{Kind: OpAdd, Key: "g", X: 1}, {Kind: OpAdd, Key: "g", X: 2},
		{Kind: OpAdd, Key: "g", X: 3}, {Kind: OpAdd, Key: "g", X: 5},
		{Kind: OpRemove, Key: "g", X: 1}, {Kind: OpAdd, Key: "g", X: 4},
		{Kind: OpRemove, Key: "g", X: 3}, {Kind: OpMerge, Key: "g", Other: "g2"},
	}
	for _, op := range ops {
		if err := s.Apply(op); err != nil {
			t.Fatal(err)
		}
	}
	if v := s.View("g"); v.N != 5 || !close(v.Mean, 8.2) || !close(v.M2, 208.8) || !close(v.Variance, 41.76) {
		t.Errorf("got %+v", v)
	}
}

// 不变量 4：被拒操作不留痕，错误互不相同，拒绝后仍可正常使用。
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	s := New()
	for _, x := range []float64{1, 2, 3} {
		if err := s.Apply(Op{Kind: OpAdd, Key: "g", X: x}); err != nil {
			t.Fatal(err)
		}
	}
	before := s.View("g")
	cases := []struct {
		name string
		op   Op
		err  error
	}{
		{"remove-absent", Op{Kind: OpRemove, Key: "g", X: 999}, ErrValueNotFound},
		{"merge-empty", Op{Kind: OpMerge, Key: "g", Other: "ghost"}, ErrEmptyGroup},
		{"empty-key-add", Op{Kind: OpAdd, Key: "", X: 1}, ErrEmptyKey},
		{"empty-key-remove", Op{Kind: OpRemove, Key: "", X: 1}, ErrEmptyKey},
		{"self-merge", Op{Kind: OpMerge, Key: "g", Other: "g"}, ErrSelfMerge},
	}
	for _, c := range cases {
		if err := s.Apply(c.op); !errors.Is(err, c.err) {
			t.Errorf("%s: got %v, want %v", c.name, err, c.err)
		}
		if after := s.View("g"); after != before {
			t.Errorf("%s: 状态被改变 %+v", c.name, after)
		}
	}
	if ErrValueNotFound == ErrEmptyGroup || ErrEmptyGroup == ErrEmptyKey || ErrValueNotFound == ErrEmptyKey {
		t.Error("三类哨兵错误必须互不相同")
	}
	if err := s.Apply(Op{Kind: OpAdd, Key: "g", X: 4}); err != nil || s.View("g").N != 4 {
		t.Error("拒绝后实例不可继续使用")
	}
}

// 复杂度：Remove 的哈希查找次数不随组内不同值个数 m 增长。
func TestRemoveLookupScaling(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		s := New()
		for i := 0; i < m; i++ {
			if err := s.Apply(Op{Kind: OpAdd, Key: "g", X: float64(i)}); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Apply(Op{Kind: OpRemove, Key: "g", X: float64(m / 2)}); err != nil {
			t.Fatal(err)
		}
		if s.lastLookups > 4 {
			t.Errorf("m=%d: 查找次数 %d 超过常数上界", m, s.lastLookups)
		}
	}
}
