package ontology

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

// logStats 打印输入、各分区计数、水位与判定依据。
func logStats(t *testing.T, label string, inputs []Event, s Stats) {
	t.Helper()
	t.Logf("[%s] inputs=%v", label, inputs)
	for p := range s.PartitionCounts {
		t.Logf("[%s] partition=%d count=%d sum=%d 依据: 位点连续接受 %d 条",
			label, p, s.PartitionCounts[p], s.PartitionSums[p], s.PartitionCounts[p])
	}
	t.Logf("[%s] globalSum=%d watermark=%d 依据: 最小前缀 min%v committedSum=%d",
		label, s.GlobalSum, s.Watermark, s.PartitionCounts, s.CommittedSum)
}

func TestNewRouterInvalid(t *testing.T) {
	for _, n := range []int{0, -1} {
		if _, err := NewRouter(n); err == nil {
			t.Fatalf("NewRouter(%d) expected error", n)
		}
	}
}

// TestInterleavingsReproducible 验证同一批事件以任意交错顺序喂入，终值逐字段一致。
func TestInterleavingsReproducible(t *testing.T) {
	const partitions = 3
	orders := [][]Event{
		{
			{0, 0, 1}, {0, 1, 2}, {0, 2, 3}, {0, 3, 4},
			{1, 0, 2}, {1, 1, 4}, {1, 2, 6}, {1, 3, 8},
			{2, 0, 3}, {2, 1, 6}, {2, 2, 9}, {2, 3, 12},
		},
		{
			{0, 0, 1}, {1, 0, 2}, {2, 0, 3},
			{0, 1, 2}, {1, 1, 4}, {2, 1, 6},
			{0, 2, 3}, {1, 2, 6}, {2, 2, 9},
			{0, 3, 4}, {1, 3, 8}, {2, 3, 12},
		},
		{
			{2, 0, 3}, {0, 0, 1}, {2, 1, 6}, {1, 0, 2},
			{0, 1, 2}, {2, 2, 9}, {2, 3, 12}, {1, 1, 4},
			{0, 2, 3}, {1, 2, 6}, {0, 3, 4}, {1, 3, 8},
		},
	}

	var want Stats
	for i, order := range orders {
		r, err := NewRouter(partitions)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range order {
			if rej := r.Feed(e); rej != nil {
				t.Fatalf("order %d: unexpected reject %v", i, rej)
			}
		}
		got, checkErr := r.SelfCheck()
		if checkErr != nil {
			t.Fatalf("order %d: self-check failed: %v", i, checkErr)
		}
		logStats(t, fmt.Sprintf("interleave-%d", i), order, got)
		if i == 0 {
			want = got
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("order %d final stats %+v != order 0 %+v", i, got, want)
		}
	}

	// 水位推进：最短分区落后时 watermark 与 committedSum 必须随喂入单调不减。
	r, _ := NewRouter(partitions)
	prev := r.Snapshot()
	progression := []Event{
		{0, 0, 5}, {1, 0, 7}, {2, 0, 1},
		{0, 1, 5}, {1, 1, 7},
		{2, 1, 1},
	}
	for _, e := range progression {
		if rej := r.Feed(e); rej != nil {
			t.Fatalf("unexpected reject: %v", rej)
		}
		s := r.Snapshot()
		logStats(t, "watermark-progression", []Event{e}, s)
		if s.Watermark < prev.Watermark || s.CommittedSum < prev.CommittedSum ||
			s.GlobalSum < prev.GlobalSum {
			t.Fatalf("stats not monotonic: prev=%+v cur=%+v", prev, s)
		}
		prev = s
	}
	if got := r.Snapshot(); got.Watermark != 2 || got.CommittedSum != (5+1+7+1)+(5+7) {
		t.Fatalf("unexpected final watermark stats: %+v", got)
	}
}

