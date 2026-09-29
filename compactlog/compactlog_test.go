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

var base = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func at(sec int) time.Time { return base.Add(time.Duration(sec) * time.Second) }

func logRecords(t *testing.T, title string, recs []Record) {
	t.Helper()
	t.Logf("%s (共 %d 条):", title, len(recs))
	for _, r := range recs {
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
	t.Logf("%s (共 %d 键):", title, len(keys))
	for _, k := range keys {
		t.Logf("  %q => %q", k, view[k])
	}
}

func seqsOf(recs []Record) []uint64 {
	out := make([]uint64, len(recs))
	for i, r := range recs {
		out[i] = r.Seq
	}
	return out
}

// drain 让消费者读到追平，返回读到的记录序列。
func drain(t *testing.T, l *Log, name string) []Record {
	t.Helper()
	var out []Record
	for {
		rec, ok, err := l.ReadNext(name)
		if err != nil {
			t.Fatalf("ReadNext(%q) 出错: %v", name, err)
		}
		if !ok {
			return out
		}
		out = append(out, rec)
	}
}

// naiveReplay 对完整输入序列做朴素重放，得到参考视图。
func naiveReplay(recs []Record) map[string]string {
	view := make(map[string]string)
	for _, r := range recs {
		if r.Tombstone {
			delete(view, r.Key)
		} else {
			view[r.Key] = r.Value
		}
	}
	return view
}

// 墓碑恰好达到保留期时应保留，超过保留期才清除。
func TestTombstoneRetentionBoundary(t *testing.T) {
	const retention = 10 * time.Second
	l := New(16, retention)

	if _, err := l.Append("a", "1", false, at(0)); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append("a", "", true, at(5)); err != nil { // 墓碑写入于 t=5s
		t.Fatal(err)
	}
	logRecords(t, "输入: put a=1 @0s, tombstone a @5s", l.Snapshot())

	// 恰好达到保留期: now - writtenAt == retention，未"超过"，应保留。
	if err := l.Compact(at(5 + 10)); err != nil {
		t.Fatal(err)
	}
	snap := l.Snapshot()
	logRecords(t, "在 now=15s(恰好达到保留期) 压实后", snap)
	t.Logf("判定依据: 年龄 %v == 保留期 %v，规则为超过才清除，故墓碑保留", 10*time.Second, retention)
	if len(snap) != 1 || !snap[0].Tombstone || snap[0].Seq != 2 {
		t.Fatalf("恰好达到保留期的墓碑应保留, 实际: %v", snap)
	}

	// 超过保留期 1 纳秒，应清除。
	if err := l.Compact(at(15).Add(time.Nanosecond)); err != nil {
		t.Fatal(err)
	}
	snap = l.Snapshot()
	logRecords(t, "在 now=15s+1ns(超过保留期) 压实后", snap)
	t.Logf("判定依据: 年龄 10s+1ns > 保留期 10s，墓碑清除")
	if len(snap) != 0 {
		t.Fatalf("超过保留期的墓碑应清除, 实际: %v", snap)
	}
}

// 压实只留空洞、序号不重编号，每个键只保留最新记录。
func TestCompactionOrderAndHoles(t *testing.T) {
	l := New(16, time.Hour)
	inputs := []struct {
		key, value string
		tomb       bool
	}{
		{"a", "1", false}, // #1 被 #4 覆盖
		{"b", "1", false}, // #2 保留(b 最新)
		{"a", "2", false}, // #3 被 #4 覆盖
		{"a", "3", false}, // #4 保留(a 最新)
		{"c", "1", false}, // #5 被 #6 覆盖
		{"c", "", true},   // #6 保留(未过期的墓碑)
	}
	for i, in := range inputs {
		if _, err := l.Append(in.key, in.value, in.tomb, at(i)); err != nil {
			t.Fatal(err)
		}
	}
	logRecords(t, "输入(按追加顺序)", l.Snapshot())

	if err := l.Compact(at(100)); err != nil {
		t.Fatal(err)
	}
	snap := l.Snapshot()
	logRecords(t, "压实后日志", snap)
	t.Logf("判定依据: 每键只留最大序号记录; 序号不重编号, 被覆盖记录成为空洞")

	wantSeqs := []uint64{2, 4, 6}
	if got := seqsOf(snap); !reflect.DeepEqual(got, wantSeqs) {
		t.Fatalf("压实后序号 = %v, 期望 %v", got, wantSeqs)
	}

	// 新消费者从压实后的日志追赶，视图应与朴素重放一致。
	if err := l.Subscribe("late"); err != nil {
		t.Fatal(err)
	}
	read := drain(t, l, "late")
	logRecords(t, "消费者 late 读到的记录(跳过空洞)", read)
	view, err := l.View("late")
	if err != nil {
		t.Fatal(err)
	}
	logView(t, "消费者 late 的视图", view)
	want := map[string]string{"a": "3", "b": "1"} // c 已被墓碑删除
	if !reflect.DeepEqual(view, want) {
		t.Fatalf("视图 = %v, 期望 %v", view, want)
	}
}

// 各类非法输入必须被拒绝且不得改变任何状态。
func TestRejectedOperationsKeepState(t *testing.T) {
	l := New(3, time.Minute)
	if _, err := l.Append("k", "v1", false, at(10)); err != nil {
		t.Fatal(err)
	}
	if err := l.Subscribe("c1"); err != nil {
		t.Fatal(err)
	}
	before := l.Snapshot()

	cases := []struct {
		name string
		op   func() error
		want error
	}{
		{"空键", func() error { _, err := l.Append("", "v", false, at(11)); return err }, ErrEmptyKey},
		{"时间回退", func() error { _, err := l.Append("k", "v2", false, at(9)); return err }, ErrTimeRegression},
		{"压实时间回退", func() error { return l.Compact(at(9)) }, ErrTimeRegression},
		{"未订阅消费者读取", func() error { _, _, err := l.ReadNext("ghost"); return err }, ErrUnknownConsumer},
		{"未订阅消费者视图", func() error { _, err := l.View("ghost"); return err }, ErrUnknownConsumer},
		{"重复订阅", func() error { return l.Subscribe("c1") }, ErrConsumerExists},
	}
	for _, tc := range cases {
		err := tc.op()
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s: 错误 = %v, 期望 %v", tc.name, err, tc.want)
		}
		t.Logf("拒绝 %s: %v (原因可区分)", tc.name, err)
	}

	// 填满日志后追加必须拒绝(capacity=3, 已有 3 条)。
	if _, err := l.Append("k2", "v", false, at(11)); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append("k3", "v", false, at(12)); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append("k4", "v", false, at(13)); !errors.Is(err, ErrLogFull) {
		t.Fatalf("日志已满: 错误 = %v, 期望 %v", err, ErrLogFull)
	}
	t.Logf("拒绝 日志已满: %v", ErrLogFull)

	// 验证状态未被任何被拒绝的操作改变: 序号连续无空洞。
	snap := l.Snapshot()
	logRecords(t, "所有拒绝操作后的日志", snap)
	if got, want := seqsOf(snap), []uint64{1, 2, 3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("序号 = %v, 期望 %v (被拒绝的追加不得消耗序号)", got, want)
	}
	if !reflect.DeepEqual(before, snap[:1]) {
		t.Fatalf("首条记录被改变: %v vs %v", before, snap[:1])
	}
	// 时钟仍停留在最后一次成功写入 at(12): 以 at(12) 追加应只触发
	// ErrLogFull 而非 ErrTimeRegression, 证明被拒绝的 at(13) 未推进时钟。
	if _, err := l.Append("k5", "v", false, at(12)); !errors.Is(err, ErrLogFull) || errors.Is(err, ErrTimeRegression) {
		t.Fatalf("时钟应停留在 at(12), 错误 = %v, 期望仅 ErrLogFull", err)
	}
	t.Logf("判定依据: 快照首条未变, 序号无空洞, at(12) 不触发时间回退")

	// 消费者位置与视图不受拒绝操作影响。
	read := drain(t, l, "c1")
	if got := seqsOf(read); !reflect.DeepEqual(got, []uint64{1, 2, 3}) {
		t.Fatalf("消费者读到序号 %v, 期望 [1 2 3]", got)
	}
	view, _ := l.View("c1")
	logView(t, "消费者 c1 追平后的视图", view)
	if want := map[string]string{"k": "v1", "k2": "v", "k3": "v"}; !reflect.DeepEqual(view, want) {
		t.Fatalf("视图 = %v, 期望 %v", view, want)
	}
}

