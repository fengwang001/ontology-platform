package asyncio

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"os"
	"sync"
	"testing"
	"time"
)

func testLogger() *slog.Logger {
	h := slog.NewTextHandler(io.Writer(os.Stderr), &slog.HandlerOptions{Level: slog.LevelInfo})
	return slog.New(h)
}

func eventsSummary(evs []Event) string {
	s := ""
	for _, e := range evs {
		if e.Kind == KindElement {
			s += fmt.Sprintf("elem(%s,seq=%d) ", e.ID, e.Seq)
		} else {
			s += fmt.Sprintf("wm(%d,seq=%d) ", e.Watermark, e.Seq)
		}
	}
	if s == "" {
		return "<empty>"
	}
	return s
}

func eventsEqual(a, b []Event) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !sameEvent(a[i], b[i]) {
			return false
		}
	}
	return true
}

func drainPop(t *testing.T, b *Buffer, label string) []Event {
	t.Helper()
	evs := b.PopOutputs()
	t.Logf("%s -> 输出: %s", label, eventsSummary(evs))
	return evs
}

// TestOrderedPreservesInputOrder 验证有序模式：输出严格等于输入顺序，
// 乱序完成不改变输出，水位线在其之前全部元素输出后立即输出。
func TestOrderedPreservesInputOrder(t *testing.T) {
	b, err := New(Ordered, 3, testLogger())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	mustPush := func(id string, val any) {
		t.Helper()
		if err := b.PushElement(id, val); err != nil {
			t.Fatalf("PushElement(%s): %v", id, err)
		}
		t.Logf("输入元素 id=%s val=%v inFlight=%d", id, val, b.Stats().InFlight)
	}
	mustWM := func(wm int64) {
		t.Helper()
		if err := b.PushWatermark(wm); err != nil {
			t.Fatalf("PushWatermark(%d): %v", wm, err)
		}
		t.Logf("输入水位线 wm=%d", wm)
	}
	mustDone := func(id string) {
		t.Helper()
		if err := b.Complete(id); err != nil {
			t.Fatalf("Complete(%s): %v", id, err)
		}
		t.Logf("完成声明 id=%s inFlight=%d", id, b.Stats().InFlight)
	}

	mustPush("a", 1)
	mustPush("b", 2)
	mustWM(10)
	mustPush("c", 3)
	if evs := drainPop(t, b, "乱序完成前"); len(evs) != 0 {
		t.Fatalf("期望无输出，实际 %v", evs)
	}

	// c 先完成，但队头 a 未完成，必须被扣住，什么都不输出。
	mustDone("c")
	if evs := drainPop(t, b, "c 先完成（应扣住）"); len(evs) != 0 {
		t.Fatalf("队头未完成时不应输出，实际 %v", evs)
	}

	mustDone("b") // 队头仍被 a 阻塞
	if evs := drainPop(t, b, "b 完成（仍应扣住）"); len(evs) != 0 {
		t.Fatalf("队头未完成时不应输出，实际 %v", evs)
	}

	mustDone("a") // a 完成：a,b,wm10,c 应级联按输入顺序全部输出
	evs := drainPop(t, b, "a 完成（应级联输出 a,b,wm10,c）")
	want := []Event{
		{Kind: KindElement, ID: "a", Value: 1, Seq: 0},
		{Kind: KindElement, ID: "b", Value: 2, Seq: 1},
		{Kind: KindWatermark, Watermark: 10, Seq: 2},
		{Kind: KindElement, ID: "c", Value: 3, Seq: 3},
	}
	if !eventsEqual(evs, want) {
		t.Fatalf("有序级联输出不匹配:\n got %s\nwant %s", eventsSummary(evs), eventsSummary(want))
	}

	// 空队时输入水位线：其之前无元素，应立即输出。
	mustWM(20)
	if evs := drainPop(t, b, "空队列水位线（应立即输出）"); len(evs) != 1 ||
		evs[0].Kind != KindWatermark || evs[0].Watermark != 20 {
		t.Fatalf("空队列水位线应立即输出，实际 %s", eventsSummary(evs))
	}

	if st := b.Stats(); st.InFlight != 0 || st.Pending != 0 || st.Buffered != 0 {
		t.Fatalf("收尾状态异常 %+v", st)
	}
}

