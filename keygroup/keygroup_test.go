package keygroup

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
)

// TestRangePartitioning 验证多组参数下区间两两不交、连续且恰好覆盖全部键组，
// 且 OwnerOf 的逐键组归属与区间划分一致。
func TestRangePartitioning(t *testing.T) {
	cases := []struct{ maxParallelism, parallelism int }{
		{1, 1},
		{10, 1},
		{10, 2},
		{10, 3},
		{10, 10},
		{128, 7},
		{128, 128},
		{1000, 37},
	}
	for _, tc := range cases {
		name := fmt.Sprintf("max=%d/par=%d", tc.maxParallelism, tc.parallelism)
		t.Run(name, func(t *testing.T) {
			assignment, err := ComputeKeyGroupAssignment(tc.maxParallelism, tc.parallelism)
			if err != nil {
				t.Fatalf("ComputeKeyGroupAssignment: %v", err)
			}
			t.Logf("输入 maxParallelism=%d parallelism=%d -> 区间划分 %v",
				tc.maxParallelism, tc.parallelism, assignment)

			if len(assignment) != tc.parallelism {
				t.Fatalf("区间数=%d, 期望 %d", len(assignment), tc.parallelism)
			}
			// 连续性：下一个区间的起点必须紧邻上一个区间的终点。
			if assignment[0].Start != 0 {
				t.Fatalf("首个区间起点=%d, 期望 0", assignment[0].Start)
			}
			for i := 1; i < len(assignment); i++ {
				if assignment[i].Start != assignment[i-1].End+1 {
					t.Fatalf("区间 %v 与 %v 之间存在空洞或重叠",
						assignment[i-1], assignment[i])
				}
			}
			if assignment[len(assignment)-1].End != tc.maxParallelism-1 {
				t.Fatalf("末个区间终点=%d, 期望 %d",
					assignment[len(assignment)-1].End, tc.maxParallelism-1)
			}
			// 逐键组归属：每个键组恰好归属一个实例，且与区间一致。
			seen := make([]int, tc.maxParallelism)
			for g := 0; g < tc.maxParallelism; g++ {
				owner := OwnerOf(assignment, g)
				if owner < 0 || owner >= tc.parallelism {
					t.Fatalf("键组 %d 归属实例 %d 越界", g, owner)
				}
				if !assignment[owner].Contains(g) {
					t.Fatalf("键组 %d 归属实例 %d, 但不在其区间 %v 内",
						g, owner, assignment[owner])
				}
				seen[g]++
			}
			for g, n := range seen {
				if n != 1 {
					t.Fatalf("键组 %d 被覆盖 %d 次, 期望恰好 1 次", g, n)
				}
			}
			t.Logf("判定依据: 区间连续无重叠、覆盖 [0,%d]、逐键组归属唯一一致 -> 通过",
				tc.maxParallelism-1)
		})
	}
}

// TestKeyToGroupDeterministic 验证键到键组的映射确定且在界内。
func TestKeyToGroupDeterministic(t *testing.T) {
	const maxParallelism = 128
	for i := 0; i < 1000; i++ {
		key := fmt.Sprintf("key-%d", i)
		g1, err := KeyToGroup(key, maxParallelism)
		if err != nil {
			t.Fatalf("KeyToGroup(%q): %v", key, err)
		}
		g2, err := KeyToGroup(key, maxParallelism)
		if err != nil {
			t.Fatalf("KeyToGroup(%q) 第二次: %v", key, err)
		}
		if g1 != g2 {
			t.Fatalf("键 %q 两次映射结果不同: %d vs %d", key, g1, g2)
		}
		if g1 < 0 || g1 >= maxParallelism {
			t.Fatalf("键 %q 映射到键组 %d, 超出 [0,%d)", key, g1, maxParallelism)
		}
	}
}

