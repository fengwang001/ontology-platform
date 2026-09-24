package segment

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

func ceilLog2(n int) int {
	c := 0
	for 1<<c < n {
		c++
	}
	return c
}

// 追加 m 条等长记录，断言 Lookup 检查个数不随 m 线性增长：
// 先二分定位 floor 条目，再做有界的段内顺序扫描。
func TestLookupCheckedBound(t *testing.T) {
	const size, k = 10, 4 // indexInterval = k*size
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		seg, err := New(0, int64(k*size))
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < m; i++ {
			if _, err := seg.Append(int64(i), size); err != nil {
				t.Fatal(err)
			}
		}
		bound := int64(2*ceilLog2(m+1) + k + 4)
		rnd := rand.New(rand.NewSource(int64(m)))
		for j := 0; j < 300; j++ {
			if _, _, err := seg.Lookup(rnd.Int63n(int64(m) + 1)); err != nil && err != ErrNotFound {
				t.Fatal(err)
			}
			if got := seg.checked.Load(); got > bound {
				t.Fatalf("m=%d: checked %d > bound %d", m, got, bound)
			}
		}
	}
}

// 第三节的八条记录：物理位置、完整索引、典型查找。
func TestAppendLookupRules(t *testing.T) {
	seg, err := New(1000, 100)
	if err != nil {
		t.Fatal(err)
	}
	eight := [][2]int64{
		{1000, 60}, {1001, 50}, {1003, 40}, {1004, 60}, {1007, 30}, {1008, 80}, {1010, 20}, {1012, 50},
	}
	for i, want := range []int64{0, 60, 110, 150, 210, 240, 320, 340} {
		if pos, err := seg.Append(eight[i][0], eight[i][1]); err != nil || pos != want {
			t.Fatalf("append #%d: pos=%d err=%v, want %d", i, pos, err, want)
		}
	}
	if got := fmt.Sprint(seg.Entries()); got != "[{3 110} {7 210} {10 320}]" {
		t.Fatalf("index = %s", got)
	}
	lookups := []struct {
		target, o, p int64
		err          error
	}{
		{1003, 1003, 110, nil}, {1004, 1004, 150, nil}, {1006, 1007, 210, nil},
		{1007, 1007, 210, nil}, {1012, 1012, 340, nil}, {1013, 0, 0, ErrNotFound},
	}
	for _, l := range lookups {
		if o, p, err := seg.Lookup(l.target); o != l.o || p != l.p || !errors.Is(err, l.err) {
			t.Errorf("Lookup(%d) = (%d,%d,%v)", l.target, o, p, err)
		}
	}
}

// 不变量 1：任意状态下 Lookup 与从头扫描的朴素参照一致。
func TestNaiveConsistency(t *testing.T) {
	cases := []struct{ seed, interval, n, maxSize, maxGap int64 }{{1, 40, 300, 30, 5}, {2, 1, 200, 5, 3}, {3, 1000, 500, 100, 10}}
	for _, c := range cases {
		rnd := rand.New(rand.NewSource(c.seed))
		seg, err := New(0, c.interval)
		if err != nil {
			t.Fatal(err)
		}
		var recs [][2]int64
		off := int64(0)
		for i := int64(0); i < c.n; i++ {
			pos, err := seg.Append(off, 1+rnd.Int63n(c.maxSize))
			if err != nil {
				t.Fatal(err)
			}
			recs = append(recs, [2]int64{off, pos})
			off += 1 + rnd.Int63n(c.maxGap)
		}
		for j := 0; j < 500; j++ {
			target := rnd.Int63n(off + 1)
			o, p, err := seg.Lookup(target)
			no, np, nerr := int64(0), int64(0), error(ErrNotFound)
			for _, r := range recs {
				if r[0] >= target {
					no, np, nerr = r[0], r[1], nil
					break
				}
			}
			if o != no || p != np || !errors.Is(err, nerr) {
				t.Fatalf("target=%d: got (%d,%d,%v), want (%d,%d,%v)", target, o, p, err, no, np, nerr)
			}
		}
	}
}
