package ontology

import (
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"sync"
	"testing"
)

// oracle 是与处理器实现无关的参考模型：
// 每一步都把全部待处理事件按 (优先级降序, 到达顺序升序) 排序，取第一条。
// 若抢占式处理器的已处理序列与该模型逐元素相同，则说明
// “优先级降序 + 同优先级 FIFO + 抢占保存/恢复”的结果正确。
type oracle struct {
	waiting []oracleEvent
	nextSeq int
}

type oracleEvent struct {
	seq      int
	priority int
	id       string
}

func (o *oracle) enqueue(events ...Event) {
	for _, e := range events {
		o.waiting = append(o.waiting, oracleEvent{seq: o.nextSeq, priority: e.Priority, id: e.ID})
		o.nextSeq++
	}
}

func (o *oracle) next() (Event, bool) {
	best := -1
	for i, e := range o.waiting {
		if best < 0 || e.priority > o.waiting[best].priority ||
			(e.priority == o.waiting[best].priority && e.seq < o.waiting[best].seq) {
			best = i
		}
	}
	if best < 0 {
		return Event{}, false
	}
	e := o.waiting[best]
	o.waiting = append(o.waiting[:best], o.waiting[best+1:]...)
	return Event{ID: e.id, Priority: e.priority}, true
}

// logState 打印一次操作后的完整状态与判定依据。
func logState(t *testing.T, op string, p *Processor, basis string) {
	t.Helper()
	snap := p.Snapshot()
	t.Logf("操作=%s", op)
	t.Logf("  队列(未处理)=%v", snap.Pending)
	if snap.Current == nil {
		t.Logf("  当前批次=<无>")
	} else {
		t.Logf("  当前批次=优先级%d 已处理位置%d 下一个=%q", snap.Current.Priority, snap.Current.Position, snap.Current.Next)
	}
	t.Logf("  保存现场(栈底->栈顶)=%+v", snap.Saved)
	t.Logf("  已处理序列=%v", snap.Processed)
	t.Logf("  判定依据: %s", basis)
}

func wantErr(t *testing.T, op string, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("%s: 期望错误 %v, 实际 %v", op, want, got)
	}
}

func errOnly(_ Event, err error) error { return err }

func ids(events []Event) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = e.ID
	}
	return out
}

// TestPreemptionSavesContext 验证高优先级打断低优先级、保存现场并在耗尽后恢复。
func TestPreemptionSavesContext(t *testing.T) {
	p := New()
	ref := &oracle{}

	mustEnqueue := func(events ...Event) {
		t.Helper()
		if err := p.Enqueue(events...); err != nil {
			t.Fatalf("Enqueue 意外失败: %v", err)
		}
		ref.enqueue(events...)
	}
	mustAdvance := func(op string) Event {
		t.Helper()
		e, err := p.Advance()
		if err != nil {
			t.Fatalf("%s: Advance 意外失败: %v", op, err)
		}
		want, ok := ref.next()
		if !ok || want != e {
			t.Fatalf("%s: 参考模型期望 %v(ok=%v), 实际 %v", op, want, ok, e)
		}
		logState(t, op, p, "与排序参考模型逐元素对照；保存现场应含被打断的低优先级批次")
		return e
	}

	mustEnqueue(Event{"a", 1}, Event{"b", 1}, Event{"c", 1})
	first := mustAdvance("Advance#1 低优先级批次启动")
	if first.ID != "a" {
		t.Fatalf("首个事件应为 a, 实际 %s", first.ID)
	}

	mustEnqueue(Event{"x", 5}, Event{"y", 5})
	snap := p.Snapshot()
	if snap.Current == nil || snap.Current.Priority != 1 || snap.Current.Position != 1 {
		t.Fatalf("打断前当前批次应停在优先级1位置1, 实际 %+v", snap.Current)
	}
	second := mustAdvance("Advance#2 高优先级抢占")
	if second.ID != "x" || second.Priority != 5 {
		t.Fatalf("抢占后应处理 x(p=5), 实际 %v", second)
	}
	snap = p.Snapshot()
	if len(snap.Saved) != 1 || snap.Saved[0].Priority != 1 || snap.Saved[0].Position != 1 {
		t.Fatalf("保存现场应含 [优先级1 位置1], 实际 %+v", snap.Saved)
	}

	third := mustAdvance("Advance#3 高优先级同批继续")
	if third.ID != "y" {
		t.Fatalf("高优先级批内应继续处理 y, 实际 %v", third)
	}

	fourth := mustAdvance("Advance#4 高优先级耗尽, 栈顶恢复")
	if fourth.ID != "b" || fourth.Priority != 1 {
		t.Fatalf("恢复后应从保存位置继续处理 b(p=1), 实际 %v", fourth)
	}
	snap = p.Snapshot()
	if len(snap.Saved) != 0 {
		t.Fatalf("恢复后保存现场应清空, 实际 %+v", snap.Saved)
	}

	fifth := mustAdvance("Advance#5 恢复后继续到耗尽")
	if fifth.ID != "c" {
		t.Fatalf("最后应处理 c, 实际 %v", fifth)
	}
	if _, err := p.Advance(); !errors.Is(err, ErrNoProcessable) {
		t.Fatalf("全部耗尽后应为 ErrNoProcessable, 实际 %v", err)
	}
	if err := p.Check(); err != nil {
		t.Fatalf("自检失败: %v", err)
	}
	logState(t, "结束", p, "每个事件恰好一次；已处理序列为排序参考模型的完整结果")
}

