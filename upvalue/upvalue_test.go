package upvalue

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

func mustCapture(t *testing.T, m *Manager, slot int) Handle {
	t.Helper()
	h, err := m.Capture(slot)
	if err != nil {
		t.Fatalf("Capture(%d) 意外失败: %v", slot, err)
	}
	return h
}

func mustRead(t *testing.T, m *Manager, h Handle) Value {
	t.Helper()
	v, err := m.Read(h)
	if err != nil {
		t.Fatalf("Read(%d) 意外失败: %v", h, err)
	}
	return v
}

func mustReadStack(t *testing.T, m *Manager, slot int) Value {
	t.Helper()
	v, err := m.ReadStack(slot)
	if err != nil {
		t.Fatalf("ReadStack(%d) 意外失败: %v", slot, err)
	}
	return v
}

// 1. 同槽两次捕获共享同一句柄。
func TestCaptureSameSlotShares(t *testing.T) {
	m := New()
	m.Push(10)
	h1 := mustCapture(t, m, 0)
	h2 := mustCapture(t, m, 0)
	t.Logf("输入: Push(10), Capture(0)x2; 输出: h1=%d h2=%d", h1, h2)
	if h1 != h2 {
		t.Fatalf("判定依据: 同槽已有开放变量应返回同一句柄, 实际 h1=%d h2=%d", h1, h2)
	}
	if h1 != 1 {
		t.Fatalf("判定依据: 句柄从 1 起递增, 实际首个句柄为 %d", h1)
	}
	// 释放一次后持有数仍为 1，共享关系保持。
	if err := m.Release(h1); err != nil {
		t.Fatalf("Release 意外失败: %v", err)
	}
	h3 := mustCapture(t, m, 0)
	t.Logf("输入: Release(h1), Capture(0); 输出: h3=%d", h3)
	if h3 != h1 {
		t.Fatalf("判定依据: 持有数未到 0 时同槽再捕获仍共享, 实际 h3=%d h1=%d", h3, h1)
	}
}

// 2. 写经开放变量改栈槽；栈槽直接写对开放变量可见。
func TestOpenWriteThrough(t *testing.T) {
	m := New()
	m.Push(1)
	h := mustCapture(t, m, 0)
	if err := m.Write(h, 42); err != nil {
		t.Fatalf("Write 意外失败: %v", err)
	}
	got := mustReadStack(t, m, 0)
	t.Logf("输入: Write(h,42); 输出: ReadStack(0)=%d", got)
	if got != 42 {
		t.Fatalf("判定依据: 开放变量写应直接作用于栈槽, 实际栈槽=%d", got)
	}
	if err := m.WriteStack(0, 77); err != nil {
		t.Fatalf("WriteStack 意外失败: %v", err)
	}
	got = mustRead(t, m, h)
	t.Logf("输入: WriteStack(0,77); 输出: Read(h)=%d", got)
	if got != 77 {
		t.Fatalf("判定依据: 栈槽直接写应对开放变量可见, 实际读到 %d", got)
	}
}

// 3. 关闭后变量与栈互不影响。
func TestCloseIndependence(t *testing.T) {
	m := New()
	m.Push(5)
	h := mustCapture(t, m, 0)
	if err := m.Close(0); err != nil {
		t.Fatalf("Close 意外失败: %v", err)
	}
	if got := m.Top(); got != 0 {
		t.Fatalf("判定依据: 关闭后栈顶应设为 level=0, 实际 %d", got)
	}
	m.Push(100)
	if got := mustRead(t, m, h); got != 5 {
		t.Fatalf("判定依据: 关闭变量保存关闭时刻的值 5, 实际 %d", got)
	}
	if err := m.Write(h, 9); err != nil {
		t.Fatalf("Write 意外失败: %v", err)
	}
	if got := mustReadStack(t, m, 0); got != 100 {
		t.Fatalf("判定依据: 关闭变量写不影响栈槽, 实际栈槽=%d", got)
	}
	t.Logf("输入: Close(0) 后 Push(100), Write(h,9); 输出: Read(h)=%d, ReadStack(0)=%d",
		mustRead(t, m, h), mustReadStack(t, m, 0))
}

