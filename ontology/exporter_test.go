package ontology

import (
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"sync"
	"testing"
)

// logState 打印投递后各分区已投递事件数、对齐点与待定缓冲及判定依据。
func logState(t *testing.T, tag string, e *Exporter) {
	t.Helper()
	alignment := e.Alignment()
	line := fmt.Sprintf("[%s] alignment=%d |", tag, alignment)
	for p := range e.partitions {
		delivered, _ := e.PartitionDelivered(p)
		pending, _ := e.PartitionPending(p)
		line += fmt.Sprintf(" p%d{delivered=%d,pending=%d}", p, delivered, pending)
	}
	t.Log(line)
}

func mustAppend(t *testing.T, e *Exporter, p int, ev Event) {
	t.Helper()
	if err := e.Append(p, ev); err != nil {
		t.Fatalf("append p=%d key=%q unexpected error: %v", p, ev.Key, err)
	}
	t.Logf("[append] p=%d key=%q value=%q -> accepted", p, ev.Key, ev.Value)
	logState(t, "append", e)
}

func assertReject(t *testing.T, e *Exporter, p int, ev Event, want error) {
	t.Helper()
	before := snapshotCounts(e)
	err := e.Append(p, ev)
	if !errors.Is(err, want) {
		t.Fatalf("append p=%d key=%q err=%v, want %v", p, ev.Key, err, want)
	}
	if after := snapshotCounts(e); before != after {
		t.Fatalf("rejected append changed state: before=%s after=%s", before, after)
	}
	t.Logf("[append] p=%d key=%q value=%q -> REJECTED (%v), state unchanged %s",
		p, ev.Key, ev.Value, err, before)
	logState(t, "reject", e)
}

func snapshotCounts(e *Exporter) string {
	s := ""
	for p := range e.partitions {
		delivered, _ := e.PartitionDelivered(p)
		pending, _ := e.PartitionPending(p)
		s += fmt.Sprintf("p%d(d=%d,pen=%d);", p, delivered, pending)
	}
	return s
}

// TestBackpressureReject 待定缓冲达上限即拒收，且状态不变。
func TestBackpressureReject(t *testing.T) {
	const limit = 2
	e := NewExporter(3, limit)

	// 对齐点为0时，分区0连收2条即达到待定上限。
	mustAppend(t, e, 0, Event{Key: "k0", Value: "v0"})
	mustAppend(t, e, 0, Event{Key: "k1", Value: "v1"})
	assertReject(t, e, 0, Event{Key: "k2", Value: "v2"}, ErrBackpressure)

	mustAppend(t, e, 1, Event{Key: "a", Value: "1"})
	mustAppend(t, e, 1, Event{Key: "b", Value: "2"})
	assertReject(t, e, 1, Event{Key: "c", Value: "3"}, ErrBackpressure)

	// 推进落后的分区2，对齐点抬高，待定缓冲释放后可继续收。
	mustAppend(t, e, 2, Event{Key: "x", Value: "p2-0"})
	if got := e.Alignment(); got != 1 {
		t.Fatalf("alignment=%d want 1", got)
	}
	mustAppend(t, e, 0, Event{Key: "k2", Value: "v2"})
	mustAppend(t, e, 1, Event{Key: "c", Value: "3"})
	logState(t, "backpressure-end", e)

	if err := e.SelfCheck(); err != nil {
		t.Fatalf("self check: %v", err)
	}
}