// TestResumeOrderLIFO 验证多级抢占时按 LIFO（栈顶最近打断）恢复。
func TestResumeOrderLIFO(t *testing.T) {
	p := New()
	ref := &oracle{}

	step := func(op, wantID string) {
		t.Helper()
		if err := p.Check(); err != nil {
			t.Fatalf("%s: 自检失败: %v", op, err)
		}
		e, err := p.Advance()
		if err != nil {
			t.Fatalf("%s: Advance 失败: %v", op, err)
		}
		want, _ := ref.next()
		if want != e || e.ID != wantID {
			t.Fatalf("%s: 期望 %s(参考模型 %v), 实际 %v", op, wantID, want, e)
		}
		logState(t, op, p, "多级抢占后从栈顶恢复；保存现场按优先级递增排列")
	}
	enq := func(events ...Event) {
		t.Helper()
		if err := p.Enqueue(events...); err != nil {
			t.Fatalf("Enqueue 失败: %v", err)
		}
		ref.enqueue(events...)
	}

	enq(Event{"L1", 1}, Event{"L2", 1})
	step("p1 启动", "L1")
	enq(Event{"M1", 3}, Event{"M2", 3})
	step("p3 抢占 p1", "M1")
	enq(Event{"H1", 9})
	step("p9 抢占 p3", "H1")

	snap := p.Snapshot()
	if len(snap.Saved) != 2 || snap.Saved[0].Priority != 1 || snap.Saved[1].Priority != 3 {
		t.Fatalf("保存现场栈底->栈顶应为 p1,p3, 实际 %+v", snap.Saved)
	}

	step("栈顶恢复 p3", "M2")
	step("再恢复 p1", "L2")
	if err := p.Check(); err != nil {
		t.Fatalf("最终自检失败: %v", err)
	}

	got := ids(p.Processed())
	want := []string{"L1", "M1", "H1", "M2", "L2"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("LIFO 恢复顺序错误: 期望 %v, 实际 %v", want, got)
	}
}

// TestSamePriorityFIFO 验证同优先级严格 FIFO，穿插高优先级事件不改变其相对顺序。
func TestSamePriorityFIFO(t *testing.T) {
	p := New()
	ref := &oracle{}

	events := []Event{{"f1", 2}, {"f2", 2}, {"f3", 2}, {"f4", 2}}
	if err := p.Enqueue(events...); err != nil {
		t.Fatalf("Enqueue 失败: %v", err)
	}
	ref.enqueue(events...)

	if e, _ := p.Advance(); e.ID != "f1" {
		t.Fatalf("同优先级首个应为 f1, 实际 %v", e)
	} else if want, ok := ref.next(); !ok || want != e {
		t.Fatalf("参考模型首步不一致: %v ok=%v", want, ok)
	}
	if err := p.Enqueue(Event{"hi", 4}); err != nil {
		t.Fatalf("Enqueue 高优先级失败: %v", err)
	}
	ref.enqueue(Event{"hi", 4})

	processed := []string{}
	for range 4 {
		e, err := p.Advance()
		if err != nil {
			t.Fatalf("抢占/恢复阶段 Advance 失败: %v", err)
		}
		want, _ := ref.next()
		if want != e {
			t.Fatalf("与参考模型不符: 期望 %v, 实际 %v", want, e)
		}
		processed = append(processed, e.ID)
	}
	// f1 已在抢占前处理；之后必须是 hi 抢占，恢复后仍为 f2,f3,f4。
	wantOrder := []string{"hi", "f2", "f3", "f4"}
	if fmt.Sprint(processed) != fmt.Sprint(wantOrder) {
		t.Fatalf("同优先级 FIFO 被抢占打乱: 期望 %v, 实际 %v", wantOrder, processed)
	}
	if err := p.Check(); err != nil {
		t.Fatalf("自检失败: %v", err)
	}
	logState(t, "FIFO 场景结束", p, "抢占只切换批次，不改变同优先级内的相对顺序")
}

