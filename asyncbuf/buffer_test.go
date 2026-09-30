package asyncbuf

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"
)

// ---------- 输出与步骤的日志辅助 ----------

func describeOutputs(outs []Output) string {
	if len(outs) == 0 {
		return "(无输出)"
	}
	var sb strings.Builder
	for i, o := range outs {
		if i > 0 {
			sb.WriteString(" ")
		}
		if o.Kind == WatermarkOutput {
			fmt.Fprintf(&sb, "WM(%d)#%d", o.Watermark, o.Index)
		} else {
			fmt.Fprintf(&sb, "Elem(%s)#%d", o.ID, o.Index)
		}
	}
	return sb.String()
}

func logStep(t *testing.T, mode Mode, step int, op string, err error, outs []Output, why string) {
	t.Helper()
	res := "OK"
	if err != nil {
		res = "拒绝(" + err.Error() + ")"
	}
	t.Logf("[%s] 步骤%02d 输入=%-24s 结果=%s 输出=%s 判定依据=%s",
		mode, step, op, res, describeOutputs(outs), why)
}

func outputsEqual(a, b []Output) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ---------- 场景 1：有序模式严格保序 ----------

func TestOrderedPreservesInputOrder(t *testing.T) {
	b, err := New(Ordered, 8)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	step := 0
	run := func(op string, fn func() error, why string) []Output {
		step++
		err := fn()
		outs := b.Drain()
		logStep(t, Ordered, step, op, err, outs, why)
		if err != nil {
			t.Fatalf("步骤%d 意外拒绝: %v", step, err)
		}
		return outs
	}

	run(`Submit(a)`, func() error { return b.Submit("a", 1) }, "入队，队头 a 未完成，阻塞")
	run(`Submit(b)`, func() error { return b.Submit("b", 2) }, "入队，排在 a 后")
	run(`Submit(c)`, func() error { return b.Submit("c", 3) }, "入队，排在 b 后")

	// 乱序完成：先完成 c、b，但队头 a 未完成，全部阻塞。
	if outs := run(`Complete(c)`, func() error { return b.Complete("c") }, "c 完成但非队头，保序阻塞"); len(outs) != 0 {
		t.Fatalf("保序被违反：%v", outs)
	}
	if outs := run(`Complete(b)`, func() error { return b.Complete("b") }, "b 完成但非队头，保序阻塞"); len(outs) != 0 {
		t.Fatalf("保序被违反：%v", outs)
	}

	// 完成队头 a，应一次性按输入顺序吐出 a,b,c。
	outs := run(`Complete(a)`, func() error { return b.Complete("a") }, "队头 a 完成，级联输出 a,b,c")
	want := []Output{
		{Kind: ElementOutput, ID: "a", Value: 1, Index: 0},
		{Kind: ElementOutput, ID: "b", Value: 2, Index: 1},
		{Kind: ElementOutput, ID: "c", Value: 3, Index: 2},
	}
	if !outputsEqual(outs, want) {
		t.Fatalf("有序输出不符\n got=%v\nwant=%v", outs, want)
	}
	if got := b.Occupied(); got != 0 {
		t.Fatalf("占用应为 0，实际 %d", got)
	}
}

// ---------- 场景 2：无序模式跨段扣留与级联释放 ----------