// TestUnorderedHeldAndCascade 验证无序模式的段落划分、跨段扣留与级联释放。
func TestUnorderedHeldAndCascade(t *testing.T) {
	b, err := New(Unordered, 5, testLogger())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mustPush := func(id string, val any) {
		t.Helper()
		if err := b.PushElement(id, val); err != nil {
			t.Fatalf("PushElement(%s): %v", id, err)
		}
		t.Logf("[无序] 输入元素 id=%s", id)
	}
	mustWM := func(wm int64) {
		t.Helper()
		if err := b.PushWatermark(wm); err != nil {
			t.Fatalf("PushWatermark(%d): %v", wm, err)
		}
		t.Logf("[无序] 输入水位线 wm=%d", wm)
	}
	done := func(id string) {
		t.Helper()
		if err := b.Complete(id); err != nil {
			t.Fatalf("Complete(%s): %v", id, err)
		}
		t.Logf("[无序] 完成声明 id=%s", id)
	}

	// 段0：a,b；wm10；段1：c,d；wm20；段2：e
	mustPush("a", 1)
	mustPush("b", 2)
	mustWM(10)
	mustPush("c", 3)
	mustPush("d", 4)
	mustWM(20)
	mustPush("e", 5)

	// 未开放段内的元素即使完成也被扣住：d、c（段1）、e（段2）。
	done("d")
	if evs := drainPop(t, b, "段1 的 d 完成（未开放，扣住）"); len(evs) != 0 {
		t.Fatalf("未开放段完成的元素必须被扣住，实际 %s", eventsSummary(evs))
	}
	done("c")
	done("e")
	if evs := drainPop(t, b, "c,e 完成（均未开放，扣住）"); len(evs) != 0 {
		t.Fatalf("未开放段完成的元素必须被扣住，实际 %s", eventsSummary(evs))
	}

	// 段0 的 a 完成（开放段）：立即输出。
	done("a")
	evs := drainPop(t, b, "段0 的 a 完成（开放段，立即输出）")
	if len(evs) != 1 || evs[0].ID != "a" {
		t.Fatalf("开放段完成元素应立即输出，实际 %s", eventsSummary(evs))
	}

	// b 完成：段0 清空 -> wm10 输出，开放段1 -> 按完成先后释放 d、c；
	// 随后 wm20 前置元素已全部输出 -> wm20 输出，开放段2 -> e。
	done("b")
	evs = drainPop(t, b, "b 完成（应级联：b,wm10,d,c,wm20,e）")
	want := []Event{
		{Kind: KindElement, ID: "b", Value: 2, Seq: 1},
		{Kind: KindWatermark, Watermark: 10, Seq: 2},
		{Kind: KindElement, ID: "d", Value: 4, Seq: 4},
		{Kind: KindElement, ID: "c", Value: 3, Seq: 3},
		{Kind: KindWatermark, Watermark: 20, Seq: 5},
		{Kind: KindElement, ID: "e", Value: 5, Seq: 6},
	}
	if !eventsEqual(evs, want) {
		t.Fatalf("无序级联释放不匹配:\n got %s\nwant %s", eventsSummary(evs), eventsSummary(want))
	}
	if st := b.Stats(); st.InFlight != 0 || st.Pending != 0 || st.Buffered != 0 {
		t.Fatalf("收尾状态异常 %+v", st)
	}
}

// TestCapacityBoundary 验证占用数边界：占满后拒绝，完成释放后可再入；
// 已完成但仍在队（有序模式被队头阻塞）的元素不再占用容量。
func TestCapacityBoundary(t *testing.T) {
	b, _ := New(Ordered, 2, testLogger())
	if err := b.PushElement("a", 1); err != nil {
		t.Fatal(err)
	}
	if err := b.PushElement("b", 2); err != nil {
		t.Fatal(err)
	}
	if st := b.Stats(); st.InFlight != 2 || st.Capacity != 2 {
		t.Fatalf("占用数应为 2/2，实际 %+v", st)
	}
	if err := b.PushElement("c", 3); !errors.Is(err, ErrCapacityFull) {
		t.Fatalf("容量满应拒绝 ErrCapacityFull，实际 %v", err)
	}

	// b 先完成：占用立即释放，但因队头 a 未完成，b 不输出。
	if err := b.Complete("b"); err != nil {
		t.Fatal(err)
	}
	if st := b.Stats(); st.InFlight != 1 {
		t.Fatalf("完成声明后占用应降为 1，实际 %+v", st)
	}
	if err := b.PushElement("c", 3); err != nil {
		t.Fatalf("释放容量后应可接收，实际 %v", err)
	}
	if err := b.PushElement("d", 4); !errors.Is(err, ErrCapacityFull) {
		t.Fatalf("再次占满应拒绝，实际 %v", err)
	}

	if err := b.Complete("a"); err != nil {
		t.Fatal(err)
	}
	// c 尚未完成；有序输出到队头 c 为止：a、b。
	evs := b.PopOutputs()
	if len(evs) != 2 || evs[0].ID != "a" || evs[1].ID != "b" {
		t.Fatalf("应输出 a,b，实际 %s", eventsSummary(evs))
	}
	if err := b.Complete("c"); err != nil {
		t.Fatal(err)
	}
	evs = b.PopOutputs()
	if len(evs) != 1 || evs[0].ID != "c" {
		t.Fatalf("应输出 c，实际 %s", eventsSummary(evs))
	}
}