// 同一输入序列反复计算必须得到完全相同的输出。
func TestDeterminism(t *testing.T) {
	run := func() ([]Record, map[string]string) {
		l := New(64, 10*time.Second)
		for i := 0; i < 20; i++ {
			key := fmt.Sprintf("key-%d", i%4)
			tomb := i%7 == 6
			if _, err := l.Append(key, fmt.Sprintf("v%d", i), tomb, at(i)); err != nil {
				t.Fatal(err)
			}
		}
		if err := l.Compact(at(19)); err != nil { // t=6 的墓碑年龄 13s 过期, t=13 的年龄 6s 保留
			t.Fatal(err)
		}
		if err := l.Subscribe("c"); err != nil {
			t.Fatal(err)
		}
		drain(t, l, "c")
		view, _ := l.View("c")
		return l.Snapshot(), view
	}
	snap1, view1 := run()
	snap2, view2 := run()
	logRecords(t, "第一次运行的压实日志", snap1)
	logView(t, "第一次运行的消费者视图", view1)
	if !reflect.DeepEqual(snap1, snap2) || !reflect.DeepEqual(view1, view2) {
		t.Fatal("同一输入序列两次运行输出不一致")
	}
	t.Logf("判定依据: 两次运行的压实日志与视图完全一致")
}