func TestUnorderedCrossSegmentHoldAndCascade(t *testing.T) {
	b, err := New(Unordered, 16)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	step := 0
	run := func(op string, fn func() error, why string) []Output {
		step++
		err := fn()
		outs := b.Drain()
		logStep(t, Unordered, step, op, err, outs, why)
		if err != nil {
			t.Fatalf("步骤%d 意外拒绝: %v", step, err)
		}
		return outs
	}

	// 段1: [a,b]  段2: [c,d]  段3: [e]
	run(`Submit(a)`, func() error { return b.Submit("a", 1) }, "段1 开放，a 未完成")
	run(`Submit(b)`, func() error { return b.Submit("b", 2) }, "段1 开放，b 未完成")
	run(`Watermark(10)`, func() error { return b.SubmitWatermark(10) }, "段1 未排空，水位线被屏障挡住")
	run(`Submit(c)`, func() error { return b.Submit("c", 3) }, "段2 未开放")
	run(`Submit(d)`, func() error { return b.Submit("d", 4) }, "段2 未开放")
	run(`Watermark(20)`, func() error { return b.SubmitWatermark(20) }, "段2 屏障")
	run(`Submit(e)`, func() error { return b.Submit("e", 5) }, "段3 未开放")

	// 段2、段3 的元素先完成，应被扣住（未开放）。
	if outs := run(`Complete(c)`, func() error { return b.Complete("c") }, "段2 未开放，c 完成被扣"); len(outs) != 0 {
		t.Fatalf("未开放段元素被提前输出: %v", outs)
	}
	if outs := run(`Complete(e)`, func() error { return b.Complete("e") }, "段3 未开放，e 完成被扣"); len(outs) != 0 {
		t.Fatalf("未开放段元素被提前输出: %v", outs)
	}
	if outs := run(`Complete(d)`, func() error { return b.Complete("d") }, "段2 未开放，d 完成被扣"); len(outs) != 0 {
		t.Fatalf("未开放段元素被提前输出: %v", outs)
	}

	// 段1 内乱序完成：先 b 后 a，段内按完成先后输出。
	outs := run(`Complete(b)`, func() error { return b.Complete("b") }, "段1 开放，b 完成即输出")
	if !outputsEqual(outs, []Output{{Kind: ElementOutput, ID: "b", Value: 2, Index: 1}}) {
		t.Fatalf("段内应按完成先后输出 b，实际 %v", outs)
	}

	// 完成 a：段1 排空 -> WM(10) 输出 -> 段2 开放，按完成先后释放 c,d
	//          -> 段2 排空 -> WM(20) 输出 -> 段3 开放，释放 e（级联）。
	outs = run(`Complete(a)`, func() error { return b.Complete("a") },
		"段1 排空触发级联：a,WM10,c,d,WM20,e")
	want := []Output{
		{Kind: ElementOutput, ID: "a", Value: 1, Index: 0},
		{Kind: WatermarkOutput, Watermark: 10, Index: 2},
		{Kind: ElementOutput, ID: "c", Value: 3, Index: 3}, // c 先于 d 完成
		{Kind: ElementOutput, ID: "d", Value: 4, Index: 4},
		{Kind: WatermarkOutput, Watermark: 20, Index: 5},
		{Kind: ElementOutput, ID: "e", Value: 5, Index: 6},
	}
	if !outputsEqual(outs, want) {
		t.Fatalf("级联释放顺序不符\n got=%v\nwant=%v", outs, want)
	}
	if got := b.Occupied(); got != 0 {
		t.Fatalf("占用应为 0，实际 %d", got)
	}
	if got := b.Pending(); got != 0 {
		t.Fatalf("队列应为空，实际 %d", got)
	}
}

// ---------- 场景 3：占用数边界 ----------

