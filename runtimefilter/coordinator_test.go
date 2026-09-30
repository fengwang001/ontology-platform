package runtimefilter

import (
	"errors"
	"fmt"
	"log"
	"sync"
	"testing"
	"time"
)

// testWriter 把协调器日志接入测试日志，打印输入、输出与判定依据。
type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Logf("%s", p)
	return len(p), nil
}

func newCoord(t *testing.T, jt JoinType, shards, maxDistinct int, timeout time.Duration) *Coordinator {
	t.Helper()
	return NewCoordinator(jt, shards, maxDistinct, timeout, log.New(testWriter{t}, "[rf] ", 0))
}

func rowsOf(keys ...any) []Row {
	rows := make([]Row, len(keys))
	for i, k := range keys {
		if k == nil {
			rows[i] = Row{KeyNull: true, Payload: fmt.Sprintf("r%d", i)}
		} else {
			rows[i] = Row{Key: int64(k.(int)), Payload: fmt.Sprintf("r%d", i)}
		}
	}
	return rows
}

func checkInvariant(t *testing.T, s BatchStats) {
	t.Helper()
	if s.Dropped+s.Passed != s.Scanned {
		t.Fatalf("不变量被破坏: dropped=%d + passed=%d != scanned=%d", s.Dropped, s.Passed, s.Scanned)
	}
}

func reportAll(t *testing.T, c *Coordinator, summaries ...ShardSummary) {
	t.Helper()
	for i, s := range summaries {
		if err := c.ReportShard(i, s); err != nil {
			t.Fatalf("ReportShard(%d) 失败: %v", i, err)
		}
	}
}

// 左外连接与反连接：探测侧是保留侧，一律不过滤。
func TestPreservedSideNotFiltered(t *testing.T) {
	for _, jt := range []JoinType{JoinLeftOuter, JoinLeftAnti, JoinFullOuter} {
		t.Run(jt.String(), func(t *testing.T) {
			c := newCoord(t, jt, 2, 100, time.Second)
			reportAll(t, c,
				ShardSummary{Min: 10, Max: 20, Keys: []int64{10, 15, 20}},
				ShardSummary{Empty: true},
			)
			in := rowsOf(5, nil, 15, 999)
			got, stats := c.FilterBatch(in)
			checkInvariant(t, stats)
			if stats.Dropped != 0 || len(got) != len(in) {
				t.Fatalf("保留侧不应过滤: dropped=%d got=%d want=%d", stats.Dropped, len(got), len(in))
			}
		})
	}
}

// 空键：就绪后键为空的行被丢弃（等值连接下空键永不匹配）。
func TestNullKeysDropped(t *testing.T) {
	c := newCoord(t, JoinInner, 1, 100, time.Second)
	reportAll(t, c, ShardSummary{Min: 1, Max: 10, Keys: []int64{1, 5, 10}})
	in := rowsOf(nil, 1, 7, nil, 10)
	got, stats := c.FilterBatch(in)
	checkInvariant(t, stats)
	if stats.Dropped != 3 || stats.Passed != 2 {
		t.Fatalf("dropped=%d passed=%d, want dropped=3 passed=2", stats.Dropped, stats.Passed)
	}
	for _, r := range got {
		if r.KeyNull || (r.Key != 1 && r.Key != 10) {
			t.Fatalf("放行行不符合摘要: %+v", r)
		}
	}
}

// 构建侧全部为空：内连接与半连接的探测行全部丢弃。
func TestEmptyBuildSide(t *testing.T) {
	for _, jt := range []JoinType{JoinInner, JoinLeftSemi} {
		t.Run(jt.String(), func(t *testing.T) {
			c := newCoord(t, jt, 2, 100, time.Second)
			reportAll(t, c, ShardSummary{Empty: true}, ShardSummary{Empty: true})
			in := rowsOf(1, nil, 42)
			got, stats := c.FilterBatch(in)
			checkInvariant(t, stats)
			if len(got) != 0 || stats.Dropped != len(in) {
				t.Fatalf("构建侧为空应全部丢弃: passed=%d dropped=%d", stats.Passed, stats.Dropped)
			}
		})
	}
}

