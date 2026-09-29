package ontology

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func strptr(s string) *string { return &s }

func kindName(k ChangeKind) string {
	switch k {
	case ChangeRetract:
		return "RETRACT"
	case ChangeEstablish:
		return "ESTABLISH"
	default:
		return "TOMBSTONE"
	}
}

// logApply 打印事件、变更日志与每条事件的判定依据，便于可复现排查。
func logApply(t *testing.T, label string, events []Event, changes []Change, err error, basis []string) {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "---- %s ----\n", label)
	b.WriteString("events:\n")
	for i, ev := range events {
		if ev.Value == nil {
			fmt.Fprintf(&b, "  [%d] key=%q time=%d value=<DELETE>\n", i, ev.Key, ev.EventTime)
		} else {
			fmt.Fprintf(&b, "  [%d] key=%q time=%d value=%q\n", i, ev.Key, ev.EventTime, *ev.Value)
		}
	}
	if err != nil {
		fmt.Fprintf(&b, "batch rejected: %v (state unchanged)\n", err)
	} else {
		b.WriteString("changelog:\n")
		for _, ch := range changes {
			switch ch.Kind {
			case ChangeRetract:
				fmt.Fprintf(&b, "  seq=%d %s key=%q old=%q@%d due=%d\n",
					ch.Seq, kindName(ch.Kind), ch.Key, ch.OldValue, ch.OldEventTime, ch.CauseEventTime)
			case ChangeEstablish:
				fmt.Fprintf(&b, "  seq=%d %s key=%q new=%q@%d due=%d\n",
					ch.Seq, kindName(ch.Kind), ch.Key, ch.NewValue, ch.NewEventTime, ch.CauseEventTime)
			default:
				fmt.Fprintf(&b, "  seq=%d %s key=%q absent@%d due=%d\n",
					ch.Seq, kindName(ch.Kind), ch.Key, ch.NewEventTime, ch.CauseEventTime)
			}
		}
	}
	b.WriteString("decision basis:\n")
	for _, line := range basis {
		fmt.Fprintf(&b, "  - %s\n", line)
	}
	t.Log("\n" + b.String())
}

func dumpView(t *testing.T, v *MaterializedView, label string) {
	t.Helper()
	snap := v.View()
	var b strings.Builder
	fmt.Fprintf(&b, "view after %s: live=%d applied=%d dropped=%d changes=%d\n",
		label, snap.KeyCount, snap.AppliedEvents, snap.DroppedEvents, snap.ChangeCount)
	for k, r := range snap.Records {
		fmt.Fprintf(&b, "  key=%q value=%q eventTime=%d\n", k, r.Value, r.EventTime)
	}
	t.Log("\n" + b.String())
	if err := v.SelfCheck(); err != nil {
		t.Fatalf("self-check after %s: %v", label, err)
	}
}

func TestOutOfOrderLatestByEventTime(t *testing.T) {
	v, err := NewMaterializedView(10)
	if err != nil {
		t.Fatal(err)
	}

	batch1 := []Event{
		{Key: "k", EventTime: 10, Value: strptr("v10")},
		{Key: "k", EventTime: 5, Value: strptr("v5-late")},
	}
	changes, err := v.Apply(batch1)
	if err != nil {
		t.Fatal(err)
	}
	logApply(t, "batch1: t10 then late t5", batch1, changes, nil, []string{
		"t=10 > 无记录 => 生效 ESTABLISH v10",
		"t=5 <= 当前事件时间 10 => 迟到忽略，丢弃计数 +1，值与事件时间不变",
	})
	if got := v.DroppedCount(); got != 1 {
		t.Fatalf("dropped = %d, want 1", got)
	}
	r, ok := v.Get("k")
	if !ok || r.Value != "v10" || r.EventTime != 10 {
		t.Fatalf("get = %+v ok=%v, want v10@10", r, ok)
	}

	batch2 := []Event{{Key: "k", EventTime: 12, Value: strptr("v12")}}
	changes, err = v.Apply(batch2)
	if err != nil {
		t.Fatal(err)
	}
	logApply(t, "batch2: t12 supersedes t10", batch2, changes, nil, []string{
		"t=12 > 当前事件时间 10 => 先 RETRACT v10@10，再 ESTABLISH v12@12",
	})
	if len(changes) != 2 || changes[0].Kind != ChangeRetract || changes[1].Kind != ChangeEstablish {
		t.Fatalf("changes = %+v, want retract+establish", changes)
	}
	r, _ = v.Get("k")
	if r.Value != "v12" || r.EventTime != 12 {
		t.Fatalf("get = %+v, want v12@12", r)
	}
	dumpView(t, v, "out-of-order writes")
}