// TestRejectionLeavesNoTrace 验证每类非法输入互不相同且整体拒绝：
// 队列、占用数、已产生输出在拒绝前后完全一致。
func TestRejectionLeavesNoTrace(t *testing.T) {
	for _, mode := range []Mode{Ordered, Unordered} {
		t.Run(mode.String(), func(t *testing.T) {
			b, _ := New(mode, 2, testLogger())
			// cap2，两种模式共用同一准备状态：
			// a,b 占满 -> wm5（之前有 a,b，不能输出）-> 完成 a：两种模式都立即
			// 输出 a（有序队头完成；无序开放段完成），wm5 因 b 仍在前而保留；
			// c 补入 -> 完成 c（有序被队头 b 阻塞；无序段未开放被扣住）-> d 补满。
			for _, id := range []string{"a", "b"} {
				if err := b.PushElement(id, int(id[0]-'a'+1)); err != nil {
					t.Fatal(err)
				}
			}
			if err := b.PushWatermark(5); err != nil {
				t.Fatal(err)
			}
			if err := b.Complete("a"); err != nil {
				t.Fatal(err)
			}
			b.PopOutputs() // 取走已输出的 a，随后断言拒绝不改变输出。
			if err := b.PushElement("c", 3); err != nil {
				t.Fatal(err)
			}
			if err := b.Complete("c"); err != nil {
				t.Fatal(err)
			}
			if err := b.PushElement("d", 4); err != nil {
				t.Fatal(err)
			}
			heldCompleted, dupID := "c", "c"

			snapshot := func() (Stats, []Event) {
				return b.Stats(), b.SnapshotOutputs()
			}

			cases := []struct {
				name string
				run  func() error
				want error
			}{
				{"push 空标识", func() error { return b.PushElement("", 9) }, ErrEmptyID},
				{"push 重复标识", func() error { return b.PushElement(dupID, 9) }, ErrDuplicateID},
				{"push 容量满", func() error { return b.PushElement("zz", 9) }, ErrCapacityFull},
				{"wm 相等", func() error { return b.PushWatermark(5) }, ErrWatermarkNotIncreasing},
				{"wm 回退", func() error { return b.PushWatermark(4) }, ErrWatermarkNotIncreasing},
				{"done 空标识", func() error { return b.Complete("") }, ErrEmptyID},
				{"done 未知标识", func() error { return b.Complete("ghost") }, ErrUnknownID},
				{"done 已完成标识", func() error { return b.Complete(heldCompleted) }, ErrAlreadyCompleted},
			}

			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					st0, out0 := snapshot()
					err := tc.run()
					if !errors.Is(err, tc.want) {
						t.Fatalf("%s: 期望 %v，实际 %v", tc.name, tc.want, err)
					}
					st1, out1 := snapshot()
					if st1 != st0 {
						t.Fatalf("%s: Stats 被拒绝改变 before=%+v after=%+v", tc.name, st0, st1)
					}
					if !eventsEqual(out1, out0) {
						t.Fatalf("%s: 已产生输出被拒绝改变 before=%s after=%s",
							tc.name, eventsSummary(out0), eventsSummary(out1))
					}
				})
			}

			// 各类原因必须互不相同、可区分。
			distinct := []error{
				ErrEmptyID, ErrDuplicateID, ErrCapacityFull,
				ErrWatermarkNotIncreasing, ErrUnknownID, ErrAlreadyCompleted,
			}
			for i := range distinct {
				for j := i + 1; j < len(distinct); j++ {
					if errors.Is(distinct[i], distinct[j]) {
						t.Fatalf("错误原因未区分: %v 与 %v", distinct[i], distinct[j])
					}
				}
			}
		})
	}
}

// TestConstructorErrors 验证构造期非法参数。
func TestConstructorErrors(t *testing.T) {
	if _, err := New(Ordered, 0, nil); !errors.Is(err, ErrInvalidCapacity) {
		t.Fatalf("容量 0 应拒绝，实际 %v", err)
	}
	if _, err := New(Unordered, -1, nil); !errors.Is(err, ErrInvalidCapacity) {
		t.Fatalf("负容量应拒绝，实际 %v", err)
	}
	if _, err := New(Mode(99), 1, nil); !errors.Is(err, ErrInvalidMode) {
		t.Fatalf("非法模式应拒绝，实际 %v", err)
	}
}

