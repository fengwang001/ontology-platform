package idempotent

import (
	"bytes"
	"errors"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
)

func testLogger(t *testing.T) *slog.Logger {
	t.Helper()
	var buf bytes.Buffer
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("sink logs:\n%s", buf.String())
		}
	})
	return slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func openTestSink(t *testing.T, maxPartitions int) (*Sink, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.json")
	sink, err := Open(Config{Path: path, MaxPartitions: maxPartitions, Logger: testLogger(t)})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return sink, path
}

func TestApplyBasicAndWatermark(t *testing.T) {
	sink, _ := openTestSink(t, 4)

	out := sink.Apply(Batch{ID: "b1", Records: []Record{
		{Partition: 0, Offset: 0, Key: "a", Value: 1},
		{Partition: 0, Offset: 2, Key: "b", Value: 5},
		{Partition: 1, Offset: 3, Key: "a", Value: 10},
	}})
	if out.Rejected || out.Err != nil || out.Applied != 3 || out.Duplicate != 0 {
		t.Fatalf("unexpected outcome: %+v", out)
	}
	snap := sink.Snapshot()
	if snap.Results["a"] != 11 || snap.Results["b"] != 5 {
		t.Fatalf("unexpected results: %v", snap.Results)
	}
	if snap.Watermarks[0] != 2 || snap.Watermarks[1] != 3 {
		t.Fatalf("unexpected watermarks: %v", snap.Watermarks)
	}

	// 位点不超过水位即重复（不同批次之间位点可以回跳投递）。
	out = sink.Apply(Batch{ID: "b1-redelivered", Records: []Record{
		{Partition: 0, Offset: 0, Key: "a", Value: 1},
		{Partition: 0, Offset: 1, Key: "x", Value: 100},
		{Partition: 0, Offset: 3, Key: "c", Value: 7},
	}})
	if out.Rejected || out.Applied != 1 || out.Duplicate != 2 {
		t.Fatalf("unexpected outcome: %+v", out)
	}
	snap = sink.Snapshot()
	if snap.Results["a"] != 11 || snap.Results["x"] != 0 || snap.Results["c"] != 7 {
		t.Fatalf("redelivery changed results incorrectly: %v", snap.Results)
	}
	if snap.Watermarks[0] != 3 || snap.Duplicates != 2 {
		t.Fatalf("watermark/duplicates wrong: %v dup=%d", snap.Watermarks, snap.Duplicates)
	}
}

func TestRestartRebuildsFromPersistedState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	batches := []Batch{
		{ID: "b1", Records: []Record{{Partition: 0, Offset: 0, Key: "k", Value: 3}}},
		{ID: "b2", Records: []Record{
			{Partition: 0, Offset: 1, Key: "k", Value: 4},
			{Partition: 2, Offset: 0, Key: "m", Value: 9},
		}},
	}

	sink, err := Open(Config{Path: path, Logger: testLogger(t)})
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range batches {
		if out := sink.Apply(b); out.Rejected || out.Err != nil {
			t.Fatalf("apply %s: %+v", b.ID, out)
		}
	}
	want := sink.Snapshot()

	// 模拟崩溃：丢弃全部内存状态，重新 Open 只能从磁盘重建。
	sink2, err := Open(Config{Path: path, Logger: testLogger(t)})
	if err != nil {
		t.Fatal(err)
	}
	if got := sink2.Snapshot(); !snapshotsEqual(got, want) {
		t.Fatalf("rebuilt state mismatch:\n got=%+v\nwant=%+v", got, want)
	}

	// 重启后重投历史批次，全部判重，结果与重复总数不变。
	out := sink2.Apply(batches[1])
	if out.Applied != 0 || out.Duplicate != 2 {
		t.Fatalf("unexpected outcome after restart: %+v", out)
	}
	after := sink2.Snapshot()
	// 结果表与水位必须完全不变；重复计数等于实际多写的次数（2 条）。
	if !mapsEqual(after.Results, want.Results) || !mapsEqualInt64(after.Watermarks, want.Watermarks) {
		t.Fatalf("results/watermarks changed after redelivery:\n got=%+v\nwant=%+v", after, want)
	}
	if after.Duplicates != want.Duplicates+2 {
		t.Fatalf("duplicates = %d, want %d", after.Duplicates, want.Duplicates+2)
	}
}

