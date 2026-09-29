package router

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
)

// logStats 打印单测要求的输入、各分区计数、水位与判定依据。
func logStats(t *testing.T, tag string, events []Event, s Stats, reason string) {
	t.Helper()
	t.Logf("[%s] input=%v", tag, events)
	for p, ps := range s.Partitions {
		t.Logf("[%s]   partition[%d] count=%d sum=%d", tag, p, ps.Accepted, ps.Sum)
	}
	t.Logf("[%s]   totalSum=%d watermark=%d committedSum=%d | %s",
		tag, s.TotalSum, s.Watermark, s.CommittedSum, reason)
}

func TestIngestBasicWatermark(t *testing.T) {
	r := New(2)
	batch := []Event{
		{Partition: 0, Offset: 0, Value: 1},
		{Partition: 1, Offset: 0, Value: 10},
		{Partition: 0, Offset: 1, Value: 2},
	}
	if err := r.Ingest(batch); err != nil {
		t.Fatalf("ingest failed: %v", err)
	}
	s := r.Stats()
	logStats(t, "basic", batch, s, "判定依据: watermark=min(各分区计数)=1，p0 有 2 条、p1 有 1 条，公共前缀仅位点 0")

	want := Stats{
		Partitions:   []PartitionStats{{Accepted: 2, Sum: 3}, {Accepted: 1, Sum: 10}},
		TotalSum:     13,
		Watermark:    1,
		CommittedSum: 11,
	}
	if !reflect.DeepEqual(s, want) {
		t.Fatalf("stats mismatch:\n got=%+v\nwant=%+v", s, want)
	}
	if err := r.Verify(); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestInterleavingsConverge(t *testing.T) {
	// 同一批事件以不同交错/排列顺序喂入独立实例，终态（含水位与已提交前缀和）必须逐字段一致。
	base := []Event{
		{Partition: 0, Offset: 0, Value: 1},
		{Partition: 1, Offset: 0, Value: 10},
		{Partition: 0, Offset: 1, Value: 2},
		{Partition: 1, Offset: 1, Value: 20},
		{Partition: 0, Offset: 2, Value: 4},
	}
	orders := [][]int{
		{0, 1, 2, 3, 4}, // 位点顺序
		{4, 3, 2, 1, 0}, // 完全逆序
		{1, 0, 3, 2, 4}, // 跨分区交错
		{2, 4, 1, 3, 0}, // 随机打散
	}

	var reference Stats
	for i, idx := range orders {
		r := New(2)
		batch := make([]Event, len(base))
		for j, k := range idx {
			batch[j] = base[k]
		}
		if err := r.Ingest(batch); err != nil {
			t.Fatalf("order %d ingest: %v", i, err)
		}
		got := r.Stats()
		logStats(t, fmt.Sprintf("interleave#%d", i), batch, got,
			"判定依据: 批内按位点排序后必须与各分区当前计数首尾相接，故终态与顺序无关")
		if err := r.Verify(); err != nil {
			t.Fatalf("order %d verify: %v", i, err)
		}
		if i == 0 {
			reference = got
		} else if !reflect.DeepEqual(got, reference) {
			t.Fatalf("order %d stats %+v != reference %+v", i, got, reference)
		}
	}

	if reference.Watermark != 2 || reference.CommittedSum != 33 || reference.TotalSum != 37 {
		t.Fatalf("unexpected reference: %+v", reference)
	}
}

func TestSequentialBatchesAdvanceWatermark(t *testing.T) {
	r := New(3)
	steps := []struct {
		events        []Event
		wantMark      int64
		wantCommitted int64
	}{
		{[]Event{{0, 0, 1}, {1, 0, 2}}, 0, 0},
		{[]Event{{2, 0, 4}, {0, 1, 1}}, 1, 7},
		{[]Event{{1, 1, 2}, {2, 1, 4}, {0, 2, 1}}, 2, 14},
	}
	for i, st := range steps {
		if err := r.Ingest(st.events); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
		s := r.Stats()
		logStats(t, fmt.Sprintf("step#%d", i), st.events, s,
			fmt.Sprintf("判定依据: 期望水位=%d（最小分区计数），期望已提交前缀和=%d",
				st.wantMark, st.wantCommitted))
		if s.Watermark != st.wantMark || s.CommittedSum != st.wantCommitted {
			t.Fatalf("step %d: got mark=%d committed=%d, want %d/%d",
				i, s.Watermark, s.CommittedSum, st.wantMark, st.wantCommitted)
		}
		if err := r.Verify(); err != nil {
			t.Fatalf("step %d verify: %v", i, err)
		}
	}
}

func TestRejectionsLeaveStateUntouched(t *testing.T) {
	cases := []struct {
		name   string
		events []Event
		cause  error
	}{
		{
			"partition negative",
			[]Event{{Partition: -1, Offset: 0, Value: 1}},
			ErrInvalidPartition,
		},
		{
			"partition too large",
			[]Event{{Partition: 2, Offset: 0, Value: 1}},
			ErrInvalidPartition,
		},
		{
			"first offset not zero",
			[]Event{{Partition: 1, Offset: 1, Value: 1}},
			ErrDiscontinuousOffset,
		},
		{
			"hole in offsets",
			[]Event{{Partition: 0, Offset: 0, Value: 1}, {Partition: 0, Offset: 2, Value: 1}},
			ErrDiscontinuousOffset,
		},
		{
			"duplicate offset",
			[]Event{{Partition: 0, Offset: 0, Value: 1}, {Partition: 0, Offset: 0, Value: 2}},
			ErrDiscontinuousOffset,
		},
		{
			"negative value",
			[]Event{{Partition: 0, Offset: 0, Value: -1}},
			ErrNegativeValue,
		},
		{
			"one bad event poisons batch",
			[]Event{{Partition: 1, Offset: 0, Value: 5}, {Partition: 0, Offset: 9, Value: 1}},
			ErrDiscontinuousOffset,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := New(2)
			seed := []Event{{Partition: 0, Offset: 0, Value: 7}}
			if err := r.Ingest(seed); err != nil {
				t.Fatalf("seed: %v", err)
			}
			before := r.Stats()

			err := r.Ingest(tc.events)
			if err == nil {
				t.Fatalf("expected rejection, got nil")
			}
			if !errors.Is(err, tc.cause) {
				t.Fatalf("cause = %v, want errors.Is %v", err, tc.cause)
			}
			after := r.Stats()
			logStats(t, "reject:"+tc.name, tc.events, after,
				fmt.Sprintf("判定依据: errors.Is(err,%v)=true；拒绝前后快照必须逐字段相同（before mark=%d）",
					tc.cause, before.Watermark))
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("state changed by failed ingest:\nbefore=%+v\nafter =%+v", before, after)
			}
			if err := r.Verify(); err != nil {
				t.Fatalf("verify after reject: %v", err)
			}

			// 拒绝之后，正确的位点仍然可以照常接受。
			if err := r.Ingest([]Event{{Partition: 0, Offset: 1, Value: 3}}); err != nil {
				t.Fatalf("ingest after rejection failed: %v", err)
			}
		})
	}
}

