package lwwview

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func strptr(s string) *string { return &s }

func describeValue(value *string) string {
	if value == nil {
		return "DELETE"
	}
	if *value == "" {
		return "WRITE<empty>"
	}
	return "WRITE<" + *value + ">"
}

func logEvents(t *testing.T, tag string, events []Event) {
	t.Helper()
	t.Logf("==== %s ====", tag)
	for i, event := range events {
		t.Logf("事件[%d] key=%q eventTime=%d op=%s",
			i, event.Key, event.EventTime, describeValue(event.Value))
	}
}

func logState(t *testing.T, v *View, basis string) {
	t.Helper()
	t.Logf("-- 键值（判定依据: %s; dropped=%d; tracked=%d; existing=%d） --",
		basis, v.Dropped(), v.KeyCount(), v.ExistingKeyCount())
	for _, kv := range v.Entries() {
		t.Logf("  key=%q eventTime=%d exists=%v value=%q",
			kv.Key, kv.EventTime, kv.Exists, kv.Value)
	}
	for _, change := range v.Changelog() {
		if change.Kind == KindRetract {
			t.Logf("变更日志 key=%q kind=RETRACT eventTime=%d old(exists=%v,value=%q)",
				change.Key, change.EventTime, change.OldExists, change.OldValue)
			continue
		}
		t.Logf("变更日志 key=%q kind=UPSERT eventTime=%d new(exists=%v,value=%q)",
			change.Key, change.EventTime, change.NewExists, change.NewValue)
	}
}

func mustApply(t *testing.T, v *View, events []Event, wantApplied, wantDropped int) {
	t.Helper()
	applied, dropped, err := v.Apply(events)
	if err != nil {
		t.Fatalf("Apply 意外失败: %v", err)
	}
	if applied != wantApplied || dropped != wantDropped {
		t.Fatalf("Apply 计数不符: got applied=%d dropped=%d, want applied=%d dropped=%d",
			applied, dropped, wantApplied, wantDropped)
	}
}

// 乱序到达：结果只取决于事件时间，而不是到达顺序。
func TestOutOfOrderArbitration(t *testing.T) {
	v := New(8)

	batch1 := []Event{
		{Key: "k1", EventTime: 10, Value: strptr("a")},
		{Key: "k1", EventTime: 5, Value: strptr("late")},
		{Key: "k1", EventTime: 20, Value: strptr("b")},
	}
	logEvents(t, "批次1 乱序写入", batch1)
	mustApply(t, v, batch1, 2, 1)
	logState(t, v, "事件时间 20 最大；eventTime=5 迟到忽略")

	if entry, ok := v.Lookup("k1"); !ok || entry.Value != "b" || entry.EventTime != 20 {
		t.Fatalf("k1 应为 (b@20)，got ok=%v %+v", ok, entry)
	}

	batch2 := []Event{{Key: "k1", EventTime: 15, Value: nil}}
	logEvents(t, "批次2 迟到删除", batch2)
	mustApply(t, v, batch2, 0, 1)
	logState(t, v, "删除 eventTime=15 小于当前 20，迟到忽略，键仍存在")

	if _, ok := v.Lookup("k1"); !ok {
		t.Fatalf("迟到删除后 k1 必须仍存在")
	}
	if v.Dropped() != 2 {
		t.Fatalf("dropped 应为 2，got %d", v.Dropped())
	}

	batch3 := []Event{{Key: "k1", EventTime: 30, Value: nil}}
	logEvents(t, "批次3 生效删除", batch3)
	mustApply(t, v, batch3, 1, 0)
	logState(t, v, "删除 eventTime=30 最大：先 RETRACT b@20，再 UPSERT 墓碑")

	if _, ok := v.Lookup("k1"); ok {
		t.Fatalf("k1 在 eventTime=30 删除后必须不存在")
	}
	if err := v.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck 失败: %v", err)
	}
}

