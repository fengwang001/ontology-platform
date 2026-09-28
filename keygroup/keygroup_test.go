package keygroup

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"
)

type discardLogger struct{}

func (discardLogger) Printf(string, ...any) {}

func TestAssignRangesPartition(t *testing.T) {
	cases := []struct{ groups, parallelism int }{
		{1, 1}, {10, 1}, {10, 3}, {7, 4}, {100, 7},
		{128, 16}, {1000, 13}, {16, 16},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%d/%d", tc.groups, tc.parallelism), func(t *testing.T) {
			ranges, err := AssignRanges(tc.groups, tc.parallelism)
			if err != nil {
				t.Fatalf("AssignRanges: %v", err)
			}
			if len(ranges) != tc.parallelism {
				t.Fatalf("区间数 = %d, 期望 %d", len(ranges), tc.parallelism)
			}
			if ranges[0].Start != 0 || ranges[tc.parallelism-1].End != tc.groups {
				t.Fatalf("区间未恰好覆盖 [0,%d): %+v", tc.groups, ranges)
			}
			minWidth, maxWidth := math.MaxInt, 0
			for i, r := range ranges {
				if r.Instance != i || r.Start >= r.End {
					t.Fatalf("非法区间 %d: %+v", i, r)
				}
				if i > 0 && r.Start != ranges[i-1].End {
					t.Fatalf("区间不连续: %+v 与 %+v", ranges[i-1], r)
				}
				if r.Width() < minWidth {
					minWidth = r.Width()
				}
				if r.Width() > maxWidth {
					maxWidth = r.Width()
				}
			}
			if maxWidth-minWidth > 1 {
				t.Fatalf("负载不均: 最小区间=%d 最大区间=%d", minWidth, maxWidth)
			}
			for g := 0; g < tc.groups; g++ {
				owner, err := OwnerOf(g, tc.groups, tc.parallelism)
				if err != nil {
					t.Fatalf("OwnerOf(%d): %v", g, err)
				}
				if !ranges[owner].Contains(g) {
					t.Fatalf("键组 %d 归属 %d 但不在其区间 %+v", g, owner, ranges[owner])
				}
				for other := 0; other < tc.parallelism; other++ {
					if other != owner && ranges[other].Contains(g) {
						t.Fatalf("键组 %d 同时落入区间 %d 与 %d（相交）", g, owner, other)
					}
				}
			}
		})
	}
}

func TestKeyToGroupStable(t *testing.T) {
	const numGroups = 128
	keys := []string{"user:1", "order-42", "中文键", "a/b/c", strings.Repeat("x", 4096)}
	seen := make(map[int]string)
	for _, key := range keys {
		g1, err := KeyToGroup(key, numGroups)
		if err != nil {
			t.Fatalf("KeyToGroup(%q): %v", key, err)
		}
		g2, _ := KeyToGroup(key, numGroups)
		if g1 != g2 || g1 < 0 || g1 >= numGroups {
			t.Fatalf("键组映射不稳定或越界: %d %d", g1, g2)
		}
		if other, dup := seen[g1]; dup {
			t.Fatalf("测试键哈希碰撞: %q 与 %q -> %d", other, key, g1)
		}
		seen[g1] = key
		owner, err := OwnerOf(g1, numGroups, 7)
		if err != nil || owner < 0 || owner >= 7 {
			t.Fatalf("OwnerOf 与公式不一致: %d err=%v", owner, err)
		}
	}
}