// TestAlignmentIsMinimum 对齐点取所有分区已投递事件数的最小值。
func TestAlignmentIsMinimum(t *testing.T) {
	e := NewExporter(3, 10)
	mustAppend(t, e, 0, Event{Key: "a", Value: "0"})
	mustAppend(t, e, 0, Event{Key: "b", Value: "0"})
	mustAppend(t, e, 1, Event{Key: "a", Value: "1"})
	if got := e.Alignment(); got != 0 {
		t.Fatalf("alignment=%d want 0 (p2 empty)", got)
	}

	mustAppend(t, e, 2, Event{Key: "a", Value: "2"})
	if got := e.Alignment(); got != 1 {
		t.Fatalf("alignment=%d want 1", got)
	}

	snap := e.Snapshot()
	t.Logf("[snapshot] alignment=%d entries=%v", snap.Alignment, snap.Entries)
	if snap.Alignment != 1 || len(snap.Entries) != 1 {
		t.Fatalf("snap=%+v want alignment=1 with single key a", snap)
	}
	// 同键跨分区后写覆盖前写：分区2的值覆盖分区0、1。
	if got := snap.Entries["a"].Value; got != "2" {
		t.Fatalf("a=%q want \"2\" (higher partition wins)", got)
	}

	mustAppend(t, e, 1, Event{Key: "b", Value: "1"})
	mustAppend(t, e, 2, Event{Key: "b", Value: "2"})
	if got := e.Alignment(); got != 2 {
		t.Fatalf("alignment=%d want 2", got)
	}
	snap = e.Snapshot()
	t.Logf("[snapshot] alignment=%d entries=%v", snap.Alignment, snap.Entries)
	if snap.Entries["a"].Value != "2" || snap.Entries["b"].Value != "2" {
		t.Fatalf("entries=%v want both overwritten by p2", snap.Entries)
	}

	// 可复现：同一状态下重复快照结果一致。
	snap2 := e.Snapshot()
	if !reflect.DeepEqual(snap, snap2) {
		t.Fatalf("snapshot not reproducible: %v vs %v", snap, snap2)
	}
}

// TestEqualContribution 任一快照内每分区贡献条数恰等于对齐点。
func TestEqualContribution(t *testing.T) {
	e := NewExporter(4, 10)
	for p := 0; p < 4; p++ {
		for i := 0; i <= p; i++ { // 分区长度 1,2,3,4
			mustAppend(t, e, p, Event{
				Key:   fmt.Sprintf("p%d-k%d", p, i),
				Value: fmt.Sprintf("p%d-v%d", p, i),
			})
		}
	}

	snap := e.Snapshot()
	if snap.Alignment != 1 {
		t.Fatalf("alignment=%d want 1", snap.Alignment)
	}
	if len(snap.Entries) != 4 {
		t.Fatalf("entries=%d want 4 (one unique key per partition)", len(snap.Entries))
	}
	t.Logf("[snapshot] alignment=%d entries=%v; 每分区各贡献 %d 条",
		snap.Alignment, snap.Entries, snap.Alignment)

	for p := 0; p < 4; p++ {
		key := fmt.Sprintf("p%d-k0", p)
		if got, ok := snap.Entries[key]; !ok || got.Value != fmt.Sprintf("p%d-v0", p) {
			t.Fatalf("partition %d prefix event missing: %v", p, snap.Entries)
		}
	}

	// 逐轮追平最短分区，对齐点只整跳变；
	// 每轮用“取各分区等长前缀拼接”独立核对快照，杜绝半个分区的中间态。
	for round := 2; round <= 4; round++ {
		for p := 0; p < 4; p++ {
			delivered, _ := e.PartitionDelivered(p)
			for delivered < round {
				mustAppend(t, e, p, Event{
					Key:   fmt.Sprintf("p%d-k%d", p, delivered),
					Value: fmt.Sprintf("p%d-v%d", p, delivered),
				})
				delivered++
			}
		}
		snap := e.Snapshot()
		if snap.Alignment != round {
			t.Fatalf("round=%d alignment=%d", round, snap.Alignment)
		}
		want := map[string]Event{}
		for p := 0; p < 4; p++ {
			for i := 0; i < round; i++ {
				ev := Event{Key: fmt.Sprintf("p%d-k%d", p, i), Value: fmt.Sprintf("p%d-v%d", p, i)}
				want[ev.Key] = ev
			}
		}
		if !reflect.DeepEqual(want, snap.Entries) {
			t.Fatalf("round=%d snap=%v want=%v", round, snap.Entries, want)
		}
		t.Logf("[check] round=%d alignment=%d 每分区贡献=%d 与前缀拼接核对一致",
			round, snap.Alignment, round)
	}
}

// TestRejectCauses 三类拒收原因可区分，且一次失败状态不变。
func TestRejectCauses(t *testing.T) {
	e := NewExporter(2, 1)
	mustAppend(t, e, 0, Event{Key: "z", Value: "0"}) // p0 pending=1 达上限

	assertReject(t, e, 2, Event{Key: "k", Value: "v"}, ErrPartitionOutOfRange)
	assertReject(t, e, -1, Event{Key: "k", Value: "v"}, ErrPartitionOutOfRange)
	assertReject(t, e, 0, Event{Key: "", Value: "v"}, ErrEmptyKey)
	assertReject(t, e, 0, Event{Key: "overflow", Value: "v"}, ErrBackpressure)

	if delivered, _ := e.PartitionDelivered(0); delivered != 1 {
		t.Fatalf("p0 delivered=%d want 1", delivered)
	}
}