// TestRejectionsAreAtomic 验证三类非法输入整体拒绝且不产生任何副作用。
func TestRejectionsAreAtomic(t *testing.T) {
	seed := func() *Processor {
		p := New()
		if err := p.Enqueue(Event{"keep-1", 2}, Event{"keep-2", 2}); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Advance(); err != nil {
			t.Fatal(err)
		}
		return p
	}

	cases := []struct {
		name   string
		events []Event
		want   error
	}{
		{"负优先级", []Event{{"bad", -1}}, ErrNegativePriority},
		{"批次内重复标识", []Event{{"dup", 1}, {"dup", 2}}, ErrDuplicateID},
		{"与已入队标识重复", []Event{{"keep-2", 0}}, ErrDuplicateID},
		{"与已处理标识重复", []Event{{"keep-1", 0}}, ErrDuplicateID},
		{"负优先级优先于重复判定", []Event{{"keep-1", -1}, {"x", -2}}, ErrNegativePriority},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := seed()
			before := p.Snapshot()
			wantErr(t, "Enqueue", p.Enqueue(tc.events...), tc.want)
			after := p.Snapshot()
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("拒绝后状态被改变:\n拒绝前=%+v\n拒绝后=%+v", before, after)
			}
			if err := p.Check(); err != nil {
				t.Fatalf("拒绝后自检失败: %v", err)
			}
			logState(t, "拒绝 "+tc.name, p, "整批拒绝：队列/当前批次/保存现场/已处理序列均与拒绝前一致")
		})
	}

	t.Run("空处理器推进", func(t *testing.T) {
		p := New()
		wantErr(t, "Advance(empty)", errOnly(p.Advance()), ErrNoProcessable)
	})
	t.Run("耗尽后推进", func(t *testing.T) {
		p := seed()
		if _, err := p.Advance(); err != nil {
			t.Fatal(err)
		}
		wantErr(t, "Advance(drained)", errOnly(p.Advance()), ErrNoProcessable)
		logState(t, "无可处理事件拒绝", p, "ErrNoProcessable 不改变任何状态")
	})
}

// TestSortOracleRandomized 用确定性伪随机操作流对照排序参考模型，覆盖任意抢占/恢复交织。
func TestSortOracleRandomized(t *testing.T) {
	p := New()
	ref := &oracle{}
	nextID := 0
	advanced := 0

	// 确定性 LCG，保证测试可复现、不依赖随机种子。
	state := uint64(0x12345678)
	rand := func(n int) int {
		state = state*6364136223846793005 + 1442695040888963407
		return int((state >> 33) % uint64(n))
	}

	for stepIndex := 0; stepIndex < 600; stepIndex++ {
		if rand(4) != 0 {
			batch := make([]Event, 1+rand(3))
			for i := range batch {
				batch[i] = Event{ID: fmt.Sprintf("e%d", nextID), Priority: rand(6)}
				nextID++
			}
			if err := p.Enqueue(batch...); err != nil {
				t.Fatalf("step %d Enqueue 失败: %v", stepIndex, err)
			}
			ref.enqueue(batch...)
			continue
		}
		e, err := p.Advance()
		if errors.Is(err, ErrNoProcessable) {
			if _, ok := ref.next(); ok {
				t.Fatalf("step %d: 处理器报空但参考模型仍有事件", stepIndex)
			}
			continue
		}
		if err != nil {
			t.Fatalf("step %d Advance 失败: %v", stepIndex, err)
		}
		want, ok := ref.next()
		if !ok || want != e {
			t.Fatalf("step %d: 参考模型期望 %v(ok=%v), 实际 %v", stepIndex, want, ok, e)
		}
		advanced++
		if advanced%50 == 0 {
			if err := p.Check(); err != nil {
				t.Fatalf("step %d 自检失败: %v", stepIndex, err)
			}
		}
	}
	if advanced == 0 {
		t.Fatal("测试流没有推进任何事件")
	}
	// 排空并逐元素核对最终结果与排序参考模型一致。
	for {
		e, err := p.Advance()
		if errors.Is(err, ErrNoProcessable) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		want, ok := ref.next()
		if !ok || want != e {
			t.Fatalf("排空阶段: 参考模型期望 %v(ok=%v), 实际 %v", want, ok, e)
		}
	}
	if _, ok := ref.next(); ok {
		t.Fatal("参考模型仍有未处理事件")
	}
	processed := p.Processed()
	seen := make(map[string]bool, len(processed))
	for _, e := range processed {
		if seen[e.ID] {
			t.Fatalf("事件 %s 被处理超过一次", e.ID)
		}
		seen[e.ID] = true
	}
	if len(processed) != nextID {
		t.Fatalf("每个事件应恰好处理一次: 入队 %d, 已处理 %d", nextID, len(processed))
	}
	if err := p.Check(); err != nil {
		t.Fatalf("最终自检失败: %v", err)
	}
	logState(t, "排序对照随机流结束", p, "600 步确定性操作流与排序参考模型逐元素一致；每个标识恰好出现一次")
}

