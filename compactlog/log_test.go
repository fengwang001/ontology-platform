package compactlog

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"
)

var testEpoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func newTestLog(retention time.Duration, capacity int) (*Log, *ManualClock) {
	clock := NewManualClock(testEpoch)
	return New(clock, retention, capacity), clock
}

// naiveReplay 对日志快照做朴素重放，作为消费者视图的对照基准。
func naiveReplay(records []Record) map[string]string {
	view := make(map[string]string)
	for _, r := range records {
		if r.Tombstone {
			delete(view, r.Key)
		} else {
			view[r.Key] = r.Value
		}
	}
	return view
}

func logRecords(t *testing.T, title string, records []Record) {
	t.Helper()
	t.Logf("%s（共 %d 条）:", title, len(records))
	for _, r := range records {
		t.Logf("  %s", r)
	}
}

func logView(t *testing.T, title string, view map[string]string) {
	t.Helper()
	keys := make([]string, 0, len(view))
	for k := range view {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	t.Logf("%s（共 %d 键）:", title, len(keys))
	for _, k := range keys {
		t.Logf("  %q=%q", k, view[k])
	}
}

func seqsOf(records []Record) []uint64 {
	seqs := make([]uint64, 0, len(records))
	for _, r := range records {
		seqs = append(seqs, r.Seq)
	}
	return seqs
}

// 墓碑年龄恰好等于保留期时必须保留，超过保留期才清除。
func TestTombstoneExactlyAtRetention(t *testing.T) {
	const retention = time.Hour
	l, clock := newTestLog(retention, 100)

	if _, err := l.Append("a", "1"); err != nil {
		t.Fatalf("append a: %v", err)
	}
	clock.Advance(10 * time.Minute)
	if _, err := l.Delete("a"); err != nil {
		t.Fatalf("delete a: %v", err)
	}

	// 情形一：年龄 == 保留期，判定依据：规则为“超过保留期才清除”，等于则保留。
	clock.Advance(retention)
	if err := l.Compact(); err != nil {
		t.Fatalf("compact at exact retention: %v", err)
	}
	snap := l.Snapshot()
	logRecords(t, "压实后日志（墓碑年龄恰好等于保留期）", snap)
	if len(snap) != 1 || !snap[0].Tombstone || snap[0].Seq != 2 {
		t.Fatalf("判定依据：年龄==保留期应保留墓碑，期望仅有序号 2 的墓碑，实际 %v", seqsOf(snap))
	}
	t.Log("判定依据：年龄(1h) == 保留期(1h)，未超过，墓碑保留")

	// 情形二：年龄 > 保留期，清除墓碑。
	clock.Advance(time.Nanosecond)
	if err := l.Compact(); err != nil {
		t.Fatalf("compact past retention: %v", err)
	}
	snap = l.Snapshot()
	logRecords(t, "压实后日志（墓碑年龄超过保留期 1ns）", snap)
	if len(snap) != 0 {
		t.Fatalf("判定依据：年龄>保留期应清除墓碑，期望日志为空，实际 %v", seqsOf(snap))
	}
	t.Log("判定依据：年龄(1h+1ns) > 保留期(1h)，墓碑清除")
}

// 压实只保留每个键的最新记录，序号不重编号，只留空洞。
func TestCompactionOrderAndHoles(t *testing.T) {
	l, clock := newTestLog(24*time.Hour, 100)

	inputs := []struct {
		op, key, val string
	}{
		{"put", "a", "1"},
		{"put", "b", "1"},
		{"put", "a", "2"},
		{"del", "b", ""},
		{"put", "c", "1"},
		{"put", "a", "3"},
	}
	var written []Record
	for _, in := range inputs {
		var r Record
		var err error
		if in.op == "put" {
			r, err = l.Append(in.key, in.val)
		} else {
			r, err = l.Delete(in.key)
		}
		if err != nil {
			t.Fatalf("%s %s: %v", in.op, in.key, err)
		}
		written = append(written, r)
		clock.Advance(time.Second)
	}
	logRecords(t, "输入序列", written)

	if err := l.Compact(); err != nil {
		t.Fatalf("compact: %v", err)
	}
	snap := l.Snapshot()
	logRecords(t, "压实后日志", snap)

	wantSeqs := []uint64{4, 5, 6} // #4=b 的墓碑，#5=c=1，#6=a=3
	if got := seqsOf(snap); !reflect.DeepEqual(got, wantSeqs) {
		t.Fatalf("判定依据：每键仅留最新记录且不重编号，期望序号 %v，实际 %v", wantSeqs, got)
	}
	t.Logf("判定依据：a 的最新为 #6，b 的最新为 #4（墓碑，保留期内），c 的最新为 #5；序号不重编号，#1/#2/#3 成为空洞")

	// 新消费者从空洞日志读到的视图必须与朴素重放一致。
	id := l.Subscribe()
	if _, err := l.Read(id, 100); err != nil {
		t.Fatalf("read: %v", err)
	}
	view, err := l.View(id)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	want := naiveReplay(snap)
	logView(t, "新消费者视图", view)
	if !reflect.DeepEqual(view, want) {
		t.Fatalf("判定依据：消费者视图须与朴素重放一致，期望 %v，实际 %v", want, view)
	}
}

// 各类非法输入必须被拒绝，且不得改变时钟、序号、日志与消费者状态。
func TestRejectedOperations(t *testing.T) {
	l, clock := newTestLog(time.Hour, 2)

	if _, err := l.Append("k", "v1"); err != nil {
		t.Fatalf("append k: %v", err)
	}
	id := l.Subscribe()
	if _, err := l.Read(id, 10); err != nil {
		t.Fatalf("read: %v", err)
	}

	state := func() string {
		snap := l.Snapshot()
		view, _ := l.View(id)
		return fmt.Sprintf("seqs=%v view=%v clock=%s", seqsOf(snap), view, clock.Now())
	}
	before := state()

	// 1. 空键写入与删除。
	if _, err := l.Append("", "x"); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("期望 ErrEmptyKey，实际 %v", err)
	}
	if _, err := l.Delete(""); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("期望 ErrEmptyKey，实际 %v", err)
	}
	t.Log("判定依据：空键写入/删除 → ErrEmptyKey")

	// 2. 日志已满（容量 2，当前 1 条；先写满再拒绝）。
	if _, err := l.Append("k2", "v2"); err != nil {
		t.Fatalf("append k2: %v", err)
	}
	if _, err := l.Append("k3", "v3"); !errors.Is(err, ErrLogFull) {
		t.Fatalf("期望 ErrLogFull，实际 %v", err)
	}
	if _, err := l.Delete("k"); !errors.Is(err, ErrLogFull) {
		t.Fatalf("期望 ErrLogFull，实际 %v", err)
	}
	t.Log("判定依据：保留记录数达到容量上限 → ErrLogFull")

	// 3. 时间回退。
	clock.Set(testEpoch.Add(-time.Hour))
	if _, err := l.Append("k4", "v4"); !errors.Is(err, ErrClockRegression) {
		t.Fatalf("期望 ErrClockRegression，实际 %v", err)
	}
	if err := l.Compact(); !errors.Is(err, ErrClockRegression) {
		t.Fatalf("期望压实返回 ErrClockRegression，实际 %v", err)
	}
	t.Log("判定依据：当前时间早于日志最大写入时间 → ErrClockRegression")
	clock.Set(testEpoch.Add(2 * time.Hour))

	// 4. 未订阅消费者读取。
	if _, err := l.Read(999, 10); !errors.Is(err, ErrUnknownConsumer) {
		t.Fatalf("期望 ErrUnknownConsumer，实际 %v", err)
	}
	if _, err := l.View(999); !errors.Is(err, ErrUnknownConsumer) {
		t.Fatalf("期望 ErrUnknownConsumer，实际 %v", err)
	}
	t.Log("判定依据：对未订阅消费者读取 → ErrUnknownConsumer")

	// 被拒绝的操作不得改变状态：仅允许第 2 步中成功的 k2 写入生效。
	after := state()
	snap := l.Snapshot()
	logRecords(t, "非法操作后的日志", snap)
	if want := seqsOf(snap); !reflect.DeepEqual(want, []uint64{1, 2}) {
		t.Fatalf("判定依据：被拒绝的写入不得分配序号或写入日志，期望序号 [1 2]，实际 %v", want)
	}
	view, _ := l.View(id)
	if !reflect.DeepEqual(view, map[string]string{"k": "v1"}) {
		t.Fatalf("判定依据：被拒绝的操作不得改变消费者视图，实际 %v", view)
	}
	t.Logf("状态对比：前=%q 后=%q（差异仅来自合法写入 k2）", before, after)
}

