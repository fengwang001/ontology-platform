package compactlog

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
)

func mustWrite(t *testing.T, l *Log, key, value string, now int64) uint64 {
	t.Helper()
	var v []byte
	if value != "" {
		v = []byte(value)
	}
	seq, err := l.Write(key, v, now)
	if err != nil {
		t.Fatalf("write key=%q time=%d rejected unexpectedly: %v", key, now, err)
	}
	t.Logf("输入: write key=%q value=%q time=%d -> seq=%d", key, value, now, seq)
	return seq
}

func logSnapshot(t *testing.T, l *Log, label string) {
	t.Helper()
	var b strings.Builder
	for _, r := range l.Snapshot() {
		val := "TOMBSTONE"
		if !r.Tombstone() {
			val = fmt.Sprintf("%q", r.Value)
		}
		fmt.Fprintf(&b, " {seq=%d key=%q value=%s time=%d}", r.Seq, r.Key, val, r.Time)
	}
	t.Logf("%s:%s", label, b.String())
}

func drain(t *testing.T, l *Log, name string) map[string][]byte {
	t.Helper()
	for {
		rec, ok, err := l.ReadNext(name)
		if err != nil {
			t.Fatalf("consumer %q read failed: %v", name, err)
		}
		if !ok {
			break
		}
		t.Logf("消费者 %q 应用: seq=%d key=%q tombstone=%v", name, rec.Seq, rec.Key, rec.Tombstone())
	}
	view, err := l.View(name)
	if err != nil {
		t.Fatalf("consumer %q view failed: %v", name, err)
	}
	keys := make([]string, 0, len(view))
	for k := range view {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, " %s=%q", k, view[k])
	}
	t.Logf("消费者 %q 视图:%s", name, b.String())
	return view
}

// 墓碑恰好达到保留期时必须保留，超过保留期才清除。
func TestTombstoneExactlyAtRetention(t *testing.T) {
	const retention = 10
	l := NewLog(16, retention)

	mustWrite(t, l, "a", "1", 0)
	mustWrite(t, l, "a", "", 5) // 墓碑写入于 t=5
	mustWrite(t, l, "b", "2", 5)
	mustWrite(t, l, "b", "", 6) // 墓碑写入于 t=6

	l.Compact(15) // a 墓碑年龄 10（恰好达到），b 墓碑年龄 9
	logSnapshot(t, l, "压实后日志(t=15)")
	snap := l.Snapshot()
	if len(snap) != 2 || snap[0].Key != "a" || !snap[0].Tombstone() || snap[1].Key != "b" || !snap[1].Tombstone() {
		t.Fatalf("判定依据: a 墓碑年龄 15-5=10 恰好等于保留期未超过，b 墓碑年龄 15-6=9，两者都必须保留; got %+v", snap)
	}
	t.Logf("判定依据: 年龄恰好等于保留期 %d 不算超过，a、b 墓碑均保留", retention)

	l.Compact(16) // a 墓碑年龄 11（超过），b 墓碑年龄 10（恰好达到）
	logSnapshot(t, l, "压实后日志(t=16)")
	snap = l.Snapshot()
	if len(snap) != 1 || snap[0].Key != "b" || !snap[0].Tombstone() {
		t.Fatalf("判定依据: a 墓碑年龄 16-5=11 超过保留期被清除，b 墓碑年龄 16-6=10 恰好达到应保留; got %+v", snap)
	}
	t.Logf("判定依据: a 墓碑年龄 11 > 10 被清除；b 墓碑年龄 10 恰好等于保留期，保留")

	l.Compact(17) // b 墓碑年龄 11（超过）
	logSnapshot(t, l, "压实后日志(t=17)")
	if snap = l.Snapshot(); len(snap) != 0 {
		t.Fatalf("判定依据: b 墓碑年龄 17-6=11 超过保留期，日志应为空; got %+v", snap)
	}
	t.Logf("判定依据: b 墓碑年龄 11 > 10 被清除，日志清空")
}

