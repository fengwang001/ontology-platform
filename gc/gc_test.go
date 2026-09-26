package gc

import "testing"

// naive 朴素参照实现：枚举并集逐条目取 max。
func naive(a, b Counter) Counter {
	out := Counter{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		if v > out[k] {
			out[k] = v
		}
	}
	return out
}

func equal(x, y Counter) bool {
	if len(x) != len(y) {
		return false
	}
	for k, v := range x {
		if y[k] != v {
			return false
		}
	}
	return true
}

var mergeCases = []struct {
	name string
	a, b Counter
}{
	{"都空", Counter{}, Counter{}},
	{"一侧空", Counter{0: 5}, Counter{}},
	{"不相交", Counter{0: 5}, Counter{1: 3}},
	{"相交取大", Counter{0: 7, 1: 3}, Counter{0: 5, 2: 9}},
	{"大节点号稀疏", Counter{9999: 1}, Counter{0: 2, 9999: 1}},
}

func TestMergeNaive(t *testing.T) {
	for _, tc := range mergeCases {
		got, err := Merge(tc.a, tc.b)
		if err != nil {
			t.Fatalf("%s: 意外错误 %v", tc.name, err)
		}
		if !equal(got, naive(tc.a, tc.b)) {
			t.Errorf("%s: Merge=%v, 朴素=%v", tc.name, got, naive(tc.a, tc.b))
		}
	}
}

func TestValueNaive(t *testing.T) {
	for _, tc := range mergeCases {
		sum := 0
		for _, v := range tc.a {
			sum += v
		}
		if Value(tc.a) != sum {
			t.Errorf("%s: Value=%d, 朴素求和=%d", tc.name, Value(tc.a), sum)
		}
	}
}

func TestMergeCommutativeIdempotent(t *testing.T) {
	for _, tc := range mergeCases {
		ab, _ := Merge(tc.a, tc.b)
		ba, _ := Merge(tc.b, tc.a)
		if !equal(ab, ba) {
			t.Errorf("%s: 交换律不成立 %v != %v", tc.name, ab, ba)
		}
		aa, _ := Merge(tc.a, tc.a)
		if !equal(aa, naive(tc.a, Counter{})) {
			t.Errorf("%s: 幂等律不成立 %v != %v", tc.name, aa, tc.a)
		}
	}
}

func TestMergeNegativeEntry(t *testing.T) {
	bad := Counter{0: -1}
	if _, err := Merge(bad, Counter{}); err != ErrNegativeEntry {
		t.Errorf("a 含负条目: err=%v", err)
	}
	if _, err := Merge(Counter{}, bad); err != ErrNegativeEntry {
		t.Errorf("b 含负条目: err=%v", err)
	}
}

// TestMergeReadCountSparse 证明 Merge 的读取个数不随 node id 空间 m 增长。
func TestMergeReadCountSparse(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		a := Counter{0: 1, m - 1: 2}
		b := Counter{m / 2: 3, m - 1: 1}
		if _, err := Merge(a, b); err != nil {
			t.Fatal(err)
		}
		if got := lastMergeReads.Load(); got > 4 {
			t.Errorf("m=%d: 读取 %d 个条目, 超过非零条目总数 4", m, got)
		}
	}
}