func TestEqualEventTimeArbitration(t *testing.T) {
	v, _ := NewMaterializedView(10)

	batch := []Event{
		{Key: "k", EventTime: 7, Value: strptr("first")},
		{Key: "k", EventTime: 7, Value: strptr("second")},
		{Key: "k", EventTime: 7, Value: nil},
	}
	changes, err := v.Apply(batch)
	if err != nil {
		t.Fatal(err)
	}
	logApply(t, "equal event time: first writer wins", batch, changes, nil, []string{
		"第一条 t=7 生效 ESTABLISH first",
		"第二条 t=7 == 当前 7 => 先到者胜，忽略并计数",
		"删除 t=7 == 当前 7 => 同样忽略，键仍然存在",
	})
	r, ok := v.Get("k")
	if !ok || r.Value != "first" || r.EventTime != 7 {
		t.Fatalf("get = %+v ok=%v, want first@7", r, ok)
	}
	if v.DroppedCount() != 2 {
		t.Fatalf("dropped = %d, want 2", v.DroppedCount())
	}

	if _, err := v.Apply([]Event{{Key: "k", EventTime: 7, Value: strptr("later batch")}}); err != nil {
		t.Fatal(err)
	}
	r, _ = v.Get("k")
	if r.Value != "first" {
		t.Fatalf("value changed to %q by equal-time event", r.Value)
	}
	if v.DroppedCount() != 3 {
		t.Fatalf("dropped = %d, want 3", v.DroppedCount())
	}
	dumpView(t, v, "equal-time arbitration")
}

func TestDeleteAndWriteSameEventTime(t *testing.T) {
	v, _ := NewMaterializedView(10)

	batch := []Event{
		{Key: "k", EventTime: 8, Value: strptr("v8")},
		{Key: "k", EventTime: 9, Value: nil},
		{Key: "k", EventTime: 9, Value: strptr("v9 same time as delete")},
	}
	changes, err := v.Apply(batch)
	if err != nil {
		t.Fatal(err)
	}
	logApply(t, "delete wins at t9; equal-time write ignored", batch, changes, nil, []string{
		"t=8 ESTABLISH v8",
		"t=9 > 8 => RETRACT v8 后 TOMBSTONE（标记不存在，水位=9）",
		"写入 t=9 == 水位 9 => 迟到忽略，键保持不存在",
	})
	if _, ok := v.Get("k"); ok {
		t.Fatal("key should be absent after delete")
	}
	if v.DroppedCount() != 1 {
		t.Fatalf("dropped = %d, want 1", v.DroppedCount())
	}

	late := []Event{{Key: "k", EventTime: 8, Value: strptr("zombie t8")}}
	if _, err := v.Apply(late); err != nil {
		t.Fatal(err)
	}
	logApply(t, "late write t8 cannot revive", late, nil, nil, []string{
		"删除水位停留在 9；t=8 <= 9 => 忽略，键仍不存在",
	})
	if _, ok := v.Get("k"); ok {
		t.Fatal("late write must not revive a deleted key")
	}

	revive := []Event{{Key: "k", EventTime: 10, Value: strptr("v10")}}
	changes, _ = v.Apply(revive)
	logApply(t, "t10 re-establishes", revive, changes, nil, []string{
		"键不存在故无 RETRACT，直接 ESTABLISH v10@10",
	})
	if r, ok := v.Get("k"); !ok || r.Value != "v10" {
		t.Fatalf("get = %+v ok=%v, want v10", r, ok)
	}
	dumpView(t, v, "delete/write arbitration")
}