func TestDuplicateOffsetsWithinBatchRejected(t *testing.T) {
	sink, _ := openTestSink(t, 4)

	out := sink.Apply(Batch{ID: "dup", Records: []Record{
		{Partition: 0, Offset: 1, Key: "a", Value: 1},
		{Partition: 0, Offset: 1, Key: "b", Value: 2},
	}})
	if !out.Rejected || !errors.Is(out.Err, ErrOutOfOrder) {
		t.Fatalf("want ErrOutOfOrder, got %+v", out)
	}

	out = sink.Apply(Batch{ID: "unordered", Records: []Record{
		{Partition: 1, Offset: 5, Key: "a", Value: 1},
		{Partition: 1, Offset: 4, Key: "b", Value: 2},
	}})
	if !out.Rejected || !errors.Is(out.Err, ErrOutOfOrder) {
		t.Fatalf("want ErrOutOfOrder, got %+v", out)
	}

	// 不同分区的位点独立，不构成乱序。
	out = sink.Apply(Batch{ID: "multi", Records: []Record{
		{Partition: 1, Offset: 5, Key: "a", Value: 1},
		{Partition: 0, Offset: 0, Key: "b", Value: 2},
		{Partition: 1, Offset: 6, Key: "c", Value: 3},
	}})
	if out.Rejected || out.Applied != 3 {
		t.Fatalf("valid multi-partition batch rejected: %+v", out)
	}

	snap := sink.Snapshot()
	if len(snap.Results) != 3 || snap.Results["a"] != 1 || snap.Duplicates != 0 {
		t.Fatalf("rejected batches must leave no trace: %+v", snap)
	}
}

func TestInvalidInputs(t *testing.T) {
	sink, _ := openTestSink(t, 2)

	cases := []struct {
		name    string
		batch   Batch
		wantErr error
	}{
		{
			name:    "negative partition",
			batch:   Batch{ID: "np", Records: []Record{{Partition: -1, Offset: 0, Key: "a", Value: 1}}},
			wantErr: ErrNegativePartition,
		},
		{
			name:    "negative offset",
			batch:   Batch{ID: "no", Records: []Record{{Partition: 0, Offset: -1, Key: "a", Value: 1}}},
			wantErr: ErrNegativeOffset,
		},
		{
			name:    "empty key",
			batch:   Batch{ID: "ek", Records: []Record{{Partition: 0, Offset: 0, Key: "", Value: 1}}},
			wantErr: ErrEmptyKey,
		},
		{
			name: "out of order across interleaved records",
			batch: Batch{ID: "oo", Records: []Record{
				{Partition: 0, Offset: 2, Key: "a", Value: 1},
				{Partition: 1, Offset: 0, Key: "b", Value: 1},
				{Partition: 0, Offset: 1, Key: "c", Value: 1},
			}},
			wantErr: ErrOutOfOrder,
		},
		{
			name: "too many partitions",
			batch: Batch{ID: "tm", Records: []Record{
				{Partition: 0, Offset: 0, Key: "a", Value: 1},
				{Partition: 1, Offset: 0, Key: "b", Value: 1},
				{Partition: 2, Offset: 0, Key: "c", Value: 1},
			}},
			wantErr: ErrTooManyPartitions,
		},
	}

	base := sink.Apply(Batch{ID: "base", Records: []Record{
		{Partition: 0, Offset: 0, Key: "base", Value: 42},
	}})
	if base.Rejected {
		t.Fatalf("base batch rejected: %+v", base)
	}
	before := sink.Snapshot()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := sink.Apply(tc.batch)
			if !out.Rejected || !errors.Is(out.Err, tc.wantErr) {
				t.Fatalf("want %v, got %+v", tc.wantErr, out)
			}
			if after := sink.Snapshot(); !snapshotsEqual(after, before) {
				t.Fatalf("rejected batch changed state:\nbefore=%+v\nafter =%+v", before, after)
			}
		})
	}

	// 已存在的分区再次出现不增加分区数，不应触发超限。
	out := sink.Apply(Batch{ID: "existing-partitions", Records: []Record{
		{Partition: 0, Offset: 1, Key: "base", Value: 1},
	}})
	if out.Rejected {
		t.Fatalf("batch using existing partition rejected: %+v", out)
	}
}

