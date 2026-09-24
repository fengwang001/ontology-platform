// Package rside 维护右表、订阅表与按 fk 的 FIFO 响应队列。不依赖其他包。
package rside

import (
	"errors"
	"sort"
)

// 三类可判定哨兵错误，互不相同。
var (
	ErrEmptyKey       = errors.New("rside: empty key")
	ErrEmptyQueue     = errors.New("rside: empty response queue")
	ErrTooManyPending = errors.New("rside: too many pending responses")
)

// Response 是一条待投递响应：左键 k、订阅时哈希、右值（HasRVal=false 表示 nil）。
type Response struct {
	K       string
	Hash    uint64
	RVal    string
	HasRVal bool
}

// Right 是右表 + 订阅表 + 响应队列。不是并发安全的，由调用方串行化。
type Right struct {
	tab        map[string]string
	sub        map[string]map[string]uint64
	queue      map[string][]Response
	pending    int
	maxPending int
	checked    int // 最近一次右表变更检查过的订阅条目数
}

func New(maxPending int) *Right {
	return &Right{tab: map[string]string{}, sub: map[string]map[string]uint64{},
		queue: map[string][]Response{}, maxPending: maxPending}
}

// CanEnqueue 预检：再入队 n 条响应是否会超过 maxPending。
func (r *Right) CanEnqueue(n int) bool { return r.pending+n <= r.maxPending }

// Subscribe 记录 sub[fk][k]=h 并向 fk 队尾入队一条携带当前右值的响应。
// 调用前必须已用 CanEnqueue(1) 预检，否则可能返回 ErrTooManyPending 且状态不变。
func (r *Right) Subscribe(fk, k string, h uint64) error {
	if !r.CanEnqueue(1) {
		return ErrTooManyPending
	}
	if r.sub[fk] == nil {
		r.sub[fk] = make(map[string]uint64)
	}
	r.sub[fk][k] = h
	v, ok := r.tab[fk]
	r.enqueue(fk, Response{K: k, Hash: h, RVal: v, HasRVal: ok})
	return nil
}

// Unsubscribe 从 sub[fk] 删去 k；不存在则空操作。
func (r *Right) Unsubscribe(fk, k string) { delete(r.sub[fk], k) }

// Put 写入右表；Delete 删除右表键。值不变 / 键不存在为空操作；否则向 sub[fk]
// 每个订阅者（按 k 字典序）入队响应（删除时为 nil），要么全部入队要么一条不入。
// 右表删除不删除订阅。
func (r *Right) Put(fk, rval string) error { return r.change(fk, rval, false) }
func (r *Right) Delete(fk string) error    { return r.change(fk, "", true) }

func (r *Right) change(fk, rval string, del bool) error {
	if fk == "" {
		return ErrEmptyKey
	}
	old, ok := r.tab[fk]
	if (!del && ok && old == rval) || (del && !ok) {
		return nil
	}
	if !r.CanEnqueue(len(r.sub[fk])) {
		return ErrTooManyPending
	}
	if del {
		delete(r.tab, fk)
	} else {
		r.tab[fk] = rval
	}
	r.checked = len(r.sub[fk])
	for _, k := range sortedKeys(r.sub[fk]) {
		r.enqueue(fk, Response{K: k, Hash: r.sub[fk][k], RVal: rval, HasRVal: !del})
	}
	return nil
}

// Deliver 取出 fk 队首响应。fk 为空或队列为空（含从未出现的 fk）报错，状态不变。
func (r *Right) Deliver(fk string) (Response, error) {
	if fk == "" {
		return Response{}, ErrEmptyKey
	}
	q := r.queue[fk]
	if len(q) == 0 {
		return Response{}, ErrEmptyQueue
	}
	resp := q[0]
	r.queue[fk] = q[1:]
	r.pending--
	return resp, nil
}

// PendingFKs 返回当前有未投递响应的 fk 列表（字典序）。
func (r *Right) PendingFKs() []string {
	var out []string
	for fk, q := range r.queue {
		if len(q) > 0 {
			out = append(out, fk)
		}
	}
	sort.Strings(out)
	return out
}

// Snapshot 返回右表与订阅表副本（供批量重算与一致性校验）。
func (r *Right) Snapshot() (map[string]string, map[string]map[string]uint64) {
	tab := make(map[string]string, len(r.tab))
	for k, v := range r.tab {
		tab[k] = v
	}
	sub := make(map[string]map[string]uint64, len(r.sub))
	for fk, m := range r.sub {
		cp := make(map[string]uint64, len(m))
		for k, h := range m {
			cp[k] = h
		}
		sub[fk] = cp
	}
	return tab, sub
}

func (r *Right) enqueue(fk string, resp Response) {
	r.queue[fk] = append(r.queue[fk], resp)
	r.pending++
}

func sortedKeys(m map[string]uint64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
