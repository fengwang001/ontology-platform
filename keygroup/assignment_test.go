package keygroup

import (
	"fmt"
	"testing"
)

// TestComputeKeyGroupRange 多组参数下验证：区间两两不交、首尾相接、
// 恰好覆盖 [0, maxParallelism-1]，且逐键组归属与 computeAssignment 一致。
func TestComputeKeyGroupRange(t *testing.T) {
	cases := []struct{ maxParallelism, parallelism int }{
		{1, 1},
		{2, 1}, {2, 2},
		{10, 1}, {10, 2}, {10, 3}, {10, 4}, {10, 7}, {10, 10},
		{128, 1}, {128, 3}, {128, 5}, {128, 16}, {128, 100}, {128, 128},
		{1024, 7}, {1024, 33}, {1024, 512},
	}
	for _, tc := range cases {
		name := fmt.Sprintf("max=%d/par=%d", tc.maxParallelism, tc.parallelism)
		t.Run(name, func(t *testing.T) {
			ranges := make([]Range, tc.parallelism)
			for i := 0; i < tc.parallelism; i++ {
				r, err := ComputeKeyGroupRange(tc.maxParallelism, tc.parallelism, i)
				if err != nil {
					t.Fatalf("ComputeKeyGroupRange(%d, %d, %d) 出错: %v",
						tc.maxParallelism, tc.parallelism, i, err)
				}
				ranges[i] = r
			}
			// 覆盖性与不交性：逐键组归属必须恰好命中一个实例。
			owners, err := computeAssignment(tc.maxParallelism, tc.parallelism)
			if err != nil {
				t.Fatalf("computeAssignment 出错: %v", err)
			}
			for g := 0; g < tc.maxParallelism; g++ {
				hits := 0
				owner := -1
				for i, r := range ranges {
					if r.Contains(g) {
						hits++
						owner = i
					}
				}
				if hits != 1 {
					t.Fatalf("键组 %d 被 %d 个区间覆盖（应为 1），ranges=%v", g, hits, ranges)
				}
				if owners[g] != owner {
					t.Fatalf("键组 %d 归属不一致：assignment=%d, range=%d", g, owners[g], owner)
				}
			}
			// 连续覆盖：首区间从 0 开始，末区间到 maxParallelism-1，相邻区间首尾相接。
			if ranges[0].Start != 0 {
				t.Fatalf("首区间起点=%d，应为 0", ranges[0].Start)
			}
			if ranges[len(ranges)-1].End != tc.maxParallelism-1 {
				t.Fatalf("末区间终点=%d，应为 %d", ranges[len(ranges)-1].End, tc.maxParallelism-1)
			}
			for i := 1; i < len(ranges); i++ {
				if ranges[i].Start != ranges[i-1].End+1 {
					t.Fatalf("区间 %d 与 %d 不相接：%v 后接 %v", i-1, i, ranges[i-1], ranges[i])
				}
			}
			t.Logf("判定依据：%d 个区间两两不交、首尾相接、覆盖 [0,%d]，逐键组归属一致；ranges=%v",
				tc.parallelism, tc.maxParallelism-1, ranges)
		})
	}
}

// TestKeyGroupOfDeterministic 同一键反复映射结果一致，且落在合法范围内。
func TestKeyGroupOfDeterministic(t *testing.T) {
	const maxParallelism = 128
	for i := 0; i < 500; i++ {
		key := fmt.Sprintf("key-%d", i)
		first, err := KeyGroupOf(key, maxParallelism)
		if err != nil {
			t.Fatalf("KeyGroupOf(%q) 出错: %v", key, err)
		}
		for j := 0; j < 5; j++ {
			again, _ := KeyGroupOf(key, maxParallelism)
			if again != first {
				t.Fatalf("KeyGroupOf(%q) 不确定：%d != %d", key, again, first)
			}
		}
		if first < 0 || first >= maxParallelism {
			t.Fatalf("KeyGroupOf(%q)=%d 越界 [0,%d)", key, first, maxParallelism)
		}
	}
}
