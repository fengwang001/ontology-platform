package ontology

import (
	"errors"
	"testing"
)

func TestNewInvalidParams(t *testing.T) {
	cases := []struct {
		name string
		n    int
		p    float64
		want error
	}{
		{"n=0", 0, 0.01, ErrInvalidN},
		{"n<0", -100, 0.01, ErrInvalidN},
		{"p=0", 1000, 0, ErrInvalidP},
		{"p<0", 1000, -0.5, ErrInvalidP},
		{"p=1", 1000, 1, ErrInvalidP},
		{"p>1", 1000, 1.5, ErrInvalidP},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, err := New(c.n, c.p)
			if !errors.Is(err, c.want) {
				t.Fatalf("New(%d, %g): want %v, got f=%v err=%v",
					c.n, c.p, c.want, f, err)
			}
		})
	}
}

func TestDerivedParams(t *testing.T) {
	f, err := New(10000, 0.01)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	// m = ceil(-10000*ln(0.01)/ln(2)^2) = 95851, k = round(m/n*ln2) = 7。
	if f.M() != 95851 {
		t.Fatalf("M() = %d, want 95851", f.M())
	}
	if f.K() != 7 {
		t.Fatalf("K() = %d, want 7", f.K())
	}
}

func TestZeroFalseNegatives(t *testing.T) {
	const n = 10000
	f := fillFilter(t, n, 0.01)
	for i := 0; i < n; i++ {
		if !f.MayContain(insElem(i)) {
			t.Fatalf("false negative for inserted element %d", i)
		}
	}
}

// TestMeasuredFPR 用十万个从未插入的元素统计实测假阳性率。
// 上界取 2p 的理由：按 (n, p) 最优参数构造并插入 n 个元素后，
// 理论假阳性率约等于 p；实测值是均值为 p 的二项随机变量，
// 十万次试验下标准差约 sqrt(p(1-p)/1e5) ≈ 0.0003，2p 距均值
// 约 30 个标准差，既几乎不会抖动失败，又足以发现参数推错
// （例如 m 或 k 差一个量级）的实现。
func TestMeasuredFPR(t *testing.T) {
	const (
		n       = 10000
		p       = 0.01
		queries = 100000
	)
	f := fillFilter(t, n, p)
	var hits int
	for i := 0; i < queries; i++ {
		if f.MayContain(qryElem(i)) {
			hits++
		}
	}
	fpr := float64(hits) / queries
	t.Logf("measured FPR = %.5f (target p = %g)", fpr, p)
	if fpr < 0 || fpr > 2*p {
		t.Fatalf("measured FPR %.5f outside [0, %g]", fpr, 2*p)
	}
}

func TestEmptyFilterContainsNothing(t *testing.T) {
	f, err := New(1000, 0.01)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	for i := 0; i < 1000; i++ {
		if f.MayContain(qryElem(i)) {
			t.Fatalf("empty filter reported element %d present", i)
		}
	}
	if f.EstimateCount() != 0 {
		t.Fatalf("empty filter EstimateCount = %v, want 0", f.EstimateCount())
	}
}