// 事件时间相等：先到者胜，后续同事件时间事件（含删除）一律忽略。
func TestEqualEventTimeFirstWins(t *testing.T) {
	v := New(8)

	batch := []Event{
		{Key: "k", EventTime: 10, Value: strptr("first")},
		{Key: "k", EventTime: 10, Value: strptr("second")},
		{Key: "k", EventTime: 10, Value: nil},
	}
	logEvents(t, "同事件时间仲裁", batch)
	mustApply(t, v, batch, 1, 2)
	logState(t, v, "eventTime 相等先到者 first 胜出；后两条全部迟到")

	if entry, ok := v.Lookup("k"); !ok || entry.Value != "first" {
		t.Fatalf("同事件时间必须先到者胜，got ok=%v %+v", ok, entry)
	}

	next := []Event{{Key: "k", EventTime: 10, Value: strptr("cross-batch")}}
	logEvents(t, "跨批次同事件时间", next)
	mustApply(t, v, next, 0, 1)
	logState(t, v, "跨批次同事件时间仍然迟到，值与事件时间不变")

	if entry, ok := v.Lookup("k"); !ok || entry.Value != "first" {
		t.Fatalf("跨批次同事件时间不得改变值，got %+v", entry)
	}
	if err := v.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck 失败: %v", err)
	}
}

// 删除先到的同事件时间场景：删除胜出后，同事件时间写入不能复活键。
func TestDeleteWinsSameEventTime(t *testing.T) {
	v := New(8)

	mustApply(t, v, []Event{{Key: "k", EventTime: 1, Value: strptr("v")}}, 1, 0)

	batch := []Event{
		{Key: "k", EventTime: 10, Value: nil},
		{Key: "k", EventTime: 10, Value: strptr("after-delete")},
	}
	logEvents(t, "删除与写入同事件时间（删除先到）", batch)
	mustApply(t, v, batch, 1, 1)
	logState(t, v, "删除@10 先到生效；写入@10 迟到，键保持不存在")

	if _, ok := v.Lookup("k"); ok {
		t.Fatalf("删除先到且同事件时间，键必须不存在")
	}

	revive := []Event{{Key: "k", EventTime: 11, Value: strptr("")}}
	logEvents(t, "更大事件时间的空值写入", revive)
	mustApply(t, v, revive, 1, 0)
	logState(t, v, "空值写入@11 生效：键存在但值为空，与不存在可区分")

	entry, ok := v.Lookup("k")
	if !ok || !entry.Exists || entry.Value != "" || entry.EventTime != 11 {
		t.Fatalf("空值写入必须可查询为存在且值为空，got ok=%v %+v", ok, entry)
	}
	if err := v.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck 失败: %v", err)
	}
}

// 空值存在与不存在是两种不同状态。
func TestEmptyValueVersusAbsent(t *testing.T) {
	v := New(8)

	empty := []Event{{Key: "e", EventTime: 1, Value: strptr("")}}
	logEvents(t, "空值写入", empty)
	mustApply(t, v, empty, 1, 0)
	logState(t, v, "e 存在且值为空")

	emptyEntry, emptyOK := v.Lookup("e")
	absentEntry, absentOK := v.Lookup("missing")
	t.Logf("查询空值键 e: ok=%v entry=%+v", emptyOK, emptyEntry)
	t.Logf("查询从未出现键 missing: ok=%v entry=%+v", absentOK, absentEntry)

	if !emptyOK || emptyEntry.Value != "" || !emptyEntry.Exists {
		t.Fatalf("空值键必须 ok=true 且 exists=true")
	}
	if absentOK {
		t.Fatalf("未出现键必须 ok=false")
	}

	deleted := []Event{{Key: "e", EventTime: 2, Value: nil}}
	logEvents(t, "删除空值键", deleted)
	mustApply(t, v, deleted, 1, 0)
	logState(t, v, "e 已删除，查询必须与空值存在可区分")

	if _, ok := v.Lookup("e"); ok {
		t.Fatalf("删除后必须不存在，与空值状态区分")
	}
	if err := v.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck 失败: %v", err)
	}
}

