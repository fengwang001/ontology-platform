package pkchange

import (
	"bytes"
	"io"
	"log/slog"
	"reflect"
	"sync"
	"testing"
)

func newTestStore(t *testing.T, partitionCount, batchLimit int) (*Store, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(io.MultiWriter(&buf), &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	return NewStore(partitionCount, batchLimit, logger), &buf
}

func TestApplyKeepsViewEqualSource(t *testing.T) {
	store, _ := newTestStore(t, 4, 100)

	batches := [][]Change{
		{
			{Op: OpInsert, Key: "a", Data: r("v", "1")},
			{Op: OpInsert, Key: "b", Data: r("v", "2")},
		},
		{
			{Op: OpUpdate, Key: "a", NewKey: "a1", Data: r("v", "1m")},
			{Op: OpDelete, Key: "b"},
			{Op: OpInsert, Key: "c", Data: r("v", "3")},
		},
		{
			// 主键不变的更新，只有一次写入。
			{Op: OpUpdate, Key: "c", Data: r("v", "3m")},
		},
	}

	for i, batch := range batches {
		parts, err := store.Apply(batch)
		if err != nil {
			t.Fatalf("batch %d: %v", i, err)
		}
		if !reflect.DeepEqual(store.ViewSnapshot(), store.SourceSnapshot()) {
			t.Fatalf("batch %d: view != source\nsource=%v\nview=%v",
				i, store.SourceSnapshot(), store.ViewSnapshot())
		}
		count := 0
		for _, p := range parts {
			for _, ev := range p.Events {
				if KeyPartition(ev.Key, store.PartitionCount()) != p.Index {
					t.Fatalf("event %s in wrong partition", ev.Key)
				}
				count++
			}
		}
		if count == 0 {
			t.Fatalf("batch %d produced no partitioned events", i)
		}
	}

	source := store.SourceSnapshot()
	want := map[string]Row{"a1": r("v", "1m"), "c": r("v", "3m")}
	if !reflect.DeepEqual(source, want) {
		t.Fatalf("source = %v, want %v", source, want)
	}

	// 快照深拷贝隔离：改返回值不影响内部状态。
	source["a1"]["v"] = "tampered"
	if got := store.SourceSnapshot()["a1"]["v"]; got != "1m" {
		t.Fatalf("internal state leaked through snapshot: %q", got)
	}
}

func TestApplyRejectionChangesNothing(t *testing.T) {
	store, _ := newTestStore(t, 4, 2)

	if _, err := store.Apply([]Change{
		{Op: OpInsert, Key: "a", Data: r("v", "1")},
		{Op: OpInsert, Key: "b", Data: r("v", "2")},
	}); err != nil {
		t.Fatal(err)
	}

	beforeSource := store.SourceSnapshot()
	beforeView := store.ViewSnapshot()
	beforeEmitted := store.EmittedEvents()

	// 批内先有合法变更、后有非法变更，整批必须回滚。
	_, err := store.Apply([]Change{
		{Op: OpUpdate, Key: "a", NewKey: "a1", Data: r("v", "9")},
		{Op: OpDelete, Key: "missing"},
	})
	if rj, ok := AsReject(err); !ok || rj.Reason != ReasonKeyNotFound {
		t.Fatalf("want key_not_found rejection, got %v", err)
	}

	_, err = store.Apply([]Change{
		{Op: OpInsert, Key: "a"}, // 键已存在
	})
	if rj, ok := AsReject(err); !ok || rj.Reason != ReasonKeyExists {
		t.Fatalf("want key_exists rejection, got %v", err)
	}

	_, err = store.Apply([]Change{
		{Op: OpInsert, Key: "x"},
		{Op: OpInsert, Key: "y"},
		{Op: OpInsert, Key: "z"},
	})
	if rj, ok := AsReject(err); !ok || rj.Reason != ReasonBatchTooLarge {
		t.Fatalf("want batch_too_large rejection, got %v", err)
	}

	if !reflect.DeepEqual(store.SourceSnapshot(), beforeSource) {
		t.Fatal("source changed after rejected batches")
	}
	if !reflect.DeepEqual(store.ViewSnapshot(), beforeView) {
		t.Fatal("view changed after rejected batches")
	}
	if !reflect.DeepEqual(store.EmittedEvents(), beforeEmitted) {
		t.Fatal("emitted events changed after rejected batches")
	}

	// 被拒绝后仍可继续提交合法批。
	if _, err := store.Apply([]Change{{Op: OpInsert, Key: "z", Data: r("v", "26")}}); err != nil {
		t.Fatalf("apply after rejection failed: %v", err)
	}
}

func TestChainedKeyChangesThroughStore(t *testing.T) {
	store, _ := newTestStore(t, 8, 100)
	_, err := store.Apply([]Change{
		{Op: OpInsert, Key: "k1", Data: r("v", "1")},
		{Op: OpInsert, Key: "k2", Data: r("v", "2")},
	})
	if err != nil {
		t.Fatal(err)
	}

	// k1 -> k2 不允许（k2 存在），先删 k2 再链式改名 k1 -> k1m -> k1n。
	parts, err := store.Apply([]Change{
		{Op: OpDelete, Key: "k2"},
		{Op: OpUpdate, Key: "k1", NewKey: "k1m", Data: r("v", "1")},
		{Op: OpUpdate, Key: "k1m", NewKey: "k1n", Data: r("v", "1f")},
		{Op: OpInsert, Key: "k1", Data: r("v", "reborn")},
	})
	if err != nil {
		t.Fatalf("chained batch rejected: %v", err)
	}

	all := flatten(parts)
	// 合并后：k2 delete、k1m delete、k1n write、k1 write。
	kindByKey := map[string]EventKind{}
	for _, ev := range all {
		kindByKey[ev.Key] = ev.Kind
	}
	if kindByKey["k2"] != EventDelete || kindByKey["k1m"] != EventDelete {
		t.Fatalf("missing deletes: %v", kindByKey)
	}
	if kindByKey["k1n"] != EventWrite || kindByKey["k1"] != EventWrite {
		t.Fatalf("missing writes: %v", kindByKey)
	}

	want := map[string]Row{"k1n": r("v", "1f"), "k1": r("v", "reborn")}
	if !reflect.DeepEqual(store.SourceSnapshot(), want) {
		t.Fatalf("source = %v, want %v", store.SourceSnapshot(), want)
	}
	if !reflect.DeepEqual(store.ViewSnapshot(), want) {
		t.Fatalf("view = %v, want %v", store.ViewSnapshot(), want)
	}
}

func TestDeterministicReplay(t *testing.T) {
	batches := [][]Change{
		{{Op: OpInsert, Key: "a", Data: r("v", "1")}, {Op: OpInsert, Key: "b", Data: r("v", "2")}},
		{{Op: OpUpdate, Key: "a", NewKey: "aa", Data: r("v", "11")}, {Op: OpDelete, Key: "b"}},
		{{Op: OpInsert, Key: "c", Data: r("v", "3")}},
	}

	run := func() ([]Event, map[string]Row) {
		store, _ := newTestStore(t, 7, 100)
		var emitted []Event
		for _, b := range batches {
			parts, err := store.Apply(b)
			if err != nil {
				t.Fatal(err)
			}
			emitted = append(emitted, flatten(parts)...)
		}
		return emitted, store.SourceSnapshot()
	}

	ev1, snap1 := run()
	for i := 0; i < 5; i++ {
		ev2, snap2 := run()
		if !reflect.DeepEqual(ev1, ev2) {
			t.Fatal("emitted event sequence differs across replays")
		}
		if !reflect.DeepEqual(snap1, snap2) {
			t.Fatal("snapshot differs across replays")
		}
	}
}

func TestConcurrentReadsNeverSeeInconsistentState(t *testing.T) {
	store, _ := newTestStore(t, 8, 100000)

	var writers sync.WaitGroup
	var readers sync.WaitGroup
	stop := make(chan struct{})

	for w := 0; w < 4; w++ {
		writers.Add(1)
		go func(id int) {
			defer writers.Done()
			for i := 0; i < 50; i++ {
				key := "w" + string(rune('a'+id)) + itoa(i)
				_, _ = store.Apply([]Change{{Op: OpInsert, Key: key, Data: r("v", itoa(i))}})
				_, _ = store.Apply([]Change{{Op: OpDelete, Key: key}})
			}
		}(w)
	}

	for r := 0; r < 8; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				src, view := store.Snapshots()
				if !reflect.DeepEqual(src, view) {
					t.Errorf("concurrent read saw view != source: %d vs %d", len(src), len(view))
					return
				}
			}
		}()
	}

	writers.Wait()
	close(stop)
	readers.Wait()

	// 所有插入最终都被删除，收敛为空。
	if len(store.SourceSnapshot()) != 0 || len(store.ViewSnapshot()) != 0 {
		t.Fatalf("expected empty final state, got source=%v view=%v",
			store.SourceSnapshot(), store.ViewSnapshot())
	}
}

func TestLogsContainInputSplitAndPartitions(t *testing.T) {
	store, buf := newTestStore(t, 4, 100)
	_, err := store.Apply([]Change{
		{Op: OpInsert, Key: "a", Data: r("v", "1")},
		{Op: OpUpdate, Key: "a", NewKey: "b", Data: r("v", "1")},
	})
	if err != nil {
		t.Fatal(err)
	}

	log := buf.String()
	for _, want := range []string{"batch input", "split result", "merged and partitioned output", "decision"} {
		if !bytes.Contains(buf.Bytes(), []byte(want)) {
			t.Errorf("log missing %q\n%s", want, log)
		}
	}

	buf.Reset()
	_, err = store.Apply([]Change{{Op: OpDelete, Key: "ghost"}})
	if err == nil {
		t.Fatal("expected rejection")
	}
	if !bytes.Contains(buf.Bytes(), []byte("batch rejected")) ||
		!bytes.Contains(buf.Bytes(), []byte("key_not_found")) {
		t.Fatalf("rejection log missing reason: %s", buf.String())
	}
}

func flatten(partitions []Partition) []Event {
	var out []Event
	for _, p := range partitions {
		out = append(out, p.Events...)
	}
	return out
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