// 合并后去重数超过上限：降级为只保留最小/最大值。
func TestDegradeOnDistinctOverflow(t *testing.T) {
	c := newCoord(t, JoinInner, 2, 3, time.Second)
	reportAll(t, c,
		ShardSummary{Min: 1, Max: 100, Keys: []int64{1, 50, 100}},
		ShardSummary{Min: 2, Max: 99, Keys: []int64{2, 99}},
	)
	// 5 个去重键 > 上限 3，降级：区间内不在集合中的 42 也放行，区间外丢弃。
	in := rowsOf(42, 0, 101, 50)
	got, stats := c.FilterBatch(in)
	checkInvariant(t, stats)
	if stats.Passed != 2 || stats.Dropped != 2 {
		t.Fatalf("降级后应按区间过滤: passed=%d dropped=%d", stats.Passed, stats.Dropped)
	}
	if got[0].Key != 42 || got[1].Key != 50 {
		t.Fatalf("降级后放行行错误: %v", rowKeys(got))
	}
}

// 某分片放弃：过滤器作废，所有行（含空键）放行。
func TestShardAbortInvalidates(t *testing.T) {
	c := newCoord(t, JoinInner, 2, 100, time.Second)
	reportAll(t, c,
		ShardSummary{Min: 1, Max: 10, Keys: []int64{1}},
		ShardSummary{Aborted: true},
	)
	in := rowsOf(nil, 999, 1)
	got, stats := c.FilterBatch(in)
	checkInvariant(t, stats)
	if stats.Dropped != 0 || len(got) != len(in) {
		t.Fatalf("过滤器作废应全部放行: dropped=%d got=%d", stats.Dropped, len(got))
	}
}

// 超时后晚到：超时批原样放行，晚到的过滤器只作用于其后的批。
func TestTimeoutThenLateArrival(t *testing.T) {
	c := newCoord(t, JoinInner, 1, 100, 50*time.Millisecond)
	in := rowsOf(1, nil, 999)
	start := time.Now()
	got, stats := c.FilterBatch(in)
	checkInvariant(t, stats)
	if stats.Dropped != 0 || len(got) != len(in) {
		t.Fatalf("超时批应原样放行: dropped=%d", stats.Dropped)
	}
	if elapsed := time.Since(start); elapsed < 50*time.Millisecond {
		t.Fatalf("应等待至超时: elapsed=%v", elapsed)
	}
	// 过滤器晚到，只影响后续批。
	reportAll(t, c, ShardSummary{Min: 1, Max: 1, Keys: []int64{1}})
	got, stats = c.FilterBatch(in)
	checkInvariant(t, stats)
	if stats.Passed != 1 || got[0].Key != 1 {
		t.Fatalf("晚到的过滤器应作用于其后批: passed=%d got=%v", stats.Passed, rowKeys(got))
	}
}

// 报告校验：未知连接类型、分片越界、重复报告给出可区分错误且不改变状态。
func TestReportRejection(t *testing.T) {
	c := newCoord(t, JoinUnknown, 2, 100, time.Second)
	if err := c.ReportShard(0, ShardSummary{Empty: true}); !errors.Is(err, ErrUnknownJoinType) {
		t.Fatalf("want ErrUnknownJoinType, got %v", err)
	}

	c = newCoord(t, JoinInner, 2, 100, time.Second)
	if err := c.ReportShard(2, ShardSummary{Empty: true}); !errors.Is(err, ErrShardOutOfRange) {
		t.Fatalf("want ErrShardOutOfRange, got %v", err)
	}
	if err := c.ReportShard(-1, ShardSummary{Empty: true}); !errors.Is(err, ErrShardOutOfRange) {
		t.Fatalf("want ErrShardOutOfRange, got %v", err)
	}
	if err := c.ReportShard(0, ShardSummary{Min: 1, Max: 1, Keys: []int64{1}}); err != nil {
		t.Fatalf("首次报告应成功: %v", err)
	}
	if err := c.ReportShard(0, ShardSummary{Empty: true}); !errors.Is(err, ErrDuplicateShard) {
		t.Fatalf("want ErrDuplicateShard, got %v", err)
	}
	// 被拒绝的报告不改变状态：分片 1 报告后正常就绪，且分片 0 仍是首次报告的值。
	if err := c.ReportShard(1, ShardSummary{Min: 2, Max: 2, Keys: []int64{2}}); err != nil {
		t.Fatalf("被拒绝报告不应影响后续: %v", err)
	}
	got, stats := c.FilterBatch(rowsOf(1, 2, 3))
	checkInvariant(t, stats)
	if stats.Passed != 2 {
		t.Fatalf("状态应只含两次有效报告的合并: passed=%d want 2", stats.Passed)
	}
	_ = got
}

