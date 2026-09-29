package eventproc

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
)

// logState 打印一次操作后的完整现场与判定依据。
func logState(t *testing.T, op, basis string, p *Processor) {
	t.Helper()
	t.Logf("操作=%-30s | %s | 判定依据: %s", op, p.Snapshot(), basis)
}

func ev(id string, pr int) Event { return Event{ID: id, Priority: pr} }

func mustEnqueue(t *testing.T, p *Processor, events []Event) {
	t.Helper()
	if err := p.Enqueue(events); err != nil {
		t.Fatalf("Enqueue(%v) 意外失败: %v", events, err)
	}
}

func mustAdvance(t *testing.T, p *Processor) string {
	t.Helper()
	id, err := p.Advance()
	if err != nil {
		t.Fatalf("Advance 意外失败: %v", err)
	}
	return id
}

func drain(t *testing.T, p *Processor) []string {
	t.Helper()
	var got []string
	for {
		id, err := p.Advance()
		if errors.Is(err, ErrNoProcessable) {
			return got
		}
		if err != nil {
			t.Fatalf("Advance 意外失败: %v", err)
		}
		got = append(got, id)
	}
}

func assertSeq(t *testing.T, basis string, got, want []string) {
	t.Helper()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("%s: 处理顺序不符\n got=%v\nwant=%v", basis, got, want)
	}
	t.Logf("判定: %s -> %v 一致", basis, got)
}

// 优先级降序：高优先级先于低优先级处理。
func TestPriorityDescending(t *testing.T) {
	p := New()
	mustEnqueue(t, p, []Event{ev("a", 1), ev("b", 1)})
	mustEnqueue(t, p, []Event{ev("c", 5), ev("d", 0)})
	logState(t, "Enqueue p1[a,b]; p5[c]; p0[d]", "c 所在 p5 最高，应为当前批次", p)

	got := drain(t, p)
	assertSeq(t, "优先级降序", got, []string{"c", "a", "b", "d"})
	if err := p.SelfCheck(); err != nil {
		t.Fatalf("自检失败: %v", err)
	}
}

// 同优先级先进先出：跨批次严格按到达顺序。
func TestSamePriorityFIFO(t *testing.T) {
	p := New()
	mustEnqueue(t, p, []Event{ev("a", 3)})
	mustEnqueue(t, p, []Event{ev("b", 3), ev("c", 3)})
	mustEnqueue(t, p, []Event{ev("d", 3)})
	logState(t, "Enqueue a; b,c; d（均 p3）", "同优先级按到达顺序 FIFO", p)

	got := drain(t, p)
	assertSeq(t, "同优先级 FIFO", got, []string{"a", "b", "c", "d"})
}

// 抢占保存现场与恢复顺序（核心场景）。
func TestPreemptSaveAndResume(t *testing.T) {
	p := New()
	mustEnqueue(t, p, []Event{ev("a", 0), ev("b", 0), ev("c", 0)})

	if id := mustAdvance(t, p); id != "a" {
		t.Fatalf("首次推进应为 a，实际 %s", id)
	}
	logState(t, "Advance -> a", "p0 批次 cursor=1（a 已处理）", p)

	mustEnqueue(t, p, []Event{ev("x", 9), ev("y", 9)})
	snap := p.Snapshot()
	logState(t, "Enqueue p9[x,y]", "p0 应被打断压栈，栈帧 cursor=1，当前为 p9", p)
	if snap.Current == nil || snap.Current.Priority != 9 || len(snap.Current.Events) != 2 {
		t.Fatalf("抢占后当前批次应为 p9[x,y]，实际 %+v", snap.Current)
	}
	if len(snap.Stack) != 1 || snap.Stack[0].Priority != 0 || snap.Stack[0].Cursor != 1 {
		t.Fatalf("保存现场应为 (p0,cursor=1)，实际 %+v", snap.Stack)
	}

	got := drain(t, p)
	assertSeq(t, "高优先级耗尽后恢复被打断批次", got, []string{"x", "y", "b", "c"})
	logState(t, "Drain 完成", "x,y 处理完后从栈顶 p0@1 继续 b,c", p)
}