// TestZeroPendingLimit 上限为0时任何分区都不能领先对齐点，
// 因此首条事件也会被背压拒收（退化但自洽的配置）。
func TestZeroPendingLimit(t *testing.T) {
	e := NewExporter(2, 0)
	assertReject(t, e, 0, Event{Key: "a", Value: "1"}, ErrBackpressure)
	assertReject(t, e, 1, Event{Key: "a", Value: "2"}, ErrBackpressure)
	snap := e.Snapshot()
	if snap.Alignment != 0 || len(snap.Entries) != 0 {
		t.Fatalf("snap=%+v want empty", snap)
	}
}

// TestConcurrentSnapshotAndAppend 快照/自检与投递并发：
// 每次快照每分区贡献条数恒等于该快照对齐点，不存在半个分区被纳入的中间态。
func TestConcurrentSnapshotAndAppend(t *testing.T) {
	const partitions = 4
	const limit = 8
	e := NewExporter(partitions, limit)

	var wg sync.WaitGroup
	stop := make(chan struct{})

	for p := 0; p < partitions; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				err := e.Append(p, Event{
					Key:   fmt.Sprintf("p%d-k%d", p, i%5),
					Value: fmt.Sprintf("p%d-v%d", p, i),
				})
				switch {
				case err == nil:
				case errors.Is(err, ErrBackpressure):
					runtime.Gosched() // 等其他分区追上来抬高对齐点
				default:
					t.Errorf("unexpected append error: %v", err)
					return
				}
			}
		}(p)
	}

	for reader := 0; reader < 4; reader++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				snap := e.Snapshot()
				// 快照是某一时刻的点态：在同一把读锁内核对
				// “各分区前 alignment 条拼接”与快照完全一致，
				// 保证不存在半个分区被纳入的中间态。
				if err := verifySnapshot(e, snap); err != nil {
					t.Errorf("reader %d: %v", id, err)
					return
				}
				if err := e.SelfCheck(); err != nil {
					t.Errorf("reader %d selfcheck: %v", id, err)
					return
				}
			}
		}(reader)
	}

	for e.Alignment() < 100 {
		runtime.Gosched()
	}
	close(stop)
	wg.Wait()

	snap := e.Snapshot()
	if err := verifySnapshot(e, snap); err != nil {
		t.Fatal(err)
	}
	logState(t, "concurrent-end", e)
	t.Logf("[snapshot] final alignment=%d entries=%d", snap.Alignment, len(snap.Entries))
	if err := e.SelfCheck(); err != nil {
		t.Fatalf("final self check: %v", err)
	}
}

// verifySnapshot 在同一把读锁内按“取每个分区前 alignment 条、按分区顺序拼接、
// 同键后写覆盖”重建期望结果，与已导出的点态快照逐一核对。
func verifySnapshot(e *Exporter, snap Snapshot) error {
	e.mu.RLock()
	defer e.mu.RUnlock()
	current := e.alignmentLocked()
	// 投递只增不减，快照对齐点不可能晚于当前对齐点。
	if snap.Alignment > current {
		return fmt.Errorf("snapshot alignment=%d > current=%d", snap.Alignment, current)
	}
	alignment := snap.Alignment
	want := make(map[string]Event)
	for p := 0; p < len(e.partitions); p++ {
		buf := e.partitions[p]
		if len(buf) < alignment {
			return fmt.Errorf("partition %d delivered=%d < snapshot alignment=%d",
				p, len(buf), alignment)
		}
		contributed := 0
		for _, ev := range buf[:alignment] {
			want[ev.Key] = ev
			contributed++
		}
		if contributed != alignment {
			return fmt.Errorf("partition %d contributed=%d want %d", p, contributed, alignment)
		}
	}
	if !reflect.DeepEqual(want, snap.Entries) {
		return fmt.Errorf("snapshot mismatch alignment=%d snap=%v want=%v",
			alignment, snap.Entries, want)
	}
	return nil
}