func TestEmptyValueDistinctFromAbsent(t *testing.T) {
	v, _ := NewMaterializedView(10)

	writeEmpty := []Event{{Key: "k", EventTime: 1, Value: strptr("")}}
	changes, err := v.Apply(writeEmpty)
	if err != nil {
		t.Fatal(err)
	}
	logApply(t, "empty-string write means present", writeEmpty, changes, nil, []string{
		"Value 非 nil（空串）=> 键存在，值为空字符串",
	})
	r, ok := v.Get("k")
	if !ok || !r.Exists || r.Value != "" {
		t.Fatalf("get = %+v ok=%v, want present empty value", r, ok)
	}

	del := []Event{{Key: "k", EventTime: 2, Value: nil}}
	changes, _ = v.Apply(del)
	logApply(t, "delete marks absent", del, changes, nil, []string{
		"Value == nil => RETRACT 空值后 TOMBSTONE，Get 返回 ok=false",
	})
	if _, ok := v.Get("k"); ok {
		t.Fatal("deleted key must be absent even though its last value was empty")
	}
	if snap := v.Snapshot(); len(snap) != 0 {
		t.Fatalf("snapshot of deleted key = %v, want empty", snap)
	}
	dumpView(t, v, "empty vs absent")
}

func TestTombstoneOnNeverSeenKey(t *testing.T) {
	v, _ := NewMaterializedView(10)

	del := []Event{{Key: "ghost", EventTime: 5, Value: nil}}
	changes, err := v.Apply(del)
	if err != nil {
		t.Fatal(err)
	}
	logApply(t, "delete of never-seen key", del, changes, nil, []string{
		"键从未存在 => 无 RETRACT，仅 TOMBSTONE，水位记为 5",
		"随后 t=3 写入 <= 5 将被忽略",
	})
	if len(changes) != 1 || changes[0].Kind != ChangeTombstone {
		t.Fatalf("changes = %+v, want single tombstone", changes)
	}
	late := []Event{{Key: "ghost", EventTime: 3, Value: strptr("too early")}}
	if _, err := v.Apply(late); err != nil {
		t.Fatal(err)
	}
	if _, ok := v.Get("ghost"); ok {
		t.Fatal("write below tombstone watermark must be ignored")
	}
	if v.DroppedCount() != 1 {
		t.Fatalf("dropped = %d, want 1", v.DroppedCount())
	}
	dumpView(t, v, "tombstone watermark")
}

func TestNewMaterializedViewValidation(t *testing.T) {
	for _, n := range []int{0, -1} {
		if _, err := NewMaterializedView(n); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("NewMaterializedView(%d) err = %v, want ErrInvalidArgument", n, err)
		}
	}
}