// 嵌套抢占：栈从底到顶优先级递增，按 LIFO 恢复。
func TestNestedPreemption(t *testing.T) {
	p := New()
	mustEnqueue(t, p, []Event{ev("a", 0), ev("b", 0), ev("c", 0), ev("d", 0)})
	mustAdvance(t, p) // a
	mustEnqueue(t, p, []Event{ev("x1", 2), ev("x2", 2)})
	mustAdvance(t, p) // x1
	mustEnqueue(t, p, []Event{ev("z", 5)})
	logState(t, "Enqueue p5[z]", "栈应为 [p0@1 p2@1]，当前 p5", p)

	snap := p.Snapshot()
	if len(snap.Stack) != 2 {
		t.Fatalf("嵌套打断后栈深应为 2，实际 %v", snap.Stack)
	}
	if snap.Stack[0].Priority != 0 || snap.Stack[0].Cursor != 1 ||
		snap.Stack[1].Priority != 2 || snap.Stack[1].Cursor != 1 {
		t.Fatalf("栈帧现场错误: %v", snap.Stack)
	}

	got := drain(t, p)
	assertSeq(t, "LIFO 恢复顺序", got, []string{"z", "x2", "b", "c", "d"})
}

// 高优先级到达时若低优先级恰好在事件边界耗尽，不应产生空栈帧。
func TestPreemptAtBatchBoundary(t *testing.T) {
	p := New()
	mustEnqueue(t, p, []Event{ev("a", 0)})
	mustAdvance(t, p)
	mustEnqueue(t, p, []Event{ev("x", 9)})
	snap := p.Snapshot()
	logState(t, "a 耗尽后 Enqueue p9[x]", "p0 已在边界结束，不应有保存现场", p)
	if len(snap.Stack) != 0 {
		t.Fatalf("耗尽批次不应压栈，实际 %v", snap.Stack)
	}
	assertSeq(t, "无残留现场", drain(t, p), []string{"x"})
}

// 拒绝必须整体生效且原因可区分，失败不改变任何状态。
func TestRejectionAtomicity(t *testing.T) {
	cases := []struct {
		name   string
		seed   []Event
		batch  []Event
		target error
	}{
		{"负优先级", nil, []Event{ev("x", -1)}, ErrNegativePriority},
		{"空批次", nil, nil, ErrEmptyBatch},
		{"批次内重复ID", nil, []Event{ev("x", 1), ev("x", 2)}, ErrDuplicateID},
		{"与已入队ID重复", []Event{ev("x", 1)}, []Event{ev("x", 2)}, ErrDuplicateID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := New()
			if tc.seed != nil {
				mustEnqueue(t, p, tc.seed)
			}
			before := p.Snapshot().String()
			err := p.Enqueue(tc.batch)
			if !errors.Is(err, tc.target) {
				t.Fatalf("期望原因 %v，实际 %v", tc.target, err)
			}
			after := p.Snapshot().String()
			if after != before {
				t.Fatalf("拒绝后状态被改变:\n before=%s\n after =%s", before, after)
			}
			t.Logf("操作=Enqueue 被拒绝 | 原因=%v | %s | 判定依据: 拒绝前后快照逐字段相同", err, after)
		})
	}

	// 与已处理 ID 重复、空转推进均不得改变状态。
	p := New()
	mustEnqueue(t, p, []Event{ev("a", 1)})
	mustAdvance(t, p)
	before := p.Snapshot().String()
	if err := p.Enqueue([]Event{ev("a", 2)}); !errors.Is(err, ErrDuplicateID) {
		t.Fatalf("已处理 ID 应判重，实际 %v", err)
	}
	if _, err := p.Advance(); !errors.Is(err, ErrNoProcessable) {
		t.Fatalf("无事件应返回 ErrNoProcessable，实际 %v", err)
	}
	if p.Snapshot().String() != before {
		t.Fatalf("失败操作改变了状态: %s", p.Snapshot())
	}
	t.Logf("操作=重复已处理ID + 空转Advance | %s | 判定依据: 状态不变", p.Snapshot())
}

