package cuck

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	"ontology/hash"
)

func norm(bs [][]int) [][]int {
	out := make([][]int, len(bs))
	for i, b := range bs {
		out[i] = slices.DeleteFunc(slices.Clone(b), func(v int) bool { return v == 0 })
	}
	return out
}

func TestHash(t *testing.T) {
	cases := []struct {
		x         int64
		f, i1, i2 int
	}{{1, 2, 1, 2}, {5, 6, 1, 0}, {9, 3, 1, 0}, {2, 3, 2, 3}, {6, 7, 2, 0}, {10, 4, 2, 0}, {14, 1, 2, 0}}
	for _, c := range cases {
		if hash.Fingerprint(c.x) != c.f || hash.I1(c.x, 4) != c.i1 || hash.I2(c.x, 4) != c.i2 {
			t.Errorf("x=%d f/i1/i2=%d/%d/%d, want %d/%d/%d", c.x,
				hash.Fingerprint(c.x), hash.I1(c.x, 4), hash.I2(c.x, 4), c.f, c.i1, c.i2)
		}
	}
	for f := 1; f <= 7; f++ {
		for b := 0; b < 16; b++ {
			if hash.Alternate(hash.Alternate(b, f), f) != b {
				t.Fatalf("alternate 不对称: b=%d f=%d", b, f)
			}
		}
	}
	for _, p := range [][3]int{{2, 1, 1}, {4, 2, 4}, {1024, 4, 8}} {
		if !hash.ValidParams(p[0], p[1], p[2]) {
			t.Errorf("合法参数被拒: %v", p)
		}
	}
	for _, p := range [][3]int{{0, 1, 1}, {1, 1, 1}, {3, 2, 4}, {6, 2, 4}, {4, 0, 4}, {4, 2, 0}, {4, 2, -1}} {
		if hash.ValidParams(p[0], p[1], p[2]) {
			t.Errorf("非法参数被接受: %v", p)
		}
	}
}

// TestTenStep 逐步核验 NOTES.md 第三节的十行表（含踢出 f3:b2->b3）。
func TestTenStep(t *testing.T) {
	f, err := New(4, 2, 4)
	if err != nil {
		t.Fatal(err)
	}
	steps := []struct {
		op   string
		x    int64
		want [][]int
	}{
		{"i", 1, [][]int{{}, {2}, {}, {}}},
		{"i", 5, [][]int{{}, {2, 6}, {}, {}}},
		{"i", 9, [][]int{{3}, {2, 6}, {}, {}}},
		{"i", 2, [][]int{{3}, {2, 6}, {3}, {}}},
		{"i", 6, [][]int{{3}, {2, 6}, {3, 7}, {}}},
		{"i", 10, [][]int{{3, 4}, {2, 6}, {3, 7}, {}}},
		{"i", 14, [][]int{{3, 4}, {2, 6}, {7, 1}, {3}}},
		{"l", 2, [][]int{{3, 4}, {2, 6}, {7, 1}, {3}}},
		{"d", 9, [][]int{{4}, {2, 6}, {7, 1}, {3}}},
		{"l", 2, [][]int{{4}, {2, 6}, {7, 1}, {3}}},
	}
	for i, s := range steps {
		ok := true
		switch s.op {
		case "i":
			ok = f.Insert(s.x) == nil
		case "d":
			ok = f.Delete(s.x) == nil
		case "l":
			ok = f.Lookup(s.x)
		}
		if !ok || !reflect.DeepEqual(norm(f.Buckets()), s.want) {
			t.Fatalf("step %d (%s %d) 失败, 桶=%v", i+1, s.op, s.x, norm(f.Buckets()))
		}
	}
}

// TestFailureNoTrace 四类被拒操作互不相同且不改状态；满回滚后仍可继续使用。
func TestFailureNoTrace(t *testing.T) {
	for _, p := range [][3]int{{3, 2, 1}, {1, 1, 1}, {4, 0, 1}, {4, 1, 0}} {
		if _, err := New(p[0], p[1], p[2]); !errors.Is(err, hash.ErrInvalidParams) {
			t.Errorf("参数 %v: 应返回 ErrInvalidParams, got %v", p, err)
		}
	}
	f, _ := New(4, 1, 1)
	for _, x := range []int64{1, 5, 2, 4} {
		f.Insert(x)
	}
	snap, cnt := f.Buckets(), f.Count()
	rej := []error{f.Insert(8), f.Insert(-1), f.Delete(999), f.Delete(-5)}
	want := []error{ErrFull, ErrNegativeKey, ErrNotInserted, ErrNegativeKey}
	for i := range rej {
		if !errors.Is(rej[i], want[i]) {
			t.Errorf("rej[%d]=%v, want %v", i, rej[i], want[i])
		}
	}
	sentinels := []error{hash.ErrInvalidParams, ErrNegativeKey, ErrFull, ErrNotInserted}
	for i := range sentinels {
		for j := range sentinels {
			if i != j && errors.Is(sentinels[i], sentinels[j]) {
				t.Errorf("哨兵错误 %v 与 %v 不可区分", sentinels[i], sentinels[j])
			}
		}
	}
	if !reflect.DeepEqual(f.Buckets(), snap) || f.Count() != cnt {
		t.Error("被拒操作改变了状态")
	}
	if f.Delete(4) != nil || f.Insert(8) != nil || !f.Lookup(8) {
		t.Error("被拒后不可继续使用")
	}
}

// TestBucketsCheckedTwo 检查桶数恒为 2，与 numBuckets 无关（白盒读非导出计数器）。
func TestBucketsCheckedTwo(t *testing.T) {
	for _, nb := range []int{128, 256, 512, 1024, 2048, 4096, 8192} {
		f, _ := New(nb, 4, 8)
		for x := int64(0); x < 50; x++ {
			f.Insert(x)
		}
		f.Lookup(7)
		if g := f.checked.Load(); g != 2 {
			t.Errorf("nb=%d Lookup 检查桶数=%d, want 2", nb, g)
		}
		f.Delete(7)
		if g := f.checked.Load(); g != 2 {
			t.Errorf("nb=%d Delete 检查桶数=%d, want 2", nb, g)
		}
	}
}
