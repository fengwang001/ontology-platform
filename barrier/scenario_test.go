package barrier

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func rec(key, value string) Event {
	return Event{Record: Record{Key: key, Value: value}}
}

func recOn(ch int, key, value string) Event {
	return Event{Channel: ch, Record: Record{Key: key, Value: value}}
}

func bar(ch, no int) Event {
	return Event{Channel: ch, BarrierNo: no}
}

func describeOutputs(out []Output) string {
	parts := make([]string, len(out))
	for i, o := range out {
		if o.Kind == "barrier" {
			parts[i] = fmt.Sprintf("barrier(ch=%d,no=%d)", o.Channel, o.BarrierNo)
		} else {
			parts[i] = fmt.Sprintf("record(ch=%d,%s=%s)", o.Channel, o.Record.Key, o.Record.Value)
		}
	}
	return strings.Join(parts, ", ")
}

// TestOneSideBlockedAndBuffers 覆盖“一侧先收到屏障后持续缓冲”：
// 通道 0 在屏障后阻塞，其记录全部缓冲且不产生下游输出；
// 通道 1 未阻塞，记录到达即处理。
func TestOneSideBlockedAndBuffers(t *testing.T) {
	a := New(4)

	apply := func(events []Event, why string) {
		t.Helper()
		out, err := a.Apply(events)
		t.Logf("输入=%s | 判定=%s | 输出=[%s] | err=%v",
			describeOutputs(toOutputs(events)), why, describeOutputs(out), err)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	apply([]Event{recOn(1, "k1", "a"), recOn(0, "k0", "x"), bar(0, 1)},
		"通道0收到屏障1进入阻塞，之前的记录直接处理，通道1记录到达即处理")

	apply([]Event{recOn(0, "k0", "y"), recOn(0, "k2", "b")},
		"通道0已阻塞：记录进入缓冲，不产生任何输出")

	if out, _ := a.Apply([]Event{recOn(1, "k1", "c")}); len(out) != 1 {
		t.Fatalf("未阻塞通道的记录应到达即处理，got %d outputs", len(out))
	}
	t.Logf("判定依据：阻塞通道缓冲记录数=%d；已完成快照=%v",
		a.state.channels[0].buffered, a.Snapshots())
	if a.state.channels[0].buffered != 2 {
		t.Fatalf("通道0应缓冲2条记录，got %d", a.state.channels[0].buffered)
	}
	if nums := a.Snapshots(); len(nums) != 0 {
		t.Fatalf("对齐前不应存在快照，got %v", nums)
	}
}

// TestAlignmentSnapshotAndReplay 覆盖对齐时快照内容与按到达顺序重放。
func TestAlignmentSnapshotAndReplay(t *testing.T) {
	a := New(8)

	events := []Event{
		recOn(0, "a", "0"),
		bar(0, 1),
		recOn(0, "a", "1"), // 缓冲
		recOn(1, "b", "0"), // 未阻塞，立即处理
		recOn(0, "c", "0"), // 缓冲（到达早于 b1 之后的事件）
		recOn(1, "b", "1"),
		bar(1, 1), // 对齐
	}
	out, err := a.Apply(events)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	t.Logf("输入=%s", describeOutputs(toOutputs(events)))
	t.Logf("输出=%s", describeOutputs(out))
	t.Logf("判定依据：两个屏障必须先于缓冲记录转发；快照只含屏障之前的记录")

	snap, ok := a.SnapshotAt(1)
	if !ok {
		t.Fatal("对齐后应存在编号1的快照")
	}
	wantSnap := []Record{{Key: "a", Value: "0"}, {Key: "b", Value: "0"}, {Key: "b", Value: "1"}}
	if fmt.Sprint(snap.Records) != fmt.Sprint(wantSnap) {
		t.Fatalf("快照内容错误：got %v want %v", snap.Records, wantSnap)
	}
	t.Logf("快照1=%v（仅含两侧屏障之前的全部记录，按Key排序）", snap.Records)

	wantOut := []Output{
		{Kind: "record", Channel: 0, Record: Record{Key: "a", Value: "0"}},
		{Kind: "record", Channel: 1, Record: Record{Key: "b", Value: "0"}},
		{Kind: "record", Channel: 1, Record: Record{Key: "b", Value: "1"}},
		{Kind: "barrier", Channel: 0, BarrierNo: 1},
		{Kind: "barrier", Channel: 1, BarrierNo: 1},
		{Kind: "record", Channel: 0, Record: Record{Key: "a", Value: "1"}},
		{Kind: "record", Channel: 0, Record: Record{Key: "c", Value: "0"}},
	}
	if fmt.Sprint(out) != fmt.Sprint(wantOut) {
		t.Fatalf("对齐输出/重放顺序错误：\n got %v\nwant %v", out, wantOut)
	}

	// 快照不可变：外部修改取回的快照不影响内部状态。
	snap.Records[0].Value = "tampered"
	again, _ := a.SnapshotAt(1)
	if again.Records[0].Value != "0" {
		t.Fatal("快照必须是深拷贝，读取方修改不得污染内部状态")
	}
}

// TestReplayHitsNextBarrier 覆盖重放中遇到下一号屏障时重新阻塞，
// 且后续连续多个检查点可依次对齐。
func TestReplayHitsNextBarrier(t *testing.T) {
	a := New(8)
	events := []Event{
		bar(0, 1),
		recOn(0, "a", "post0"),
		bar(0, 2), // 缓冲：重放到它时通道0重新阻塞
		recOn(0, "a", "post1"),
		recOn(1, "z", "0"),
		bar(1, 1), // 对齐1：重放 a=post0 后在屏障2处再次阻塞
	}
	out, err := a.Apply(events)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	t.Logf("输入=%s", describeOutputs(toOutputs(events)))
	t.Logf("输出=%s", describeOutputs(out))

	s1, _ := a.SnapshotAt(1)
	if fmt.Sprint(s1.Records) != fmt.Sprint([]Record{{Key: "z", Value: "0"}}) {
		t.Fatalf("快照1不应包含屏障后的记录，got %v", s1.Records)
	}
	if !a.state.channels[0].blocked || a.state.channels[1].blocked {
		t.Fatal("重放遇到屏障2后通道0应重新阻塞，通道1应已解除阻塞")
	}
	if a.state.channels[0].buffered != 1 {
		t.Fatalf("a=post1 应在通道0继续缓冲，got %d", a.state.channels[0].buffered)
	}

	out2, err := a.Apply([]Event{recOn(1, "z", "1"), bar(1, 2)})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	t.Logf("第二次对齐输出=%s", describeOutputs(out2))
	s2, ok := a.SnapshotAt(2)
	if !ok {
		t.Fatal("应对齐出编号2的快照")
	}
	wantSnap2 := []Record{
		{Key: "a", Value: "post0"},
		{Key: "z", Value: "0"},
		{Key: "z", Value: "1"},
	}
	if fmt.Sprint(s2.Records) != fmt.Sprint(wantSnap2) {
		t.Fatalf("快照2内容错误：got %v want %v", s2.Records, wantSnap2)
	}
	t.Logf("判定依据：快照2=%v；a=post1在屏障2之后到达，属于检查点2之后", s2.Records)
}

// TestInvalidInputs 覆盖各类非法输入及可区分的错误原因。
func TestInvalidInputs(t *testing.T) {
	cases := []struct {
		name   string
		events []Event
		want   error
	}{
		{"非法通道号", []Event{{Channel: 7, BarrierNo: 1}}, ErrInvalidChannel},
		{"空键", []Event{recOn(0, "", "v")}, ErrEmptyKey},
		{"屏障编号跳号", []Event{bar(0, 2)}, ErrInvalidBarrierNo},
		{"屏障编号重复", []Event{bar(0, 1), bar(0, 1)}, ErrInvalidBarrierNo},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := New(4)
			out, err := a.Apply(tc.events)
			t.Logf("输入=%s | 判定=拒绝(%s) | 输出片段=[%s] | err=%v",
				describeOutputs(toOutputs(tc.events)), tc.want, describeOutputs(out), err)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
			if out != nil || len(a.TakeOutputs()) != 0 || len(a.Snapshots()) != 0 {
				t.Fatal("被拒绝的批不得产生输出或快照")
			}
		})
	}
}