// 每个事件恰好处理一次；相同操作序列在两个实例上结果完全一致（可复现）。
func TestExactlyOnceAndReproducible(t *testing.T) {
	scripts := [][]struct {
		enqueue []Event
		advance int
	}{
		{
			{[]Event{ev("a", 0), ev("b", 0)}, 0},
			{[]Event{ev("x", 5)}, 2},
			{[]Event{ev("m", 2), ev("n", 2)}, 2},
			{nil, 1},
		},
		{
			{[]Event{ev("a", 3)}, 1},
			{[]Event{ev("b", 1), ev("c", 3)}, 1},
			{nil, 1},
		},
	}
	for i, script := range scripts {
		p1, p2 := New(), New()
		total := 0
		for _, step := range script {
			if len(step.enqueue) > 0 {
				mustEnqueue(t, p1, step.enqueue)
				mustEnqueue(t, p2, step.enqueue)
				total += len(step.enqueue)
			}
			for k := 0; k < step.advance; k++ {
				id1, err1 := p1.Advance()
				id2, err2 := p2.Advance()
				if err1 != nil || err2 != nil {
					t.Fatalf("脚本%d 推进出错: %v %v", i+1, err1, err2)
				}
				if id1 != id2 {
					t.Fatalf("两实例同位置输出不一致: %s vs %s", id1, id2)
				}
			}
		}
		r1, r2 := p1.Processed(), p2.Processed()
		if fmt.Sprint(r1) != fmt.Sprint(r2) {
			t.Fatalf("两实例结果不一致: %v vs %v", r1, r2)
		}
		if len(r1) != total {
			t.Fatalf("事件数不符: 处理 %d，提交 %d", len(r1), total)
		}
		seen := map[string]bool{}
		for _, id := range r1 {
			if seen[id] {
				t.Fatalf("事件 %s 被处理超过一次", id)
			}
			seen[id] = true
		}
		if err := p1.SelfCheck(); err != nil {
			t.Fatalf("自检失败: %v", err)
		}
		t.Logf("脚本%d 完成 | %s | 判定依据: 两实例逐事件一致、无重复、长度=%d",
			i+1, p1.Snapshot(), total)
	}
}

// 并发读：任意读到的都是完整事件边界前缀，且读者间逐元素相同。
func TestConcurrentReaders(t *testing.T) {
	p := New()
	const total = 200
	for i := 0; i < total; i++ {
		mustEnqueue(t, p, []Event{ev(fmt.Sprintf("e%d", i), i%5)})
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	var reads [][]string
	stop := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			if _, err := p.Advance(); errors.Is(err, ErrNoProcessable) {
				close(stop)
				return
			}
		}
	}()

	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					got := p.Processed()
					cp := append([]string(nil), got...)
					mu.Lock()
					reads = append(reads, cp)
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()

	final := p.Processed()
	if len(final) != total {
		t.Fatalf("最终应处理 %d，实际 %d", total, len(final))
	}
	for _, r := range reads {
		if len(r) > len(final) {
			t.Fatalf("读到比最终序列更长的结果: %d", len(r))
		}
		for i := range r {
			if r[i] != final[i] {
				t.Fatalf("位置 %d 读到 %s，与最终 %s 不一致", i, r[i], final[i])
			}
		}
	}
	t.Logf("并发读完成 | 采样=%d 份, 最终长度=%d | 判定依据: 每份采样均为最终序列的完整事件边界前缀",
		len(reads), len(final))
}

// ---- 排序对照参考模型 ----
//
// oracleBatch 是独立于实现的参考批次：不使用栈，
// 每次推进前把所有“可见批次的队头候选”排序后取第一名：
// 优先级降序 -> 到达序号升序（FIFO）-> 当前批次优先（恢复最近现场）。
type oracleBatch struct {
	events   []Event
	cursor   int
	seq      int
	priority int
	current  bool
}