func TestInvalidInputs(t *testing.T) {
	bad := []struct {
		groups, parallelism int
		want                error
	}{
		{0, 1, ErrInvalidGroups},
		{-3, 1, ErrInvalidGroups},
		{10, 0, ErrInvalidParallelism},
		{10, -1, ErrInvalidParallelism},
		{10, 11, ErrParallelismTooLarge},
	}
	for _, tc := range bad {
		_, err := NewStore(tc.groups, tc.parallelism, discardLogger{})
		if !errors.Is(err, tc.want) {
			t.Errorf("NewStore(%d,%d) err=%v, 期望 %v", tc.groups, tc.parallelism, err, tc.want)
		}
	}

	s, err := NewStore(64, 4, discardLogger{})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if _, _, err := s.Get(""); !errors.Is(err, ErrEmptyKey) {
		t.Errorf("Get 空键 err=%v", err)
	}
	if err := s.Put("", "v"); !errors.Is(err, ErrEmptyKey) {
		t.Errorf("Put 空键 err=%v", err)
	}
	if err := s.Delete(""); !errors.Is(err, ErrEmptyKey) {
		t.Errorf("Delete 空键 err=%v", err)
	}

	if _, err := s.Rescale(4); !errors.Is(err, ErrParallelismUnchanged) {
		t.Errorf("Rescale 相同并行度 err=%v, 期望 ErrParallelismUnchanged", err)
	}
	for _, p := range []int{0, -2, 65} {
		_, err := s.Rescale(p)
		switch {
		case p <= 0 && !errors.Is(err, ErrInvalidParallelism):
			t.Errorf("Rescale(%d) err=%v, 期望 ErrInvalidParallelism", p, err)
		case p > 64 && !errors.Is(err, ErrParallelismTooLarge):
			t.Errorf("Rescale(%d) err=%v, 期望 ErrParallelismTooLarge", p, err)
		}
	}
	if p := s.Parallelism(); p != 4 {
		t.Fatalf("被拒绝的扩缩容改变了并行度: %d", p)
	}
	if _, err := NewStore(8, 1, nil); !errors.Is(err, ErrInvalidLogger) {
		t.Errorf("nil logger err=%v, 期望 ErrInvalidLogger", err)
	}
	if _, err := KeyToGroup("", 8); !errors.Is(err, ErrEmptyKey) {
		t.Errorf("KeyToGroup 空键 err=%v", err)
	}
	if _, err := KeyToGroup("k", 0); !errors.Is(err, ErrInvalidGroups) {
		t.Errorf("KeyToGroup 零桶 err=%v", err)
	}
	if _, err := OwnerOf(64, 64, 4); err == nil {
		t.Errorf("OwnerOf 越界键组应当报错")
	}
}

func TestRescaleConservation(t *testing.T) {
	var logBuf bytes.Buffer
	s, err := NewStore(37, 1, NewLogger(&logBuf))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	const keyCount = 500
	original := make(map[string]string, keyCount)
	for i := 0; i < keyCount; i++ {
		key := fmt.Sprintf("key-%04d", i)
		value := fmt.Sprintf("value-%04d", i)
		if err := s.Put(key, value); err != nil {
			t.Fatalf("Put: %v", err)
		}
		original[key] = value
	}

	sequence := []int{3, 7, 2, 13, 4, 37, 5, 1}
	var firstResult RescaleResult
	var firstLog string
	for round, target := range sequence {
		before := s.Snapshot()
		result, err := s.Rescale(target)
		if err != nil {
			t.Fatalf("round %d Rescale(%d): %v", round, target, err)
		}
		if round == 0 {
			firstResult = result
			firstLog = logBuf.String()
		}
		wantMoved := 0
		for _, ownership := range result.Groups {
			expectedMoved := ownership.OldOwner != ownership.NewOwner
			if ownership.Moved != expectedMoved {
				t.Fatalf("键组 %d Moved=%v, 期望 %v", ownership.Group, ownership.Moved, expectedMoved)
			}
			if expectedMoved {
				wantMoved += ownership.KeyCount
			}
		}
		if result.MovedKeys != wantMoved || result.TotalKeys != keyCount {
			t.Fatalf("round %d 迁移统计不符: moved=%d want=%d total=%d",
				round, result.MovedKeys, wantMoved, result.TotalKeys)
		}
		after := s.Snapshot()
		if after.Parallelism != target {
			t.Fatalf("扩缩容后并行度=%d, 期望 %d", after.Parallelism, target)
		}
		if len(after.Values) != len(before.Values) || len(after.Values) != keyCount {
			t.Fatalf("键数量变化: before=%d after=%d", len(before.Values), len(after.Values))
		}
		for key, value := range original {
			got, ok := after.Values[key]
			if !ok {
				t.Fatalf("键 %q 在扩缩容后丢失", key)
			}
			if got != value {
				t.Fatalf("键 %q 值变化: %q -> %q", key, value, got)
			}
		}
		for key, owner := range after.KeyOwners {
			group, _ := s.GroupOf(key)
			expected, _ := OwnerOf(group, s.NumGroups(), target)
			if owner != expected || owner < 0 || owner >= target {
				t.Fatalf("键 %q 快照归属 %d 与区间划分 %d 不一致或越界", key, owner, expected)
			}
		}
	}

	s2, _ := NewStore(37, 1, discardLogger{})
	for i := 0; i < keyCount; i++ {
		_ = s2.Put(fmt.Sprintf("key-%04d", i), fmt.Sprintf("value-%04d", i))
	}
	r2, err := s2.Rescale(3)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprintf("%v", r2.MovedGroups) != fmt.Sprintf("%v", firstResult.MovedGroups) ||
		r2.MovedKeys != firstResult.MovedKeys {
		t.Fatalf("重复计算结果不确定: %+v vs %+v", r2, firstResult)
	}

	for _, want := range []string{
		"扩缩容输入", "旧区间", "新区间", "归属", "整组迁移",
		"原地不动", "迁移结果", "判定依据", "迁移键数",
	} {
		if !strings.Contains(firstLog, want) {
			t.Errorf("日志缺少 %q\n日志:\n%s", want, firstLog)
		}
	}
}