func TestBatchRejectionIsAtomic(t *testing.T) {
	v, _ := NewMaterializedView(2)

	seed := []Event{{Key: "a", EventTime: 1, Value: strptr("A")}}
	if _, err := v.Apply(seed); err != nil {
		t.Fatal(err)
	}
	before := v.View()
	beforeLog := v.Changes()

	cases := []struct {
		name   string
		events []Event
		want   error
	}{
		{"nil batch", nil, ErrInvalidArgument},
		{"negative event time", []Event{{Key: "a", EventTime: -1, Value: strptr("x")}}, ErrInvalidArgument},
		{"empty key", []Event{{Key: "", EventTime: 2, Value: strptr("x")}}, ErrEmptyKey},
		{
			"too many keys",
			[]Event{
				{Key: "b", EventTime: 1, Value: strptr("B")},
				{Key: "c", EventTime: 1, Value: strptr("C")},
			},
			ErrTooManyKeys,
		},
		{
			"empty key after valid event in same batch",
			[]Event{
				{Key: "b", EventTime: 1, Value: strptr("B")},
				{Key: "", EventTime: 2, Value: strptr("x")},
			},
			ErrEmptyKey,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			changes, err := v.Apply(tc.events)
			logApply(t, tc.name, tc.events, changes, err, []string{
				fmt.Sprintf("整体拒绝，errors.Is(err, %v) 为真", tc.want),
				"拒绝前后 View 逐字段相同，变更日志长度不变",
			})
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want wrapping %v", err, tc.want)
			}
			if changes != nil {
				t.Fatalf("rejected batch returned changes %v, want nil", changes)
			}
			after := v.View()
			if !reflect.DeepEqual(before.Records, after.Records) ||
				before.DroppedEvents != after.DroppedEvents ||
				before.AppliedEvents != after.AppliedEvents ||
				before.ChangeCount != after.ChangeCount ||
				before.KeyCount != after.KeyCount {
				t.Fatalf("state changed after rejection:\nbefore=%+v\nafter =%+v", before, after)
			}
			if got := v.Changes(); !reflect.DeepEqual(beforeLog, got) {
				t.Fatalf("changelog changed after rejection")
			}
		})
	}

	// 空批次是合法的 no-op，不产生任何变更。
	if changes, err := v.Apply([]Event{}); err != nil || len(changes) != 0 {
		t.Fatalf("empty batch: changes=%v err=%v", changes, err)
	}
	dumpView(t, v, "atomic rejections")
}

func TestChangesSinceAndSequence(t *testing.T) {
	v, _ := NewMaterializedView(10)
	_, _ = v.Apply([]Event{
		{Key: "a", EventTime: 1, Value: strptr("A1")},
		{Key: "b", EventTime: 1, Value: strptr("B1")},
	})
	_, _ = v.Apply([]Event{{Key: "a", EventTime: 2, Value: strptr("A2")}})

	all := v.Changes()
	for i, ch := range all {
		if ch.Seq != int64(i+1) {
			t.Fatalf("seq gap at %d: %+v", i, ch)
		}
	}
	if len(all) != 4 {
		t.Fatalf("len(changes) = %d, want 4 (establish a, establish b, retract+establish a)", len(all))
	}
	tail := v.ChangesSince(1)
	if len(tail) != 3 || tail[0].Seq != 2 || tail[2].Seq != 4 {
		t.Fatalf("ChangesSince(1) = %+v, want seq 2,3,4", tail)
	}
	// 拷贝互不影响：修改返回切片不能污染视图内部日志。
	tail[0].Key = "mutated"
	if v.Changes()[1].Key != "b" {
		t.Fatal("ChangesSince must return a defensive copy")
	}
	if len(v.ChangesSince(999)) != 0 {
		t.Fatal("ChangesSince past the end must be empty")
	}
}

// oracleState 是独立于实现的参考模型：按与实现相同的流式到达语义推进，
// 用于交叉核对计数器；最终键值再用 globalArgmax 独立复核。
type oracleState struct {
	watermark map[string]int64
	value     map[string]string
	exists    map[string]bool
	dropped   int64
	applied   int64
}

func newOracle() *oracleState {
	return &oracleState{
		watermark: map[string]int64{},
		value:     map[string]string{},
		exists:    map[string]bool{},
	}
}

// applyBatch 按到达顺序逐条核对：严格大于当前水位才生效，否则丢弃计数 +1。
func (o *oracleState) applyBatch(events []Event) {
	for _, ev := range events {
		if w, seen := o.watermark[ev.Key]; seen && ev.EventTime <= w {
			o.dropped++
			continue
		}
		o.applied++
		o.watermark[ev.Key] = ev.EventTime
		if ev.Value != nil {
			o.exists[ev.Key] = true
			o.value[ev.Key] = *ev.Value
		} else {
			o.exists[ev.Key] = false
			delete(o.value, ev.Key)
		}
	}
}

func (o *oracleState) snapshot() map[string]Record {
	out := map[string]Record{}
	for key := range o.exists {
		if o.exists[key] {
			out[key] = Record{Exists: true, Value: o.value[key], EventTime: o.watermark[key]}
		}
	}
	return out
}

