package exec

import (
	"fmt"
	"sort"

	"ontology/action"
)

// naiveOp / naiveWaiter 是与生产实现完全独立的逐步朴素模拟。
// 它直接把题目规则逐条编码，不使用堆（每步线性排序），用于随机对照。
type naiveWaiter struct {
	id, prio int
	terminal bool
	out      action.Outcome
}

type naiveOp struct {
	digest string
	plat   action.Platform
	seq    int
	state  action.State
	losses int
	waiter map[int]*naiveWaiter
	holder string
}

type naiveWorker struct {
	props map[string]string
	slots int
	held  map[string]bool
}

type naive struct {
	M        int
	cache    map[string]int
	inflight map[string]*naiveOp
	workers  map[string]*naiveWorker
	waiters  map[int]*naiveWaiter
	seq, wid int
}

func newNaive(M int) *naive {
	return &naive{
		M:        M,
		cache:    map[string]int{},
		inflight: map[string]*naiveOp{},
		workers:  map[string]*naiveWorker{},
		waiters:  map[int]*naiveWaiter{},
	}
}

func nValidKV(kvs []action.KV) bool {
	if len(kvs) > 8 {
		return false
	}
	for _, kv := range kvs {
		if kv.Key == "" || kv.Value == "" {
			return false
		}
	}
	return true
}

func nEqual(a, b action.Platform) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]string{}
	for _, kv := range b {
		m[kv.Key] = kv.Value
	}
	for _, kv := range a {
		if m[kv.Key] != kv.Value {
			return false
		}
	}
	return true
}

func nSubset(p action.Platform, props map[string]string) bool {
	for _, kv := range p {
		if props[kv.Key] != kv.Value {
			return false
		}
	}
	return true
}

type stepResult struct {
	id       int
	err      error
	ok       bool
	digest   string
	att      int
	outcomes map[int]action.Outcome
	queue    []string
	cache    map[string]int
}

func (n *naive) alloc() int { n.wid++; return n.wid }

func (n *naive) finish(o *naiveOp, out action.Outcome) map[int]action.Outcome {
	got := map[int]action.Outcome{}
	for id, w := range o.waiter {
		if !w.terminal {
			w.terminal = true
			w.out = out
			got[id] = out
		}
	}
	return got
}

func (n *naive) prio(o *naiveOp) int {
	m := -1
	for _, w := range o.waiter {
		if w.prio > m {
			m = w.prio
		}
	}
	return m
}

func (n *naive) queued() []*naiveOp {
	var qs []*naiveOp
	for _, o := range n.inflight {
		if o.state == action.Queued {
			qs = append(qs, o)
		}
	}
	sort.Slice(qs, func(i, j int) bool {
		pi, pj := n.prio(qs[i]), n.prio(qs[j])
		if pi != pj {
			return pi > pj
		}
		return qs[i].seq < qs[j].seq
	})
	return qs
}

func (n *naive) queueOrder() []string {
	qs := n.queued()
	out := make([]string, len(qs))
	for i, o := range qs {
		out[i] = o.digest
	}
	return out
}

func (n *naive) handleLoss(o *naiveOp) map[int]action.Outcome {
	o.losses++
	o.holder = ""
	if len(o.waiter) == 0 {
		o.state = action.Done
		delete(n.inflight, o.digest)
		return nil
	}
	if o.losses >= n.M {
		got := n.finish(o, action.Outcome{Kind: action.Lost})
		o.state = action.Done
		delete(n.inflight, o.digest)
		return got
	}
	o.state = action.Queued // seq 保持原值，排序自然按原序号归位
	return nil
}

func (n *naive) execute(digest string, p action.Platform, prio int, skip bool) stepResult {
	r := stepResult{outcomes: map[int]action.Outcome{}}
	if digest == "" || !nValidKV(p) || prio < 0 || prio > 9 {
		r.err = action.ErrInvalid
		return r
	}
	if !skip {
		if exit, ok := n.cache[digest]; ok {
			id := n.alloc()
			w := &naiveWaiter{id: id, prio: prio, terminal: true, out: action.Outcome{Kind: action.Cached, Exit: exit}}
			n.waiters[id] = w
			r.id, r.outcomes[id] = id, w.out
			r.digest = digest
			r.queue, r.cache = n.queueOrder(), n.snapCache()
			return r
		}
	}
	if o, ok := n.inflight[digest]; ok {
		if !nEqual(p, o.plat) {
			r.err = action.ErrInvalid
			return r
		}
		id := n.alloc()
		o.waiter[id] = &naiveWaiter{id: id, prio: prio}
		n.waiters[id] = o.waiter[id]
		r.id, r.digest = id, digest
		r.queue, r.cache = n.queueOrder(), n.snapCache()
		return r
	}
	id := n.alloc()
	w := &naiveWaiter{id: id, prio: prio}
	n.waiters[id] = w
	n.seq++
	o := &naiveOp{
		digest: digest, plat: append(action.Platform(nil), p...), seq: n.seq,
		state: action.Queued, waiter: map[int]*naiveWaiter{id: w},
	}
	n.inflight[digest] = o
	r.id, r.digest = id, digest
	r.queue, r.cache = n.queueOrder(), n.snapCache()
	return r
}

