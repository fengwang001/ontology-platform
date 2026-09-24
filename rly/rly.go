// Package rly 实现中继与下游：按规则取批、先投递后标记、
// 崩溃注入、下游按 id 幂等接收。依赖 obx。
package rly

import (
	"sync"

	"ontology/obx"
)

// Sink 是下游：已见 id 集合、已应用序列、重复计数。
type Sink struct {
	seen    map[int]struct{}
	applied []int
	dups    int
}

// Relay 是中继。进程内不保存任何游标，每批状态全部来自发件箱。
type Relay struct {
	mu   sync.Mutex
	box  *obx.Box
	sink Sink
}

func New(box *obx.Box) *Relay {
	return &Relay{box: box, sink: Sink{seen: make(map[int]struct{})}}
}

// Relay 取出全部已提交未标记消息，按 (csn, id) 逐条先投递后标记。
// crash=true 时在本批最后一条「已投递、未标记」处崩溃；空批什么也不发生。
// 返回本批实际投递的 id 序列。
func (r *Relay) Relay(crash bool) []int {
	r.mu.Lock()
	defer r.mu.Unlock()
	batch := r.box.Take()
	delivered := make([]int, 0, len(batch))
	for i, m := range batch {
		r.deliver(m.ID)
		delivered = append(delivered, m.ID)
		if crash && i == len(batch)-1 {
			return delivered // 崩溃点：最后一条已投递、未标记
		}
		r.box.Mark(m.ID)
	}
	return delivered
}

// deliver 下游幂等接收：已见过的 id 只计重复，否则应用并记入集合。
func (r *Relay) deliver(id int) {
	if _, ok := r.sink.seen[id]; ok {
		r.sink.dups++
		return
	}
	r.sink.seen[id] = struct{}{}
	r.sink.applied = append(r.sink.applied, id)
}

// Applied 返回下游已应用 id 序列的副本。
func (r *Relay) Applied() []int {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]int, len(r.sink.applied))
	copy(out, r.sink.applied)
	return out
}

// Dups 返回下游重复计数。
func (r *Relay) Dups() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sink.dups
}