type oracle struct {
	batches []*oracleBatch
	done    []string
	seq     int
}

func (o *oracle) enqueue(events []Event) {
	o.seq++
	// 与实现相同：按连续优先级归组，保持组内 FIFO。
	var groups []*oracleBatch
	for _, ev := range events {
		if n := len(groups); n > 0 && groups[n-1].priority == ev.Priority {
			groups[n-1].events = append(groups[n-1].events, ev)
		} else {
			groups = append(groups, &oracleBatch{
				events: []Event{ev}, seq: o.seq, priority: ev.Priority,
			})
		}
	}

	var highest *oracleBatch
	for _, g := range groups {
		if highest == nil || g.priority > highest.priority {
			highest = g
		}
	}

	var active *oracleBatch
	for _, b := range o.batches {
		if b.cursor < len(b.events) && b.current {
			active = b
		}
	}

	o.batches = append(o.batches, groups...)

	if active != nil && highest.priority > active.priority {
		active.current = false
		highest.current = true
	} else if active == nil {
		highest.current = true
	}
}

func (o *oracle) advance() (string, bool) {
	var candidates []*oracleBatch
	for _, b := range o.batches {
		if b.cursor < len(b.events) {
			candidates = append(candidates, b)
		}
	}
	if len(candidates) == 0 {
		return "", false
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.priority != b.priority {
			return a.priority > b.priority
		}
		if a.seq != b.seq {
			return a.seq < b.seq
		}
		// 同优先级同到达批次内 cursor 小者在前（稳定排序天然满足）。
		return a.current && !b.current
	})
	chosen := candidates[0]
	for _, b := range o.batches {
		b.current = b == chosen
	}
	id := chosen.events[chosen.cursor].ID
	chosen.cursor++
	o.done = append(o.done, id)
	return id, true
}

// 排序对照：独立参考模型逐步核对处理器输出。
func TestSortingOracleCrossCheck(t *testing.T) {
	type step struct {
		enqueue []Event
		advance int
	}
	scripts := []struct {
		name  string
		steps []step
	}{
		{
			"抢占+恢复",
			[]step{
				{[]Event{ev("a", 0), ev("b", 0), ev("c", 0)}, 1},
				{[]Event{ev("x", 9), ev("y", 9)}, 3},
				{[]Event{ev("m", 5)}, 5},
			},
		},
		{
			"嵌套抢占与同优先级FIFO",
			[]step{
				{[]Event{ev("a", 1), ev("b", 1)}, 1},
				{[]Event{ev("x", 4), ev("y", 4)}, 1},
				{[]Event{ev("z", 8)}, 1},
				{[]Event{ev("q", 4)}, 10},
			},
		},
		{
			"同优先级新批次不抢占",
			[]step{
				{[]Event{ev("a", 2), ev("b", 2)}, 1},
				{[]Event{ev("c", 2), ev("d", 1)}, 4},
			},
		},
	}
	for _, script := range scripts {
		t.Run(script.name, func(t *testing.T) {
			p, or := New(), &oracle{}
			var ref []string
			for _, st := range script.steps {
				if len(st.enqueue) > 0 {
					mustEnqueue(t, p, st.enqueue)
					or.enqueue(st.enqueue)
				}
				for k := 0; k < st.advance; k++ {
					id, err := p.Advance()
					want, ok := or.advance()
					if err != nil && ok {
						t.Fatalf("处理器报无可处理事件，但参考模型期望 %s", want)
					}
					if err == nil {
						ref = append(ref, want)
						if id != want {
							t.Fatalf("排序对照不符: 处理器=%s 参考模型=%s", id, want)
						}
					}
				}
			}
			if _, err := p.Advance(); !errors.Is(err, ErrNoProcessable) {
				t.Fatalf("参考脚本结束后应全部耗尽")
			}
			assertSeq(t, "排序对照("+script.name+")", p.Processed(), ref)
			t.Logf("参考模型输出=%v | %s | 判定依据: 处理器输出与候选排序参考模型逐步一致",
				ref, p.Snapshot())
		})
	}
}
