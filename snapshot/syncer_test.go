package snapshot

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// viewMap 把视图切片转成 map 便于断言。
func viewMap(kvs []KV) map[Key]Value {
	m := make(map[Key]Value, len(kvs))
	for _, kv := range kvs {
		m[kv.Key] = kv.Value
	}
	return m
}

// sourceRangeMap 在静止时刻读取源表某范围作为期望结果。
func sourceRangeMap(s *Source, start, end Key) map[Key]Value {
	return viewMap(s.Snapshot(start, end))
}

// installRange 串行完成一个范围的三步流程，返回高水位。
func installRange(t *testing.T, sy *Syncer, start, end Key) int64 {
	t.Helper()
	low, err := sy.BeginRange(start, end)
	if err != nil {
		t.Fatalf("BeginRange[%d,%d]: %v", start, end, err)
	}
	if err := sy.SnapshotRange(start, end); err != nil {
		t.Fatalf("SnapshotRange[%d,%d]: %v", start, end, err)
	}
	high, err := sy.CompleteRange(start, end)
	if err != nil {
		t.Fatalf("CompleteRange[%d,%d]: %v", start, end, err)
	}
	if high < low {
		t.Fatalf("highSeq %d < lowSeq %d", high, low)
	}
	return high
}

func TestBasicThreePhaseAndPoll(t *testing.T) {
	src := NewSource()
	sy := NewSyncer(src)

	src.Put(1, "a")
	src.Put(2, "b")
	src.Put(100, "outside")

	high := installRange(t, sy, 1, 10)
	want := map[Key]Value{1: "a", 2: "b"}
	if got := viewMap(sy.View()); !mapsEqual(got, want) {
		t.Fatalf("after complete: got %v want %v", got, want)
	}

	// 完成后新写入：轮询应用，区间外的写入被过滤。
	src.Put(3, "c")
	src.Put(100, "outside2")
	src.Delete(1)
	n, err := sy.Poll()
	if err != nil || n != 2 {
		t.Fatalf("Poll: n=%d err=%v, want 2 nil", n, err)
	}
	want = map[Key]Value{2: "b", 3: "c"}
	if got := viewMap(sy.View()); !mapsEqual(got, want) {
		t.Fatalf("after poll: got %v want %v", got, want)
	}

	// 再次轮询：无新日志，不重复应用，处理位置停住。
	n, err = sy.Poll()
	if err != nil || n != 0 {
		t.Fatalf("idle Poll: n=%d err=%v, want 0 nil", n, err)
	}
	if got := viewMap(sy.View()); !mapsEqual(got, want) {
		t.Fatalf("after idle poll: got %v want %v", got, want)
	}
	_ = high
}

func TestBoundaryKeys(t *testing.T) {
	src := NewSource()
	sy := NewSyncer(src)

	// 键恰好落在范围两端：必须包含；紧邻两端之外：必须排除。
	src.Put(10, "lo")
	src.Put(20, "hi")
	src.Put(9, "before")
	src.Put(21, "after")
	installRange(t, sy, 10, 20)
	want := map[Key]Value{10: "lo", 20: "hi"}
	if got := viewMap(sy.View()); !mapsEqual(got, want) {
		t.Fatalf("boundary snapshot got %v want %v", got, want)
	}

	// 轮询阶段同样按闭区间过滤。
	src.Put(10, "lo2")
	src.Put(20, "hi2")
	src.Put(9, "before2")
	src.Put(21, "after2")
	src.Delete(20)
	if n, err := sy.Poll(); err != nil || n != 3 { // 10更新、20更新、20删除
		t.Fatalf("Poll: n=%d err=%v, want 3 nil", n, err)
	}
	want = map[Key]Value{10: "lo2"}
	if got := viewMap(sy.View()); !mapsEqual(got, want) {
		t.Fatalf("boundary poll got %v want %v", got, want)
	}

	// 单键范围合法。
	src.Put(50, "x")
	installRange(t, sy, 50, 50)
	want[50] = "x"
	if got := viewMap(sy.View()); !mapsEqual(got, want) {
		t.Fatalf("single-key range got %v want %v", got, want)
	}
}