// 压实只保留每个键序号最大的记录，剩余记录保持升序且只留空洞、不重编号。
func TestCompactionOrder(t *testing.T) {
	l := NewLog(32, 1000)

	mustWrite(t, l, "x", "1", 1) // seq=1，被 seq=4 覆盖
	mustWrite(t, l, "y", "1", 2) // seq=2，被 seq=5 覆盖
	mustWrite(t, l, "z", "1", 3) // seq=3，保留
	mustWrite(t, l, "x", "2", 4) // seq=4，保留
	mustWrite(t, l, "y", "2", 5) // seq=5，保留
	logSnapshot(t, l, "压实前日志")

	l.Compact(6)
	logSnapshot(t, l, "压实后日志")

	snap := l.Snapshot()
	wantKeys := []string{"z", "x", "y"}
	wantSeqs := []uint64{3, 4, 5}
	if len(snap) != len(wantKeys) {
		t.Fatalf("判定依据: 每键只留最新记录，应剩 3 条; got %+v", snap)
	}
	for i, r := range snap {
		if r.Key != wantKeys[i] || r.Seq != wantSeqs[i] {
			t.Fatalf("判定依据: 压实后第 %d 条应为 seq=%d key=%q（升序、留空洞、不重编号）; got seq=%d key=%q",
				i, wantSeqs[i], wantKeys[i], r.Seq, r.Key)
		}
	}
	t.Logf("判定依据: seq 1、2 被同键的 seq 4、5 覆盖而清除；seq 3、4、5 保留且升序，序号不重排")

	// 压实后新写入继续使用递增序号，不复用空洞。
	if seq := mustWrite(t, l, "w", "1", 6); seq != 6 {
		t.Fatalf("判定依据: 序号计数器不因压实回退，下一条应为 6; got %d", seq)
	}
}

// 各类非法输入必须以可区分的原因拒绝，且不改变任何状态。
func TestRejectedOperations(t *testing.T) {
	l := NewLog(2, 100)
	mustWrite(t, l, "k", "v", 10)
	l.Subscribe("c")
	drain(t, l, "c")

	type step struct {
		name   string
		reason Reason
		setup  func()
		run    func() error
	}
	steps := []step{
		{name: "空键", reason: ReasonEmptyKey, run: func() error {
			_, err := l.Write("", []byte("v"), 11)
			return err
		}},
		{name: "时间回退", reason: ReasonClockRegression, run: func() error {
			_, err := l.Write("k2", []byte("v"), 9) // 上次成功写入时间为 10
			return err
		}},
		{name: "日志已满", reason: ReasonLogFull, setup: func() {
			if _, err := l.Write("filler", []byte("v"), 11); err != nil {
				t.Fatalf("填充写入应成功: %v", err)
			}
		}, run: func() error {
			_, err := l.Write("overflow", []byte("v"), 12) // 容量 2 已满
			return err
		}},
		{name: "未订阅消费者读取", reason: ReasonConsumerNotSubscribed, run: func() error {
			_, _, err := l.ReadNext("ghost")
			return err
		}},
		{name: "未订阅消费者视图", reason: ReasonConsumerNotSubscribed, run: func() error {
			_, err := l.View("ghost")
			return err
		}},
	}

	for _, s := range steps {
		if s.setup != nil {
			s.setup()
			drain(t, l, "c") // setup 产生的新记录先让消费者追平
		}
		beforeSeq, beforeClock := l.NextSeq(), l.Clock()
		beforeSnap := l.Snapshot()
		beforeView, _ := l.View("c")

		err := s.run()
		var opErr *Error
		if err == nil || !errors.As(err, &opErr) || opErr.Reason != s.reason {
			t.Fatalf("%s: 期望拒绝原因 %v; got err=%v", s.name, s.reason, err)
		}
		t.Logf("输入: %s -> 拒绝原因 %q", s.name, opErr.Reason)

		if l.NextSeq() != beforeSeq || l.Clock() != beforeClock {
			t.Fatalf("%s: 被拒绝的操作不得改变时钟或序号计数器", s.name)
		}
		if !reflect.DeepEqual(l.Snapshot(), beforeSnap) {
			t.Fatalf("%s: 被拒绝的操作不得改变日志", s.name)
		}
		afterView, _ := l.View("c")
		if !reflect.DeepEqual(afterView, beforeView) {
			t.Fatalf("%s: 被拒绝的操作不得改变消费者视图", s.name)
		}
		if _, ok, rerr := l.ReadNext("c"); rerr != nil || ok {
			t.Fatalf("%s: 被拒绝的操作不得改变消费者位置", s.name)
		}
		t.Logf("判定依据: %s 被拒绝后时钟=%d 下一序号=%d 日志条数=%d 均未变化",
			s.name, l.Clock(), l.NextSeq(), len(l.Snapshot()))
	}
}