// 并发对拍：多探测扫描与分片报告并发执行，过滤结果与不加过滤器对拍，
// 且同一输入反复执行结果完全相同。
func TestConcurrentDifferential(t *testing.T) {
	const (
		shards      = 4
		scanners    = 8
		batches     = 20
		batchSize   = 32
		maxKey      = 1000
		maxDistinct = 4096
	)
	// 固定的构建侧全集，用于对拍。
	buildKeys := make(map[int64]struct{})
	var summaries [shards]ShardSummary
	seed := int64(42)
	rnd := func() int64 { seed = seed*6364136223846793005 + 1442695040888963407; return seed >> 33 }
	for i := 0; i < shards; i++ {
		keys := map[int64]struct{}{}
		for len(keys) < 50 {
			keys[rnd()%maxKey] = struct{}{}
		}
		s := ShardSummary{Min: maxKey, Max: -1}
		for k := range keys {
			s.Keys = append(s.Keys, k)
			buildKeys[k] = struct{}{}
			if k < s.Min {
				s.Min = k
			}
			if k > s.Max {
				s.Max = k
			}
		}
		summaries[i] = s
	}
	// 固定的探测输入（含空键），由确定性序列生成。
	probe := make([][]Row, batches)
	for b := 0; b < batches; b++ {
		for i := 0; i < batchSize; i++ {
			v := rnd()
			if v%7 == 0 {
				probe[b] = append(probe[b], Row{KeyNull: true})
			} else {
				probe[b] = append(probe[b], Row{Key: v % (maxKey + 50)})
			}
		}
	}
	// 不加过滤器的基准：等值内连接只保留键在构建侧全集中的行。
	// 判定依据是最终连接结果而非每批放行数——放行数随超时/就绪时序变化，
	// 但连接结果必须恒定。
	want := map[int]map[int64]int{} // 批号 -> 键 -> 应输出行数
	for b, batch := range probe {
		for _, r := range batch {
			if r.KeyNull {
				continue
			}
			if _, ok := buildKeys[r.Key]; ok {
				if want[b] == nil {
					want[b] = map[int64]int{}
				}
				want[b][r.Key]++
			}
		}
	}

	// runOnce 并发执行一次，返回每批过滤放行行中真正匹配连接结果的键计数。
	runOnce := func() []map[int64]int {
		c := newCoord(t, JoinInner, shards, maxDistinct, 2*time.Millisecond)
		matched := make([]map[int64]int, batches)
		for b := range matched {
			matched[b] = map[int64]int{}
		}
		var mu sync.Mutex
		var wg sync.WaitGroup
		// 并发上报分片。
		for i := 0; i < shards; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				time.Sleep(time.Duration(i) * time.Millisecond)
				if err := c.ReportShard(i, summaries[i]); err != nil {
					t.Errorf("ReportShard(%d): %v", i, err)
				}
			}(i)
		}
		// 并发扫描批。
		for s := 0; s < scanners; s++ {
			wg.Add(1)
			go func(s int) {
				defer wg.Done()
				for b := s; b < batches; b += scanners {
					kept, stats := c.FilterBatch(probe[b])
					if stats.Dropped+stats.Passed != stats.Scanned {
						t.Errorf("不变量被破坏: %+v", stats)
					}
					mu.Lock()
					for _, r := range kept {
						if _, ok := buildKeys[r.Key]; ok && !r.KeyNull {
							matched[b][r.Key]++
						}
					}
					mu.Unlock()
				}
			}(s)
		}
		wg.Wait()
		return matched
	}

	first := runOnce()
	second := runOnce()
	for b := 0; b < batches; b++ {
		// 对拍：过滤后的连接结果必须与不加过滤器完全相同。
		if len(first[b]) != len(want[b]) {
			t.Fatalf("批 %d 连接结果与基准不同: got=%v want=%v", b, first[b], want[b])
		}
		for k, n := range want[b] {
			if first[b][k] != n {
				t.Fatalf("批 %d 键 %d 连接结果与基准不同: got=%d want=%d", b, k, first[b][k], n)
			}
		}
		// 同一输入反复执行，连接结果完全相同。
		if len(first[b]) != len(second[b]) {
			t.Fatalf("批 %d 两次执行结果不同: %v != %v", b, first[b], second[b])
		}
		for k, n := range first[b] {
			if second[b][k] != n {
				t.Fatalf("批 %d 键 %d 两次执行结果不同: %d != %d", b, k, n, second[b][k])
			}
		}
	}
	t.Logf("对拍基准(不加过滤器的连接结果): %v", want)
	t.Logf("过滤后连接结果(两次执行一致): %v", first)
}
