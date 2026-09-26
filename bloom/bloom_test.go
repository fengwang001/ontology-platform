package bloom

import (
	"math/rand"
	"reflect"
	"strconv"
	"testing"

	"ontology/hashk"
)

// 钉住 NOTES.md 八行表：位置/位数组/Test 结果全部为字面值
// （pos_0=h1、pos_1-pos_0=h2，位置金值唯一确定 h1/h2）。
func TestEightOpsGolden(t *testing.T) {
	rows := []struct {
		add, test bool
		x, bits   string
		pos       [3]int
	}{
		{true, false, "a", "0001010100", [3]int{7, 5, 3}},
		{true, false, "b", "0001011110", [3]int{8, 7, 6}},
		{true, false, "c", "1101011111", [3]int{9, 0, 1}},
		{false, true, "o", "1101011111", [3]int{1, 5, 9}},
		{false, true, "a", "1101011111", [3]int{7, 5, 3}},
		{false, false, "d", "1101011111", [3]int{0, 2, 4}},
		{false, true, "b", "1101011111", [3]int{8, 7, 6}},
		{false, false, "x", "1101011111", [3]int{0, 4, 8}},
	}
	f, _ := New(10, 3, 8)
	for i, r := range rows {
		x := []byte(r.x)
		if got := hashk.Positions(x, 10, 3); !reflect.DeepEqual(got, r.pos[:]) {
			t.Errorf("行%d pos=%v 期望%v", i, got, r.pos)
		}
		if r.add {
			if err := f.Add(x); err != nil {
				t.Fatalf("行%d Add: %v", i, err)
			}
		} else if got := f.Test(x); got != r.test {
			t.Errorf("行%d Test(%q)=%v 期望%v", i, r.x, got, r.test)
		}
		if got := f.BitString(); got != r.bits {
			t.Errorf("行%d 位数组=%s 期望%s", i, got, r.bits)
		}
	}
}

// 不变量1：任何 Add 成功的元素之后 Test 必为 true。
func TestNoFalseNegative(t *testing.T) {
	for _, tc := range []struct{ m, k, n int }{{1, 1, 5}, {10, 3, 8}, {100, 4, 100}, {5000, 8, 1000}} {
		f, _ := New(tc.m, tc.k, tc.n)
		for i := 0; i < tc.n; i++ {
			if err := f.Add([]byte("e" + strconv.Itoa(i))); err != nil {
				t.Fatalf("m=%d: Add %d: %v", tc.m, i, err)
			}
		}
		for i := 0; i < tc.n; i++ {
			if !f.Test([]byte("e" + strconv.Itoa(i))) {
				t.Fatalf("m=%d k=%d: 假阴性 e%d", tc.m, tc.k, i)
			}
		}
		if f.Count() != tc.n {
			t.Errorf("m=%d: Count=%d 期望%d", tc.m, f.Count(), tc.n)
		}
	}
}

// 不变量2：与「展开 k 个位置按位集合逐位比对」的朴素参照逐元素一致。
func TestNaiveReference(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for _, tc := range []struct{ m, k, adds, probes int }{{10, 3, 8, 200}, {97, 5, 50, 500}, {1024, 7, 300, 1000}} {
		f, _ := New(tc.m, tc.k, tc.adds)
		naive := map[int]bool{}
		for i := 0; i < tc.adds; i++ {
			x := []byte(strconv.Itoa(rng.Intn(100000)))
			if err := f.Add(x); err != nil {
				t.Fatal(err)
			}
			for _, p := range hashk.Positions(x, tc.m, tc.k) {
				naive[p] = true
			}
		}
		for i := 0; i < tc.probes; i++ {
			x := []byte(strconv.Itoa(rng.Intn(100000)))
			want := true
			for _, p := range hashk.Positions(x, tc.m, tc.k) {
				want = want && naive[p]
			}
			if got := f.Test(x); got != want {
				t.Fatalf("m=%d: Test(%s)=%v 参照=%v", tc.m, x, got, want)
			}
		}
	}
}

// 第四节：检查位数恒等于 k，不随已插入元素个数增长（白盒读非导出计数器）。
func TestCheckedBitsConstantK(t *testing.T) {
	for _, k := range []int{1, 3, 8} {
		for _, n := range []int{100, 1000, 10000} {
			f, _ := New(n*64, k, n)
			for i := 0; i < n; i++ {
				if err := f.Add([]byte("e" + strconv.Itoa(i))); err != nil {
					t.Fatal(err)
				}
			}
			f.Test([]byte("probe"))
			if got := f.lastTestBits.Load(); got != int64(k) {
				t.Errorf("k=%d n=%d: 检查位数=%d 期望%d", k, n, got, k)
			}
		}
	}
}

// 不变量3：位只被 Add 置位——插入顺序不影响位数组；假阳性的 k 位必被插入覆盖。
func TestFalsePositiveSource(t *testing.T) {
	f1, _ := New(32, 3, 64)
	f2, _ := New(32, 3, 64)
	covered := map[int]bool{}
	for i := 0; i < 20; i++ {
		x := []byte("elem-" + strconv.Itoa(i))
		if err := f1.Add(x); err != nil {
			t.Fatal(err)
		}
		for _, p := range hashk.Positions(x, 32, 3) {
			covered[p] = true
		}
	}
	for i := 19; i >= 0; i-- { // 逆序插入，位数组必须相同
		f2.Add([]byte("elem-" + strconv.Itoa(i)))
	}
	if f1.BitString() != f2.BitString() {
		t.Error("插入顺序影响了位数组：存在隐藏状态")
	}
	found := 0
	for i := 0; i < 2000; i++ {
		x := []byte("probe-" + strconv.Itoa(i))
		if !f1.Test(x) {
			continue
		}
		found++
		for _, p := range hashk.Positions(x, 32, 3) {
			if !covered[p] {
				t.Fatalf("Test(%s) 为 true 但位 %d 未被任何插入覆盖", x, p)
			}
		}
	}
	if found == 0 {
		t.Error("未观察到假阳性，本测试失去意义")
	}
}
