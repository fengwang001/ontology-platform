package idempotent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"

)

func testLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return logger, &buf
}

func newTestSink(t *testing.T, cfg Config) *Sink {
	t.Helper()
	if cfg.Logger == nil {
		logger, _ := testLogger()
		cfg.Logger = logger
	}
	sink, err := NewSink(newMemStore(), cfg)
	if err != nil {
		t.Fatalf("NewSink: %v", err)
	}
	return sink
}

func assertCode(t *testing.T, err error, want ErrorCode) {
	t.Helper()
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("want ValidationError(%s), got %v", want, err)
	}
	if ve.Code != want {
		t.Fatalf("want code %s, got %s (%s)", want, ve.Code, ve.Message)
	}
}

func TestDedupAcrossRedelivery(t *testing.T) {
	sink := newTestSink(t, Config{})
	batch := []Record{
		{Partition: 0, Offset: 0, Key: "a", Amount: 1},
		{Partition: 0, Offset: 1, Key: "b", Amount: 2},
		{Partition: 1, Offset: 0, Key: "a", Amount: 10},
	}

	first, err := sink.Write(context.Background(), batch)
	if err != nil {
		t.Fatalf("first write: %v", err)
	}
	if first.Applied != 3 || first.Duplicate != 0 || first.AdvancedWater != 3 {
		t.Fatalf("unexpected first result: %+v", first)
	}

	for attempt := 0; attempt < 3; attempt++ {
		res, err := sink.Write(context.Background(), batch)
		if err != nil {
			t.Fatalf("redelivery %d: %v", attempt, err)
		}
		if res.Applied != 0 || res.Duplicate != 3 {
			t.Fatalf("attempt %d: expected 3 duplicates, got %+v", attempt, res)
		}
	}

	w, d, r := sink.Snapshot()
	if w[0] != 1 || w[1] != 0 {
		t.Fatalf("watermarks wrong: %v", w)
	}
	if d[0] != 6 || d[1] != 3 {
		t.Fatalf("per-partition duplicate counts wrong: %v", d)
	}
	if r["a"] != 11 || r["b"] != 2 {
		t.Fatalf("results must equal single delivery, got %v", r)
	}
}

func TestIndependentPartitionWatermarks(t *testing.T) {
	sink := newTestSink(t, Config{})
	ctx := context.Background()
	if _, err := sink.Write(ctx, []Record{
		{Partition: 5, Offset: 100, Key: "k", Amount: 1},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := sink.Write(ctx, []Record{
		{Partition: 6, Offset: 3, Key: "k", Amount: 1},
		{Partition: 6, Offset: 4, Key: "k", Amount: 1},
	}); err != nil {
		t.Fatal(err)
	}
	res, err := sink.Write(ctx, []Record{
		{Partition: 5, Offset: 50, Key: "late", Amount: 7},
	})
	if err != nil {
		t.Fatalf("stale offset: %v", err)
	}
	if res.Applied != 0 || res.Duplicate != 1 {
		t.Fatalf("stale offset behind watermark must be duplicate, got %+v", res)
	}
	_, _, r := sink.Snapshot()
	if r["k"] != 3 {
		t.Fatalf("late record must not accumulate, got %v", r)
	}
	if _, ok := r["late"]; ok {
		t.Fatalf("duplicate key must not appear in results: %v", r)
	}
}

func TestWithinBatchOutOfOrderOffsets(t *testing.T) {
	cases := []struct {
		name  string
		batch []Record
	}{
		{
			"equal offsets",
			[]Record{
				{Partition: 0, Offset: 1, Key: "a", Amount: 1},
				{Partition: 0, Offset: 1, Key: "b", Amount: 1},
			},
		},
		{
			"decreasing offsets",
			[]Record{
				{Partition: 2, Offset: 9, Key: "a", Amount: 1},
				{Partition: 2, Offset: 4, Key: "b", Amount: 1},
			},
		},
		{
			"out of order across other partitions",
			[]Record{
				{Partition: 0, Offset: 2, Key: "a", Amount: 1},
				{Partition: 1, Offset: 0, Key: "a", Amount: 1},
				{Partition: 0, Offset: 1, Key: "b", Amount: 1},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sink := newTestSink(t, Config{})
			_, err := sink.Write(context.Background(), tc.batch)
			assertCode(t, err, ErrOutOfOrderOffset)
			w, d, r := sink.Snapshot()
			if len(w) != 0 || len(d) != 0 || len(r) != 0 {
				t.Fatalf("rejected batch must change nothing: w=%v d=%v r=%v", w, d, r)
			}
		})
	}
}

func TestInvalidInputs(t *testing.T) {
	cases := []struct {
		name    string
		batch   []Record
		wantErr ErrorCode
	}{
		{"negative partition",
			[]Record{{Partition: -1, Offset: 0, Key: "a"}},
			ErrNegativePartition},
		{"negative offset",
			[]Record{{Partition: 0, Offset: -1, Key: "a"}},
			ErrNegativeOffset},
		{"empty key",
			[]Record{{Partition: 0, Offset: 0, Key: ""}},
			ErrEmptyKey},
		{"whitespace key",
			[]Record{{Partition: 0, Offset: 0, Key: "   "}},
			ErrEmptyKey},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sink := newTestSink(t, Config{})
			_, err := sink.Write(context.Background(), tc.batch)
			assertCode(t, err, tc.wantErr)
			w, d, r := sink.Snapshot()
			if len(w) != 0 || len(d) != 0 || len(r) != 0 {
				t.Fatalf("rejected batch must change nothing: w=%v d=%v r=%v", w, d, r)
			}
		})
	}
}