// TestConcurrentReads 验证并发读取：读到的是完整事件边界前缀，且同一实例的并发读取逐元素相同。
func TestConcurrentReads(t *testing.T) {
	p := New()
	if err := p.Enqueue(Event{"s0", 1}); err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// 读者与自检先跑完固定轮数；它们全部结束后再停写者，
	// 保证并发窗口内写者一直在产生抢占/恢复。
	var readerWg sync.WaitGroup

	// 写者：不断制造抢占与恢复。
	wg.Add(1)
	go func() {
		defer wg.Done()
		id := 1
		for id <= 300 {
			select {
			case <-stop:
				return
			default:
			}
			events := []Event{
				{ID: fmt.Sprintf("lo%d", id), Priority: 1},
				{ID: fmt.Sprintf("hi%d", id), Priority: 5},
			}
			_ = p.Enqueue(events...)
			_, _ = p.Advance()
			_, _ = p.Advance()
			_, _ = p.Advance()
			runtime.Gosched()
			id++
		}
	}()

	// 多个读者：序列只能以追加方式增长，且必须是上一次读取结果的前缀扩展。
	for reader := 0; reader < 4; reader++ {
		readerWg.Add(1)
		go func() {
			defer readerWg.Done()
			var last []Event
			for i := 0; i < 500; i++ {
				got := p.Processed()
				if last != nil {
					if len(got) < len(last) {
						t.Errorf("已处理序列缩短: %d -> %d", len(last), len(got))
						return
					}
					for j := range last {
						if got[j] != last[j] {
							t.Errorf("前缀被改写: 位置 %d %v != %v", j, got[j], last[j])
							return
						}
					}
				}
				last = got
			}
		}()
	}

	// Snapshot 与 Check 也必须能在写者运行时并发调用。
	readerWg.Add(1)
	go func() {
		defer readerWg.Done()
		for i := 0; i < 60; i++ {
			_ = p.Snapshot()
			if err := p.Check(); err != nil {
				t.Errorf("并发自检失败: %v", err)
				return
			}
		}
	}()

	readerWg.Wait()
	close(stop)
	wg.Wait()

	// 写者停止后排空，最终两次并发读取必须逐元素相同。
	for {
		if _, err := p.Advance(); errors.Is(err, ErrNoProcessable) {
			break
		}
	}
	if err := p.Check(); err != nil {
		t.Fatalf("最终自检失败: %v", err)
	}
	f1, f2 := p.Processed(), p.Processed()
	if fmt.Sprint(f1) != fmt.Sprint(f2) {
		t.Fatal("并发读同一实例结果不一致")
	}
	uniq := map[string]bool{}
	for _, e := range f1 {
		if uniq[e.ID] {
			t.Fatalf("最终序列出现重复事件 %s", e.ID)
		}
		uniq[e.ID] = true
	}
	t.Logf("并发场景结束，共处理 %d 个事件；两次读取逐元素相同，且为完整事件边界前缀", len(f1))
}
