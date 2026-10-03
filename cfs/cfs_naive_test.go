package cfs

import (
	"errors"
	"fmt"
	"strings"
)

// naive 是按题目规则逐边界、逐 CPU 写成的朴素模拟，用作对照实现。
// 不做任何算术快进，每个边界都逐个处理。
type naive struct {
	q, p, s, b    int64
	g             int64
	cpus          []cpu
	queue         []int
	periods       int64
	nThrottled    int64
	throttledTime int64
	lastNow       int64
	// 不变量核算：l 恒等于 累计借得 - 累计归还 - 累计消耗
	borrowed    []int64
	returned    []int64
	consumed    []int64
	unthrottles []int64 // 每次解除节流的 b-since
}

func newNaive(q, p, s, b int64, ncpu int) *naive {
	return &naive{
		q: q, p: p, s: s, b: b,
		g:        q,
		cpus:     make([]cpu, ncpu),
		borrowed: make([]int64, ncpu),
		returned: make([]int64, ncpu),
		consumed: make([]int64, ncpu),
	}
}

// naiveSnap 是朴素模拟在操作前的快照，用于状态类拒绝时回滚边界。
type naiveSnap struct {
	g             int64
	cpus          []cpu
	queue         []int
	periods       int64
	nThrottled    int64
	throttledTime int64
	borrowed      []int64
	unthrottles   []int64
}

func (n *naive) snapshot() *naiveSnap {
	return &naiveSnap{
		g:             n.g,
		cpus:          append([]cpu(nil), n.cpus...),
		queue:         append([]int(nil), n.queue...),
		periods:       n.periods,
		nThrottled:    n.nThrottled,
		throttledTime: n.throttledTime,
		borrowed:      append([]int64(nil), n.borrowed...),
		unthrottles:   append([]int64(nil), n.unthrottles...),
	}
}

func (n *naive) restore(s *naiveSnap) {
	n.g = s.g
	n.cpus = s.cpus
	n.queue = s.queue
	n.periods = s.periods
	n.nThrottled = s.nThrottled
	n.throttledTime = s.throttledTime
	n.borrowed = s.borrowed
	n.unthrottles = s.unthrottles
}

func (n *naive) checkCommon(now int64, cpuID int) error {
	if cpuID < 0 || cpuID >= len(n.cpus) {
		return ErrCPUOutOfRange
	}
	if now < 0 || now > maxNow {
		return ErrInvalidParam
	}
	if now < n.lastNow {
		return ErrTimeRegression
	}
	return nil
}

// advance 逐个处理所有满足 k*P <= now 的边界，并把判定依据写入 trace。
func (n *naive) advance(now int64, trace *strings.Builder) {
	for (n.periods+1)*n.p <= now {
		n.periods++
		b := n.periods * n.p
		n.g = min(n.q+n.b, n.g+n.q)
		fmt.Fprintf(trace, "边界%d:G=%d;", b, n.g)
		var rest []int
		for i, id := range n.queue {
			cp := &n.cpus[id]
			need := 1 - cp.l
			take := min(need, n.g)
			cp.l += take
			n.g -= take
			n.borrowed[id] += take
			fmt.Fprintf(trace, "cpu%d need=%d take=%d l=%d;", id, need, take, cp.l)
			if cp.l > 0 {
				cp.state = Running
				n.throttledTime += b - cp.since
				n.unthrottles = append(n.unthrottles, b-cp.since)
				fmt.Fprintf(trace, "cpu%d解除节流;", id)
			} else {
				rest = append(rest, id)
			}
			if take < need {
				rest = append(rest, n.queue[i+1:]...)
				break
			}
		}
		n.queue = rest
	}
}

func (n *naive) wake(now int64, cpuID int) (error, string) {
	if err := n.checkCommon(now, cpuID); err != nil {
		return err, "拒绝: " + err.Error()
	}
	snap := n.snapshot()
	var trace strings.Builder
	n.advance(now, &trace)
	cp := &n.cpus[cpuID]
	if cp.state != Idle {
		n.restore(snap)
		return ErrNotIdle, "拒绝: 边界处理后 CPU 非空闲"
	}
	cp.state = Running
	n.lastNow = now
	return nil, "接受: 空闲变运行; " + trace.String()
}

func (n *naive) run(now int64, cpuID int, d int64) (error, string) {
	if err := n.checkCommon(now, cpuID); err != nil {
		return err, "拒绝: " + err.Error()
	}
	if d < 1 || d > maxRun {
		return ErrInvalidParam, "拒绝: d 越界"
	}
	snap := n.snapshot()
	var trace strings.Builder
	n.advance(now, &trace)
	cp := &n.cpus[cpuID]
	switch cp.state {
	case Idle:
		n.restore(snap)
		return ErrNotRunning, "拒绝: 边界处理后 CPU 空闲"
	case Throttled:
		n.restore(snap)
		return ErrThrottled, "拒绝: 边界处理后 CPU 被节流"
	}
	cp.l -= d
	n.consumed[cpuID] += d
	fmt.Fprintf(&trace, "消耗%d后l=%d;", d, cp.l)
	if cp.l <= 0 {
		want := n.s - cp.l
		take := min(want, n.g)
		cp.l += take
		n.g -= take
		n.borrowed[cpuID] += take
		fmt.Fprintf(&trace, "want=%d take=%d l=%d;", want, take, cp.l)
		if cp.l <= 0 {
			cp.state = Throttled
			cp.since = now
			n.queue = append(n.queue, cpuID)
			n.nThrottled++
			fmt.Fprintf(&trace, "l<=0节流since=%d;", now)
		}
	}
	n.lastNow = now
	return nil, "接受: " + trace.String()
}

func (n *naive) idle(now int64, cpuID int) (error, string) {
	if err := n.checkCommon(now, cpuID); err != nil {
		return err, "拒绝: " + err.Error()
	}
	snap := n.snapshot()
	var trace strings.Builder
	n.advance(now, &trace)
	cp := &n.cpus[cpuID]
	switch cp.state {
	case Idle:
		n.restore(snap)
		return ErrNotRunning, "拒绝: 边界处理后 CPU 空闲"
	case Throttled:
		n.restore(snap)
		return ErrThrottled, "拒绝: 边界处理后 CPU 被节流"
	}
	cp.state = Idle
	if cp.l > 1 {
		slack := cp.l - 1
		n.g = min(n.q+n.b, n.g+slack)
		cp.l = 1
		n.returned[cpuID] += slack
		fmt.Fprintf(&trace, "归还slack=%d G=%d;", slack, n.g)
	}
	n.lastNow = now
	return nil, "接受: 运行变空闲; " + trace.String()
}

// sameErr 判定两个错误是否同类（nil 与 nil 相等）。
func sameErr(a, b error) bool {
	if a == nil || b == nil {
		return a == b
	}
	return errors.Is(a, b)
}