// TestRescaleConservation 验证扩缩容后键值与集合守恒、
// 迁移量等于归属变化键组内的键数，且同一输入序列结果完全相同。
func TestRescaleConservation(t *testing.T) {
	const maxParallelism = 128
	scales := []struct{ from, to int }{
		{2, 4}, // 扩容
		{4, 2}, // 缩容
		{3, 7},
		{7, 3},
		{1, 128},
		{128, 1},
		{5, 5}, // 并行度不变
	}
	for _, sc := range scales {
		name := fmt.Sprintf("%d->%d", sc.from, sc.to)
		t.Run(name, func(t *testing.T) {
			table, err := NewTable(maxParallelism, sc.from)
			if err != nil {
				t.Fatalf("NewTable: %v", err)
			}
			want := make(map[string]string)
			for i := 0; i < 500; i++ {
				k, v := fmt.Sprintf("key-%d", i), fmt.Sprintf("value-%d", i)
				if err := table.Put(k, v); err != nil {
					t.Fatalf("Put(%q): %v", k, err)
				}
				want[k] = v
			}
			before := table.Snapshot()

			report, err := table.Rescale(sc.to)
			if err != nil {
				t.Fatalf("Rescale(%d): %v", sc.to, err)
			}
			t.Logf("输入 parallelism %d -> %d, 键数=%d", sc.from, sc.to, len(want))
			for _, m := range report.Migrations {
				t.Logf("迁移: 键组 %d 实例 %d -> 实例 %d, 迁移键数=%d",
					m.Group, m.From, m.To, m.Keys)
			}
			t.Logf("迁移结果: 迁移键组数=%d 迁移键数=%d 总键数=%d",
				len(report.Migrations), report.MigratedKeys, report.TotalKeys)

			// 键值守恒：集合与值均不变。
			after := table.Snapshot()
			if !reflect.DeepEqual(after.States, want) {
				t.Fatalf("扩缩容后键值不守恒")
			}
			if report.TotalKeys != len(want) {
				t.Fatalf("TotalKeys=%d, 期望 %d", report.TotalKeys, len(want))
			}
			// 迁移量校验：逐键组比对前后归属，仅变化的键组计入。
			oldOwner := before.GroupOwner
			wantMigrated := 0
			migratedGroups := make(map[int]bool)
			for _, m := range report.Migrations {
				migratedGroups[m.Group] = true
				if oldOwner[m.Group] != m.From {
					t.Fatalf("键组 %d 记录的原归属=%d, 实际=%d",
						m.Group, m.From, oldOwner[m.Group])
				}
				if after.GroupOwner[m.Group] != m.To {
					t.Fatalf("键组 %d 记录的新归属=%d, 实际=%d",
						m.Group, m.To, after.GroupOwner[m.Group])
				}
				if m.From == m.To {
					t.Fatalf("键组 %d 归属未变却被迁移", m.Group)
				}
			}
			for g := 0; g < maxParallelism; g++ {
				changed := oldOwner[g] != after.GroupOwner[g]
				if changed != migratedGroups[g] {
					t.Fatalf("键组 %d 归属变化=%v, 是否迁移=%v, 不一致",
						g, changed, migratedGroups[g])
				}
			}
			// 直接统计归属变化键组内的键数，与报告比对。
			keysPerGroup := make([]int, maxParallelism)
			for k := range want {
				g, _ := KeyToGroup(k, maxParallelism)
				keysPerGroup[g]++
			}
			for g := 0; g < maxParallelism; g++ {
				if oldOwner[g] != after.GroupOwner[g] {
					wantMigrated += keysPerGroup[g]
				}
			}
			if report.MigratedKeys != wantMigrated {
				t.Fatalf("MigratedKeys=%d, 期望 %d", report.MigratedKeys, wantMigrated)
			}
			if sc.from == sc.to && report.MigratedKeys != 0 {
				t.Fatalf("并行度不变时不应有迁移, 实际迁移 %d 键", report.MigratedKeys)
			}
			t.Logf("判定依据: 键值守恒、迁移键组与归属变化逐组一致、迁移键数=%d 与独立统计相符 -> 通过",
				report.MigratedKeys)
		})
	}
}

// TestRescaleDeterministic 验证同一输入序列反复计算得到完全相同的输出。
func TestRescaleDeterministic(t *testing.T) {
	const maxParallelism = 64
	run := func() RescaleReport {
		table, err := NewTable(maxParallelism, 3)
		if err != nil {
			t.Fatalf("NewTable: %v", err)
		}
		for i := 0; i < 200; i++ {
			if err := table.Put(fmt.Sprintf("k-%d", i), fmt.Sprintf("v-%d", i)); err != nil {
				t.Fatalf("Put: %v", err)
			}
		}
		report, err := table.Rescale(9)
		if err != nil {
			t.Fatalf("Rescale: %v", err)
		}
		return report
	}
	first := run()
	for i := 0; i < 5; i++ {
		if got := run(); !reflect.DeepEqual(got, first) {
			t.Fatalf("第 %d 次重复计算结果不同:\n首次 %+v\n本次 %+v", i+1, first, got)
		}
	}
	t.Logf("判定依据: 6 次相同输入序列的迁移报告完全一致 -> 通过")
}

