package eventloop

// 本文件包含一个独立编写的朴素模型（naiveLoop）与随机操作序列对照测试。
// 朴素模型用最直接的线性扫描实现同一份语义，与主实现（堆 + 队首调度）
// 结构完全不同，用于交叉验证执行轨迹的唯一确定性。

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

type nTask struct {
	handle    Handle
	src       Source
	enq       int64
	cb        func()
	cancelled bool
	done      bool
}

type nMicro struct {
	handle    Handle
	cb        func()
	cancelled bool
}

type nTimer struct {
	handle    Handle
	due       int64
	seq       uint64
	depth     int
	cb        func()
	cancelled bool
	task      *nTask
}

type nRaf struct {
	handle    Handle
	cb        func()
	cancelled bool
}

type nIdle struct {
	handle     Handle
	cb         func(IdleDeadline)
	deadline   int64
	hasTimeout bool
	cancelled  bool
	executed   bool
}

type naiveLoop struct {
	cfg    Config
	now    int64
	target int64
	seq    uint64
	nextH  Handle

	tasks []*nTask
	skips [numSources]int

	micro []*nMicro

	timers []*nTimer

	rafs          []*nRaf
	dirty         bool
	lastBoundary  int64
	idles         []*nIdle
	idleCooldown  bool
	curTimerDepth int

	live      map[Handle]func()
	cancelled map[Handle]bool

	trace   []TraceEntry
	errs    []ErrorReport
	pending *AdvanceResult
}

func newNaiveLoop(cfg Config) *naiveLoop {
	return &naiveLoop{
		cfg:           cfg,
		curTimerDepth: -1,
		live:          make(map[Handle]func()),
		cancelled:     make(map[Handle]bool),
	}
}

func (n *naiveLoop) Now() int64 { return n.now }

func (n *naiveLoop) Errors() []ErrorReport { return append([]ErrorReport(nil), n.errs...) }

func (n *naiveLoop) alloc() Handle {
	n.nextH++
	return n.nextH
}

func (n *naiveLoop) EnqueueTask(src Source, cb func()) (Handle, error) {
	if !src.valid() {
		return 0, &Error{Category: ErrInvalidArg, Message: "unknown task source"}
	}
	if cb == nil {
		return 0, &Error{Category: ErrInvalidArg, Message: "nil task callback"}
	}
	h := n.alloc()
	t := &nTask{handle: h, src: src, enq: n.now, cb: cb}
	n.tasks = append(n.tasks, t)
	n.live[h] = func() { t.cancelled = true }
	return h, nil
}

func (n *naiveLoop) QueueMicrotask(cb func()) (Handle, error) {
	if cb == nil {
		return 0, &Error{Category: ErrInvalidArg, Message: "nil microtask callback"}
	}
	h := n.alloc()
	m := &nMicro{handle: h, cb: cb}
	n.micro = append(n.micro, m)
	n.live[h] = func() { m.cancelled = true }
	return h, nil
}

func (n *naiveLoop) RequestAnimationFrame(cb func()) (Handle, error) {
	if cb == nil {
		return 0, &Error{Category: ErrInvalidArg, Message: "nil animation frame callback"}
	}
	h := n.alloc()
	r := &nRaf{handle: h, cb: cb}
	n.rafs = append(n.rafs, r)
	n.live[h] = func() { r.cancelled = true }
	return h, nil
}

func (n *naiveLoop) MarkDirty() { n.dirty = true }

func (n *naiveLoop) RequestIdleCallback(cb func(IdleDeadline), timeout int64) (Handle, error) {
	if cb == nil {
		return 0, &Error{Category: ErrInvalidArg, Message: "nil idle callback"}
	}
	if timeout < 0 {
		return 0, &Error{Category: ErrInvalidArg, Message: "negative idle timeout"}
	}
	h := n.alloc()
	ic := &nIdle{handle: h, cb: cb}
	if timeout != NoTimeout {
		ic.hasTimeout = true
		ic.deadline = n.now + timeout
	}
	n.idles = append(n.idles, ic)
	n.live[h] = func() { ic.cancelled = true }
	return h, nil
}

