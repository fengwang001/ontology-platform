package keygroup

import (
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"sync"
	"testing"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

// putKeys 批量写入 key-0 .. key-(n-1)。
func putKeys(t *testing.T, s *Store, n int) map[string]string {
	t.Helper()
	want := make(map[string]string, n)
	for i := 0; i < n; i++ {
		k, v := fmt.Sprintf("key-%d", i), fmt.Sprintf("value-%d", i)
		if err := s.Put(k, v); err != nil {
			t.Fatalf("Put(%q) 出错: %v", k, err)
		}
		want[k] = v
	}
	return want
}

// TestRescaleConservesKeys 多组参数下扩缩容后键值守恒：
// 键集合与值既不丢也不重，迁移量与逐键组归属比对结果一致。
func TestRescaleConservesKeys(t *testing.T) {
	cases := []struct {
		maxParallelism int
		parallelism    int
		targets        []int // 依次调整到的并行度
		keys           int
	}{
		{10, 2, []int{3, 5, 1, 10, 2}, 500},
		{128, 4, []int{8, 3, 16, 1, 128, 4}, 2000},
		{1024, 16, []int{7, 33, 512, 16}, 5000},
	}
	for _, tc := range cases {
		name := fmt.Sprintf("max=%d/par=%d", tc.maxParallelism, tc.parallelism)
		t.Run(name, func(t *testing.T) {
			logger := testLogger()
			s, err := NewStore(tc.maxParallelism, tc.parallelism, WithLogger(logger))
			if err != nil {
				t.Fatalf("NewStore 出错: %v", err)
			}
			want := putKeys(t, s, tc.keys)
			logger.Info("测试输入", "maxParallelism", tc.maxParallelism,
				"parallelism", tc.parallelism, "keys", tc.keys, "targets", tc.targets)

			for _, target := range tc.targets {
				before := s.Snapshot()
				report, err := s.Rescale(target)
				if err != nil {
					t.Fatalf("Rescale(%d) 出错: %v", target, err)
				}
				after := s.Snapshot()

				// 守恒：键集合与值完全一致。
				if got := after.KeySet(); !maps.Equal(got, want) {
					t.Fatalf("Rescale(%d) 后键值不守恒：丢失/多余 %d 个键",
						target, len(want)-len(got))
				}
				if after.TotalKeys() != len(want) || report.TotalKeys != len(want) {
					t.Fatalf("键总数异常：after=%d, report=%d, want=%d",
						after.TotalKeys(), report.TotalKeys, len(want))
				}

				// 迁移量核验：独立重算归属变化键组内的键数。
				newOwners, _ := computeAssignment(tc.maxParallelism, target)
				expectMoved := 0
				for g := 0; g < tc.maxParallelism; g++ {
					if before.Owners[g] != newOwners[g] {
						expectMoved += len(before.Buckets[g])
					}
				}
				if report.MovedKeys != expectMoved {
					t.Fatalf("迁移量不符：report=%d, 独立重算=%d", report.MovedKeys, expectMoved)
				}
				// 归属未变的键组不得出现在迁移记录中。
				for _, m := range report.MovedGroups {
					if before.Owners[m.Group] == newOwners[m.Group] {
						t.Fatalf("键组 %d 归属未变却被迁移", m.Group)
					}
					if m.From != before.Owners[m.Group] || m.To != newOwners[m.Group] {
						t.Fatalf("键组 %d 迁移记录错误：%+v", m.Group, m)
					}
				}
				if after.Parallelism != target {
					t.Fatalf("并行度未更新：got=%d, want=%d", after.Parallelism, target)
				}
				logger.Info("迁移判定",
					"from", report.OldParallelism, "to", report.NewParallelism,
					"movedGroups", len(report.MovedGroups),
					"movedKeys", report.MovedKeys, "expectMoved", expectMoved,
					"totalKeys", report.TotalKeys,
					"依据", "逐键组比较前后归属，仅归属变化者整组迁移")
			}
		})
	}
}

// TestInvalidInputs 各类非法输入必须被拒绝且原因可区分，
// 被拒绝的操作不得改变并行度、键值或分桶。
func TestInvalidInputs(t *testing.T) {
	// 构造参数非法。
	if _, err := NewStore(0, 1); !errors.Is(err, ErrInvalidMaxParallelism) {
		t.Fatalf("maxParallelism=0 应报 ErrInvalidMaxParallelism，got %v", err)
	}
	if _, err := NewStore(-3, 1); !errors.Is(err, ErrInvalidMaxParallelism) {
		t.Fatalf("maxParallelism=-3 应报 ErrInvalidMaxParallelism，got %v", err)
	}
	for _, p := range []int{0, -1, 11} {
		if _, err := NewStore(10, p); !errors.Is(err, ErrInvalidParallelism) {
			t.Fatalf("parallelism=%d 应报 ErrInvalidParallelism，got %v", p, err)
		}
	}
	// 区间计算参数非法。
	if _, err := ComputeKeyGroupRange(0, 1, 0); !errors.Is(err, ErrInvalidMaxParallelism) {
		t.Fatalf("应报 ErrInvalidMaxParallelism，got %v", err)
	}
	if _, err := ComputeKeyGroupRange(10, 0, 0); !errors.Is(err, ErrInvalidParallelism) {
		t.Fatalf("应报 ErrInvalidParallelism，got %v", err)
	}
	if _, err := ComputeKeyGroupRange(10, 11, 0); !errors.Is(err, ErrInvalidParallelism) {
		t.Fatalf("应报 ErrInvalidParallelism，got %v", err)
	}
	for _, idx := range []int{-1, 3, 100} {
		if _, err := ComputeKeyGroupRange(10, 3, idx); !errors.Is(err, ErrInvalidOperatorIndex) {
			t.Fatalf("index=%d 应报 ErrInvalidOperatorIndex，got %v", idx, err)
		}
	}
	// 空键。
	if _, err := KeyGroupOf("", 10); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("空键应报 ErrEmptyKey，got %v", err)
	}

	// 被拒绝的操作不得改变状态。
	s, err := NewStore(16, 2, WithLogger(testLogger()))
	if err != nil {
		t.Fatalf("NewStore 出错: %v", err)
	}
	want := putKeys(t, s, 200)
	before := s.Snapshot()

	if err := s.Put("", "x"); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("Put 空键应报 ErrEmptyKey，got %v", err)
	}
	if _, _, err := s.Get(""); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("Get 空键应报 ErrEmptyKey，got %v", err)
	}
	if err := s.Delete(""); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("Delete 空键应报 ErrEmptyKey，got %v", err)
	}
	for _, p := range []int{0, -2, 17, 1000} {
		if _, err := s.Rescale(p); !errors.Is(err, ErrInvalidParallelism) {
			t.Fatalf("Rescale(%d) 应报 ErrInvalidParallelism，got %v", p, err)
		}
	}

	after := s.Snapshot()
	if after.Parallelism != before.Parallelism {
		t.Fatalf("非法操作改变了并行度：%d -> %d", before.Parallelism, after.Parallelism)
	}
	if got := after.KeySet(); !maps.Equal(got, want) {
		t.Fatalf("非法操作改变了键值或分桶")
	}
	for g := range after.Buckets {
		if !maps.Equal(after.Buckets[g], before.Buckets[g]) {
			t.Fatalf("非法操作改变了键组 %d 的分桶", g)
		}
	}
	t.Logf("判定依据：全部非法输入被 errors.Is 区分拒绝，并行度/键值/分桶与操作前完全一致")
}