func TestOccupancyBoundary(t *testing.T) {
	const cap = 2
	b, err := New(Unordered, cap)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	step := 0
	run := func(op string, fn func() error, why string) (error, []Output) {
		step++
		err := fn()
		outs := b.Drain()
		logStep(t, Unordered, step, op, err, outs, why)
		return err, outs
	}

	mustOK := func(err error, _ []Output) {
		t.Helper()
		if err != nil {
			t.Fatalf("意外拒绝: %v", err)
		}
	}

	mustOK(run(`Submit(a)`, func() error { return b.Submit("a", 1) }, "占用 0->1"))
	mustOK(run(`Submit(b)`, func() error { return b.Submit("b", 2) }, "占用 1->2，达到容量"))
	if got := b.Occupied(); got != cap {
		t.Fatalf("占用应为 %d，实际 %d", cap, got)
	}

	// 容量已满：再提交应被拒绝，且占用不变。
	err, _ = run(`Submit(c)`, func() error { return b.Submit("c", 3) }, "容量已满，拒绝")
	if !errors.Is(err, errCapacityFull) {
		t.Fatalf("应为容量满错误，实际 %v", err)
	}
	if got := b.Occupied(); got != cap {
		t.Fatalf("拒绝后占用不应改变，实际 %d", got)
	}

	// 完成 a 并输出后占用下降，可再提交。
	err, outs := run(`Complete(a)`, func() error { return b.Complete("a") }, "a 完成输出，占用 2->1")
	mustOK(err, outs)
	if !outputsEqual(outs, []Output{{Kind: ElementOutput, ID: "a", Value: 1, Index: 0}}) {
		t.Fatalf("应输出 a，实际 %v", outs)
	}
	if got := b.Occupied(); got != 1 {
		t.Fatalf("占用应为 1，实际 %d", got)
	}
	mustOK(run(`Submit(c)`, func() error { return b.Submit("c", 3) }, "有空间，占用 1->2"))
	if got := b.Occupied(); got != cap {
		t.Fatalf("占用应回到 %d，实际 %d", cap, got)
	}
}

// ---------- 场景 4：非法输入类别可区分 + 拒绝后状态不变 ----------

func TestRejectionsAreDistinctAndLeaveNoTrace(t *testing.T) {
	b, err := New(Ordered, 2)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// 预置：a 占用一格，w=5 已提交。
	if err := b.Submit("a", 1); err != nil {
		t.Fatalf("Submit a: %v", err)
	}
	if err := b.SubmitWatermark(5); err != nil {
		t.Fatalf("SubmitWatermark 5: %v", err)
	}
	if err := b.Submit("b", 2); err != nil { // 占满容量 2
		t.Fatalf("Submit b: %v", err)
	}

	snapshot := func() (int, int, int) { return b.Occupied(), b.Pending(), b.Buffered() }
	before := func() (int, int, int) { return snapshot() }

	type tc struct {
		name string
		op   string
		fn   func() error
		want error
	}
	cases := []tc{
		{"空标识", `Submit("")`, func() error { return b.Submit("", 9) }, errEmptyID},
		{"重复标识", `Submit(a)`, func() error { return b.Submit("a", 9) }, errDuplicateID},
		{"容量已满", `Submit(z)`, func() error { return b.Submit("z", 9) }, errCapacityFull},
		{"未知标识", `Complete(ghost)`, func() error { return b.Complete("ghost") }, errUnknownID},
		{"水位线非递增-相等", `Watermark(5)`, func() error { return b.SubmitWatermark(5) }, errWatermarkNotMonotonic},
		{"水位线非递增-回退", `Watermark(3)`, func() error { return b.SubmitWatermark(3) }, errWatermarkNotMonotonic},
	}

	for i, c := range cases {
		occ, pend, buf := before()
		err := c.fn()
		logStep(t, Ordered, i+1, c.op, err, nil, "非法输入整体拒绝")
		if !errors.Is(err, c.want) {
			t.Fatalf("%s: 期望 %v，实际 %v", c.name, c.want, err)
		}
		// 拒绝不留痕：占用、队列、输出计数均不变。
		occ2, pend2, buf2 := snapshot()
		if occ != occ2 || pend != pend2 || buf != buf2 {
			t.Fatalf("%s: 拒绝后状态被改变 (%d,%d,%d)->(%d,%d,%d)",
				c.name, occ, pend, buf, occ2, pend2, buf2)
		}
	}

	// 各错误类别必须互不相同、可用 errors.Is 区分。
	sentinels := []error{
		errCapacityFull, errEmptyID, errDuplicateID,
		errUnknownID, errAlreadyCompleted, errWatermarkNotMonotonic,
	}
	for i := range sentinels {
		for j := range sentinels {
			if i != j && errors.Is(sentinels[i], sentinels[j]) {
				t.Fatalf("错误类别不可区分: %v 与 %v", sentinels[i], sentinels[j])
			}
		}
	}

	// 重复完成：b 不是队头，完成后仍滞留队中；再次完成应报“已完成”。
	if err := b.Complete("b"); err != nil {
		t.Fatalf("Complete b: %v", err)
	}
	b.Drain() // b 被队头 a 阻塞，无输出；此处仅为清空计数
	occ, pend, buf := snapshot()
	err = b.Complete("b")
	logStep(t, Ordered, len(cases)+1, `Complete(b)`, err, nil, "重复完成拒绝")
	if !errors.Is(err, errAlreadyCompleted) {
		t.Fatalf("重复完成应报已完成，实际 %v", err)
	}
	occ2, pend2, buf2 := snapshot()
	if occ != occ2 || pend != pend2 || buf != buf2 {
		t.Fatalf("重复完成拒绝后状态被改变")
	}

	// 元素输出后被遗忘：完成队头 a 使其与 b、水位线一起输出，
	// 此时再完成 a 应报“未知”（标识已随输出移除）。
	if err := b.Complete("a"); err != nil {
		t.Fatalf("Complete a: %v", err)
	}
	b.Drain()
	err = b.Complete("a")
	logStep(t, Ordered, len(cases)+2, `Complete(a)`, err, nil, "输出后标识被遗忘，报未知")
	if !errors.Is(err, errUnknownID) {
		t.Fatalf("输出后再完成应报未知，实际 %v", err)
	}
}

