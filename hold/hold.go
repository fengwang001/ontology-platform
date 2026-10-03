// Package hold 维护法律保全（legal hold）登记表。
// 每条保全是一个半开时间区间 [From,To)；它只阻止之后发生的折叠/删除，
// 不恢复已经折叠的数据。Registry 的方法可被并发调用。
package hold

import (
	"sort"
	"sync"

	"ontology/tier"
)

// Hold 是一条半开区间 [From,To) 的保全。
type Hold struct {
	ID    string
	From  int64
	To    int64
	Owner string
}

// Registry 是线程安全的保全登记表。
type Registry struct {
	mu    sync.Mutex
	holds map[string]Hold
}

// NewRegistry 创建空登记表。
func NewRegistry() *Registry {
	return &Registry{holds: make(map[string]Hold)}
}

// validRange 校验时间参数在 [0,10^13]。
func validRange(x int64) bool { return 0 <= x && x <= tier.MaxClock }

// Place 登记一条保全。
// 拒绝顺序：参数非法 -> id 重复。
func (r *Registry) Place(id string, from, to int64, owner string) error {
	if id == "" || owner == "" || !validRange(from) || !validRange(to) || from >= to {
		return tier.ErrInvalidArgument
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.holds[id]; ok {
		return tier.ErrDuplicateHold
	}
	r.holds[id] = Hold{ID: id, From: from, To: to, Owner: owner}
	return nil
}

// Release 解除保全。拒绝顺序：参数非法 -> 不存在 -> 权限。
func (r *Registry) Release(id, who string) error {
	if id == "" || who == "" {
		return tier.ErrInvalidArgument
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	h, ok := r.holds[id]
	if !ok {
		return tier.ErrHoldNotFound
	}
	if who != h.Owner && who != "admin" {
		return tier.ErrPermission
	}
	delete(r.holds, id)
	return nil
}

// Snapshot 返回当前全部保全，按 ID 排序，保证遍历结果可复现。
func (r *Registry) Snapshot() []Hold {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Hold, 0, len(r.holds))
	for _, h := range r.holds {
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// IntersectsInterval 报告是否存在与半开区间 [from,to) 相交的生效保全。
// 相交按半开判定，端点相接不算相交。
func (r *Registry) IntersectsInterval(from, to int64) bool {
	for _, h := range r.Snapshot() {
		if tier.IntervalsOverlap(from, to, h.From, h.To) {
			return true
		}
	}
	return false
}
