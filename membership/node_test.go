package membership

import (
	"bytes"
	"errors"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

func newTestNode(t *testing.T, self string, members []string, lambda, b int, timeout int64) (*Node, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	n, err := New(Config{
		SelfID:         self,
		Members:        members,
		Lambda:         lambda,
		B:              b,
		SuspectTimeout: timeout,
		LogOutput:      &buf,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return n, &buf
}

func viewMap(items []ViewItem) map[string]ViewItem {
	out := make(map[string]ViewItem, len(items))
	for _, it := range items {
		out[it.Member] = it
	}
	return out
}

// 可疑 5 被存活 6 自证推翻。
func TestSuspect5RefutedByAlive6(t *testing.T) {
	n, _ := newTestNode(t, "a", []string{"a", "b", "c"}, 2, 10, 10)

	if err := n.Receive(Message{Member: "b", Status: Suspect, Incarnation: 5}); err != nil {
		t.Fatalf("receive suspect: %v", err)
	}
	if got := viewMap(n.View())["b"]; got.Status != Suspect || got.Incarnation != 5 {
		t.Fatalf("after suspect: %+v", got)
	}

	// 存活(6) 化身号更大，推翻可疑(5)。
	if err := n.Receive(Message{Member: "b", Status: Alive, Incarnation: 6}); err != nil {
		t.Fatalf("receive alive: %v", err)
	}
	if got := viewMap(n.View())["b"]; got.Status != Alive || got.Incarnation != 6 {
		t.Fatalf("after alive(6), want ALIVE(6), got %+v", got)
	}

	// 被替换为新的存活更新并重新计次。
	out, err := n.Generate()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if !reflect.DeepEqual(out, []Message{{Member: "b", Status: Alive, Incarnation: 6}}) {
		t.Fatalf("generated %v", out)
	}
	t.Logf("判定依据: suspect(5) accepted -> alive(6) dominates because i>j; pending replaced and recount reset")
}

// 同化身号下：可疑压过存活，存活不能推翻同化身号可疑。
func TestSameIncarnationSuspectDominatesAlive(t *testing.T) {
	n, _ := newTestNode(t, "a", []string{"a", "b"}, 1, 10, 10)

	if err := n.Receive(Message{Member: "b", Status: Alive, Incarnation: 3}); err != nil {
		t.Fatal(err)
	}
	if err := n.Receive(Message{Member: "b", Status: Suspect, Incarnation: 3}); err != nil {
		t.Fatal(err)
	}
	if got := viewMap(n.View())["b"]; got.Status != Suspect {
		t.Fatalf("suspect(3) must override alive(3) at same incarnation, got %+v", got)
	}

	// 存活(3) 不能推翻可疑(3)：存活要求 i>j。
	if err := n.Receive(Message{Member: "b", Status: Alive, Incarnation: 3}); err != nil {
		t.Fatal(err)
	}
	if got := viewMap(n.View())["b"]; got.Status != Suspect {
		t.Fatalf("alive(3) must not refute suspect(3), got %+v", got)
	}

	// 存活(4) 才能推翻。
	if err := n.Receive(Message{Member: "b", Status: Alive, Incarnation: 4}); err != nil {
		t.Fatal(err)
	}
	if got := viewMap(n.View())["b"]; got.Status != Alive || got.Incarnation != 4 {
		t.Fatalf("alive(4) should refute, got %+v", got)
	}
	t.Logf("判定依据: suspect(i)>=alive(j) 覆盖；alive 仅 i>j 才覆盖 suspect")
}

// 确认失效压过一切且不可逆。
func TestDeadTerminalAndIrreversible(t *testing.T) {
	n, _ := newTestNode(t, "a", []string{"a", "b"}, 1, 10, 10)

	if err := n.Receive(Message{Member: "b", Status: Alive, Incarnation: 9}); err != nil {
		t.Fatal(err)
	}
	if err := n.Receive(Message{Member: "b", Status: Dead, Incarnation: 2}); err != nil {
		t.Fatal(err)
	}
	got := viewMap(n.View())["b"]
	if got.Status != Dead || got.Incarnation != 2 {
		t.Fatalf("dead should win even at lower incarnation, got %+v", got)
	}

	for _, m := range []Message{
		{Member: "b", Status: Alive, Incarnation: 100},
		{Member: "b", Status: Suspect, Incarnation: 100},
		{Member: "b", Status: Dead, Incarnation: 100},
	} {
		if err := n.Receive(m); err != nil {
			t.Fatal(err)
		}
		after := viewMap(n.View())["b"]
		if after.Status != Dead || after.Incarnation != 2 {
			t.Fatalf("%s must not change terminal dead view, got %+v", m, after)
		}
	}
	t.Logf("判定依据: dead-overrides-all 且 dead-is-terminal，任何后续更新丢弃")
}

// 超时恰好到达即升级（>=），不到点不升级。
func TestSuspectTimeoutExactBoundary(t *testing.T) {
	n, _ := newTestNode(t, "a", []string{"a", "b"}, 1, 10, 5)

	if err := n.Receive(Message{Member: "b", Status: Suspect, Incarnation: 1}); err != nil {
		t.Fatal(err)
	}
	if err := n.Tick(5); err != nil {
		t.Fatal(err)
	}
	if got := viewMap(n.View())["b"]; got.Status != Dead {
		t.Fatalf("clock==deadline must promote suspect to dead, got %+v", got)
	}

	// 另一个成员：deadline 前一拍不升级，到点才升级。
	n2, _ := newTestNode(t, "a", []string{"a", "c"}, 1, 10, 5)
	if err := n2.Receive(Message{Member: "c", Status: Suspect, Incarnation: 1}); err != nil {
		t.Fatal(err)
	}
	if err := n2.Tick(4); err != nil {
		t.Fatal(err)
	}
	if got := viewMap(n2.View())["c"]; got.Status != Suspect {
		t.Fatalf("clock<deadline must keep suspect, got %+v", got)
	}
	if err := n2.Tick(5); err != nil {
		t.Fatal(err)
	}
	if got := viewMap(n2.View())["c"]; got.Status != Dead {
		t.Fatalf("clock==deadline must promote, got %+v", got)
	}

	// 升级前被存活(2)覆盖则取消超时。
	n3, _ := newTestNode(t, "a", []string{"a", "d"}, 1, 10, 5)
	if err := n3.Receive(Message{Member: "d", Status: Suspect, Incarnation: 1}); err != nil {
		t.Fatal(err)
	}
	if err := n3.Receive(Message{Member: "d", Status: Alive, Incarnation: 2}); err != nil {
		t.Fatal(err)
	}
	if err := n3.Tick(100); err != nil {
		t.Fatal(err)
	}
	if got := viewMap(n3.View())["d"]; got.Status != Alive {
		t.Fatalf("refuted suspect must not time out, got %+v", got)
	}
	t.Logf("判定依据: deadline = suspect 到达时钟 + timeout，clock>=deadline 恰好等于即升级")
}

// 捎带上限 lambda*ceil(log2(n+1))，B 截断，同成员新更新重新计次。
func TestCarryLimitBatchCapAndReplacement(t *testing.T) {
	// n=3 => ceil(log2(4)) = 2，lambda=3 => 上限 6。
	n, _ := newTestNode(t, "a", []string{"a", "b", "c"}, 3, 2, 10)

	if err := n.Receive(Message{Member: "b", Status: Suspect, Incarnation: 1}); err != nil {
		t.Fatal(err)
	}
	if err := n.Receive(Message{Member: "c", Status: Suspect, Incarnation: 1}); err != nil {
		t.Fatal(err)
	}

	// B=2：每轮两条，b、c 各计 1 次。
	out1, err := n.Generate()
	if err != nil || len(out1) != 2 {
		t.Fatalf("round1: %v %v", out1, err)
	}
	// b 再来新更新：替换并重新计次，应排到 c 前面（sent=0 < c 的 1）。
	if err := n.Receive(Message{Member: "b", Status: Suspect, Incarnation: 2}); err != nil {
		t.Fatal(err)
	}
	out2, err := n.Generate()
	if err != nil || len(out2) != 2 {
		t.Fatalf("round2: %v %v", out2, err)
	}
	if out2[0].Member != "b" || out2[0].Incarnation != 2 {
		t.Fatalf("replaced update must reset count and be picked first, got %v", out2)
	}
	if out2[1].Member != "c" {
		t.Fatalf("c must be second, got %v", out2)
	}

	// 继续发到所有更新耗尽上限为止。
	emissions := map[string]int{}
	for _, m := range out1 {
		emissions[m.Member]++
	}
	for _, m := range out2 {
		emissions[m.Member]++
	}
	for {
		out, err := n.Generate()
		if err != nil {
			t.Fatal(err)
		}
		if len(out) == 0 {
			break
		}
		for _, m := range out {
			emissions[m.Member]++
		}
	}
	// c 的化身 1 共发 6 次；b 旧更新替换前已发 1 次，替换后的化身 2 重新计 6 次，共 7 次。
	if emissions["b"] != 7 || emissions["c"] != 6 {
		t.Fatalf("emission counts = %v, want b=7 c=6", emissions)
	}
	out, err := n.Generate()
	if err != nil || len(out) != 0 {
		t.Fatalf("queue must be exhausted: %v %v", out, err)
	}
	t.Logf("判定依据: carryLimit=lambda*ceil(log2(n+1))=6, B=2, 新更新替换旧更新且 sentCount 归零")
}

// 乱序、重复、批量任意顺序合并后视图逐项相同。
func TestConvergesUnderReorderAndDuplication(t *testing.T) {
	stream := []Message{
		{Member: "b", Status: Alive, Incarnation: 2},
		{Member: "b", Status: Suspect, Incarnation: 1},
		{Member: "c", Status: Suspect, Incarnation: 4},
		{Member: "c", Status: Alive, Incarnation: 5},
		{Member: "d", Status: Suspect, Incarnation: 3},
		{Member: "d", Status: Dead, Incarnation: 0},
		{Member: "b", Status: Suspect, Incarnation: 2},
	}

	refNode, _ := newTestNode(t, "a", []string{"a", "b", "c", "d"}, 1, 10, 1000)
	if err := refNode.ReceiveBatch(stream); err != nil {
		t.Fatal(err)
	}
	refView := refNode.View()

	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 20; trial++ {
		node, _ := newTestNode(t, "a", []string{"a", "b", "c", "d"}, 1, 10, 1000)
		perm := append([]Message(nil), stream...)
		rng.Shuffle(len(perm), func(i, j int) { perm[i], perm[j] = perm[j], perm[i] })
		noisy := make([]Message, 0, len(perm)*3)
		for _, m := range perm {
			times := 1 + rng.Intn(3)
			for k := 0; k < times; k++ {
				noisy = append(noisy, m)
			}
		}
		if err := node.ReceiveBatch(noisy); err != nil {
			t.Fatal(err)
		}
		if got := node.View(); !reflect.DeepEqual(got, refView) {
			t.Fatalf("trial %d view mismatch:\n got=%v\nwant=%v", trial, got, refView)
		}
	}

	want := viewMap(refView)
	if want["b"].Status != Suspect || want["b"].Incarnation != 2 {
		t.Fatalf("b want SUSPECT(2), got %+v", want["b"])
	}
	if want["c"].Status != Alive || want["c"].Incarnation != 5 {
		t.Fatalf("c want ALIVE(5), got %+v", want["c"])
	}
	if want["d"].Status != Dead {
		t.Fatalf("d want DEAD, got %+v", want["d"])
	}
	t.Logf("判定依据: 合并是基于偏序 dominates 的交换/幂等运算，故与顺序、重复无关")
}

// 自己被可疑(化身号不小于自身)时化身号 +1 并发存活；被确认失效即退出。
func TestSelfRefuteAndExit(t *testing.T) {
	n, _ := newTestNode(t, "a", []string{"a", "b"}, 1, 10, 1000)

	if err := n.Receive(Message{Member: "a", Status: Suspect, Incarnation: 0}); err != nil {
		t.Fatal(err)
	}
	if got := viewMap(n.View())["a"]; got.Status != Alive || got.Incarnation != 1 {
		t.Fatalf("self refute want ALIVE(1), got %+v", got)
	}
	out, err := n.Generate()
	if err != nil || !reflect.DeepEqual(out, []Message{{Member: "a", Status: Alive, Incarnation: 1}}) {
		t.Fatalf("self alive must be piggybacked: %v %v", out, err)
	}

	// 同化身号(1)的可疑再次触发自证 -> ALIVE(2)。
	if err := n.Receive(Message{Member: "a", Status: Suspect, Incarnation: 1}); err != nil {
		t.Fatal(err)
	}
	if got := viewMap(n.View())["a"]; got.Status != Alive || got.Incarnation != 2 {
		t.Fatalf("second self refute want ALIVE(2), got %+v", got)
	}

	// 低化身号可疑不能迫使自证。
	if err := n.Receive(Message{Member: "a", Status: Suspect, Incarnation: 1}); err != nil {
		t.Fatal(err)
	}
	if got := viewMap(n.View())["a"]; got.Incarnation != 2 {
		t.Fatalf("stale suspect must be discarded, got %+v", got)
	}

	// 收到关于自己的确认失效：视图 DEAD 并退出。
	if err := n.Receive(Message{Member: "a", Status: Dead, Incarnation: 0}); err != nil {
		t.Fatal(err)
	}
	if !n.Exited() {
		t.Fatal("node must exit after dead about self")
	}
	if got := viewMap(n.View())["a"]; got.Status != Dead {
		t.Fatalf("self view must be DEAD, got %+v", got)
	}

	// 退出后一切操作被拒绝且不改状态。
	before := n.View()
	for _, op := range []func() error{
		func() error { return n.Receive(Message{Member: "b", Status: Alive, Incarnation: 1}) },
		func() error { return n.Tick(99) },
		func() error { _, e := n.Generate(); return e },
	} {
		if err := op(); !errors.Is(err, ErrNodeExited) {
			t.Fatalf("post-exit op err=%v, want ErrNodeExited", err)
		}
	}
	if got := n.View(); !reflect.DeepEqual(got, before) {
		t.Fatalf("rejected ops changed view: %v", got)
	}
	t.Logf("判定依据: self suspect(i>=self) => alive(i+1) 并捎带；self dead => 退出；退出后全部拒绝")
}

// 可区分的拒绝原因，且拒绝不改视图与缓冲。
func TestRejectionsAreAtomicAndDistinguishable(t *testing.T) {
	n, _ := newTestNode(t, "a", []string{"a", "b"}, 1, 10, 10)
	if err := n.Receive(Message{Member: "b", Status: Alive, Incarnation: 7}); err != nil {
		t.Fatal(err)
	}
	before := n.View()

	cases := []struct {
		name string
		m    Message
		want error
	}{
		{"negative incarnation", Message{Member: "b", Status: Alive, Incarnation: -1}, ErrIncarnationNegative},
		{"unknown member", Message{Member: "zzz", Status: Alive, Incarnation: 1}, ErrUnknownMember},
		{"invalid status", Message{Member: "b", Status: Status(0), Incarnation: 1}, ErrInvalidStatus},
	}
	for _, c := range cases {
		if err := n.Receive(c.m); !errors.Is(err, c.want) {
			t.Fatalf("%s: err=%v want=%v", c.name, err, c.want)
		}
		if got := n.View(); !reflect.DeepEqual(got, before) {
			t.Fatalf("%s changed view: %v", c.name, got)
		}
	}

	// 批量中只要有一条非法，整批拒绝且无副作用。
	bad := []Message{
		{Member: "b", Status: Suspect, Incarnation: 9},
		{Member: "ghost", Status: Alive, Incarnation: 1},
	}
	if err := n.ReceiveBatch(bad); !errors.Is(err, ErrUnknownMember) {
		t.Fatalf("batch err=%v want ErrUnknownMember", err)
	}
	if got := n.View(); !reflect.DeepEqual(got, before) {
		t.Fatalf("rejected batch changed view: %v", got)
	}
	out, err := n.Generate()
	if err != nil || len(out) != 1 || out[0].Incarnation != 7 {
		t.Fatalf("rejected batch must not touch pending: %v %v", out, err)
	}

	// 构造参数非法。
	for _, cfg := range []Config{
		{SelfID: "a", Members: []string{"a"}, Lambda: 0, B: 1},
		{SelfID: "a", Members: []string{"a"}, Lambda: 1, B: -1},
	} {
		if _, err := New(cfg); !errors.Is(err, ErrInvalidParameter) {
			t.Fatalf("cfg=%+v err=%v want ErrInvalidParameter", cfg, err)
		}
	}
	if _, err := New(Config{SelfID: "x", Members: []string{"a"}, Lambda: 1, B: 1}); !errors.Is(err, ErrUnknownMember) {
		t.Fatal("self not in member list must be rejected")
	}

	// 时钟不允许回拨。
	if err := n.Tick(3); err != nil {
		t.Fatal(err)
	}
	if err := n.Tick(2); !errors.Is(err, ErrClockRewind) {
		t.Fatalf("clock rewind err=%v", err)
	}
	t.Logf("判定依据: 所有拒绝先校验后生效，错误哨兵彼此可区分")
}

// 并发调用接收、时钟、生成不崩溃且结果一致。
func TestConcurrentReceiveTickGenerate(t *testing.T) {
	n, _ := newTestNode(t, "a", []string{"a", "b", "c", "d"}, 2, 2, 1_000_000)

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(3)
		go func(w int) {
			defer wg.Done()
			for k := 0; k < 50; k++ {
				_ = n.Receive(Message{Member: "b", Status: Suspect, Incarnation: int64(w*50 + k)})
			}
		}(w)
		go func(w int) {
			defer wg.Done()
			for k := 0; k <= 100; k++ {
				_ = n.Tick(int64(k))
			}
		}(w)
		go func() {
			defer wg.Done()
			for k := 0; k < 50; k++ {
				_, _ = n.Generate()
			}
		}()
	}
	wg.Wait()

	// 最终视图自洽：b 为可疑且化身号为收到的最大值，与并发交织顺序无关。
	got := viewMap(n.View())["b"]
	if got.Status != Suspect || got.Incarnation != 399 {
		t.Fatalf("concurrent final view: %+v", got)
	}
	t.Logf("判定依据: 单一互斥锁串行化全部状态访问，-race 下无数据竞争")
}

// 相同输入与时钟序列反复执行得到完全相同的视图与外发内容。
func TestDeterministicReplay(t *testing.T) {
	type result struct {
		view []ViewItem
		sent [][]Message
		logs string
	}
	run := func() result {
		var buf bytes.Buffer
		n, err := New(Config{
			SelfID:         "a",
			Members:        []string{"a", "b", "c"},
			Lambda:         2,
			B:              2,
			SuspectTimeout: 3,
			LogOutput:      &buf,
		})
		if err != nil {
			t.Fatal(err)
		}
		steps := []func() error{
			func() error { return n.Receive(Message{Member: "b", Status: Suspect, Incarnation: 1}) },
			func() error {
				if err := n.Tick(1); err != nil {
					return err
				}
				return n.Receive(Message{Member: "c", Status: Suspect, Incarnation: 2})
			},
			func() error { return n.Tick(4) },
		}
		var sent [][]Message
		for _, step := range steps {
			if err := step(); err != nil {
				t.Fatal(err)
			}
			out, err := n.Generate()
			if err != nil {
				t.Fatal(err)
			}
			sent = append(sent, out)
		}
		return result{view: n.View(), sent: sent, logs: buf.String()}
	}

	first := run()
	for i := 0; i < 3; i++ {
		got := run()
		if !reflect.DeepEqual(got.view, first.view) {
			t.Fatalf("replay %d view mismatch:\n got=%v\nwant=%v", i, got.view, first.view)
		}
		if !reflect.DeepEqual(got.sent, first.sent) {
			t.Fatalf("replay %d outbound mismatch:\n got=%v\nwant=%v", i, got.sent, first.sent)
		}
		if got.logs != first.logs {
			t.Fatalf("replay %d logs differ", i)
		}
	}

	want := viewMap(first.view)
	if want["b"].Status != Dead || want["c"].Status != Dead {
		t.Fatalf("both suspects should time out at clock 4 (deadline b=4, c=4): %+v %+v", want["b"], want["c"])
	}
	// 第 2 轮外发：b 已发 1 次、c 为 0 次，故 c 在前。
	if !reflect.DeepEqual(first.sent[1], []Message{
		{Member: "c", Status: Suspect, Incarnation: 2},
		{Member: "b", Status: Suspect, Incarnation: 1},
	}) {
		t.Fatalf("round 2 outbound = %v", first.sent[1])
	}
	t.Logf("判定依据: 无随机源、注入时钟、稳定排序，重复执行视图/外发/日志逐字节一致")
}
