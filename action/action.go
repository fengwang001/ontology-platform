package action

import "sort"

// Platform 是操作对工作者属性提出的键值要求集合。
type Platform map[string]string

// Status 是在途操作的生命周期状态。
type Status int

const (
	Queued Status = iota
	Assigned
)

// Outcome 是等待者收到的唯一一次终局。
type Outcome struct {
	Kind   string // Cached | Result | Cancelled | Lost
	Exit   int
	Cached bool
}

// Waiter 是附着在某个操作上的等待者。
type Waiter struct {
	ID      int
	Prio    int
	Done    chan Outcome
	done    bool
	outcome Outcome
	Op      *Op
}

// Op 是一个在途操作（Queued 或 Assigned）。
type Op struct {
	Digest   string
	Platform Platform
	Seq      int
	Status   Status
	Losses   int
	Prio     int
	Waiters  map[int]*Waiter
	Holder   string
	Attempt  int
}

// Index 是按摘要索引的在途操作表与结果缓存。
type Index struct {
	inflight map[string]*Op
	cache    map[string]int
	lookups  int
}

func NewIndex() *Index {
	return &Index{
		inflight: map[string]*Op{},
		cache:    map[string]int{},
	}
}

// Lookup 对缓存表与在途表各做至多一次 map 访问：一次 Execute 共 2 次查找。
func (ix *Index) Lookup(digest string, skipCache bool) (op *Op, cachedExit int, cached bool) {
	if !skipCache {
		ix.lookups++
		if exit, ok := ix.cache[digest]; ok {
			return nil, exit, true
		}
	}
	ix.lookups++
	return ix.inflight[digest], 0, false
}

func (ix *Index) Inflight(digest string) (*Op, bool) { op, ok := ix.inflight[digest]; return op, ok }

func (ix *Index) PutInflight(op *Op) { ix.inflight[op.Digest] = op }

func (ix *Index) DeleteInflight(digest string) { delete(ix.inflight, digest) }

func (ix *Index) PutCache(digest string, exit int) { ix.cache[digest] = exit }

func (ix *Index) Lookups() int { return ix.lookups }

// AddWaiter 附着一个现存等待者，并重算有效优先级。
func (op *Op) AddWaiter(w *Waiter) {
	op.Waiters[w.ID] = w
	w.Op = op
	op.RecomputePrio()
}

func (op *Op) RemoveWaiter(id int) {
	delete(op.Waiters, id)
	op.RecomputePrio()
}

// RecomputePrio 只访问该操作自己的等待者。
func (op *Op) RecomputePrio() {
	max := -1
	for _, w := range op.Waiters {
		if w.Prio > max {
			max = w.Prio
		}
	}
	op.Prio = max
}

// Deliver 向全部现存等待者各投递一次终局；channel 缓冲为 1，绝不重复投递。
func (op *Op) Deliver(o Outcome) {
	for _, w := range op.Waiters {
		w.settle(o)
	}
}

// DeliverOne 向单个等待者投递一次终局（Cancel 场景）。
func (w *Waiter) DeliverOne(o Outcome) bool {
	if w.done {
		return false
	}
	w.settle(o)
	return true
}

func (w *Waiter) settle(o Outcome) {
	w.done = true
	w.outcome = o
	w.Done <- o
	close(w.Done)
}

func (w *Waiter) Finished() bool { return w.done }

// Snapshot 非破坏性地返回终局：已终局时 settled 为 true。
func (w *Waiter) Snapshot() (o Outcome, settled bool) {
	return w.outcome, w.done
}

// EqualPlatform 判定两个 platform 是否完全相同（同样的键值对集合）。
func EqualPlatform(a, b Platform) bool {
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

// MatchedBy 判定 props 是否包含 platform 中的全部键值且相等。
func MatchedBy(platform, props map[string]string) bool {
	for k, v := range platform {
		if pv, ok := props[k]; !ok || pv != v {
			return false
		}
	}
	return true
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
