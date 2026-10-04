package exec_test

// 对照用朴素模拟器：严格按题面规则独立实现，与真实 Scheduler 不共享代码。

import (
	"fmt"
	"sort"

	"ontology/action"
)

type mStatus int

const (
	mQueued mStatus = iota
	mAssigned
)

type mWaiter struct {
	id       int
	prio     int
	terminal string
	exit     int
}

type mOp struct {
	digest   string
	platform action.Platform
	seq      int
	status   mStatus
	losses   int
	waiters  map[int]*mWaiter
	holder   string
	attempt  int
}

func (op *mOp) prio() int {
	max := -1
	for _, w := range op.waiters {
		if w.prio > max {
			max = w.prio
		}
	}
	return max
}

type mWorker struct {
	props map[string]string
	slots int
	held  map[string]bool
}

type model struct {
	m        int
	workers  map[string]*mWorker
	ops      map[string]*mOp
	cache    map[string]int
	waiters  map[int]*mWaiter
	queued   map[string]*mOp
	seq      int
	waiterID int
	log      []string
}

func newModel(m int) *model {
	return &model{
		m:       m,
		workers: map[string]*mWorker{},
		ops:     map[string]*mOp{},
		cache:   map[string]int{},
		waiters: map[int]*mWaiter{},
		queued:  map[string]*mOp{},
	}
}

func (md *model) rec(format string, args ...any) {
	md.log = append(md.log, fmt.Sprintf(format, args...))
}

func mEqualPlatform(a, b action.Platform) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}

func mMatch(platform, props map[string]string) bool {
	for k, v := range platform {
		if pv, ok := props[k]; !ok || pv != v {
			return false
		}
	}
	return true
}

func cloneP(p action.Platform) action.Platform {
	c := action.Platform{}
	for k, v := range p {
		c[k] = v
	}
	return c
}

func (md *model) sortedQueue() []*mOp {
	out := make([]*mOp, 0, len(md.queued))
	for _, op := range md.queued {
		out = append(out, op)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].prio() != out[j].prio() {
			return out[i].prio() > out[j].prio()
		}
		return out[i].seq < out[j].seq
	})
	return out
}

func (md *model) execute(digest string, p action.Platform, prio int, skip bool) (int, string) {
	if digest == "" || len(p) > 8 || prio < 0 || prio > 9 {
		return 0, "invalid"
	}
	if !skip {
		if exit, ok := md.cache[digest]; ok {
			md.waiterID++
			id := md.waiterID
			md.waiters[id] = &mWaiter{id: id, prio: prio, terminal: "Cached", exit: exit}
			md.rec("Execute(%s,prio=%d,skip=%v) => waiter %d Cached(%d)", digest, prio, skip, id, exit)
			return id, ""
		}
	}
	if op, ok := md.ops[digest]; ok {
		if !mEqualPlatform(op.platform, p) {
			return 0, "invalid"
		}
		md.waiterID++
		id := md.waiterID
		op.waiters[id] = &mWaiter{id: id, prio: prio}
		md.waiters[id] = op.waiters[id]
		md.rec("Execute(%s,prio=%d,skip=%v) => waiter %d attached seq %d", digest, prio, skip, id, op.seq)
		return id, ""
	}
	md.waiterID++
	md.seq++
	id := md.waiterID
	op := &mOp{
		digest:   digest,
		platform: cloneP(p),
		seq:      md.seq,
		status:   mQueued,
		waiters:  map[int]*mWaiter{},
	}
	op.waiters[id] = &mWaiter{id: id, prio: prio}
	md.waiters[id] = op.waiters[id]
	md.ops[digest] = op
	md.queued[digest] = op
	md.rec("Execute(%s,prio=%d,skip=%v) => waiter %d new op seq %d", digest, prio, skip, id, op.seq)
	return id, ""
}

func (md *model) register(name string, props map[string]string, slots int) string {
	if slots < 1 || slots > 64 {
		return "invalid"
	}
	if _, ok := md.workers[name]; ok {
		return "exists"
	}
	cp := map[string]string{}
	for k, v := range props {
		cp[k] = v
	}
	md.workers[name] = &mWorker{props: cp, slots: slots, held: map[string]bool{}}
	md.rec("Register(%s,slots=%d) ok", name, slots)
	return ""
}

func (md *model) poll(name string) (string, int, string) {
	w, ok := md.workers[name]
	if !ok {
		return "", 0, "notfound"
	}
	if len(w.held) >= w.slots {
		return "", 0, "noslot"
	}
	for _, op := range md.sortedQueue() {
		if mMatch(op.platform, w.props) {
			delete(md.queued, op.digest)
			op.status = mAssigned
			op.holder = name
			op.attempt = op.losses + 1
			w.held[op.digest] = true
			md.rec("Poll(%s) => %s attempt %d", name, op.digest, op.attempt)
			return op.digest, op.attempt, ""
		}
	}
	md.rec("Poll(%s) => empty", name)
	return "", 0, "empty"
}

func (md *model) complete(name, digest string, attempt, exit int, infra bool) string {
	if name == "" || digest == "" {
		return "invalid"
	}
	w, ok := md.workers[name]
	if !ok {
		return "notfound"
	}
	op, found := md.ops[digest]
	if !found || op.status != mAssigned || op.holder != name || !w.held[digest] {
		return "state"
	}
	if attempt != op.attempt {
		return "stale"
	}
	delete(w.held, digest)
	if infra {
		before := op.losses
		md.handleLoss(op)
		md.rec("Complete(%s,%s,attempt=%d,infra) losses %d->%d", name, digest, attempt, before, op.losses)
		return ""
	}
	for _, x := range op.waiters {
		x.terminal = "Result"
		x.exit = exit
	}
	if exit == 0 {
		md.cache[digest] = 0
	}
	delete(md.ops, digest)
	md.rec("Complete(%s,%s,exit=%d) => Result x%d cacheWrite=%v", name, digest, exit, len(op.waiters), exit == 0)
	return ""
}

func (md *model) handleLoss(op *mOp) {
	op.losses++
	op.holder = ""
	op.attempt = 0
	if len(op.waiters) == 0 {
		delete(md.ops, op.digest)
		return
	}
	if op.losses >= md.m {
		for _, x := range op.waiters {
			x.terminal = "Lost"
		}
		delete(md.ops, op.digest)
		return
	}
	op.status = mQueued
	md.queued[op.digest] = op
}

func (md *model) workerLost(name string) string {
	if _, ok := md.workers[name]; !ok {
		return "notfound"
	}
	var ops []*mOp
	for d := range md.workers[name].held {
		if op, ok := md.ops[d]; ok && op.status == mAssigned && op.holder == name {
			ops = append(ops, op)
		}
	}
	sort.Slice(ops, func(i, j int) bool { return ops[i].seq < ops[j].seq })
	delete(md.workers, name)
	for _, op := range ops {
		md.handleLoss(op)
	}
	md.rec("WorkerLost(%s) processed %d ops", name, len(ops))
	return ""
}

func (md *model) cancel(id int) string {
	w, ok := md.waiters[id]
	if !ok {
		return "notfound"
	}
	if w.terminal != "" {
		return "state"
	}
	w.terminal = "Cancelled"
	md.rec("Cancel(%d) ok", id)
	var op *mOp
	for _, o := range md.ops {
		if _, member := o.waiters[id]; member {
			op = o
			break
		}
	}
	if op == nil {
		return ""
	}
	delete(op.waiters, id)
	if len(op.waiters) == 0 && op.status == mQueued {
		delete(md.queued, op.digest)
		delete(md.ops, op.digest)
	}
	return ""
}