func TestWritesDuringSnapshot(t *testing.T) {
	src := NewSource()
	sy := NewSyncer(src)

	src.Put(1, "before-begin") // 一定在快照中

	low, err := sy.BeginRange(1, 10)
	if err != nil {
		t.Fatal(err)
	}
	src.Put(2, "between-begin-snapshot") // 可能在快照中，也可能只在修正日志中
	if err := sy.SnapshotRange(1, 10); err != nil {
		t.Fatal(err)
	}
	src.Put(3, "between-snapshot-complete") // 必在 (低,高] 修正日志中
	src.Delete(2)                           // 对键2的后续修正
	high, err := sy.CompleteRange(1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if high <= low {
		t.Fatalf("high %d must exceed low %d with concurrent writes", high, low)
	}
	want := sourceRangeMap(src, 1, 10)
	if got := viewMap(sy.View()); !mapsEqual(got, want) {
		t.Fatalf("corrected snapshot got %v want %v", got, want)
	}

	src.Put(4, "after-complete") // 只能由轮询追上
	src.Put(11, "out-of-range")
	if _, err := sy.Poll(); err != nil {
		t.Fatal(err)
	}
	want = sourceRangeMap(src, 1, 10)
	if got := viewMap(sy.View()); !mapsEqual(got, want) {
		t.Fatalf("after poll got %v want %v", got, want)
	}
}

func TestInvalidRange(t *testing.T) {
	src := NewSource()
	sy := NewSyncer(src)

	if _, err := sy.BeginRange(10, 5); !errors.Is(err, ErrInvalidRange) {
		t.Fatalf("start>end: got %v, want ErrInvalidRange", err)
	}
	if len(sy.Ranges()) != 0 {
		t.Fatalf("rejected begin must not register range, got %d ranges", len(sy.Ranges()))
	}
	if err := sy.SnapshotRange(10, 5); !errors.Is(err, ErrPhaseOrder) {
		t.Fatalf("snapshot unbegun invalid range: got %v, want ErrPhaseOrder", err)
	}
	if _, err := sy.CompleteRange(10, 5); !errors.Is(err, ErrPhaseOrder) {
		t.Fatalf("complete unbegun invalid range: got %v, want ErrPhaseOrder", err)
	}
	if len(sy.View()) != 0 {
		t.Fatalf("rejected ops must not change view")
	}
}

func TestRangeOverlap(t *testing.T) {
	src := NewSource()
	sy := NewSyncer(src)

	if _, err := sy.BeginRange(10, 30); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		start, end Key
	}{
		{20, 40}, // 部分相交
		{5, 15},  // 部分相交
		{10, 10}, // 端点相同
		{30, 30}, // 端点相同
		{1, 100}, // 完全包含
		{15, 25}, // 被包含
	}
	for _, c := range cases {
		if _, err := sy.BeginRange(c.start, c.end); !errors.Is(err, ErrRangeOverlap) {
			t.Fatalf("BeginRange[%d,%d]: got %v, want ErrRangeOverlap", c.start, c.end, err)
		}
	}

	// 被拒绝后状态不变：仍只有原范围、仍在 begun 阶段、低水位不变。
	rs := sy.Ranges()
	if len(rs) != 1 || rs[0].Start != 10 || rs[0].End != 30 || rs[0].Phase != PhaseBegun {
		t.Fatalf("state after rejected overlaps: %+v", rs)
	}

	// 闭区间相邻但不相交（[10,30] 与 [31,40]）必须允许。
	if _, err := sy.BeginRange(31, 40); err != nil {
		t.Fatalf("adjacent disjoint range should be allowed: %v", err)
	}
	// 已完成的范围同样参与相交判定。
	if err := sy.SnapshotRange(10, 30); err != nil {
		t.Fatal(err)
	}
	if _, err := sy.CompleteRange(10, 30); err != nil {
		t.Fatal(err)
	}
	if _, err := sy.BeginRange(25, 35); !errors.Is(err, ErrRangeOverlap) {
		t.Fatalf("overlap with completed range: got %v, want ErrRangeOverlap", err)
	}
}

