package runtimefilter

import (
	"fmt"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"
)

const (
	ccShards  = 4
	ccScans   = 8
	ccBatches = 50
	ccBatchSz = 16
)

// ccRow 用值身份区分每行，避免并发下共享指针。
type ccRow struct {
	id  int
	key *int64
}

func ccKeyOf(r Row) (int64, bool) {
	rr := r.(ccRow)
	if rr.key == nil {
		return 0, false
	}
	return *rr.key, true
}

// ccInput 生成确定性输入：同样参数永远得到同样的行集合。
func ccInput() [][][]Row {
	input := make([][][]Row, ccScans)
	id := 0
	for s := 0; s < ccScans; s++ {
		input[s] = make([][]Row, ccBatches)
		for b := 0; b < ccBatches; b++ {
			input[s][b] = make([]Row, ccBatchSz)
			for r := 0; r < ccBatchSz; r++ {
				v := int64((s*7 + b*3 + r) % 60)
				var k *int64
				if (s+b+r)%13 != 0 {
					k = &v
				}
				input[s][b][r] = ccRow{id: id, key: k}
				id++
			}
		}
	}
	return input
}

func ccBuildSet() map[int64]struct{} {
	build := make(map[int64]struct{})
	// 与每个分片报告的取值范围一致：[s*10, s*10+9]，合计 0..39。
	for s := 0; s < ccShards; s++ {
		for k := int64(s * 10); k < int64(s*10+10); k++ {
			build[k] = struct{}{}
		}
	}
	return build
}

// referenceResult 是“无过滤器 + 内连接哈希探测”的语义对拍：
// 保留空键之外、且键在构建集合内的行。
func referenceResult(input [][][]Row) []int {
	build := ccBuildSet()
	var ids []int
	for _, batches := range input {
		for _, batch := range batches {
			for _, r := range batch {
				rr := r.(ccRow)
				if rr.key == nil {
					continue
				}
				if _, ok := build[*rr.key]; ok {
					ids = append(ids, rr.id)
				}
			}
		}
	}
	sort.Ints(ids)
	return ids
}

func runConcurrent(t *testing.T, input [][][]Row, staggerReports bool) (joinedIDs, passedEarly []int, stats []Stats) {
	t.Helper()
	c, _ := newTestCoordinator(t, JoinInner, ccShards, 1<<30, 2*time.Second)

	var wg sync.WaitGroup

	// 构建侧：分片并发报告。
	wg.Add(ccShards)
	for s := 0; s < ccShards; s++ {
		s := s
		go func() {
			defer wg.Done()
			if staggerReports {
				time.Sleep(time.Duration(s) * 2 * time.Millisecond)
			}
			keys := make([]int64, 0, 10)
			for k := int64(s * 10); k < int64(s*10+10); k++ {
				keys = append(keys, k)
			}
			if err := c.Report(ShardReport[int64]{
				Shard:    s,
				Min:      keys[0],
				Max:      keys[len(keys)-1],
				Distinct: setOf(keys...),
			}); err != nil {
				t.Errorf("report shard %d: %v", s, err)
			}
		}()
	}

	scanners := make([]*Scanner[int64], ccScans)
	for s := range scanners {
		scanners[s] = c.NewScanner(fmt.Sprintf("probe-%d", s), ccKeyOf)
	}

	var mu sync.Mutex
	var keptIDs []int
	var earlyIDs []int

	// 探测侧：多个扫描并发按批取行。
	wg.Add(ccScans)
	for s := 0; s < ccScans; s++ {
		s := s
		go func() {
			defer wg.Done()
			for b := 0; b < ccBatches; b++ {
				before := c.readySnapshot()
				out, st := scanners[s].FilterBatch(input[s][b], time.Time{})
				if st.Passed+st.Dropped != st.Scanned {
					t.Errorf("batch invariant broken s=%d b=%d %+v", s, b, st)
				}
				mu.Lock()
				for _, r := range out {
					id := r.(ccRow).id
					keptIDs = append(keptIDs, id)
					if !before {
						// 就绪前放行的行：它们必须仍能通过后续真实探测，
						// 否则过滤器改变了结果。
						earlyIDs = append(earlyIDs, id)
					}
				}
				mu.Unlock()
			}
		}()
	}

	wg.Wait()

	stats = make([]Stats, ccScans)
	totalScanned := 0
	for s, sc := range scanners {
		stats[s] = sc.Stats()
		totalScanned += stats[s].Scanned
		if stats[s].Passed+stats[s].Dropped != stats[s].Scanned {
			t.Fatalf("scanner %d invariant: %+v", s, stats[s])
		}
		if stats[s].Scanned != ccBatches*ccBatchSz {
			t.Fatalf("scanner %d scanned=%d want %d", s, stats[s].Scanned, ccBatches*ccBatchSz)
		}
	}
	if totalScanned != ccScans*ccBatches*ccBatchSz {
		t.Fatalf("total scanned=%d", totalScanned)
	}

	sort.Ints(keptIDs)
	sort.Ints(earlyIDs)

	// 对拍：过滤器放行的行再做一次真实内连接探测，必须全部存活；
	// 再验证所有存活行恰好等于参考结果。
	build := ccBuildSet()
	joined := make([]int, 0, len(keptIDs))
	for _, id := range keptIDs {
		// 通过 id 找回键（输入不可变，直接重建映射）。
		if k, ok := idKey[id]; ok {
			if _, hit := build[k]; hit {
				joined = append(joined, id)
			} else {
				t.Fatalf("filter passed non-matching row id=%d key=%d", id, k)
			}
		} else {
			t.Fatalf("unknown row id=%d", id)
		}
	}
	sort.Ints(joined)
	return joined, earlyIDs, stats
}

var idKey = func() map[int]int64 {
	m := make(map[int]int64)
	for _, batches := range ccInput() {
		for _, batch := range batches {
			for _, r := range batch {
				rr := r.(ccRow)
				if rr.key != nil {
					m[rr.id] = *rr.key
				}
			}
		}
	}
	return m
}()

// 并发执行结果必须与“不加过滤器做内连接”的结果完全一致。
func TestConcurrentEquivalence(t *testing.T) {
	input := ccInput()
	want := referenceResult(input)

	for _, stagger := range []bool{false, true} {
		got, early, _ := runConcurrent(t, input, stagger)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("stagger=%v mismatch: got %d rows want %d rows", stagger, len(got), len(want))
		}
		if stagger && len(early) == 0 {
			t.Fatal("expected some batches to pass before filter ready")
		}
	}
}

// 同一输入反复执行结果完全相同（确定性）。
func TestDeterministicAcrossRuns(t *testing.T) {
	input := ccInput()
	first, _, stats1 := runConcurrent(t, input, true)
	second, _, stats2 := runConcurrent(t, input, true)
	if !reflect.DeepEqual(first, second) {
		t.Fatal("filtered result differs across runs")
	}
	if !reflect.DeepEqual(stats1, stats2) {
		t.Fatalf("stats differ across runs:\n%+v\n%+v", stats1, stats2)
	}
}

// 无过滤器基线（全部放行）下，对拍函数 referenceResult 正好是其再做内连接的结果：
// 过滤器丢弃的行不会进入最终连接输出。
func TestBaselineSanity(t *testing.T) {
	input := ccInput()
	want := referenceResult(input)
	if len(want) == 0 || len(want) >= ccScans*ccBatches*ccBatchSz {
		t.Fatalf("reference set suspicious size=%d", len(want))
	}
}