// 非法参数、空键、键数超限必须整批拒绝且状态不变，原因可区分。
func TestBatchRejectedAtomically(t *testing.T) {
	v := New(2)
	mustApply(t, v, []Event{{Key: "a", EventTime: 1, Value: strptr("seed")}}, 1, 0)

	cases := []struct {
		name   string
		events []Event
		want   error
	}{
		{"空批次(nil)", nil, ErrInvalidArgument},
		{"空批次", []Event{}, ErrInvalidArgument},
		{"空键", []Event{{Key: "", EventTime: 1, Value: strptr("x")}}, ErrEmptyKey},
		{"键数超限", []Event{{Key: "b", EventTime: 1, Value: strptr("x")},
			{Key: "c", EventTime: 1, Value: strptr("y")}}, ErrTooManyKeys},
		{
			"批次中后段空键：前面的合法事件也不得生效",
			[]Event{
				{Key: "c", EventTime: 1, Value: strptr("x")},
				{Key: "", EventTime: 2, Value: strptr("y")},
			},
			ErrEmptyKey,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logEvents(t, tc.name, tc.events)
			before := v.Snapshot()
			beforeDropped := v.Dropped()

			applied, dropped, err := v.Apply(tc.events)
			if err == nil {
				t.Fatalf("期望拒绝，却成功 applied=%d dropped=%d", applied, dropped)
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("拒绝原因不可区分: %v，期望 errors.Is %v", err, tc.want)
			}
			t.Logf("拒绝原因可区分: err=%v", err)

			if applied != 0 || dropped != 0 {
				t.Fatalf("被拒绝批次计数必须为 0，got applied=%d dropped=%d", applied, dropped)
			}
			after := v.Snapshot()
			if fmt.Sprint(after) != fmt.Sprint(before) || v.Dropped() != beforeDropped {
				t.Fatalf("拒绝后状态发生变化: before=%v/%d after=%v/%d",
					before, beforeDropped, after, v.Dropped())
			}
			logState(t, v, "整批拒绝，状态不变")
		})
	}
}

// 超限判定发生在模拟阶段：合法部分不得先提交。
func TestTooManyKeysMidBatchNoSideEffects(t *testing.T) {
	v := New(2)
	batch := []Event{
		{Key: "a", EventTime: 1, Value: strptr("1")},
		{Key: "b", EventTime: 1, Value: strptr("2")},
		{Key: "c", EventTime: 1, Value: strptr("3")},
	}
	logEvents(t, "批量键数超限", batch)

	if _, _, err := v.Apply(batch); !errors.Is(err, ErrTooManyKeys) {
		t.Fatalf("期望 ErrTooManyKeys，got %v", err)
	}
	if v.KeyCount() != 0 {
		t.Fatalf("整批不得生效，tracked=%d", v.KeyCount())
	}

	mustApply(t, v, batch[:2], 2, 0)
	logState(t, v, "拒绝后容量内批次可复现成功")
}