func TestPhaseOrder(t *testing.T) {
	src := NewSource()
	sy := NewSyncer(src)
	src.Put(1, "a")

	// 未 Begin 先 Snapshot / Complete。
	if err := sy.SnapshotRange(1, 10); !errors.Is(err, ErrPhaseOrder) {
		t.Fatalf("snapshot before begin: %v", err)
	}
	if _, err := sy.CompleteRange(1, 10); !errors.Is(err, ErrPhaseOrder) {
		t.Fatalf("complete before begin: %v", err)
	}

	low, err := sy.BeginRange(1, 10)
	if err != nil {
		t.Fatal(err)
	}
	// 未 Snapshot 先 Complete。
	if _, err := sy.CompleteRange(1, 10); !errors.Is(err, ErrPhaseOrder) {
		t.Fatalf("complete before snapshot: %v", err)
	}
	if err := sy.SnapshotRange(1, 10); err != nil {
		t.Fatal(err)
	}
	// 重复 Snapshot。
	if err := sy.SnapshotRange(1, 10); !errors.Is(err, ErrPhaseOrder) {
		t.Fatalf("duplicate snapshot: %v", err)
	}
	// 重复 Begin 同一起始键：与已有范围相交。
	if _, err := sy.BeginRange(1, 10); !errors.Is(err, ErrRangeOverlap) {
		t.Fatalf("duplicate begin: %v", err)
	}
	// end 不匹配视为未开始。
	if _, err := sy.CompleteRange(1, 11); !errors.Is(err, ErrPhaseOrder) {
		t.Fatalf("complete with mismatched end: %v", err)
	}

	high, err := sy.CompleteRange(1, 10)
	if err != nil {
		t.Fatal(err)
	}
	// 完成后再走任何阶段都拒绝，且高水位/视图不变。
	if _, err := sy.CompleteRange(1, 10); !errors.Is(err, ErrPhaseOrder) {
		t.Fatalf("complete twice: %v", err)
	}
	if err := sy.SnapshotRange(1, 10); !errors.Is(err, ErrPhaseOrder) {
		t.Fatalf("snapshot after complete: %v", err)
	}
	rs := sy.Ranges()
	if len(rs) != 1 || rs[0].LowSeq != low || rs[0].HighSeq != high || rs[0].Phase != PhaseCompleted {
		t.Fatalf("watermark/phase changed after rejected phase ops: %+v", rs)
	}
}

