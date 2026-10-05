package frames_test

import (
	"math/rand"
	"testing"

	"ontology/frames"
)

// TestAppendCurLow 表驱动：追加后校验 cur、low 与全部合法区间和。
func TestAppendCurLow(t *testing.T) {
	cases := []struct {
		name  string
		k     int64
		sizes []int64
	}{
		{"无帧", 4, nil},
		{"未写满", 4, []int64{10, 20, 30}},
		{"环绕覆盖", 3, []int64{1, 2, 3, 4, 5, 6, 7}},
		{"容量为1", 1, []int64{5, 6, 7}},
		{"零尺寸帧", 2, []int64{0, 0, 5, 0, 7}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := frames.NewBuffer(tc.k)
			all := []int64{0} // 1-based
			for _, sz := range tc.sizes {
				cur := b.Append(sz)
				all = append(all, sz)
				if want := int64(len(all)) - 1; cur != want {
					t.Fatalf("Append 返回帧号 %d，期望 %d", cur, want)
				}
				wantLow := cur - tc.k + 1
				if wantLow < 1 {
					wantLow = 1
				}
				if got := b.Low(); got != wantLow {
					t.Fatalf("Low()=%d，期望 %d（cur=%d K=%d）", got, wantLow, cur, tc.k)
				}
				if got := b.Cur(); got != cur {
					t.Fatalf("Cur()=%d，期望 %d", got, cur)
				}
			}
			pref := make([]int64, len(all))
			for i := 1; i < len(all); i++ {
				pref[i] = pref[i-1] + all[i]
			}
			cur, low := b.Cur(), b.Low()
			for lo := low; lo <= cur; lo++ {
				for hi := lo; hi <= cur; hi++ {
					if got, want := b.Sum(lo, hi), pref[hi]-pref[lo-1]; got != want {
						t.Fatalf("Sum(%d,%d)=%d，期望 %d", lo, hi, got, want)
					}
				}
			}
		})
	}
}

// TestSumEmptyAndTouched 空区间不读记录，非空区间恰读 2 条。
func TestSumEmptyAndTouched(t *testing.T) {
	b := frames.NewBuffer(4)
	for i := 0; i < 6; i++ {
		b.Append(1)
	}
	b.ResetTouched()
	if got := b.Sum(5, 4); got != 0 {
		t.Fatalf("空区间 Sum=%d，期望 0", got)
	}
	if got := b.Touched(); got != 0 {
		t.Fatalf("空区间读取 %d 条记录，期望 0", got)
	}
	if got := b.Sum(3, 6); got != 4 {
		t.Fatalf("Sum(3,6)=%d，期望 4", got)
	}
	if got := b.Touched(); got != 2 {
		t.Fatalf("非空区间读取 %d 条记录，期望 2", got)
	}
}

// TestFuzzAgainstNaive 随机追加与区间查询，对照保存全部帧的朴素实现。
func TestFuzzAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 200; trial++ {
		k := int64(1 + rng.Intn(100))
		b := frames.NewBuffer(k)
		all := []int64{0}
		for i, n := 0, 1+rng.Intn(300); i < n; i++ {
			sz := rng.Int63n(1_000_001)
			b.Append(sz)
			all = append(all, sz)
		}
		cur, low := b.Cur(), b.Low()
		for q := 0; q < 50; q++ {
			lo := low + rng.Int63n(cur-low+1)
			hi := lo + rng.Int63n(cur-lo+1)
			want := int64(0)
			for i := lo; i <= hi; i++ {
				want += all[i]
			}
			if got := b.Sum(lo, hi); got != want {
				t.Fatalf("trial=%d Sum(%d,%d)=%d，期望 %d", trial, lo, hi, got, want)
			}
		}
	}
}