// 并发：查询/丢弃数/自检可并发调用；两个读者逐字段一致，迟到事件不改值。
func TestConcurrentReadersConsistent(t *testing.T) {
	v := New(128)
	initial := make([]Event, 0, 100)
	for i := 0; i < 100; i++ {
		initial = append(initial, Event{
			Key:       fmt.Sprintf("k%03d", i),
			EventTime: 100,
			Value:     strptr(fmt.Sprintf("v%03d", i)),
		})
	}
	mustApply(t, v, initial, 100, 0)
	baseline := v.Snapshot()

	stop := make(chan struct{})
	var readersWg, writerWg sync.WaitGroup

	// 单一写者只写迟到事件（eventTime 更小），任何值与事件时间都不得改变。
	writerWg.Add(1)
	go func() {
		defer writerWg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			key := fmt.Sprintf("k%03d", i%100)
			_, _, _ = v.Apply([]Event{{Key: key, EventTime: 1, Value: strptr("late")}})
		}
	}()

	observe := func() error {
		snapshot, dropped := v.SnapshotWithDropped()
		if err := v.SelfCheck(); err != nil {
			return err
		}
		if len(snapshot) != len(baseline) {
			return fmt.Errorf("快照键数漂移: %d vs baseline %d", len(snapshot), len(baseline))
		}
		for key, want := range baseline {
			got, ok := snapshot[key]
			if !ok || got != want {
				return fmt.Errorf("迟到事件改变了 key=%q: got=%+v ok=%v want=%+v", key, got, ok, want)
			}
		}
		if got := v.Dropped(); got < dropped {
			return fmt.Errorf("dropped 计数在两次读取间倒退: %d < %d", got, dropped)
		}
		return nil
	}

	// 两个读者在同一锁语义下并发读取，逐字段必须一致且永不观察到脏状态。
	for reader := 0; reader < 2; reader++ {
		readersWg.Add(1)
		go func(id int) {
			defer readersWg.Done()
			for i := 0; i < 2000; i++ {
				leftSnapshot, leftDropped := v.SnapshotWithDropped()
				rightSnapshot, rightDropped := v.SnapshotWithDropped()
				// 写者只产出迟到事件：值不允许变；dropped 单调不减。
				if leftDropped > rightDropped {
					t.Errorf("reader%d dropped 倒退: %d > %d", id, leftDropped, rightDropped)
					return
				}
				if fmt.Sprint(leftSnapshot) != fmt.Sprint(rightSnapshot) {
					t.Errorf("reader%d 两次并发快照逐字段不一致", id)
					return
				}
				if err := observe(); err != nil {
					t.Errorf("reader%d: %v", id, err)
					return
				}
			}
		}(reader)
	}

	readersWg.Wait()
	close(stop)
	writerWg.Wait()

	t.Logf("并发核对完成：baseline 键数=%d, dropped=%d", len(baseline), v.Dropped())
	for _, kv := range v.Entries() {
		if kv.EventTime != 100 || !strings.HasPrefix(kv.Value, "v") {
			t.Fatalf("迟到事件改变了物化记录: %+v", kv)
		}
	}
}

// 按事件时间独立重算（oracle）与视图结果必须逐项一致，且不依赖到达顺序。
func TestVerifyFreshBatchByEventTime(t *testing.T) {
	events := []Event{
		{Key: "a", EventTime: 50, Value: strptr("a50")},
		{Key: "b", EventTime: 30, Value: nil},
		{Key: "a", EventTime: 20, Value: strptr("a20-late")},
		{Key: "b", EventTime: 40, Value: strptr("b40")},
		{Key: "a", EventTime: 50, Value: strptr("a50-tie-ignored")},
		{Key: "c", EventTime: 10, Value: strptr("")},
		{Key: "b", EventTime: 40, Value: nil},
	}
	logEvents(t, "按事件时间核对批次", events)

	view, expected, err := VerifyFreshBatch(16, events)
	if err != nil {
		t.Fatalf("VerifyFreshBatch 失败: %v", err)
	}
	for key, winner := range expected {
		t.Logf("oracle key=%q winnerIndex=%d entry=%+v", key, winner.EventIndex, winner.Entry)
	}
	logState(t, view, "严格按事件时间取最大；相等取先到；墓碑仍保留事件时间")

	if entry, ok := view.Lookup("a"); !ok || entry.Value != "a50" {
		t.Fatalf("a 应为 a50，got %+v", entry)
	}
	if entry, ok := view.Lookup("b"); !ok || entry.Value != "b40" {
		t.Fatalf("b 的删除@40 与写入@40 相等，写入先到；删除必须迟到，got %+v", entry)
	}
	if entry, ok := view.Lookup("c"); !ok || !entry.Exists || entry.Value != "" {
		t.Fatalf("c 应为空值存在，got ok=%v %+v", ok, entry)
	}

	// 同一批事件以保持到达顺序的不同分块喂入，最终键值与事件时间必须一致（可复现）。
	shuffled := New(16)
	mustApply(t, shuffled, events[:5], 3, 2)
	mustApply(t, shuffled, events[5:], 1, 1)
	if fmt.Sprint(shuffled.Snapshot()) != fmt.Sprint(view.Snapshot()) {
		t.Fatalf("分批喂入结果不可复现:\n%s\n%s",
			fmt.Sprint(shuffled.Snapshot()), fmt.Sprint(view.Snapshot()))
	}
}