func TestTooManyPartitions(t *testing.T) {
	sink := newTestSink(t, Config{MaxPartitions: 2})
	batch := []Record{
		{Partition: 0, Offset: 0, Key: "a", Amount: 1},
		{Partition: 1, Offset: 0, Key: "a", Amount: 1},
		{Partition: 2, Offset: 0, Key: "a", Amount: 1},
	}
	_, err := sink.Write(context.Background(), batch)
	assertCode(t, err, ErrTooManyPartitions)

	w, _, r := sink.Snapshot()
	if len(w) != 0 || len(r) != 0 {
		t.Fatalf("oversize batch must change nothing: w=%v r=%v", w, r)
	}

	if _, err := sink.Write(context.Background(), []Record{
		{Partition: 0, Offset: 0, Key: "a", Amount: 1},
	}); err != nil {
		t.Fatalf("valid batch after rejection: %v", err)
	}
	if _, err := sink.Write(context.Background(), []Record{
		{Partition: 0, Offset: 1, Key: "a", Amount: 1},
		{Partition: 1, Offset: 0, Key: "a", Amount: 1},
	}); err != nil {
		t.Fatalf("known partitions should not count as new: %v", err)
	}
}

func TestMixedBatchPrefixDuplicatesThenProgress(t *testing.T) {
	sink := newTestSink(t, Config{})
	ctx := context.Background()
	if _, err := sink.Write(ctx, []Record{
		{Partition: 0, Offset: 5, Key: "k", Amount: 100},
	}); err != nil {
		t.Fatal(err)
	}
	res, err := sink.Write(ctx, []Record{
		{Partition: 0, Offset: 0, Key: "old1", Amount: 1},
		{Partition: 0, Offset: 5, Key: "old2", Amount: 1},
		{Partition: 0, Offset: 6, Key: "k", Amount: 7},
		{Partition: 0, Offset: 7, Key: "new", Amount: 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Applied != 2 || res.Duplicate != 2 || res.AdvancedWater != 2 {
		t.Fatalf("bad mixed result: %+v", res)
	}
	if res.DuplicateTotal != 2 {
		t.Fatalf("duplicate total: %d", res.DuplicateTotal)
	}
	_, d, r := sink.Snapshot()
	if d[0] != 2 || r["k"] != 107 || r["new"] != 3 {
		t.Fatalf("mixed batch state wrong: d=%v r=%v", d, r)
	}
	if _, ok := r["old1"]; ok {
		t.Fatalf("duplicate records must not touch results: %v", r)
	}
}

func TestEmptyBatchIsNoop(t *testing.T) {
	sink := newTestSink(t, Config{})
	res, err := sink.Write(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Applied != 0 || res.Duplicate != 0 || len(res.Results) != 0 {
		t.Fatalf("empty batch: %+v", res)
	}
}

func TestCommitFailureLeavesStateUntouched(t *testing.T) {
	store := &flakyStore{inner: newMemStore(), failNext: 2}
	sink := newTestSink(t, Config{})
	sink = rebindStore(t, sink, store)

	batch := []Record{{Partition: 0, Offset: 0, Key: "a", Amount: 5}}
	for i := 0; i < 2; i++ {
		if _, err := sink.Write(context.Background(), batch); err == nil {
			t.Fatalf("attempt %d should fail", i)
		}
	}
	w, d, r := sink.Snapshot()
	if len(w) != 0 || len(d) != 0 || len(r) != 0 {
		t.Fatalf("failed commits must not be visible: w=%v d=%v r=%v", w, d, r)
	}
	res, err := sink.Write(context.Background(), batch)
	if err != nil {
		t.Fatalf("eventual commit: %v", err)
	}
	if res.Applied != 1 || res.Results["a"] != 5 {
		t.Fatalf("applied result wrong: %+v", res)
	}
}

func rebindStore(t *testing.T, _ *Sink, store StateStore) *Sink {
	t.Helper()
	logger, _ := testLogger()
	sink, err := NewSink(store, Config{Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	return sink
}

func TestRestartRebuildsFromPersistedState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	logger, _ := testLogger()

	batch := []Record{
		{Partition: 0, Offset: 0, Key: "a", Amount: 1},
		{Partition: 0, Offset: 1, Key: "a", Amount: 2},
		{Partition: 1, Offset: 7, Key: "b", Amount: 4},
	}
	sink1, err := NewSink(NewFileStore(path), Config{Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sink1.Write(context.Background(), batch); err != nil {
		t.Fatal(err)
	}
	if _, err := sink1.Write(context.Background(), batch); err != nil {
		t.Fatal(err)
	}

	sink2, err := NewSink(NewFileStore(path), Config{Logger: logger})
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	res, err := sink2.Write(context.Background(), batch)
	if err != nil {
		t.Fatalf("redelivery after restart: %v", err)
	}
	if res.Applied != 0 || res.Duplicate != 3 {
		t.Fatalf("after restart all records are duplicates: %+v", res)
	}
	w, d, r := sink2.Snapshot()
	if w[0] != 1 || w[1] != 7 {
		t.Fatalf("watermarks restored wrong: %v", w)
	}
	if d[0] != 4 || d[1] != 2 {
		t.Fatalf("duplicate counters restored wrong: %v", d)
	}
	if r["a"] != 3 || r["b"] != 4 {
		t.Fatalf("results restored wrong: %v", r)
	}

	res, err = sink2.Write(context.Background(), []Record{
		{Partition: 1, Offset: 8, Key: "b", Amount: 10},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Applied != 1 || res.Results["b"] != 14 {
		t.Fatalf("progress after restart wrong: %+v", res)
	}
}

func TestConcurrentWritesAreSerializable(t *testing.T) {
	const partitions = 4
	const rounds = 20
	const workers = 8

	batches := make([][]Record, rounds)
	for round := range batches {
		batch := make([]Record, partitions)
		for p := range batch {
			batch[p] = Record{
				Partition: p,
				Offset:    int64(round),
				Key:       fmt.Sprintf("k%d", p),
				Amount:    1,
			}
		}
		batches[round] = batch
	}

	sink := newTestSink(t, Config{})
	var wg sync.WaitGroup
	var totalDuplicates int64
	var dupMu sync.Mutex
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for round := 0; round < rounds; round++ {
				res, err := sink.Write(context.Background(), batches[round])
				if err != nil {
					t.Errorf("concurrent write: %v", err)
					return
				}
				dupMu.Lock()
				totalDuplicates += int64(res.Duplicate)
				dupMu.Unlock()
			}
		}()
	}
	wg.Wait()

	// 只有每个 (分区, 位点) 第一次生效：共 rounds*partitions 条生效，
	// 其余 (workers-1)*rounds*partitions 全部计为重复。
	wantDuplicates := int64((workers - 1) * rounds * partitions)
	if totalDuplicates != wantDuplicates {
		t.Fatalf("duplicate records = %d, want %d", totalDuplicates, wantDuplicates)
	}
	w, d, r := sink.Snapshot()
	if len(w) != partitions {
		t.Fatalf("watermarks: %v", w)
	}
	for p := 0; p < partitions; p++ {
		key := fmt.Sprintf("k%d", p)
		if w[p] != int64(rounds-1) {
			t.Fatalf("watermark p%d = %d, want %d", p, w[p], rounds-1)
		}
		if r[key] != int64(rounds) {
			t.Fatalf("result %s = %d, want %d", key, r[key], rounds)
		}
		if d[p] != wantDuplicates/partitions {
			t.Fatalf("duplicate count p%d = %d, want %d", p, d[p], wantDuplicates/partitions)
		}
	}
}

func TestDeterministicReplay(t *testing.T) {
	sequence := [][]Record{
		{{Partition: 0, Offset: 0, Key: "x", Amount: 3}},
		{{Partition: 1, Offset: 0, Key: "y", Amount: 5}},
		{{Partition: 0, Offset: 0, Key: "x", Amount: 3}},
		{{Partition: 0, Offset: 1, Key: "y", Amount: -1}},
		{{Partition: 1, Offset: 0, Key: "y", Amount: 5}},
		{{Partition: 0, Offset: 2, Key: "x", Amount: 4}},
	}

	run := func() (map[int]int64, map[int]int64, map[string]int64) {
		sink := newTestSink(t, Config{})
		for _, batch := range sequence {
			if _, err := sink.Write(context.Background(), batch); err != nil {
				t.Fatal(err)
			}
		}
		w, d, r := sink.Snapshot()
		return w, d, r
	}

	w1, d1, r1 := run()
	w2, d2, r2 := run()
	if !mapsEqualInt(w1, w2) || !mapsEqualInt(d1, d2) || !mapsEqualString(r1, r2) {
		t.Fatalf("replay not deterministic:\nrun1 w=%v d=%v r=%v\nrun2 w=%v d=%v r=%v",
			w1, d1, r1, w2, d2, r2)
	}
	if r1["x"] != 7 || r1["y"] != 4 {
		t.Fatalf("unexpected deterministic results: %v", r1)
	}
	if d1[0] != 1 || d1[1] != 1 {
		t.Fatalf("unexpected duplicate counts: %v", d1)
	}
}

func TestLoggingShowsInputDecisionsAndBasis(t *testing.T) {
	logger, buf := testLogger()
	sink, err := NewSink(newMemStore(), Config{Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	batch := []Record{
		{Partition: 0, Offset: 0, Key: "a", Amount: 1},
		{Partition: 0, Offset: 1, Key: "a", Amount: 2},
	}
	if _, err := sink.Write(context.Background(), batch); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	if _, err := sink.Write(context.Background(), batch); err != nil {
		t.Fatal(err)
	}
	logs := buf.String()
	for _, want := range []string{
		"write batch input",
		"record judged duplicate",
		"offset<=watermark",
		"batch committed atomically",
		"applied=0",
		"duplicate=2",
	} {
		if !strings.Contains(logs, want) {
			t.Fatalf("logs missing %q:\n%s", want, logs)
		}
	}

	buf.Reset()
	_, err = sink.Write(context.Background(), []Record{
		{Partition: 0, Offset: 9, Key: "a", Amount: 1},
		{Partition: 0, Offset: 2, Key: "a", Amount: 1},
	})
	if err == nil {
		t.Fatal("expected rejection")
	}
	rejectLogs := buf.String()
	if !strings.Contains(rejectLogs, "batch rejected before any effect") ||
		!strings.Contains(rejectLogs, "out_of_order_offset") {
		t.Fatalf("rejection logs wrong:\n%s", rejectLogs)
	}
}

func mapsEqualInt(a, b map[int]int64) bool {
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

func mapsEqualString(a, b map[string]int64) bool {
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