func (n *naive) register(name string, kvs []action.KV, slots int) stepResult {
	r := stepResult{outcomes: map[int]action.Outcome{}}
	if name == "" || !nValidKV(kvs) || slots < 1 || slots > 64 {
		r.err = action.ErrInvalid
		return r
	}
	if _, ok := n.workers[name]; ok {
		r.err = action.ErrExists
		return r
	}
	m := map[string]string{}
	for _, kv := range kvs {
		m[kv.Key] = kv.Value
	}
	n.workers[name] = &naiveWorker{props: m, slots: slots, held: map[string]bool{}}
	r.queue, r.cache = n.queueOrder(), n.snapCache()
	return r
}

func (n *naive) poll(name string) stepResult {
	r := stepResult{outcomes: map[int]action.Outcome{}}
	if name == "" {
		r.err = action.ErrInvalid
		return r
	}
	w, ok := n.workers[name]
	if !ok {
		r.err = action.ErrNotFound
		return r
	}
	if len(w.held) >= w.slots {
		r.err = action.ErrNoFreeSlot
		return r
	}
	for _, o := range n.queued() { // 不匹配的跳过，不挡住后面
		if nSubset(o.plat, w.props) {
			o.state = action.Assigned
			o.holder = name
			w.held[o.digest] = true
			r.ok, r.digest, r.att = true, o.digest, o.losses+1
			break
		}
	}
	r.queue, r.cache = n.queueOrder(), n.snapCache()
	return r
}

func (n *naive) complete(name, digest string, attempt, exit int, infra bool) stepResult {
	r := stepResult{outcomes: map[int]action.Outcome{}}
	if name == "" || digest == "" || attempt < 1 {
		r.err = action.ErrInvalid
		return r
	}
	w, ok := n.workers[name]
	if !ok {
		r.err = action.ErrNotFound
		return r
	}
	o, exists := n.inflight[digest]
	if !exists || !w.held[digest] || o.holder != name {
		r.err = action.ErrState
		return r
	}
	if attempt != o.losses+1 {
		r.err = action.ErrAttemptStale
		return r
	}
	delete(w.held, digest)
	if infra {
		r.outcomes = n.handleLoss(o)
		r.queue, r.cache = n.queueOrder(), n.snapCache()
		return r
	}
	if exit == 0 {
		n.cache[digest] = 0
	}
	r.outcomes = n.finish(o, action.Outcome{Kind: action.Result, Exit: exit})
	o.state = action.Done
	delete(n.inflight, digest)
	r.queue, r.cache = n.queueOrder(), n.snapCache()
	return r
}

func (n *naive) workerLost(name string) stepResult {
	r := stepResult{outcomes: map[int]action.Outcome{}}
	if name == "" {
		r.err = action.ErrInvalid
		return r
	}
	w, ok := n.workers[name]
	if !ok {
		r.err = action.ErrNotFound
		return r
	}
	digests := make([]string, 0, len(w.held))
	for d := range w.held {
		digests = append(digests, d)
	}
	sort.Slice(digests, func(i, j int) bool { return n.inflight[digests[i]].seq < n.inflight[digests[j]].seq })
	for _, d := range digests {
		for id, o := range n.handleLoss(n.inflight[d]) {
			r.outcomes[id] = o
		}
	}
	delete(n.workers, name)
	r.queue, r.cache = n.queueOrder(), n.snapCache()
	return r
}

func (n *naive) cancel(id int) stepResult {
	r := stepResult{outcomes: map[int]action.Outcome{}}
	if id < 1 {
		r.err = action.ErrInvalid
		return r
	}
	w, ok := n.waiters[id]
	if !ok {
		r.err = action.ErrNotFound
		return r
	}
	if w.terminal {
		r.err = action.ErrState
		return r
	}
	var owner *naiveOp
	for _, o := range n.inflight {
		if _, a := o.waiter[id]; a {
			owner = o
			break
		}
	}
	w.terminal = true
	w.out = action.Outcome{Kind: action.Cancelled}
	r.outcomes[id] = w.out
	if owner != nil {
		delete(owner.waiter, id)
		if len(owner.waiter) == 0 {
			switch owner.state {
			case action.Queued:
				owner.state = action.Done
				delete(n.inflight, owner.digest)
			case action.Assigned:
				owner.state = action.Abandoned
			}
		}
	}
	r.queue, r.cache = n.queueOrder(), n.snapCache()
	return r
}

func (n *naive) snapCache() map[string]int {
	out := map[string]int{}
	for k, v := range n.cache {
		out[k] = v
	}
	return out
}

func (n *naive) waiterOutcome(id int) (action.Outcome, bool) {
	w, ok := n.waiters[id]
	if !ok {
		return action.Outcome{}, false
	}
	return w.out, w.terminal
}

var _ = fmt.Sprintf