// globalArgmax 用与流式语义无关的“批量按事件时间核对”方法复核最终结果：
// 每个键在全部事件中取事件时间最大者；时间相同取最早出现者（先到者胜）。
func globalArgmax(allBatches [][]Event) map[string]Record {
	type best struct {
		time  int64
		order int
		value *string
	}
	pick := map[string]best{}
	order := 0
	for _, batch := range allBatches {
		for _, ev := range batch {
			cur, ok := pick[ev.Key]
			if !ok || ev.EventTime > cur.time {
				pick[ev.Key] = best{time: ev.EventTime, order: order, value: ev.Value}
			}
			order++
		}
	}
	out := map[string]Record{}
	for key, p := range pick {
		if p.value != nil {
			out[key] = Record{Exists: true, Value: *p.value, EventTime: p.time}
		}
	}
	return out
}

func TestBatchEventTimeOracle(t *testing.T) {
	rng := rand.New(rand.NewSource(20260929))

	verify := func(t *testing.T, v *MaterializedView, o *oracleState, label string) {
		t.Helper()
		snap := v.View()
		want := o.snapshot()
		if !reflect.DeepEqual(snap.Records, want) {
			t.Fatalf("%s: records mismatch\nview=%v\noracle=%v", label, snap.Records, want)
		}
		if snap.DroppedEvents != o.dropped {
			t.Fatalf("%s: dropped view=%d oracle=%d", label, snap.DroppedEvents, o.dropped)
		}
		if snap.AppliedEvents != o.applied {
			t.Fatalf("%s: applied view=%d oracle=%d", label, snap.AppliedEvents, o.applied)
		}
		if err := v.SelfCheck(); err != nil {
			t.Fatalf("%s: self-check: %v", label, err)
		}
		t.Logf("oracle check %s ok: live=%d applied=%d dropped=%d",
			label, len(want), o.applied, o.dropped)
	}

	t.Run("sequential shuffled batches", func(t *testing.T) {
		v, _ := NewMaterializedView(50)
		o := newOracle()
		values := []string{"a", "b", "", "dd"}
		var accepted [][]Event
		rejected := map[string]int{}
		for b := 0; b < 200; b++ {
			n := rng.Intn(8)
			batch := make([]Event, n)
			for i := range batch {
				key := fmt.Sprintf("k%d", rng.Intn(12))
				ev := Event{Key: key, EventTime: rng.Int63n(40)}
				switch rng.Intn(3) {
				case 0:
					ev.Value = nil
				default:
					s := values[rng.Intn(len(values))]
					ev.Value = &s
				}
				batch[i] = ev
			}
			_, err := v.Apply(batch)
			if err != nil {
				switch {
				case errors.Is(err, ErrInvalidArgument):
					rejected["invalid"]++
				case errors.Is(err, ErrEmptyKey):
					rejected["empty"]++
				case errors.Is(err, ErrTooManyKeys):
					rejected["too-many"]++
				default:
					t.Fatalf("batch %d unknown rejection: %v", b, err)
				}
				continue
			}
			accepted = append(accepted, batch)
			o.applyBatch(batch)
			if b%25 == 0 {
				verify(t, v, o, fmt.Sprintf("batch %d", b))
			}
		}
		verify(t, v, o, "final")

		// 批量按事件时间核对：每个键在全部已接受事件中取时间最大、同时间最早者。
		if want := globalArgmax(accepted); !reflect.DeepEqual(v.Snapshot(), want) {
			t.Fatalf("global argmax mismatch:\nstream=%v\nargmax=%v", v.Snapshot(), want)
		}
		t.Logf("rejected batches by reason: %+v", rejected)

		// 可复现：把同样的批次序列喂给新实例，结果必须逐字段相同。
		v2, _ := NewMaterializedView(50)
		o2 := newOracle()
		rng2 := rand.New(rand.NewSource(20260929))
		for b := 0; b < 200; b++ {
			n := rng2.Intn(8)
			batch := make([]Event, n)
			for i := range batch {
				key := fmt.Sprintf("k%d", rng2.Intn(12))
				ev := Event{Key: key, EventTime: rng2.Int63n(40)}
				switch rng2.Intn(3) {
				case 0:
					ev.Value = nil
				default:
					s := values[rng2.Intn(len(values))]
					ev.Value = &s
				}
				batch[i] = ev
			}
			if _, err := v2.Apply(batch); err != nil {
				continue
			}
			o2.applyBatch(batch)
		}
		if !reflect.DeepEqual(v.View(), v2.View()) {
			t.Fatal("same batch sequence produced different views: result is not reproducible")
		}
		if !reflect.DeepEqual(v.Changes(), v2.Changes()) {
			t.Fatal("same batch sequence produced different changelogs")
		}
	})
}