// 写入、压实与多消费者读取并发执行，已提交消费者的视图始终正确。
func TestConcurrentAccess(t *testing.T) {
	l, clock := newTestLog(time.Hour, 100000)

	committed := l.Subscribe()
	if _, err := l.Append("warm", "0"); err != nil {
		t.Fatalf("append warm: %v", err)
	}
	if _, err := l.Read(committed, 1); err != nil {
		t.Fatalf("warm read: %v", err)
	}

	const writers = 8
	const perWriter = 50
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				key := fmt.Sprintf("key-%d", (w*perWriter+i)%32)
				clock.Advance(time.Millisecond)
				if i%7 == 3 {
					_, _ = l.Delete(key)
				} else {
					_, _ = l.Append(key, fmt.Sprintf("w%d-%d", w, i))
				}
			}
		}(w)
	}
	for c := 0; c < 4; c++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := l.Subscribe()
			for {
				applied, err := l.Read(id, 16)
				if err != nil {
					t.Errorf("read: %v", err)
					return
				}
				if len(applied) == 0 {
					return
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			_ = l.Compact()
		}
	}()
	wg.Wait()

	// 追赶完成后，所有消费者的视图必须与当前日志的朴素重放一致。
	want := naiveReplay(l.Snapshot())
	logView(t, "朴素重放基准视图", want)
	l.mu.Lock()
	ids := make([]uint64, 0, len(l.consumers))
	for id := range l.consumers {
		ids = append(ids, id)
	}
	l.mu.Unlock()
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		for {
			applied, err := l.Read(id, 64)
			if err != nil {
				t.Fatalf("read consumer %d: %v", id, err)
			}
			if len(applied) == 0 {
				break
			}
		}
		view, err := l.View(id)
		if err != nil {
			t.Fatalf("view consumer %d: %v", id, err)
		}
		if !reflect.DeepEqual(view, want) {
			t.Fatalf("判定依据：追赶完成的消费者视图须与朴素重放一致，消费者 %d 视图 %v，期望 %v", id, view, want)
		}
	}
	t.Log("判定依据：全部消费者追赶完成后视图与日志朴素重放一致")
}