// 4. 关闭层恰等于栈顶是合法空操作。
func TestCloseLevelEqualsTop(t *testing.T) {
	m := New()
	m.Push(1)
	m.Push(2)
	h := mustCapture(t, m, 1)
	if err := m.Close(2); err != nil {
		t.Fatalf("判定依据: Close(栈顶) 应合法, 实际报错 %v", err)
	}
	if got := m.Top(); got != 2 {
		t.Fatalf("判定依据: 空操作后栈顶不变, 实际 %d", got)
	}
	// 槽 1 的变量仍开放：写栈槽对其可见。
	if err := m.WriteStack(1, 20); err != nil {
		t.Fatalf("WriteStack 意外失败: %v", err)
	}
	if got := mustRead(t, m, h); got != 20 {
		t.Fatalf("判定依据: Close(栈顶) 不关闭任何变量, 实际读到 %d", got)
	}
	t.Logf("输入: Close(2)（恰等于栈顶）; 输出: top=%d, Read(h)=%d", m.Top(), mustRead(t, m, h))
}

// 5. 关闭后同槽重新压栈并捕获得到新句柄。
func TestRecaptureAfterCloseNewHandle(t *testing.T) {
	m := New()
	m.Push(7)
	h1 := mustCapture(t, m, 0)
	if err := m.Close(0); err != nil {
		t.Fatalf("Close 意外失败: %v", err)
	}
	m.Push(70)
	h2 := mustCapture(t, m, 0)
	t.Logf("输入: Close(0), Push(70), Capture(0); 输出: h1=%d h2=%d", h1, h2)
	if h2 == h1 {
		t.Fatalf("判定依据: 关闭后同槽再捕获应得新句柄, 实际均为 %d", h1)
	}
	if got := mustRead(t, m, h1); got != 7 {
		t.Fatalf("判定依据: 旧句柄保留关闭时刻的值 7, 实际 %d", got)
	}
	if got := mustRead(t, m, h2); got != 70 {
		t.Fatalf("判定依据: 新句柄共享新栈槽值 70, 实际 %d", got)
	}
}

// 6. 持有数减到 0 后从共享表摘除，再捕获得到新句柄。
func TestReleaseToZeroRemovesShared(t *testing.T) {
	m := New()
	m.Push(3)
	h1 := mustCapture(t, m, 0)
	if err := m.Release(h1); err != nil {
		t.Fatalf("Release 意外失败: %v", err)
	}
	h2 := mustCapture(t, m, 0)
	t.Logf("输入: Release(h1) 至持有数 0, Capture(0); 输出: h1=%d h2=%d", h1, h2)
	if h2 == h1 {
		t.Fatalf("判定依据: 摘除后同槽再捕获应得新句柄, 实际均为 %d", h1)
	}
	// 已释放完的句柄读写均无效。
	if _, err := m.Read(h1); !errors.Is(err, ErrHandleReleased) {
		t.Fatalf("判定依据: 已释放完句柄读应报 ErrHandleReleased, 实际 %v", err)
	}
	if err := m.Write(h1, 1); !errors.Is(err, ErrHandleReleased) {
		t.Fatalf("判定依据: 已释放完句柄写应报 ErrHandleReleased, 实际 %v", err)
	}
	if err := m.Release(h1); !errors.Is(err, ErrHandleReleased) {
		t.Fatalf("判定依据: 已释放完句柄再释放应报 ErrHandleReleased, 实际 %v", err)
	}
}

// 7. 两个层级的部分关闭：只关闭槽号不小于 level 的变量。
func TestPartialCloseTwoLevels(t *testing.T) {
	m := New()
	m.Push(10) // 槽 0
	m.Push(11) // 槽 1
	m.Push(12) // 槽 2
	low := mustCapture(t, m, 1)
	high := mustCapture(t, m, 2)
	if err := m.Close(2); err != nil {
		t.Fatalf("Close(2) 意外失败: %v", err)
	}
	t.Logf("输入: Close(2); 输出: top=%d", m.Top())
	if got := m.Top(); got != 2 {
		t.Fatalf("判定依据: Close(2) 后栈顶为 2, 实际 %d", got)
	}
	// 槽 2 的变量已关闭，保存值 12。
	if got := mustRead(t, m, high); got != 12 {
		t.Fatalf("判定依据: 槽 2 变量已关闭并保存 12, 实际 %d", got)
	}
	// 槽 1 的变量仍开放，与栈槽共享。
	if err := m.Write(low, 111); err != nil {
		t.Fatalf("Write 意外失败: %v", err)
	}
	if got := mustReadStack(t, m, 1); got != 111 {
		t.Fatalf("判定依据: 槽 1 变量仍开放, 写应落到栈槽, 实际栈槽=%d", got)
	}
	// 第二层关闭。
	if err := m.Close(1); err != nil {
		t.Fatalf("Close(1) 意外失败: %v", err)
	}
	if err := m.WriteStack(0, 999); err != nil {
		t.Fatalf("WriteStack 意外失败: %v", err)
	}
	if got := mustRead(t, m, low); got != 111 {
		t.Fatalf("判定依据: Close(1) 后槽 1 变量已关闭并保存 111, 实际 %d", got)
	}
	t.Logf("输入: Close(1), WriteStack(0,999); 输出: Read(low)=%d, Read(high)=%d",
		mustRead(t, m, low), mustRead(t, m, high))
}