func TestMonotonicStats(t *testing.T) {
	r := New(2)
	batches := [][]Event{
		{{0, 0, 1}, {0, 1, 2}},
		{{1, 0, 3}},
		{{1, 1, 4}, {0, 2, 5}},
	}
	prev := r.Stats()
	for i, b := range batches {
		if err := r.Ingest(b); err != nil {
			t.Fatalf("batch %d: %v", i, err)
		}
		cur := r.Stats()
		logStats(t, fmt.Sprintf("mono#%d", i), b, cur,
			"判定依据: 各分区计数/和值、TotalSum、Watermark、CommittedSum 相对上一快照必须单调不减")
		if cur.Watermark < prev.Watermark || cur.CommittedSum < prev.CommittedSum || cur.TotalSum < prev.TotalSum {
			t.Fatalf("batch %d stats not monotonic: prev=%+v cur=%+v", i, prev, cur)
		}
		for p := range cur.Partitions {
			if cur.Partitions[p].Accepted < prev.Partitions[p].Accepted ||
				cur.Partitions[p].Sum < prev.Partitions[p].Sum {
				t.Fatalf("batch %d partition %d not monotonic", i, p)
			}
		}
		prev = cur
	}
}

func TestConcurrentReadsAndRecompute(t *testing.T) {
	const partitions = 3
	r := New(partitions)

	// 构造跨分区交错的输入：单写者顺序喂入批次（位点有前后依赖，
	// 写者互斥由路由器保证），读者与自检全程并发。
	var plan [][]Event
	for off := int64(0); off < 60; off++ {
		plan = append(plan, []Event{
			{Partition: int(off % partitions), Offset: off / partitions, Value: off + 1},
		})
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for _, b := range plan {
			if err := r.Ingest(b); err != nil {
				t.Errorf("ingest: %v", err)
				return
			}
		}
	}()

	// 并发读者：读到的每个快照自身必须内部一致（TotalSum、Watermark 可当场重算）。
	snapshots := make(chan Stats, 512)
	var readerWG sync.WaitGroup
	for i := 0; i < 4; i++ {
		readerWG.Add(1)
		go func() {
			defer readerWG.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				s := r.Stats()
				if err := checkSnapshotConsistency(s, partitions); err != nil {
					t.Errorf("inconsistent snapshot: %v", err)
					return
				}
				if err := r.Verify(); err != nil {
					t.Errorf("concurrent verify: %v", err)
					return
				}
				select {
				case snapshots <- s:
				default:
				}
			}
		}()
	}

	<-done
	readerWG.Wait()
	close(snapshots)

	final := r.Stats()
	logStats(t, "concurrent-final", nil, final,
		"判定依据: 终态必须等于全部事件按 (partition,offset) 分组后的批量重算结果")

	// 与按位点批量重算的参照实现逐字段对照。
	want := recompute(partitions, flatten(plan))
	if !reflect.DeepEqual(final, want) {
		t.Fatalf("final stats mismatch vs batch recompute:\n got=%+v\nwant=%+v", final, want)
	}
	// 并发读取的中途快照必须是终态的单调前缀。
	for s := range snapshots {
		if s.Watermark > final.Watermark || s.CommittedSum > final.CommittedSum || s.TotalSum > final.TotalSum {
			t.Fatalf("intermediate snapshot ahead of final: %+v", s)
		}
	}
	if err := r.Verify(); err != nil {
		t.Fatalf("verify: %v", err)
	}

	// 屏障：喂入完成后，并发读取同一实例必须逐字段相同。
	const readers = 8
	results := make([]Stats, readers)
	var wg sync.WaitGroup
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = r.Stats()
		}(i)
	}
	wg.Wait()
	for i := 1; i < readers; i++ {
		if !reflect.DeepEqual(results[0], results[i]) {
			t.Fatalf("concurrent reads differ:\n[0]=%+v\n[%d]=%+v", results[0], i, results[i])
		}
	}
}