func (n *naiveLoop) SetTimeout(cb func(), delay int64) (Handle, error) {
	if cb == nil {
		return 0, &Error{Category: ErrInvalidArg, Message: "nil timer callback"}
	}
	if delay < 0 {
		return 0, &Error{Category: ErrInvalidArg, Message: "negative timer delay"}
	}
	depth := 0
	if n.curTimerDepth >= 0 {
		depth = n.curTimerDepth + 1
	}
	eff := delay
	if depth > n.cfg.TimerNestingThreshold && eff < n.cfg.TimerClampDelay {
		eff = n.cfg.TimerClampDelay
	}
	if eff < n.cfg.MinTimerDelay {
		eff = n.cfg.MinTimerDelay
	}
	h := n.alloc()
	n.seq++
	tm := &nTimer{handle: h, due: n.now + eff, seq: n.seq, depth: depth, cb: cb}
	n.timers = append(n.timers, tm)
	n.live[h] = func() {
		tm.cancelled = true
		if tm.task != nil {
			tm.task.cancelled = true
		}
	}
	return h, nil
}

func (n *naiveLoop) Cancel(h Handle) error {
	if n.cancelled[h] {
		return &Error{Category: ErrDuplicateCancel, Message: "already cancelled"}
	}
	cancel, ok := n.live[h]
	if !ok {
		return &Error{Category: ErrHandleNotFound, Message: "handle not found"}
	}
	cancel()
	delete(n.live, h)
	n.cancelled[h] = true
	return nil
}

func (n *naiveLoop) AdvanceTo(target int64) (AdvanceResult, error) {
	if target < n.now {
		return AdvanceResult{}, &Error{Category: ErrClockRollback, Message: "clock rollback"}
	}
	res := &AdvanceResult{From: n.now, To: target}
	n.pending = res
	n.target = target
	for n.step() {
	}
	n.pending = nil
	return *res, nil
}

func (n *naiveLoop) exec(typ ExecType, src Source, h Handle, reason string, cb func()) {
	e := TraceEntry{Type: typ, Source: src, Handle: h, Time: n.now, Reason: reason}
	n.trace = append(n.trace, e)
	if n.pending != nil {
		n.pending.Entries = append(n.pending.Entries, e)
	}
	defer func() {
		if r := recover(); r != nil {
			rep := ErrorReport{Time: n.now, Handle: h, Value: r}
			n.errs = append(n.errs, rep)
			if n.pending != nil {
				n.pending.Errors = append(n.pending.Errors, rep)
			}
		}
	}()
	cb()
}

func (n *naiveLoop) drainMicro() {
	for len(n.micro) > 0 {
		m := n.micro[0]
		n.micro = n.micro[1:]
		delete(n.live, m.handle)
		if m.cancelled {
			continue
		}
		n.exec(ExecMicrotask, NoSource, m.handle, "microtask checkpoint", m.cb)
	}
}

// headOf 返回某源队首（第一个未取消未执行的任务）。
func (n *naiveLoop) headOf(src Source) *nTask {
	for _, t := range n.tasks {
		if t.src == src && !t.done && !t.cancelled {
			return t
		}
	}
	return nil
}

func (n *naiveLoop) dropCancelledHeads() {
	for s := 0; s < numSources; s++ {
		for {
			h := n.headOf(Source(s))
			if h == nil || !h.cancelled {
				break
			}
			h.done = true
			n.skips[s] = 0
		}
	}
}

// pickTask 线性扫描实现与 scheduler.selectNext 相同的规则。
func (n *naiveLoop) pickTask() (*nTask, string, bool) {
	n.dropCancelledHeads()
	chosen := -1
	reason := ""
	for s := 0; s < numSources; s++ {
		h := n.headOf(Source(s))
		if h == nil || n.skips[s] < n.cfg.StarvationLimit {
			continue
		}
		if chosen < 0 || lessHead2(h, s, n.headOf(Source(chosen)), chosen) {
			chosen = s
		}
	}
	if chosen >= 0 {
		reason = "starvation"
	} else if h := n.headOf(SourceUserInteraction); h != nil {
		chosen = int(SourceUserInteraction)
		reason = "user-interaction priority"
	} else {
		for s := 0; s < numSources; s++ {
			h := n.headOf(Source(s))
			if h == nil {
				continue
			}
			if chosen < 0 || lessHead2(h, s, n.headOf(Source(chosen)), chosen) {
				chosen = s
			}
		}
		reason = "earliest enqueue time"
	}
	if chosen < 0 {
		return nil, "", false
	}
	t := n.headOf(Source(chosen))
	t.done = true
	n.skips[chosen] = 0
	for s := 0; s < numSources; s++ {
		if s != chosen && n.headOf(Source(s)) != nil {
			n.skips[s]++
		}
	}
	return t, reason, true
}

func lessHead2(a *nTask, asrc int, b *nTask, bsrc int) bool {
	if a.enq != b.enq {
		return a.enq < b.enq
	}
	return asrc < bsrc
}

func (n *naiveLoop) hasHead() bool {
	for s := 0; s < numSources; s++ {
		if n.headOf(Source(s)) != nil {
			return true
		}
	}
	return false
}