// 8. 各类拒绝原因可区分，且被拒绝的操作不改变任何状态。
func TestRejections(t *testing.T) {
	m := New()
	m.Push(1)
	m.Push(2)
	h := mustCapture(t, m, 0)

	snapshot := func() string {
		return fmt.Sprintf("top=%d s0=%d s1=%d read(h)=%d",
			m.Top(), mustReadStack(t, m, 0), mustReadStack(t, m, 1), mustRead(t, m, h))
	}
	before := snapshot()
	reject := func(name string, err, want error) {
		t.Helper()
		if !errors.Is(err, want) {
			t.Fatalf("%s: 判定依据: 应报 %v, 实际 %v", name, want, err)
		}
		if after := snapshot(); after != before {
			t.Fatalf("%s: 判定依据: 被拒绝的操作不得改变状态, 前=%s 后=%s", name, before, after)
		}
		t.Logf("输入: %s; 输出: %v; 状态保持: %s", name, err, before)
	}

	_, err := m.Capture(2)
	reject("Capture(2)（槽号等于栈顶）", err, ErrSlotOutOfRange)
	_, err = m.Capture(-1)
	reject("Capture(-1)", err, ErrSlotOutOfRange)
	reject("Close(-1)", m.Close(-1), ErrInvalidLevel)
	reject("Close(3)（大于栈顶）", m.Close(3), ErrInvalidLevel)
	_, err = m.ReadStack(2)
	reject("ReadStack(2)", err, ErrStackOutOfRange)
	reject("WriteStack(5,0)", m.WriteStack(5, 0), ErrStackOutOfRange)
	_, err = m.Read(999)
	reject("Read(999)（句柄不存在）", err, ErrHandleNotFound)
	reject("Write(999,0)", m.Write(999, 0), ErrHandleNotFound)
	reject("Release(999)", m.Release(999), ErrHandleNotFound)

	// 句柄不存在与已释放完互斥：释放完后同一柄报 ErrHandleReleased 而非 ErrHandleNotFound。
	if err := m.Release(h); err != nil {
		t.Fatalf("Release 意外失败: %v", err)
	}
	if _, err := m.Read(h); !errors.Is(err, ErrHandleReleased) {
		t.Fatalf("判定依据: 已释放完句柄读应报 ErrHandleReleased, 实际 %v", err)
	}
	if _, err := m.Read(h + 1000); !errors.Is(err, ErrHandleNotFound) {
		t.Fatalf("判定依据: 与已释放完互斥, 不存在的句柄仍报 ErrHandleNotFound, 实际 %v", err)
	}
	t.Logf("输入: Read(已释放完句柄); 输出: ErrHandleReleased（与 ErrHandleNotFound 互斥）")
}

// naive 是按规则逐步写成的朴素模拟，用于对照。
type naive struct {
	stack  []Value
	open   map[int]int
	refs   map[int]int
	closed map[int]bool
	stored map[int]Value
	slotOf map[int]int
	next   int
}

func newNaive() *naive {
	return &naive{
		open:   make(map[int]int),
		refs:   make(map[int]int),
		closed: make(map[int]bool),
		stored: make(map[int]Value),
		slotOf: make(map[int]int),
		next:   1,
	}
}

func (n *naive) push(v Value) int {
	n.stack = append(n.stack, v)
	return len(n.stack) - 1
}

func (n *naive) capture(slot int) (int, error) {
	if slot < 0 || slot >= len(n.stack) {
		return 0, ErrSlotOutOfRange
	}
	if id, ok := n.open[slot]; ok {
		n.refs[id]++
		return id, nil
	}
	id := n.next
	n.next++
	n.open[slot] = id
	n.refs[id] = 1
	n.slotOf[id] = slot
	return id, nil
}