// 新消费者从压实后的日志读到的视图，必须与朴素重放存活记录一致。
func TestConsumerViewMatchesNaiveReplay(t *testing.T) {
	l := NewLog(64, 10)
	mustWrite(t, l, "a", "1", 0)
	mustWrite(t, l, "b", "1", 1)
	mustWrite(t, l, "a", "2", 2)
	mustWrite(t, l, "b", "", 3) // 墓碑，保留期内
	mustWrite(t, l, "c", "1", 4)
	l.Compact(5)
	logSnapshot(t, l, "压实后日志")

	l.Subscribe("late")
	got := drain(t, l, "late")

	// 朴素重放：按序号升序应用存活记录。
	want := map[string][]byte{}
	for _, r := range l.Snapshot() {
		if r.Tombstone() {
			delete(want, r.Key)
		} else {
			want[r.Key] = r.Value
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("判定依据: 消费者视图应等于朴素重放结果 %v; got %v", want, got)
	}
	if _, ok := got["b"]; ok {
		t.Fatalf("判定依据: b 的最新记录是墓碑，视图中不得存在 b")
	}
	t.Logf("判定依据: 消费者按序号顺序应用存活记录（含墓碑删除），视图 %v 与朴素重放一致", want)
}

// 写入、压实与多个消费者的读取并发执行是安全的。
func TestConcurrentAccess(t *testing.T) {
	l := NewLog(4096, 50)
	l.Subscribe("c1")
	l.Subscribe("c2")

	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				key := fmt.Sprintf("key-%d", (id*200+i)%64)
				var v []byte
				if i%3 != 0 {
					v = []byte(fmt.Sprintf("val-%d", i))
				}
				_, _ = l.Write(key, v, int64(i)) // 写满后被拒绝也允许
			}
		}(w)
	}
	for _, name := range []string{"c1", "c2"} {
		wg.Add(1)
		go func(n string) {
			defer wg.Done()
			for i := 0; i < 400; i++ {
				_, _, _ = l.ReadNext(n)
			}
		}(name)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			l.Compact(int64(i))
		}
	}()
	wg.Wait()

	if _, err := l.View("c1"); err != nil {
		t.Fatalf("c1 视图读取失败: %v", err)
	}
	t.Logf("判定依据: 并发写入/压实/读取在 -race 下无数据竞争即通过")
}

// 同一输入序列反复计算必须得到完全相同的输出。
func TestDeterministic(t *testing.T) {
	run := func() ([]Record, map[string][]byte) {
		l := NewLog(64, 10)
		inputs := []struct {
			key, val string
			now      int64
		}{
			{"a", "1", 0}, {"b", "1", 1}, {"a", "2", 2},
			{"b", "", 3}, {"c", "1", 4}, {"a", "", 5},
		}
		for _, in := range inputs {
			var v []byte
			if in.val != "" {
				v = []byte(in.val)
			}
			if _, err := l.Write(in.key, v, in.now); err != nil {
				t.Fatalf("write: %v", err)
			}
		}
		l.Compact(6)
		l.Subscribe("c")
		for {
			_, ok, err := l.ReadNext("c")
			if err != nil || !ok {
				break
			}
		}
		view, _ := l.View("c")
		return l.Snapshot(), view
	}

	snap1, view1 := run()
	for i := 0; i < 5; i++ {
		snapN, viewN := run()
		if !reflect.DeepEqual(snap1, snapN) || !reflect.DeepEqual(view1, viewN) {
			t.Fatalf("判定依据: 同一输入序列第 %d 次重放输出不同: %+v/%v vs %+v/%v",
				i, snap1, view1, snapN, viewN)
		}
	}
	t.Logf("判定依据: 同一输入序列重放 6 次，压实后日志与消费者视图完全一致")
}