func (n *naiveLoop) renderRequested() bool {
	if n.dirty {
		return true
	}
	for _, r := range n.rafs {
		if !r.cancelled {
			return true
		}
	}
	return false
}

func (n *naiveLoop) idleRemaining() int64 {
	f := n.cfg.FrameInterval
	if n.now%f == 0 {
		return 0
	}
	return f - n.now%f
}

// fireEvents 触发到期时刻不超过 now 的定时器与空闲超时。
func (n *naiveLoop) fireEvents(now int64) bool {
	fired := false
	for {
		best := -1
		for i, tm := range n.timers {
			if tm.cancelled || tm.due > now {
				continue
			}
			if best < 0 || tm.due < n.timers[best].due ||
				(tm.due == n.timers[best].due && tm.seq < n.timers[best].seq) {
				best = i
			}
		}
		if best < 0 {
			break
		}
		tm := n.timers[best]
		n.timers = append(n.timers[:best], n.timers[best+1:]...)
		fired = true
		h := n.alloc()
		depth, cb, tmHandle := tm.depth, tm.cb, tm.handle
		t := &nTask{handle: h, src: SourceTimer, enq: tm.due, cb: func() {
			delete(n.live, tmHandle)
			prev := n.curTimerDepth
			n.curTimerDepth = depth
			defer func() { n.curTimerDepth = prev }()
			cb()
		}}
		tm.task = t
		n.tasks = append(n.tasks, t)
		n.live[h] = func() { t.cancelled = true }
	}
	for {
		best := -1
		for i, ic := range n.idles {
			if !ic.hasTimeout || ic.cancelled || ic.executed || ic.deadline > now {
				continue
			}
			if best < 0 || ic.deadline < n.idles[best].deadline {
				best = i
			}
		}
		if best < 0 {
			break
		}
		ic := n.idles[best]
		fired = true
		ic.executed = true
		h := n.alloc()
		t := &nTask{handle: h, src: SourceInternal, enq: ic.deadline, cb: func() {
			cancelled := ic.cancelled
			delete(n.live, ic.handle)
			if cancelled {
				return
			}
			rem := n.idleRemaining()
			ic.cb(IdleDeadline{Remaining: rem, Deadline: n.now + rem, DidTimeout: true})
		}}
		n.tasks = append(n.tasks, t)
		n.live[h] = func() { t.cancelled = true }
	}
	return fired
}

func (n *naiveLoop) nextEvent() (int64, bool) {
	var best int64
	ok := false
	for _, tm := range n.timers {
		if tm.cancelled {
			continue
		}
		if !ok || tm.due < best {
			best, ok = tm.due, true
		}
	}
	for _, ic := range n.idles {
		if !ic.hasTimeout || ic.cancelled || ic.executed {
			continue
		}
		if !ok || ic.deadline < best {
			best, ok = ic.deadline, true
		}
	}
	return best, ok
}

func (n *naiveLoop) idleEligible() bool {
	if n.idleCooldown {
		return false
	}
	live := false
	for _, ic := range n.idles {
		if !ic.cancelled && !ic.executed {
			live = true
			break
		}
	}
	if !live {
		return false
	}
	n.dropCancelledHeads()
	if n.hasHead() || len(n.micro) > 0 || n.renderRequested() {
		return false
	}
	return n.idleRemaining() > 0
}

// step 执行一个原子动作，返回是否还有工作。
func (n *naiveLoop) step() bool {
	if len(n.micro) > 0 {
		n.drainMicro()
		return true
	}
	if t, reason, ok := n.pickTask(); ok {
		delete(n.live, t.handle)
		n.exec(ExecTask, t.src, t.handle, reason, t.cb)
		n.drainMicro()
		return true
	}
	if n.fireEvents(n.now) {
		return true
	}
	b := (n.now / n.cfg.FrameInterval) * n.cfg.FrameInterval
	if b > n.lastBoundary {
		n.lastBoundary = b
		if n.renderRequested() {
			snap := n.rafs
			n.rafs = nil
			for _, r := range snap {
				if r.cancelled {
					continue
				}
				delete(n.live, r.handle)
				n.exec(ExecAnimationFrame, NoSource, r.handle, "animation frame", r.cb)
				n.drainMicro()
			}
			n.dirty = false
		}
		return true
	}
	if n.idleEligible() {
		snap := n.idles
		n.idles = nil
		n.idleCooldown = true
		for i, ic := range snap {
			if ic.cancelled || ic.executed {
				continue
			}
			n.dropCancelledHeads()
			ok := !n.hasHead() && len(n.micro) == 0 &&
				!n.renderRequested() && n.idleRemaining() > 0
			if !ok {
				rest := append([]*nIdle(nil), snap[i:]...)
				n.idles = append(rest, n.idles...)
				return true
			}
			ic.executed = true
			delete(n.live, ic.handle)
			rem := n.idleRemaining()
			dl := n.now + rem
			n.exec(ExecIdle, NoSource, ic.handle, "idle period", func() {
				ic.cb(IdleDeadline{Remaining: rem, Deadline: dl})
			})
			n.drainMicro()
		}
		return true
	}
	next, ok := n.nextEvent()
	if ok && next <= n.target {
		n.now = next
		n.idleCooldown = false
		n.fireEvents(n.now)
		return true
	}
	if n.now < n.target {
		n.now = n.target
		n.idleCooldown = false
		return true
	}
	return false
}