func TestRejectedRescaleKeepsState(t *testing.T) {
	s, _ := NewStore(16, 2, discardLogger{})
	_ = s.Put("a", "1")
	_ = s.Put("b", "2")
	if _, err := s.Rescale(17); !errors.Is(err, ErrParallelismTooLarge) {
		t.Fatalf("err=%v", err)
	}
	snap := s.Snapshot()
	if snap.Parallelism != 2 || len(snap.Values) != 2 || snap.Values["a"] != "1" || snap.Values["b"] != "2" {
		t.Fatalf("被拒绝操作后状态被改变: %+v", snap)
	}
}

func TestConcurrentReadsDuringRescale(t *testing.T) {
	s, _ := NewStore(64, 4, discardLogger{})
	for i := 0; i < 300; i++ {
		_ = s.Put(fmt.Sprintf("k%d", i), fmt.Sprintf("v%d", i))
	}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				snap := s.Snapshot()
				if snap.Parallelism != len(snap.Ranges) {
					t.Errorf("快照不一致: parallelism=%d ranges=%d", snap.Parallelism, len(snap.Ranges))
					return
				}
				if snap.Ranges[0].Start != 0 || snap.Ranges[len(snap.Ranges)-1].End != snap.NumGroups {
					t.Errorf("快照区间不覆盖全部键组")
					return
				}
				for key, owner := range snap.KeyOwners {
					expectedOwner, _ := OwnerOf(mustGroup(t, key), snap.NumGroups, snap.Parallelism)
					if owner != expectedOwner || snap.Values[key] == "" {
						t.Errorf("快照内键 %q 归属/值不一致", key)
						return
					}
				}
			}
		}()
	}
	go func() {
		for _, p := range []int{1, 8, 3, 16, 7, 64, 2, 4} {
			_, _ = s.Rescale(p)
		}
		close(stop)
	}()
	wg.Wait()

	final := s.Snapshot()
	if len(final.Values) != 300 {
		t.Fatalf("并发扩缩容后键数 = %d, 期望 300", len(final.Values))
	}
	for i := 0; i < 300; i++ {
		key := fmt.Sprintf("k%d", i)
		if final.Values[key] != fmt.Sprintf("v%d", i) {
			t.Fatalf("键 %q 值丢失或错乱: %q", key, final.Values[key])
		}
	}
}

func mustGroup(t *testing.T, key string) int {
	t.Helper()
	g, err := KeyToGroup(key, 64)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestDeleteAndGroupOf(t *testing.T) {
	s, _ := NewStore(12, 2, NewLogger(nil))
	if err := s.Put("k1", "v1"); err != nil {
		t.Fatal(err)
	}
	gBefore, err := s.GroupOf("k1")
	if err != nil {
		t.Fatal(err)
	}
	gVirtual, err := s.GroupOf("not-stored")
	if err != nil {
		t.Fatalf("未写入的键也应能计算固定键组: %v", err)
	}
	if _, err := s.GroupOf(""); !errors.Is(err, ErrEmptyKey) {
		t.Errorf("GroupOf 空键 err=%v", err)
	}
	if err := s.Delete("k1"); err != nil {
		t.Fatal(err)
	}
	if gAfter, _ := s.GroupOf("k1"); gAfter != gBefore {
		t.Fatalf("删除后键组映射不应改变: %d != %d", gAfter, gBefore)
	}
	if s.Len() != 0 {
		t.Fatalf("删除后 Len=%d", s.Len())
	}
	if _, ok, _ := s.Get("k1"); ok {
		t.Fatalf("删除后仍可读到键")
	}
	if err := s.Delete("k1"); err != nil {
		t.Fatalf("重复删除应幂等: %v", err)
	}
	if gVirtual < 0 || gVirtual >= 12 {
		t.Fatalf("虚拟键组越界: %d", gVirtual)
	}
}