func checkSnapshotConsistency(s Stats, n int) error {
	if len(s.Partitions) != n {
		return fmt.Errorf("partition slice len=%d", len(s.Partitions))
	}
	var total int64
	minCount := s.Partitions[0].Accepted
	for _, ps := range s.Partitions {
		total += ps.Sum
		if ps.Accepted < minCount {
			minCount = ps.Accepted
		}
	}
	if total != s.TotalSum {
		return fmt.Errorf("totalSum=%d but sum of partitions=%d", s.TotalSum, total)
	}
	if minCount != s.Watermark {
		return fmt.Errorf("watermark=%d but min accepted=%d", s.Watermark, minCount)
	}
	return nil
}

func flatten(batches [][]Event) []Event {
	var out []Event
	for _, b := range batches {
		out = append(out, b...)
	}
	return out
}

// recompute 是与路由器实现无关的参照算法：忽略喂入时序，
// 把全部事件按分区、位点重新组织后整体重算统计量。
func recompute(n int, events []Event) Stats {
	counts := make([]int64, n)
	sums := make([]int64, n)
	byP := make([][]int64, n)
	for _, ev := range events {
		counts[ev.Partition]++
		sums[ev.Partition] += ev.Value
		byP[ev.Partition] = append(byP[ev.Partition], ev.Value)
	}
	mark := counts[0]
	for _, c := range counts[1:] {
		if c < mark {
			mark = c
		}
	}
	var committed int64
	for p := 0; p < n; p++ {
		for off := int64(0); off < mark; off++ {
			committed += byP[p][off]
		}
	}
	ps := make([]PartitionStats, n)
	var total int64
	for p := 0; p < n; p++ {
		ps[p] = PartitionStats{Accepted: counts[p], Sum: sums[p]}
		total += sums[p]
	}
	return Stats{
		Partitions:   ps,
		TotalSum:     total,
		Watermark:    mark,
		CommittedSum: committed,
	}
}