// ---------- 场景 5：随机操作序列下，两种模式均与朴素模型逐条一致 ----------

// opKind 为一次随机操作的类别。
type opKind int

const (
	opSubmit opKind = iota
	opWatermark
	opComplete
	opDrain
)

// applyOp 对生产缓冲与朴素模型施加同一操作，返回两者输出。
func applyOp(b *Buffer, n *naiveBuffer, kind opKind, id string, wm int64) (error, error, []Output, []Output) {
	var errB, errN error
	switch kind {
	case opSubmit:
		errB = b.Submit(id, id)
		errN = n.Submit(id, id)
	case opWatermark:
		errB = b.SubmitWatermark(wm)
		errN = n.SubmitWatermark(wm)
	case opComplete:
		errB = b.Complete(id)
		errN = n.Complete(id)
	case opDrain:
		// 仅取走输出，不改变逻辑状态。
	}
	return errB, errN, b.Drain(), n.Drain()
}

func testRandomAgainstNaive(t *testing.T, mode Mode, seed int64, steps int) {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))

	const capacity = 4
	b, err := New(mode, capacity)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	n := newNaive(mode, capacity)

	// 候选标识池与水位线单调游标（两实现共享同一随机序列，保证可复现）。
	ids := []string{"a", "b", "c", "d", "e", "f"}
	var wmCursor int64

	for step := 0; step < steps; step++ {
		// 随机选择操作：提交偏多，完成/水位线/取输出次之。
		var kind opKind
		switch r := rng.Intn(100); {
		case r < 45:
			kind = opSubmit
		case r < 65:
			kind = opComplete
		case r < 85:
			kind = opWatermark
		default:
			kind = opDrain
		}

		id := ids[rng.Intn(len(ids))]
		var wm int64
		if kind == opWatermark {
			// 一半概率严格递增（合法），一半概率回退/相等（非法）。
			if rng.Intn(2) == 0 {
				wmCursor += 1 + int64(rng.Intn(5))
				wm = wmCursor
			} else {
				wm = wmCursor - int64(rng.Intn(3)) // 可能 <= last，触发拒绝
			}
		}

		errB, errN, outB, outN := applyOp(b, n, kind, id, wm)

		// 1) 拒绝与否及类别必须一致。
		if (errB == nil) != (errN == nil) {
			t.Fatalf("步骤%d 拒绝性不一致: 生产=%v 朴素=%v", step, errB, errN)
		}
		if errB != nil && errN != nil {
			var eb, en *Error
			if !errors.As(errB, &eb) || !errors.As(errN, &en) || eb.Kind != en.Kind {
				t.Fatalf("步骤%d 错误类别不一致: 生产=%v 朴素=%v", step, errB, errN)
			}
		}
		// 2) 输出必须逐条一致。
		if !outputsEqual(outB, outN) {
			t.Fatalf("步骤%d 输出不一致 (mode=%s)\n 生产=%v\n 朴素=%v", step, mode, outB, outN)
		}
		// 3) 占用数一致。
		if b.Occupied() != n.occupied {
			t.Fatalf("步骤%d 占用不一致: 生产=%d 朴素=%d", step, b.Occupied(), n.occupied)
		}
		if step%50 == 0 || step == steps-1 {
			t.Logf("[%s seed=%d] 步骤%03d 一致：占用=%d 待输出=%d",
				mode, seed, step, b.Occupied(), b.Buffered())
		}
	}
}