// TestInvalidParams 验证各类非法输入被以可区分的原因拒绝，
// 且被拒绝的操作不改变并行度、键值或分桶。
func TestInvalidParams(t *testing.T) {
	t.Run("NewTable", func(t *testing.T) {
		cases := []struct {
			name                string
			maxParallelism, par int
			wantErr             error
		}{
			{"maxParallelism为0", 0, 1, ErrInvalidMaxParallelism},
			{"maxParallelism为负", -3, 1, ErrInvalidMaxParallelism},
			{"parallelism为0", 10, 0, ErrInvalidParallelism},
			{"parallelism为负", 10, -1, ErrInvalidParallelism},
			{"parallelism超过上限", 4, 5, ErrParallelismExceedsMax},
		}
		for _, tc := range cases {
			_, err := NewTable(tc.maxParallelism, tc.par)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("%s: err=%v, 期望 %v", tc.name, err, tc.wantErr)
			}
			t.Logf("输入 maxParallelism=%d parallelism=%d -> 拒绝原因: %v",
				tc.maxParallelism, tc.par, err)
		}
	})

	t.Run("KeyGroupRangeForInstance实例越界", func(t *testing.T) {
		for _, idx := range []int{-1, 4, 100} {
			_, err := KeyGroupRangeForInstance(10, 4, idx)
			if !errors.Is(err, ErrInstanceIndexOutOfRange) {
				t.Fatalf("operatorIndex=%d: err=%v, 期望 %v",
					idx, err, ErrInstanceIndexOutOfRange)
			}
			t.Logf("输入 operatorIndex=%d (parallelism=4) -> 拒绝原因: %v", idx, err)
		}
	})

	t.Run("Rescale非法且状态不变", func(t *testing.T) {
		table, err := NewTable(8, 2)
		if err != nil {
			t.Fatalf("NewTable: %v", err)
		}
		if err := table.Put("a", "1"); err != nil {
			t.Fatalf("Put: %v", err)
		}
		before := table.Snapshot()

		for _, bad := range []int{0, -1, 9, 100} {
			_, err := table.Rescale(bad)
			if err == nil {
				t.Fatalf("Rescale(%d) 应被拒绝", bad)
			}
			t.Logf("输入 newParallelism=%d -> 拒绝原因: %v", bad, err)
			after := table.Snapshot()
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("Rescale(%d) 被拒绝后状态发生变化", bad)
			}
		}
		t.Logf("判定依据: 非法 Rescale 后并行度/键值/分桶快照与之前逐字段一致 -> 通过")
	})

	t.Run("空键", func(t *testing.T) {
		table, err := NewTable(8, 2)
		if err != nil {
			t.Fatalf("NewTable: %v", err)
		}
		if err := table.Put("x", "1"); err != nil {
			t.Fatalf("Put: %v", err)
		}
		before := table.Snapshot()

		if err := table.Put("", "v"); !errors.Is(err, ErrEmptyKey) {
			t.Fatalf("Put 空键: err=%v, 期望 %v", err, ErrEmptyKey)
		}
		t.Logf("输入 Put(空键) -> 拒绝原因: %v", ErrEmptyKey)
		if err := table.Delete(""); !errors.Is(err, ErrEmptyKey) {
			t.Fatalf("Delete 空键: err=%v, 期望 %v", err, ErrEmptyKey)
		}
		t.Logf("输入 Delete(空键) -> 拒绝原因: %v", ErrEmptyKey)
		if _, err := KeyToGroup("", 8); !errors.Is(err, ErrEmptyKey) {
			t.Fatalf("KeyToGroup 空键: err=%v, 期望 %v", err, ErrEmptyKey)
		}
		if after := table.Snapshot(); !reflect.DeepEqual(before, after) {
			t.Fatalf("空键操作被拒绝后状态发生变化")
		}
	})
}

// TestConcurrentReadConsistency 验证并发读取得到逐字段一致的快照。
func TestConcurrentReadConsistency(t *testing.T) {
	table, err := NewTable(64, 4)
	if err != nil {
		t.Fatalf("NewTable: %v", err)
	}
	for i := 0; i < 300; i++ {
		if err := table.Put(fmt.Sprintf("key-%d", i), fmt.Sprintf("v-%d", i)); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}

	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			base := table.Snapshot()
			for i := 0; i < 50; i++ {
				snap := table.Snapshot()
				if !reflect.DeepEqual(base, snap) {
					errs <- fmt.Errorf("并发读取到不一致的快照")
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	t.Logf("判定依据: 8 协程各读 50 次快照, 全部逐字段一致 -> 通过")
}
