package samp

import (
	"cmp"
	"fmt"
	"math/bits"
	"slices"
	"testing"

	"ontology/khash"
)

// byBK 按 (bucket 升序, key 字典序升序) 排序，与 SetRate 的输出顺序一致。
func byBK(s []string) {
	slices.SortFunc(s, func(a, b string) int {
		return cmp.Or(cmp.Compare(khash.Bucket(a), khash.Bucket(b)), cmp.Compare(a, b))
	})
}

// naiveDiff 是 SetRate 的朴素参照：对全部已知键分别在 r1、r2 下判定后求差集再排序。
func naiveDiff(keys []string, r1, r2 int) (add, rem []string) {
	for _, k := range keys {
		in1, in2 := khash.Bucket(k) < r1, khash.Bucket(k) < r2
		if !in1 && in2 {
			add = append(add, k)
		}
		if in1 && !in2 {
			rem = append(rem, k)
		}
	}
	byBK(add)
	byBK(rem)
	return add, rem
}

func feedKeys(s *Sampler, keys []string) {
	evs := make([]Event, len(keys))
	for i, k := range keys {
		evs[i] = Event{Key: k, V: int64(i)}
	}
	s.Feed(evs)
}

// TestSetRateMatchesNaive 钉不变量 1：各种率变化下，SetRate 的两份差集
// 必须逐元素等于「全键重判求差再排序」的朴素结果；r2==r1 时两份皆空。
func TestSetRateMatchesNaive(t *testing.T) {
	keys := []string{"gnj", "dzv", "gnk", "kcm", "qjy", "a6m"}
	for i := 0; i < 200; i++ { // 混入人造键，扩充已知键规模
		keys = append(keys, fmt.Sprintf("z-%04d", i))
	}
	cases := [][2]int{
		{2500, 2000}, {2000, 5000}, {0, 10000}, {10000, 0},
		{3000, 3000}, {1, 9999}, {5000, 5001}, {5001, 5000}, {777, 1234},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("%d->%d", c[0], c[1]), func(t *testing.T) {
			s := New(c[0])
			feedKeys(s, keys)
			added, removed := s.SetRate(c[1])
			wantAdd, wantRem := naiveDiff(keys, c[0], c[1])
			if !slices.Equal(added, wantAdd) {
				t.Fatalf("added = %v, want %v", added, wantAdd)
			}
			if !slices.Equal(removed, wantRem) {
				t.Fatalf("removed = %v, want %v", removed, wantRem)
			}
			if c[0] == c[1] && (added != nil || removed != nil) {
				t.Fatalf("equal rates must yield nil/nil, got %v/%v", added, removed)
			}
		})
	}
}

// TestMonotonicRates 钉不变量 3：调高时 removed 必空、调低时 added 必空，
// 且 r1<=r2 时 r1 下被采样键集合是 r2 下的子集（用朴素谓词独立核验）。
func TestMonotonicRates(t *testing.T) {
	keys := []string{"gnj", "dzv", "gnk", "kcm", "qjy", "a6m"}
	for i := 0; i < 300; i++ {
		keys = append(keys, fmt.Sprintf("m-%04d", i))
	}
	ladder := []int{0, 1, 2000, 2499, 2500, 2501, 5000, 9999, 10000}
	s := New(ladder[0])
	feedKeys(s, keys)
	for i := 1; i < len(ladder); i++ { // 逐级调高
		added, removed := s.SetRate(ladder[i])
		if len(removed) != 0 {
			t.Fatalf("raise %d->%d removed %v, want none", ladder[i-1], ladder[i], removed)
		}
		// 独立朴素核验：旧集合 ⊆ 新集合。
		for _, k := range keys {
			if khash.Bucket(k) < ladder[i-1] && !(khash.Bucket(k) < ladder[i]) {
				t.Fatalf("monotonicity broken at key %q", k)
			}
		}
		_ = added
	}
	for i := len(ladder) - 2; i >= 0; i-- { // 逐级调低
		added, removed := s.SetRate(ladder[i])
		if len(added) != 0 {
			t.Fatalf("lower %d->%d added %v, want none", ladder[i+1], ladder[i], added)
		}
		_ = removed
	}
}

// TestNarrowSetRateChecksSublinear 钉第四节：lastChecked 只统计二分探测与
// 区间内真正取出的键，窄区间 SetRate 的检查数必须是 O(log m)，不随 m 线性增长。
func TestNarrowSetRateChecksSublinear(t *testing.T) {
	for _, m := range []int{100, 250, 500, 1000, 2500, 5000, 10000} {
		s := New(5000)
		keys := make([]string, m)
		for i := range keys {
			keys[i] = fmt.Sprintf("w-%d-%05d", m, i)
		}
		feedKeys(s, keys)
		added, _ := s.SetRate(5001) // 5000 -> 5001：只跨桶 [5000,5001)
		bound := 2*bits.Len(uint(m)) + 4 + len(added)
		if s.lastChecked > bound {
			t.Errorf("m=%d raise: checked %d > bound %d (2*ceil(log2(m+1))+4+returned)",
				m, s.lastChecked, bound)
		}
		if s.lastChecked >= m {
			t.Errorf("m=%d: checked %d scanned a linear fraction of keys", m, s.lastChecked)
		}
		_, removed := s.SetRate(5000) // 调回，同样只跨一个桶
		if s.lastChecked > 2*bits.Len(uint(m))+4+len(removed) {
			t.Errorf("m=%d lower: checked %d > bound", m, s.lastChecked)
		}
	}
}