// TestBufferLimitExceeded 覆盖缓冲超限，且拒绝不改变任何状态。
func TestBufferLimitExceeded(t *testing.T) {
	a := New(2)
	if _, err := a.Apply([]Event{bar(0, 1), recOn(0, "k", "1"), recOn(0, "k", "2")}); err != nil {
		t.Fatal(err)
	}

	goodEvents := []Event{recOn(1, "live", "x")}
	badEvents := []Event{recOn(0, "k", "3"), recOn(0, "k", "4")}
	out, err := a.Apply(append(goodEvents, badEvents...))
	t.Logf("输入=%s | 判定=拒绝(%s) | 输出片段=[%s] | err=%v",
		describeOutputs(toOutputs(append(goodEvents, badEvents...))),
		ErrBufferLimitExceeded, describeOutputs(out), err)
	if !errors.Is(err, ErrBufferLimitExceeded) {
		t.Fatalf("got %v, want %v", err, ErrBufferLimitExceeded)
	}

	// 整批回滚：批中那条合法的未阻塞通道记录也不得生效。
	if got := len(a.TakeOutputs()); got != 0 {
		t.Fatalf("被拒绝批次不得产生输出，got %d", got)
	}
	if a.state.channels[0].buffered != 2 {
		t.Fatalf("缓冲数量应保持为2，got %d", a.state.channels[0].buffered)
	}
	if _, exists := a.state.accum["live"]; exists {
		t.Fatal("被拒绝批次中的合法记录也必须随整批回滚")
	}

	// 未阻塞通道仍可接收数据，证明状态未被污染。
	if _, err := a.Apply([]Event{recOn(1, "k", "3")}); err != nil {
		t.Fatalf("未阻塞通道应可继续提交，got %v", err)
	}
}

func toOutputs(events []Event) []Output {
	out := make([]Output, len(events))
	for i, ev := range events {
		if ev.BarrierNo != 0 {
			out[i] = Output{Kind: "barrier", Channel: ev.Channel, BarrierNo: ev.BarrierNo}
		} else {
			out[i] = Output{Kind: "record", Channel: ev.Channel, Record: ev.Record}
		}
	}
	return out
}