// TestConcurrentReadWrite 并发读写与扩缩容期间，快照始终逐字段一致。
func TestConcurrentReadWrite(t *testing.T) {
	s, err := NewStore(128, 4, WithLogger(testLogger()))
	if err != nil {
		t.Fatalf("NewStore 出错: %v", err)
	}
	putKeys(t, s, 1000)

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				snap := s.Snapshot()
				// 逐字段一致性：Ranges 与 Owners 必须互相吻合。
				for g, owner := range snap.Owners {
					if !snap.Ranges[owner].Contains(g) {
						t.Errorf("快照不一致：键组 %d 归属实例 %d，但不在其区间 %v",
							g, owner, snap.Ranges[owner])
						return
					}
				}
				_ = s.Put(fmt.Sprintf("w%d-key", id), "v")
				_, _, _ = s.Get("key-1")
			}
		}(w)
	}
	// 并发期间反复扩缩容。
	for _, p := range []int{8, 2, 16, 1, 4} {
		if _, err := s.Rescale(p); err != nil {
			t.Errorf("Rescale(%d) 出错: %v", p, err)
		}
	}
	close(stop)
	wg.Wait()
}

// TestDeterministicReplay 同一输入序列反复计算，输出完全相同。
func TestDeterministicReplay(t *testing.T) {
	run := func() ([]MigrationReport, Snapshot) {
		s, err := NewStore(64, 3)
		if err != nil {
			t.Fatalf("NewStore 出错: %v", err)
		}
		putKeys(t, s, 800)
		var reports []MigrationReport
		for _, p := range []int{5, 2, 9, 1, 64, 3} {
			r, err := s.Rescale(p)
			if err != nil {
				t.Fatalf("Rescale(%d) 出错: %v", p, err)
			}
			reports = append(reports, r)
		}
		return reports, s.Snapshot()
	}
	reports1, snap1 := run()
	reports2, snap2 := run()

	if fmt.Sprintf("%v", reports1) != fmt.Sprintf("%v", reports2) {
		t.Fatalf("两次运行的迁移报告不一致：\n%v\n%v", reports1, reports2)
	}
	if !maps.Equal(snap1.KeySet(), snap2.KeySet()) {
		t.Fatalf("两次运行的键值集合不一致")
	}
	for g := range snap1.Buckets {
		if !maps.Equal(snap1.Buckets[g], snap2.Buckets[g]) {
			t.Fatalf("两次运行键组 %d 分桶不一致", g)
		}
	}
	for i := range snap1.Owners {
		if snap1.Owners[i] != snap2.Owners[i] {
			t.Fatalf("两次运行键组 %d 归属不一致", i)
		}
	}
	t.Logf("判定依据：相同输入序列两次执行，迁移报告、键值集合、分桶与归属逐项相等")
}