// TestRejections 覆盖分区号越界、位点不连续、负值，并验证失败不改变任何状态。
func TestRejections(t *testing.T) {
	r, err := NewRouter(2)
	if err != nil {
		t.Fatal(err)
	}
	seed := []Event{{0, 0, 10}, {1, 0, 20}}
	for _, e := range seed {
		if rej := r.Feed(e); rej != nil {
			t.Fatalf("seed rejected: %v", rej)
		}
	}
	before, _ := r.SelfCheck()

	cases := []struct {
		name   string
		e      Event
		reason RejectReason
	}{
		{"partition negative", Event{Partition: -1, Offset: 0, Value: 1}, ReasonPartitionOutOfRange},
		{"partition too large", Event{Partition: 2, Offset: 0, Value: 1}, ReasonPartitionOutOfRange},
		{"offset gap", Event{Partition: 0, Offset: 5, Value: 1}, ReasonOffsetGap},
		{"offset replay", Event{Partition: 0, Offset: 0, Value: 1}, ReasonOffsetGap},
		{"negative value", Event{Partition: 0, Offset: 1, Value: -3}, ReasonNegativeValue},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rej := r.Feed(tc.e)
			if rej == nil {
				t.Fatalf("expected rejection for %+v", tc.e)
			}
			if rej.Reason != tc.reason {
				t.Fatalf("reason=%q want %q", rej.Reason, tc.reason)
			}
			if !errors.Is(rej, ErrRejected) {
				t.Fatalf("reject error should wrap ErrRejected")
			}
			t.Logf("[reject/%s] input=%+v 判定依据=%s: %s", tc.name, tc.e, rej.Reason, rej.Error())
			after, checkErr := r.SelfCheck()
			if checkErr != nil {
				t.Fatalf("self-check after reject: %v", checkErr)
			}
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("state changed by failed feed: before=%+v after=%+v", before, after)
			}
		})
	}

	// 整批原子性：批次中第 3 条非法，前两条也不得落状态。
	badBatch := []Event{{0, 1, 1}, {1, 1, 1}, {0, 3, 1}}
	if rej := r.FeedBatch(badBatch); rej == nil || rej.Reason != ReasonOffsetGap || rej.Index != 2 {
		t.Fatalf("bad batch reject mismatch: %+v", rej)
	}
	if after, _ := r.SelfCheck(); !reflect.DeepEqual(after, before) {
		t.Fatalf("state changed by failed batch: before=%+v after=%+v", before, after)
	}

	// 批内同分区乱序也必须拒绝并整批回滚。
	outOfOrderBatch := []Event{{0, 1, 1}, {0, 1, 1}}
	if rej := r.FeedBatch(outOfOrderBatch); rej == nil || rej.Reason != ReasonOffsetGap {
		t.Fatalf("out-of-order batch reject mismatch: %+v", rej)
	}
	if after, _ := r.SelfCheck(); !reflect.DeepEqual(after, before) {
		t.Fatalf("state changed by failed batch: before=%+v after=%+v", before, after)
	}

	// 合法批次跨分区交错可整体接受。
	goodBatch := []Event{{0, 1, 2}, {1, 1, 4}, {0, 2, 6}, {1, 2, 8}}
	if rej := r.FeedBatch(goodBatch); rej != nil {
		t.Fatalf("good batch rejected: %v", rej)
	}
	s, _ := r.SelfCheck()
	logStats(t, "after-good-batch", goodBatch, s)
	if s.PartitionCounts[0] != 3 || s.PartitionCounts[1] != 3 ||
		s.Watermark != 3 || s.GlobalSum != 10+20+2+4+6+8 || s.CommittedSum != s.GlobalSum {
		t.Fatalf("unexpected stats after good batch: %+v", s)
	}
}