func TestViewLimitAtComplete(t *testing.T) {
	src := NewSource()
	sy := NewSyncer(src, WithMaxViewRows(2))
	src.Put(1, "a")
	src.Put(2, "b")
	src.Put(3, "c")

	if _, err := sy.BeginRange(1, 10); err != nil {
		t.Fatal(err)
	}
	if err := sy.SnapshotRange(1, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := sy.CompleteRange(1, 10); !errors.Is(err, ErrViewLimitExceeded) {
		t.Fatalf("complete over limit: got %v, want ErrViewLimitExceeded", err)
	}
	// 被拒绝：视图为空、范围仍停留在 snapshotted、高水位未记录。
	if len(sy.View()) != 0 {
		t.Fatalf("rejected complete must not install rows")
	}
	rs := sy.Ranges()
	if rs[0].Phase != PhaseSnapshotted || rs[0].HighSeq != 0 {
		t.Fatalf("rejected complete must not record highSeq/advance phase: %+v", rs)
	}
	// 源表删到限额内后可重试成功（快照仍是旧的，修正日志会收敛到源表现状）。
	src.Delete(3)
	if _, err := sy.CompleteRange(1, 10); err != nil {
		t.Fatalf("retry after source shrank: %v", err)
	}
	if got := viewMap(sy.View()); !mapsEqual(got, map[Key]Value{1: "a", 2: "b"}) {
		t.Fatalf("view after retry: %v", got)
	}
}

func TestViewLimitAtPoll(t *testing.T) {
	src := NewSource()
	sy := NewSyncer(src, WithMaxViewRows(3))
	src.Put(1, "a")
	src.Put(2, "b")
	installRange(t, sy, 1, 10)

	src.Put(3, "c") // 轮询后 3 行，恰好不超限
	if n, err := sy.Poll(); err != nil || n != 1 {
		t.Fatalf("poll at limit: n=%d err=%v", n, err)
	}
	savedHigh := sy.Ranges()[0].HighSeq

	src.Put(4, "d") // 4 行，超限
	if _, err := sy.Poll(); !errors.Is(err, ErrViewLimitExceeded) {
		t.Fatalf("poll over limit: %v", err)
	}
	// 整批拒绝：视图与处理位置都不变，待处理日志不丢不跳。
	if len(sy.View()) != 3 || sy.Ranges()[0].HighSeq != savedHigh {
		t.Fatalf("rejected poll changed view/position: rows=%d high=%d",
			len(sy.View()), sy.Ranges()[0].HighSeq)
	}

	// 源表再删一键：积压日志（put4 + delete3）按序应用后为 3 行，应当成功且不重不漏。
	src.Delete(3)
	if n, err := sy.Poll(); err != nil || n != 2 {
		t.Fatalf("poll after shrink: n=%d err=%v", n, err)
	}
	if got := viewMap(sy.View()); !mapsEqual(got, map[Key]Value{1: "a", 2: "b", 4: "d"}) {
		t.Fatalf("view after caught-up poll: %v", got)
	}
}

// TestDeterminism 同一输入序列重复执行，输出（视图+范围状态）必须逐字节一致。
func TestDeterminism(t *testing.T) {
	run := func() []byte {
		src := NewSource()
		sy := NewSyncer(src)
		for i := Key(0); i < 20; i++ {
			if i%4 == 0 {
				src.Delete(i)
			} else {
				src.Put(i, Value(fmt.Sprintf("v%d", i)))
			}
		}
		installRange(t, sy, 0, 9)
		for i := Key(0); i < 15; i++ {
			src.Put(i, Value(fmt.Sprintf("w%d", i)))
		}
		installRange(t, sy, 10, 19)
		sy.Poll()
		for i := Key(0); i < 20; i += 3 {
			src.Delete(i)
		}
		sy.Poll()
		sy.Poll() // 空闲轮询输出也必须稳定
		out := struct {
			View   []KV
			Ranges []RangeInfo
		}{sy.View(), sy.Ranges()}
		b, _ := json.Marshal(out)
		return b
	}
	a, b := run(), run()
	if !bytes.Equal(a, b) {
		t.Fatalf("non-deterministic output:\n%s\n%s", a, b)
	}
}

func TestConcurrentWriters(t *testing.T) {
	src := NewSource()
	sy := NewSyncer(src)

	const writers = 4
	const keysPerRange = 50
	var stop atomic.Bool
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			r := rand.New(rand.NewSource(seed))
			for !stop.Load() {
				k := Key(r.Intn(2 * keysPerRange))
				if r.Intn(3) == 0 {
					src.Delete(k)
				} else {
					src.Put(k, Value(fmt.Sprintf("w%d-%d", seed, r.Intn(1000))))
				}
			}
		}(int64(w + 1))
	}

	// 并发读者：持续读取视图，只要求不 panic、输出有序（快照读一致性）。
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for !stop.Load() {
			v := sy.View()
			for i := 1; i < len(v); i++ {
				if v[i-1].Key >= v[i].Key {
					t.Errorf("view not sorted: %d then %d", v[i-1].Key, v[i].Key)
					return
				}
			}
			time.Sleep(time.Microsecond)
		}
	}()

	// 主流程在持续写入下分三步安装两个相邻范围，再轮询追平。
	installRange(t, sy, 0, keysPerRange-1)
	installRange(t, sy, keysPerRange, 2*keysPerRange-1)
	for i := 0; i < 20; i++ {
		sy.Poll()
		time.Sleep(2 * time.Millisecond)
	}

	stop.Store(true)
	wg.Wait()
	// 停写后最后一轮轮询必须完全追平源表。
	if n, err := sy.Poll(); err != nil {
		t.Fatalf("final poll: %v", err)
	} else {
		t.Logf("final poll applied %d entries", n)
	}
	<-readerDone

	got := viewMap(sy.View())
	want := sourceRangeMap(src, 0, 2*keysPerRange-1)
	if !mapsEqual(got, want) {
		t.Fatalf("view diverges from source: got %d rows want %d rows", len(got), len(want))
	}
}

// TestLoggingInputOutputBasis 验证日志中包含输入、输出与判定依据。
func TestLoggingInputOutputBasis(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	src := NewSource()
	sy := NewSyncer(src, WithLogger(logger))

	src.Put(1, "a")
	installRange(t, sy, 1, 10)
	if _, err := sy.BeginRange(5, 8); err == nil { // 相交拒绝
		t.Fatal("expected overlap rejection")
	}
	out := buf.String()
	for _, want := range []string{
		`"msg":"BeginRange accepted"`,
		`"input":"[1,10]"`,
		`"output":"lowSeq=1"`,
		`"basis":`,
		`"msg":"BeginRange rejected"`,
		`"reason":"key range overlaps`,
	} {
		if !bytes.Contains(buf.Bytes(), []byte(want)) {
			t.Errorf("log missing %s\nfull log:\n%s", want, out)
		}
	}
}

func mapsEqual(a, b map[Key]Value) bool {
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