func TestRandomMatchesNaiveOrdered(t *testing.T) {
	for _, seed := range []int64{1, 2, 3, 42, 99} {
		testRandomAgainstNaive(t, Ordered, seed, 400)
	}
}

func TestRandomMatchesNaiveUnordered(t *testing.T) {
	for _, seed := range []int64{1, 2, 3, 42, 99} {
		testRandomAgainstNaive(t, Unordered, seed, 400)
	}
}

// ---------- 场景 6：并发调用安全（配合 -race） ----------

func TestConcurrentAccess(t *testing.T) {
	for _, mode := range []Mode{Ordered, Unordered} {
		mode := mode
		t.Run(mode.String(), func(t *testing.T) {
			const capacity = 8
			b, err := New(mode, capacity)
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			var wg sync.WaitGroup
			// 提交者：多 goroutine 竞争提交不同标识。
			for w := 0; w < 4; w++ {
				wg.Add(1)
				go func(w int) {
					defer wg.Done()
					for i := 0; i < 200; i++ {
						id := fmt.Sprintf("w%d-%d", w, i)
						_ = b.Submit(id, i) // 容量满会被拒绝，属正常
					}
				}(w)
			}
			// 完成声明者：随机尝试完成一批标识（可能未知/已完成，属正常）。
			for w := 0; w < 2; w++ {
				wg.Add(1)
				go func(w int) {
					defer wg.Done()
					for i := 0; i < 400; i++ {
						_ = b.Complete(fmt.Sprintf("w%d-%d", i%4, i%200))
					}
				}(w)
			}
			// 水位线提交者：各自维护单调游标，但彼此交错会被拒绝（属正常）。
			for w := 0; w < 2; w++ {
				wg.Add(1)
				go func(w int) {
					defer wg.Done()
					wm := int64(w * 1000)
					for i := 0; i < 100; i++ {
						wm += 1 + int64(i%3)
						_ = b.SubmitWatermark(wm)
					}
				}(w)
			}
			// 输出查询者。
			var totalOut int
			var mu sync.Mutex
			for w := 0; w < 2; w++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for i := 0; i < 300; i++ {
						outs := b.Drain()
						mu.Lock()
						totalOut += len(outs)
						mu.Unlock()
					}
				}()
			}
			wg.Wait()

			// 不变量：占用永不超容量、永不为负。
			if got := b.Occupied(); got < 0 || got > capacity {
				t.Fatalf("[%s] 占用越界: %d", mode, got)
			}
			t.Logf("[%s] 并发结束：累计输出=%d 占用=%d 待输出=%d",
				mode, totalOut, b.Occupied(), b.Buffered())
		})
	}
}