// 写入、压实与多消费者读取并发调用必须安全，且新消费者视图与朴素重放一致。
func TestConcurrentAccess(t *testing.T) {
	l := New(4096, 50*time.Second)
	var mu sync.Mutex
	var history []Record
	var tick int64         // 共享逻辑时钟, 保证并发写入时间戳单调不回退
	var writeMu sync.Mutex // 把取时间戳+追加串行化, 保证时间戳与追加顺序一致
	now := func() int {
		mu.Lock()
		defer mu.Unlock()
		return int(tick)
	}

	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				writeMu.Lock()
				mu.Lock()
				tick++
				sec := int(tick)
				mu.Unlock()
				key := fmt.Sprintf("key-%d", sec%17)
				tomb := sec%29 == 28
				seq, err := l.Append(key, fmt.Sprintf("w%d-v%d", w, i), tomb, at(sec))
				writeMu.Unlock()
				if err != nil {
					t.Errorf("Append: %v", err)
					return
				}
				mu.Lock()
				history = append(history, Record{Seq: seq, Key: key, Value: fmt.Sprintf("w%d-v%d", w, i), Tombstone: tomb})
				mu.Unlock()
			}
		}(w)
	}
	for c := 0; c < 3; c++ {
		name := fmt.Sprintf("reader-%d", c)
		if err := l.Subscribe(name); err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			for {
				if _, _, err := l.ReadNext(name); err != nil {
					t.Errorf("ReadNext(%s): %v", name, err)
					return
				}
				// 追平后 ReadNext 返回 ok=false, 但写入仍在进行, 继续读到结束信号。
				mu.Lock()
				done := len(history) == 800
				mu.Unlock()
				if done {
					return
				}
			}
		}(name)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 8; i++ {
			if err := l.Compact(at(now())); err != nil {
				t.Errorf("Compact: %v", err)
				return
			}
		}
	}()
	wg.Wait()

	// 最终压实(保留期内墓碑保留)后, 新消费者视图必须与朴素重放一致。
	if err := l.Compact(at(now())); err != nil {
		t.Fatal(err)
	}
	logRecords(t, "并发写入+压实后的日志", l.Snapshot())
	if err := l.Subscribe("final"); err != nil {
		t.Fatal(err)
	}
	drain(t, l, "final")
	view, _ := l.View("final")
	logView(t, "新消费者 final 的视图", view)

	mu.Lock()
	want := naiveReplay(history)
	mu.Unlock()
	logView(t, "朴素重放参考视图", want)
	if !reflect.DeepEqual(view, want) {
		t.Fatalf("新消费者视图与朴素重放不一致:\n got=%v\nwant=%v", view, want)
	}
	t.Logf("判定依据: 压实仅删除被覆盖记录与过期墓碑, 不改变最终视图")
}