// ---- 随机操作序列对照 ----

type kernel interface {
	EnqueueTask(src Source, cb func()) (Handle, error)
	QueueMicrotask(cb func()) (Handle, error)
	RequestAnimationFrame(cb func()) (Handle, error)
	RequestIdleCallback(cb func(IdleDeadline), timeout int64) (Handle, error)
	SetTimeout(cb func(), delay int64) (Handle, error)
	MarkDirty()
	Cancel(h Handle) error
	AdvanceTo(target int64) (AdvanceResult, error)
	Errors() []ErrorReport
	Now() int64
}

type opKind int

const (
	opTask opKind = iota
	opMicro
	opRAF
	opIdle
	opTimer
	opDirty
	opCancel
	opAdvance
	opPanic // 仅作为子动作：回调内抛出异常
)

type op struct {
	kind      opKind
	src       Source
	delay     int64
	timeout   int64 // -1 表示 NoTimeout
	advDelta  int64
	cancelRef int
	panicID   int
	children  []op
}

func genChildren(r *rand.Rand, depth int, panicCtr *int) []op {
	if depth >= 2 {
		return nil
	}
	var out []op
	for r.Intn(100) < 55 {
		var o op
		switch r.Intn(7) {
		case 0:
			o = op{kind: opTask, src: Source(r.Intn(numSources))}
		case 1:
			o = op{kind: opMicro}
		case 2:
			o = op{kind: opRAF}
		case 3:
			o = op{kind: opIdle, timeout: int64(r.Intn(4)) - 1} // 可能为 NoTimeout
		case 4:
			o = op{kind: opTimer, delay: int64(r.Intn(20))}
		case 5:
			o = op{kind: opDirty}
		default:
			*panicCtr++
			o = op{kind: opPanic, panicID: *panicCtr}
		}
		o.children = genChildren(r, depth+1, panicCtr)
		out = append(out, o)
	}
	return out
}

func genOps(r *rand.Rand, n int) []op {
	panicCtr := 0
	var out []op
	for i := 0; i < n; i++ {
		var o op
		switch r.Intn(10) {
		case 0, 1, 2:
			o = op{kind: opTask, src: Source(r.Intn(numSources))}
		case 3:
			o = op{kind: opTimer, delay: int64(r.Intn(20))}
		case 4:
			o = op{kind: opRAF}
		case 5:
			o = op{kind: opIdle, timeout: int64(r.Intn(4)) - 1}
		case 6:
			o = op{kind: opMicro}
		case 7:
			o = op{kind: opDirty}
		case 8:
			o = op{kind: opCancel, cancelRef: r.Intn(24)}
		default:
			o = op{kind: opAdvance, advDelta: int64(r.Intn(31))}
		}
		o.children = genChildren(r, 0, &panicCtr)
		out = append(out, o)
	}
	return out
}

type opRecord struct {
	sigs    []string
	idles   []string
	reports []string
}

func errCat(err error) string {
	if err == nil {
		return "ok"
	}
	if e, ok := err.(*Error); ok {
		return e.Category.String()
	}
	return "unknown"
}

func entrySig(e TraceEntry) string {
	return fmt.Sprintf("%d/%d/h%d@%d", e.Type, e.Source, e.Handle, e.Time)
}

func applyChild(k kernel, rec *opRecord, o op, handles *[]Handle) {
	switch o.kind {
	case opPanic:
		panic(fmt.Sprintf("boom-%d", o.panicID))
	case opDirty:
		k.MarkDirty()
	default:
		applyOp(k, rec, o, handles)
	}
}