func (n *naive) readStack(slot int) (Value, error) {
	if slot < 0 || slot >= len(n.stack) {
		return 0, ErrStackOutOfRange
	}
	return n.stack[slot], nil
}

func (n *naive) writeStack(slot int, v Value) error {
	if slot < 0 || slot >= len(n.stack) {
		return ErrStackOutOfRange
	}
	n.stack[slot] = v
	return nil
}

func (n *naive) close(level int) error {
	if level < 0 || level > len(n.stack) {
		return ErrInvalidLevel
	}
	for slot, id := range n.open {
		if slot >= level {
			n.stored[id] = n.stack[slot]
			n.closed[id] = true
			delete(n.open, slot)
		}
	}
	n.stack = n.stack[:level]
	return nil
}

func (n *naive) check(id int) error {
	if _, ok := n.refs[id]; !ok {
		return ErrHandleNotFound
	}
	if n.refs[id] == 0 {
		return ErrHandleReleased
	}
	return nil
}

func (n *naive) read(id int) (Value, error) {
	if err := n.check(id); err != nil {
		return 0, err
	}
	if n.closed[id] {
		return n.stored[id], nil
	}
	return n.stack[n.slotOf[id]], nil
}

func (n *naive) write(id int, v Value) error {
	if err := n.check(id); err != nil {
		return err
	}
	if n.closed[id] {
		n.stored[id] = v
		return nil
	}
	n.stack[n.slotOf[id]] = v
	return nil
}

func (n *naive) release(id int) error {
	if err := n.check(id); err != nil {
		return err
	}
	n.refs[id]--
	if n.refs[id] == 0 && !n.closed[id] {
		delete(n.open, n.slotOf[id])
	}
	return nil
}

// result 统一记录一步操作的输出，便于逐步对照。
type result struct {
	val Value
	err error
}

func (r result) String() string {
	if r.err != nil {
		return "err:" + r.err.Error()
	}
	return fmt.Sprintf("ok:%d", r.val)
}

// apply 在真实管理器上执行一步操作。
func apply(m *Manager, name string, a, b int) result {
	switch name {
	case "push":
		return result{val: Value(m.Push(Value(b)))}
	case "capture":
		h, err := m.Capture(a)
		return result{val: Value(h), err: err}
	case "readStack":
		v, err := m.ReadStack(a)
		return result{val: v, err: err}
	case "writeStack":
		return result{err: m.WriteStack(a, Value(b))}
	case "close":
		return result{err: m.Close(a)}
	case "read":
		v, err := m.Read(Handle(a))
		return result{val: v, err: err}
	case "write":
		return result{err: m.Write(Handle(a), Value(b))}
	case "release":
		return result{err: m.Release(Handle(a))}
	}
	panic("unknown op " + name)
}

// applyNaive 在朴素模拟上执行同一步操作。
func applyNaive(n *naive, name string, a, b int) result {
	switch name {
	case "push":
		return result{val: Value(n.push(Value(b)))}
	case "capture":
		h, err := n.capture(a)
		return result{val: Value(h), err: err}
	case "readStack":
		v, err := n.readStack(a)
		return result{val: v, err: err}
	case "writeStack":
		return result{err: n.writeStack(a, Value(b))}
	case "close":
		return result{err: n.close(a)}
	case "read":
		v, err := n.read(a)
		return result{val: v, err: err}
	case "write":
		return result{err: n.write(a, Value(b))}
	case "release":
		return result{err: n.release(a)}
	}
	panic("unknown op " + name)
}

type step struct {
	name string
	a, b int
}

// genSteps 用固定种子生成随机操作序列，生成时同步预演以掌握栈顶与
// 已发句柄上界，同时有意越界以覆盖拒绝路径。
func genSteps(r *rand.Rand, count int) []step {
	steps := make([]step, 0, count)
	sim := newNaive()
	names := []string{"push", "capture", "readStack", "writeStack", "close", "read", "write", "release"}
	for i := 0; i < count; i++ {
		name := names[r.Intn(len(names))]
		top := len(sim.stack)
		maxHandle := sim.next - 1
		s := step{name: name}
		switch name {
		case "push":
			s.b = r.Intn(1000)
		case "capture", "readStack":
			s.a = r.Intn(top + 2) // 可能越界
		case "writeStack":
			s.a = r.Intn(top + 2)
			s.b = r.Intn(1000)
		case "close":
			s.a = r.Intn(top+3) - 1 // 可能是 -1、恰等于栈顶或大于栈顶
		case "read", "release":
			s.a = 1 + r.Intn(maxHandle+2) // 可能不存在或已释放完
		case "write":
			s.a = 1 + r.Intn(maxHandle+2)
			s.b = r.Intn(1000)
		}
		applyNaive(sim, s.name, s.a, s.b)
		steps = append(steps, s)
	}
	return steps
}

