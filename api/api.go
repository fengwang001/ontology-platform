// Package api 是 MCS 队列锁的对外接口：获释、关闭、快照与自检。
package api

import (
	"fmt"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"

	"ontology/mcsnode"
	"ontology/qlock"
)

var (
	ErrClosed   = qlock.ErrClosed
	ErrNilNode  = qlock.ErrNilNode
	ErrNotOwner = qlock.ErrNotOwner
)

type Lock struct{ q *qlock.Lock }

func New() *Lock                                { return &Lock{q: qlock.New()} }
func (l *Lock) Acquire() (*mcsnode.Node, error) { return l.q.Acquire() }
func (l *Lock) Release(n *mcsnode.Node) error   { return l.q.Release(n) }
func (l *Lock) Close() error                    { return l.q.Close() }

func (l *Lock) Snapshot() []*mcsnode.Node { return l.q.Snapshot() }

type op struct {
	id  int
	rel bool
} // rel=false 为 Acquire(id)，true 为 Release(id)

// runScript 强制按脚本顺序入队执行，返回实际授予顺序（交接在 Release 返回前完成）。
func runScript(l *Lock, ops []op) (got []int) {
	nodes := map[int]*mcsnode.Node{}
	ids := map[*mcsnode.Node]int{}
	for _, o := range ops {
		if o.rel {
			_ = l.Release(nodes[o.id])
			if snap := l.Snapshot(); len(snap) > 0 {
				got = append(got, ids[snap[0]])
			}
			continue
		}
		base := len(l.Snapshot())
		go func() { _, _ = l.Acquire() }()
		for len(l.Snapshot()) != base+1 {
			runtime.Gosched()
		} // 等节点链入，钉住入队顺序
		n := l.Snapshot()[base]
		nodes[o.id], ids[n] = n, o.id
		if base == 0 {
			got = append(got, o.id)
		} // 空队列直接获锁
	}
	return got
}

func naiveGrants(ops []op) (g []int) {
	holder, queue := -1, []int(nil)
	for _, o := range ops {
		switch {
		case !o.rel && holder == -1:
			holder, g = o.id, append(g, o.id)
		case !o.rel:
			queue = append(queue, o.id)
		case len(queue) > 0:
			holder, queue = queue[0], queue[1:]
			g = append(g, holder)
		default:
			holder = -1
		}
	}
	return g
}

func genScript(m int, seed uint32) (out []op) {
	active := []int{}
	next := 1
	for i := 0; i < m || len(active) > 0; i++ {
		seed = seed*1103515245 + 12345
		if len(active) == 0 || (i < m && seed>>16&1 == 0) {
			out, active, next = append(out, op{next, false}), append(active, next), next+1
		} else {
			out, active = append(out, op{active[0], true}), active[1:]
		}
	}
	return out
}

// SelfCheck 对内置操作序列核验四条不变量，全部通过返回 nil。
func (l *Lock) SelfCheck() error {
	scripts := [][]op{genScript(300, 7), genScript(300, 13), genScript(1000, 29),
		{{1, false}, {2, false}, {3, false}, {1, true}, {2, true}, {3, true}},
		{{1, false}, {2, false}, {1, true}, {3, false}, {2, true}, {4, false}, {3, true}, {4, true}}}
	for i, s := range scripts { // 不变量 1、3：授予顺序与朴素参照逐次相同
		if got, want := runScript(New(), s), naiveGrants(s); !slices.Equal(got, want) {
			return fmt.Errorf("selfcheck: script %d grant %v != naive %v", i, got, want)
		}
	}
	if err := checkMutualExclusion(64); err != nil { // 不变量 2
		return err
	}
	return checkNoTrace() // 不变量 4
}

func checkMutualExclusion(n int) error {
	l := New()
	var cur, max atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			nd, _ := l.Acquire()
			c := cur.Add(1)
			for m := max.Load(); c > m && !max.CompareAndSwap(m, c); m = max.Load() {
			}
			cur.Add(-1)
			_ = l.Release(nd)
		}()
	}
	wg.Wait()
	if max.Load() != 1 {
		return fmt.Errorf("selfcheck: mutual exclusion violated, max holders=%d", max.Load())
	}
	return nil
}

func checkNoTrace() error {
	l := New()
	n0, _ := l.Acquire()
	granted := make(chan *mcsnode.Node, 1)
	go func() { n, _ := l.Acquire(); granted <- n }()
	for len(l.Snapshot()) != 2 {
		runtime.Gosched()
	}
	before, n1 := l.Snapshot(), l.Snapshot()[1]
	badErr := l.Release(nil) != ErrNilNode || l.Release(n1) != ErrNotOwner || l.Release(mcsnode.New()) != ErrNotOwner
	traced := !slices.Equal(before, l.Snapshot())
	unusable := l.Release(n0) != nil || (<-granted) != n1 || l.Release(n1) != nil
	l.Close()
	_, cerr := l.Acquire()
	if badErr || traced || unusable || cerr != ErrClosed {
		return fmt.Errorf("selfcheck: no-trace: badErr=%v traced=%v unusable=%v cerr=%v", badErr, traced, unusable, cerr)
	}
	return nil
}