func applyOp(k kernel, rec *opRecord, o op, handles *[]Handle) {
	mkCb := func() func() {
		return func() {
			for _, c := range o.children {
				applyChild(k, rec, c, handles)
			}
		}
	}
	reg := func(h Handle, err error) {
		rec.sigs = append(rec.sigs, fmt.Sprintf("reg h=%d %s", h, errCat(err)))
		if err == nil {
			*handles = append(*handles, h)
		}
	}
	switch o.kind {
	case opTask:
		reg(k.EnqueueTask(o.src, mkCb()))
	case opMicro:
		reg(k.QueueMicrotask(mkCb()))
	case opRAF:
		reg(k.RequestAnimationFrame(mkCb()))
	case opIdle:
		timeout := o.timeout
		if timeout < 0 {
			timeout = NoTimeout
		}
		reg(k.RequestIdleCallback(func(d IdleDeadline) {
			rec.idles = append(rec.idles, fmt.Sprintf("rem=%d dl=%d to=%v", d.Remaining, d.Deadline, d.DidTimeout))
			for _, c := range o.children {
				applyChild(k, rec, c, handles)
			}
		}, timeout))
	case opTimer:
		reg(k.SetTimeout(mkCb(), o.delay))
	case opDirty:
		k.MarkDirty()
		rec.sigs = append(rec.sigs, "dirty")
	case opCancel:
		var h Handle
		if o.cancelRef < len(*handles) {
			h = (*handles)[o.cancelRef]
		} else {
			h = Handle(900000 + o.cancelRef)
		}
		rec.sigs = append(rec.sigs, fmt.Sprintf("cancel h=%d %s", h, errCat(k.Cancel(h))))
	case opAdvance:
		target := k.Now() + o.advDelta
		res, err := k.AdvanceTo(target)
		sig := fmt.Sprintf("adv->%d %s [", target, errCat(err))
		for _, e := range res.Entries {
			sig += entrySig(e) + " "
		}
		sig += "] errs["
		for _, rep := range res.Errors {
			sig += fmt.Sprintf("%d/h%d/%v ", rep.Time, rep.Handle, rep.Value)
		}
		sig += "]"
		rec.sigs = append(rec.sigs, sig)
	}
}

func runOps(k kernel, ops []op) *opRecord {
	rec := &opRecord{}
	handles := []Handle{}
	for _, o := range ops {
		applyOp(k, rec, o, &handles)
	}
	for _, rep := range k.Errors() {
		rec.reports = append(rec.reports, fmt.Sprintf("%d/h%d/%v", rep.Time, rep.Handle, rep.Value))
	}
	return rec
}

func TestRandomizedAgainstNaiveModel(t *testing.T) {
	seeds := []int64{1, 7, 13, 42, 99, 2024, 31415, 271828}
	for _, seed := range seeds {
		r := rand.New(rand.NewSource(seed))
		cfg := Config{
			StarvationLimit:       []int{0, 1, 3, 5}[r.Intn(4)],
			FrameInterval:         []int64{4, 10, 16}[r.Intn(3)],
			MinTimerDelay:         []int64{0, 1, 2}[r.Intn(3)],
			TimerNestingThreshold: []int{0, 2, 5}[r.Intn(3)],
			TimerClampDelay:       []int64{4, 10, 100}[r.Intn(3)],
		}
		ops := genOps(r, 300)

		real, err := NewEventLoop(cfg)
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		naive := newNaiveLoop(cfg)

		recReal := runOps(real, ops)
		recNaive := runOps(naive, ops)

		if !reflect.DeepEqual(recReal.sigs, recNaive.sigs) {
			for i := range recReal.sigs {
				if i >= len(recNaive.sigs) || recReal.sigs[i] != recNaive.sigs[i] {
					t.Fatalf("seed %d op %d:\n real: %s\nnaive: %s",
						seed, i, recReal.sigs[i], recNaive.sigs[min(i, len(recNaive.sigs)-1)])
				}
			}
			t.Fatalf("seed %d: sig length %d vs %d", seed, len(recReal.sigs), len(recNaive.sigs))
		}
		if !reflect.DeepEqual(recReal.idles, recNaive.idles) {
			t.Fatalf("seed %d: idle deadlines differ\n real: %v\nnaive: %v", seed, recReal.idles, recNaive.idles)
		}
		if !reflect.DeepEqual(recReal.reports, recNaive.reports) {
			t.Fatalf("seed %d: error reports differ\n real: %v\nnaive: %v", seed, recReal.reports, recNaive.reports)
		}
		if real.Now() != naive.Now() {
			t.Fatalf("seed %d: clock %d vs %d", seed, real.Now(), naive.Now())
		}
		t.Logf("seed %d: %d ops, %d signatures matched", seed, len(ops), len(recReal.sigs))
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