// TestConcurrentFeedersAndReaders 验证并发喂入（每分区严格按序、跨分区交错）
// 终值可复现，且并发读取同一实例的快照始终自洽。
func TestConcurrentFeedersAndReaders(t *testing.T) {
	const partitions = 4
	const perPartition = 200
	r, err := NewRouter(partitions)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for p := 0; p < partitions; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			for off := 0; off < perPartition; off++ {
				e := Event{Partition: p, Offset: uint64(off), Value: int64(p + off)}
				if rej := r.Feed(e); rej != nil {
					t.Errorf("partition %d offset %d rejected: %v", p, off, rej)
					return
				}
			}
		}(p)
	}

	stop := make(chan struct{})
	var readerWg sync.WaitGroup
	for i := 0; i < 4; i++ {
		readerWg.Add(1)
		go func() {
			defer readerWg.Done()
			var last *Stats
			for {
				select {
				case <-stop:
					return
				default:
				}
				s := r.Snapshot()
				if len(s.PartitionCounts) != partitions || len(s.PartitionSums) != partitions {
					t.Errorf("snapshot slice length mismatch: %+v", s)
					return
				}
				var sum int64
				minCount := int64(1 << 62)
				for _, v := range s.PartitionSums {
					sum += v
				}
				for _, c := range s.PartitionCounts {
					if c < minCount {
						minCount = c
					}
				}
				if sum != s.GlobalSum || minCount != s.Watermark {
					t.Errorf("inconsistent snapshot: %+v", s)
					return
				}
				if last != nil && (s.Watermark < last.Watermark || s.GlobalSum < last.GlobalSum) {
					t.Errorf("snapshot regressed: %+v -> %+v", last, s)
					return
				}
				last = &s
			}
		}()
	}

	wg.Wait()
	close(stop)
	readerWg.Wait()

	final, checkErr := r.SelfCheck()
	if checkErr != nil {
		t.Fatalf("self-check: %v", checkErr)
	}
	logStats(t, "concurrent-final", nil, final)

	// 终值必须与串行喂入同一事件集合的结果逐字段相同。
	serial, _ := NewRouter(partitions)
	for p := 0; p < partitions; p++ {
		for off := 0; off < perPartition; off++ {
			if rej := serial.Feed(Event{p, uint64(off), int64(p + off)}); rej != nil {
				t.Fatalf("serial feed rejected: %v", rej)
			}
		}
	}
	want, _ := serial.SelfCheck()
	if !reflect.DeepEqual(final, want) {
		t.Fatalf("concurrent final %+v != serial %+v", final, want)
	}
}

// TestBatchRecomputeCrossCheck 与“按位点批量重算”的独立实现逐轮对照。
func TestBatchRecomputeCrossCheck(t *testing.T) {
	const partitions = 3
	rng := rand.New(rand.NewSource(42))

	// grid[p][off] 保存独立输入源的值，用于脱离路由器按位点重算。
	grid := make([][]int64, partitions)
	r, _ := NewRouter(partitions)

	var globalWant int64
	for round := 0; round < 20; round++ {
		n := 1 + rng.Intn(20)
		batch := make([]Event, 0, n)
		for k := 0; k < n; k++ {
			p := rng.Intn(partitions)
			off := len(grid[p])
			v := rng.Int63n(1000)
			grid[p] = append(grid[p], v)
			batch = append(batch, Event{p, uint64(off), v})
			globalWant += v
		}
		if rej := r.FeedBatch(batch); rej != nil {
			t.Fatalf("round %d batch rejected: %v", round, rej)
		}

		// 独立重算：直接遍历每个分区 [0,watermark) 前缀，不读路由器内部累加。
		got := r.Snapshot()
		watermark := int64(1<<62 - 1)
		var sums [partitions]int64
		var counts [partitions]int64
		for p := 0; p < partitions; p++ {
			counts[p] = int64(len(grid[p]))
			if counts[p] < watermark {
				watermark = counts[p]
			}
			for _, v := range grid[p] {
				sums[p] += v
			}
		}
		var committedWant int64
		for p := 0; p < partitions; p++ {
			for off := int64(0); off < watermark; off++ {
				committedWant += grid[p][off]
			}
		}
		if got.Watermark != watermark || got.GlobalSum != globalWant ||
			got.CommittedSum != committedWant {
			t.Fatalf("round %d stats %+v != recomputed watermark=%d global=%d committed=%d",
				round, got, watermark, globalWant, committedWant)
		}
		for p := 0; p < partitions; p++ {
			if got.PartitionCounts[p] != counts[p] || got.PartitionSums[p] != sums[p] {
				t.Fatalf("round %d partition %d stats %+v != counts=%d sums=%d",
					round, p, got, counts[p], sums[p])
			}
		}
		if check, err := r.SelfCheck(); err != nil {
			t.Fatalf("round %d self-check: %v (%+v)", round, err, check)
		}
		if round == 19 {
			logStats(t, "recompute-final", batch, got)
		}
	}
}