// 同一输入序列反复计算必须得到完全相同的输出。
func TestDeterministicReplay(t *testing.T) {
	run := func() ([]Record, map[string]string) {
		l, clock := newTestLog(30*time.Minute, 1000)
		ops := []struct {
			op, key, val string
		}{
			{"put", "x", "1"}, {"put", "y", "1"}, {"del", "x", ""},
			{"put", "x", "2"}, {"put", "z", "1"}, {"del", "y", ""},
		}
		for _, o := range ops {
			if o.op == "put" {
				if _, err := l.Append(o.key, o.val); err != nil {
					t.Fatalf("append: %v", err)
				}
			} else {
				if _, err := l.Delete(o.key); err != nil {
					t.Fatalf("delete: %v", err)
				}
			}
			clock.Advance(time.Minute)
		}
		clock.Advance(31 * time.Minute) // 让 x 的墓碑(#3)过期，y 的墓碑(#6)保留
		if err := l.Compact(); err != nil {
			t.Fatalf("compact: %v", err)
		}
		id := l.Subscribe()
		if _, err := l.Read(id, 100); err != nil {
			t.Fatalf("read: %v", err)
		}
		view, err := l.View(id)
		if err != nil {
			t.Fatalf("view: %v", err)
		}
		return l.Snapshot(), view
	}

	snap1, view1 := run()
	snap2, view2 := run()
	logRecords(t, "第一次运行压实后日志", snap1)
	logView(t, "第一次运行消费者视图", view1)
	if !reflect.DeepEqual(snap1, snap2) || !reflect.DeepEqual(view1, view2) {
		t.Fatalf("判定依据：同一输入序列两次运行输出必须完全相同\n第一次: %v %v\n第二次: %v %v", snap1, view1, snap2, view2)
	}
	t.Log("判定依据：注入手动时钟，两次运行输入相同，压实结果与消费者视图逐字节一致")
}