// 9. 随机操作序列与朴素模拟逐步对照，日志打印输入、输出与判定依据。
func TestModelComparison(t *testing.T) {
	r := rand.New(rand.NewSource(20261001))
	steps := genSteps(r, 500)
	m := New()
	n := newNaive()
	for i, s := range steps {
		got := apply(m, s.name, s.a, s.b)
		want := applyNaive(n, s.name, s.a, s.b)
		if i%50 == 0 || got != want {
			t.Logf("步骤 %d 输入: %s(%d,%d); 输出: 实际=%s 期望=%s; 判定依据: 与朴素模拟逐步一致",
				i, s.name, s.a, s.b, got, want)
		}
		if got != want {
			t.Fatalf("步骤 %d %s(%d,%d): 实际=%s 期望=%s", i, s.name, s.a, s.b, got, want)
		}
	}
	if got, want := m.Top(), len(n.stack); got != want {
		t.Fatalf("判定依据: 最终栈顶一致, 实际=%d 期望=%d", got, want)
	}
	t.Logf("对照完成: 共 %d 步, 最终栈顶=%d", len(steps), m.Top())
}

// 10. 相同操作序列重放得到完全相同的句柄与取值。
func TestReplayDeterminism(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	steps := genSteps(r, 300)
	run := func() []result {
		m := New()
		out := make([]result, len(steps))
		for i, s := range steps {
			out[i] = apply(m, s.name, s.a, s.b)
		}
		return out
	}
	first, second := run(), run()
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("步骤 %d 重放不一致: 首次=%s 重放=%s", i, first[i], second[i])
		}
	}
	t.Logf("重放 %d 步两次, 输出逐位一致", len(steps))
}

// 11. 并发调用等价于某个串行顺序：每个 goroutine 独占自己的槽，
// 经开放变量写入后从栈槽读回必须一致；共享槽的并发捕获必须得到同一句柄。
func TestConcurrent(t *testing.T) {
	m := New()
	const workers = 8
	const perWorker = 50

	var wg sync.WaitGroup
	errs := make(chan error, workers*perWorker)

	// 共享槽：先压栈，所有 goroutine 并发捕获，必须得到同一句柄。
	m.Push(-1)
	shared := make(chan Handle, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			h, err := m.Capture(0)
			if err != nil {
				errs <- err
				return
			}
			shared <- h
			for i := 0; i < perWorker; i++ {
				slot := m.Push(Value(w*perWorker + i))
				h, err := m.Capture(slot)
				if err != nil {
					errs <- err
					return
				}
				if err := m.Write(h, Value(slot*10)); err != nil {
					errs <- err
					return
				}
				got, err := m.ReadStack(slot)
				if err != nil {
					errs <- err
					return
				}
				if got != Value(slot*10) {
					errs <- fmt.Errorf("槽 %d 经开放变量写入后读回 %d", slot, got)
					return
				}
				if err := m.Release(h); err != nil {
					errs <- err
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(shared)
	close(errs)
	for err := range errs {
		t.Fatalf("并发执行出错: %v", err)
	}
	var first Handle
	firstSet := false
	for h := range shared {
		if !firstSet {
			first, firstSet = h, true
			continue
		}
		if h != first {
			t.Fatalf("判定依据: 同一槽至多一个共享开放变量, 并发捕获得到不同句柄 %d 与 %d", first, h)
		}
	}
	if got, want := m.Top(), 1+workers*perWorker; got != want {
		t.Fatalf("判定依据: 栈顶等于压栈总数, 实际=%d 期望=%d", got, want)
	}
	t.Logf("并发完成: %d 个 goroutine 各压 %d 个槽, 共享槽句柄=%d, 栈顶=%d",
		workers, perWorker, first, m.Top())
}