func TestConcurrentAppliesAreSerializable(t *testing.T) {
	sink, _ := openTestSink(t, 8)

	// 水位去重协议要求每个分区的首次投递按位点递增；至少一次语义下这些
	// 已生效批次会被并发重投。最终结果必须与每批只写一次一致，重复数等于
	// 实际多写的次数。
	const partitions = 4
	const lastOffset = int64(20)
	const redeliveryWorkers = 5

	var uniqueBatches []Batch
	for p := 0; p < partitions; p++ {
		for off := int64(0); off <= lastOffset; off++ {
			uniqueBatches = append(uniqueBatches, Batch{
				ID:      "uniq",
				Records: []Record{{Partition: p, Offset: off, Key: "sum", Value: 1}},
			})
		}
	}

	// 阶段一：首次投递，每个分区严格按位点升序（跨分区交错）。
	for off := int64(0); off <= lastOffset; off++ {
		for p := 0; p < partitions; p++ {
			if out := sink.Apply(uniqueBatches[p*int(lastOffset+1)+int(off)]); out.Applied != 1 {
				t.Fatalf("initial delivery must apply: %+v", out)
			}
		}
	}

	// 阶段二：多个 worker 用各自不同的顺序并发重投全部批次，全部判重。
	var wg sync.WaitGroup
	for w := 0; w < redeliveryWorkers; w++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for _, idx := range permutation(len(uniqueBatches), seed) {
				out := sink.Apply(uniqueBatches[idx])
				if out.Applied != 0 || out.Duplicate != 1 {
					t.Errorf("redelivery must be duplicate only: %+v", out)
				}
			}
		}(w + 1)
	}
	wg.Wait()

	snap := sink.Snapshot()
	wantApplied := int64(partitions) * (lastOffset + 1)
	if snap.Results["sum"] != wantApplied {
		t.Fatalf("result = %d, want %d (no missing, no double count)",
			snap.Results["sum"], wantApplied)
	}
	for p := 0; p < partitions; p++ {
		if snap.Watermarks[p] != lastOffset {
			t.Fatalf("partition %d watermark = %d, want %d", p, snap.Watermarks[p], lastOffset)
		}
	}
	wantDuplicates := int64(redeliveryWorkers) * wantApplied
	if snap.Duplicates != wantDuplicates {
		t.Fatalf("duplicates = %d, want %d", snap.Duplicates, wantDuplicates)
	}
}

func TestDeterminismRepeatedSequence(t *testing.T) {
	sequence := []Batch{
		{ID: "s1", Records: []Record{{Partition: 0, Offset: 0, Key: "a", Value: 2}}},
		{ID: "s2", Records: []Record{
			{Partition: 0, Offset: 1, Key: "a", Value: 3},
			{Partition: 1, Offset: 0, Key: "b", Value: 5},
		}},
		{ID: "s1-redeliver", Records: []Record{{Partition: 0, Offset: 0, Key: "a", Value: 2}}},
		{ID: "bad", Records: []Record{
			{Partition: 1, Offset: 2, Key: "b", Value: 1},
			{Partition: 1, Offset: 1, Key: "a", Value: 1},
		}},
		{ID: "s3", Records: []Record{{Partition: 1, Offset: 7, Key: "a", Value: -4}}},
	}

	run := func() Snapshot {
		sink, _ := openTestSink(t, 8)
		for _, b := range sequence {
			sink.Apply(b)
		}
		return sink.Snapshot()
	}

	first := run()
	for i := 0; i < 3; i++ {
		if got := run(); !snapshotsEqual(got, first) {
			t.Fatalf("run %d differs:\n got=%+v\nwant=%+v", i, got, first)
		}
	}

	if first.Results["a"] != 1 || first.Results["b"] != 5 {
		t.Fatalf("unexpected results: %v", first.Results)
	}
	if first.Watermarks[0] != 1 || first.Watermarks[1] != 7 {
		t.Fatalf("unexpected watermarks: %v", first.Watermarks)
	}
	if first.Duplicates != 1 {
		t.Fatalf("unexpected duplicates: %d", first.Duplicates)
	}
}

func permutation(n, seed int) []int {
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	// 确定性的线性同余洗牌，每个 seed 产生不同但可复现的顺序。
	state := uint64(seed)*2862933555777941757 + 3037000493
	for i := n - 1; i > 0; i-- {
		state = state*6364136223846793005 + 1442695040888963407
		j := int(state>>33) % (i + 1)
		idx[i], idx[j] = idx[j], idx[i]
	}
	return idx
}

func snapshotsEqual(a, b Snapshot) bool {
	if a.Duplicates != b.Duplicates ||
		len(a.Results) != len(b.Results) ||
		len(a.Watermarks) != len(b.Watermarks) {
		return false
	}
	return mapsEqual(a.Results, b.Results) && mapsEqualInt64(a.Watermarks, b.Watermarks)
}

func mapsEqual(a, b map[string]int64) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func mapsEqualInt64[K comparable](a, b map[K]int64) bool {
	if len(a) != len(b) {
		return false
	}
	for p, v := range a {
		if b[p] != v {
			return false
		}
	}
	return true
}