func TestConcurrentReadersAndWriters(t *testing.T) {
	v, _ := NewMaterializedView(40)

	// 预填充，制造撤回/建立混合流量。
	for k := 0; k < 20; k++ {
		s := fmt.Sprintf("init-%d", k)
		_, _ = v.Apply([]Event{{Key: fmt.Sprintf("k%d", k), EventTime: 100, Value: &s}})
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			local := rand.New(rand.NewSource(int64(100 + id)))
			for {
				select {
				case <-stop:
					return
				default:
				}
				key := fmt.Sprintf("k%d", local.Intn(25))
				ev := Event{Key: key, EventTime: 90 + local.Int63n(40)}
				if local.Intn(4) == 0 {
					ev.Value = nil
				} else {
					s := fmt.Sprintf("w%d-%d", id, local.Intn(1000))
					ev.Value = &s
				}
				_, _ = v.Apply([]Event{ev})
			}
		}(w)
	}

	for r := 0; r < 6; r++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			local := rand.New(rand.NewSource(int64(200 + id)))
			for i := 0; i < 3000; i++ {
				snap1 := v.View()
				snap2 := v.View()
				// 写入扰动期间：快照必须内部自洽，计数器只能单调前进。
				if snap1.DroppedEvents > snap2.DroppedEvents {
					panic("dropped count moved backwards under concurrent access")
				}
				if len(snap1.Records) != snap1.KeyCount {
					panic("snapshot record count inconsistent with KeyCount")
				}
				if snap1.ChangeCount != int64(len(v.Changes())) {
					panic("snapshot change count inconsistent with changelog")
				}
				if err := v.SelfCheck(); err != nil {
					panic(fmt.Sprintf("self-check failed concurrently: %v", err))
				}
				_ = v.ChangesSince(local.Int63n(500))
				_, _ = v.Get(fmt.Sprintf("k%d", local.Intn(25)))
				_ = v.Snapshot()
			}
		}(r)
	}

	close(stop)
	wg.Wait()

	// 写者停顿后不存在并发发布：屏障对齐的多个读者必须逐字段读到同一版本，
	// 且 View、DroppedCount、Changes 之间也完全一致。
	for round := 0; round < 50; round++ {
		barrier := make(chan struct{})
		got := make([]ViewSnapshot, 4)
		dropped := make([]int64, 4)
		var rwg sync.WaitGroup
		for i := 0; i < 4; i++ {
			rwg.Add(1)
			go func(idx int) {
				defer rwg.Done()
				<-barrier
				got[idx] = v.View()
				dropped[idx] = v.DroppedCount()
			}(i)
		}
		close(barrier)
		rwg.Wait()
		for i := 1; i < 4; i++ {
			if !reflect.DeepEqual(got[0], got[i]) || dropped[0] != dropped[i] {
				t.Fatalf("quiescent concurrent reads diverged:\n%+v\n%+v", got[0], got[i])
			}
		}
	}

	snap := v.View()
	if err := v.SelfCheck(); err != nil {
		t.Fatalf("final self-check: %v", err)
	}
	if snap.KeyCount > 40 {
		t.Fatalf("key limit violated: %d", snap.KeyCount)
	}
	t.Logf("concurrent run finished: live=%d applied=%d dropped=%d changes=%d",
		snap.KeyCount, snap.AppliedEvents, snap.DroppedEvents, snap.ChangeCount)
}