// TestConcurrentSmoke 在 -race 下验证：输入、水位线、完成与输出可被多个执行体
// 并发调用；最终所有元素与水位线恰好输出一次，占用归零，任何时刻不超容量。
func TestConcurrentSmoke(t *testing.T) {
	for _, mode := range []Mode{Ordered, Unordered} {
		t.Run(mode.String(), func(t *testing.T) {
			const capacity = 4
			const workers = 8
			const perWorker = 30
			const totalElem = workers * perWorker

			b, err := New(mode, capacity, testLogger())
			if err != nil {
				t.Fatal(err)
			}

			// 输入流由一个协调 goroutine 串行提交元素与水位线（保证流确定）；
			// completed 被容量拒绝时稍后重试。完成与输出由另外两组 goroutine 并发驱动。
			submitDone := make(chan string, totalElem)

			// 容量不变量监视器：并发期间定期采样。
			stopMon := make(chan struct{})
			var monWG sync.WaitGroup
			monWG.Add(1)
			go func() {
				defer monWG.Done()
				for {
					select {
					case <-stopMon:
						return
					default:
						if st := b.Stats(); st.InFlight > st.Capacity || st.InFlight < 0 {
							t.Errorf("占用越界: %+v", st)
							return
						}
					}
				}
			}()

			var producerWG sync.WaitGroup
			producerWG.Add(1)
			go func() {
				defer producerWG.Done()
				id := 0
				for w := 0; w < workers; w++ {
					for k := 0; k < perWorker; k++ {
						eid := fmt.Sprintf("w%d-e%d", w, k)
						for b.PushElement(eid, id) != nil {
							runtimeGosched()
						}
						id++
						submitDone <- eid
					}
					if err := b.PushWatermark(int64(w + 1)); err != nil {
						t.Errorf("水位线提交失败: %v", err)
					}
				}
				close(submitDone)
			}()

			// 完成者：从提交队列拿到标识后并发声明完成（含乱序抖动）。
			var completerWG sync.WaitGroup
			for i := 0; i < 4; i++ {
				completerWG.Add(1)
				go func(seed int) {
					defer completerWG.Done()
					rng := rand.New(rand.NewSource(int64(seed)))
					for eid := range submitDone {
						if rng.Intn(3) == 0 {
							time.Sleep(time.Microsecond)
						}
						if err := b.Complete(eid); err != nil {
							t.Errorf("Complete(%s): %v", eid, err)
						}
					}
				}(i + 1)
			}

			// 输出者：多执行体并发 Drain 到一个 channel，由收集器合并。
			outCh := make(chan Event, capacity*2)
			ctx, cancel := context.WithCancel(context.Background())
			var drainerWG sync.WaitGroup
			for i := 0; i < 4; i++ {
				drainerWG.Add(1)
				go func() {
					defer drainerWG.Done()
					b.Drain(ctx, outCh)
				}()
			}

			expectedEvents := totalElem + workers
			got := make([]Event, 0, expectedEvents)
			deadline := time.After(10 * time.Second)
			for len(got) < expectedEvents {
				select {
				case ev := <-outCh:
					got = append(got, ev)
					t.Logf("并发输出 %s", eventsSummary([]Event{ev}))
				case <-deadline:
					t.Fatalf("超时：只收到 %d/%d 条输出", len(got), expectedEvents)
				}
			}
			cancel()
			drainerWG.Wait()
			completerWG.Wait()
			producerWG.Wait()
			close(stopMon)
			monWG.Wait()

			// 恰好每个元素一次、每条水位线一次。
			elemIDs := map[string]bool{}
			wmSeen := map[int64]bool{}
			lastWMLen := 0
			for _, ev := range got {
				switch ev.Kind {
				case KindElement:
					if elemIDs[ev.ID] {
						t.Fatalf("元素重复输出: %s", ev.ID)
					}
					elemIDs[ev.ID] = true
				case KindWatermark:
					if wmSeen[ev.Watermark] {
						t.Fatalf("水位线重复输出: %d", ev.Watermark)
					}
					wmSeen[ev.Watermark] = true
					lastWMLen++
				}
			}
			if len(elemIDs) != totalElem {
				t.Fatalf("元素输出数 %d != %d", len(elemIDs), totalElem)
			}
			if lastWMLen != workers {
				t.Fatalf("水位线输出数 %d != %d", lastWMLen, workers)
			}

			// 多 Drain 经共享缓冲 channel 汇合时不保证跨执行体的接收先后，
			// 有序模式的严格顺序已由 TestOrderedPreservesInputOrder 确定性覆盖。
			if st := b.Stats(); st.InFlight != 0 || st.Pending != 0 || st.Buffered != 0 {
				t.Fatalf("收尾状态异常 %+v", st)
			}
		})
	}
}

func runtimeGosched() {
	// 用短暂忙等避免直接 import runtime；提交失败时让渡时间片。
	time.Sleep(time.Microsecond)
}
