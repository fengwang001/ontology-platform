// Package rside 维护 KTable 外键连接的右侧：右表、订阅表 sub[fk]={k:哈希}、
// 每个 fk 一条 FIFO 响应队列。本包不依赖其他包，自身不加锁——由 lside 串行调用。
package rside

import "sort"

// Response 是投递给左侧的一条响应：RVal 为 nil 表示右表该行已删除。
type Response struct {
	K    string
	Hash uint64
	RVal *string
}

// RSide 见包注释。lastChecked 记录最近一次右表变更检查过的订阅条目个数；
// 它是非导出字段，且没有任何导出读口，仅供同包测试核验复杂度。
type RSide struct {
	right       map[string]string            // fk → RVal
	subs        map[string]map[string]uint64 // fk → k → 订阅时哈希
	queues      map[string][]*Response
	lastChecked int
}

// New 创建空右侧。
func New() *RSide {
	return &RSide{
		right:  map[string]string{},
		subs:   map[string]map[string]uint64{},
		queues: map[string][]*Response{},
	}
}

// RightValue 返回右表当前值。
func (r *RSide) RightValue(fk string) (string, bool) {
	v, ok := r.right[fk]
	return v, ok
}

// Subscribe 记录订阅并立即向 fk 队列尾追加一条当前右值（可能为 nil）的响应。
func (r *RSide) Subscribe(fk, k string, hash uint64) {
	s := r.subs[fk]
	if s == nil {
		s = map[string]uint64{}
		r.subs[fk] = s
	}
	s[k] = hash
	resp := &Response{K: k, Hash: hash}
	if v, ok := r.right[fk]; ok {
		vv := v
		resp.RVal = &vv
	}
	r.queues[fk] = append(r.queues[fk], resp)
}

// Unsubscribe 从 sub[fk] 删去 k。
func (r *RSide) Unsubscribe(fk, k string) {
	if s := r.subs[fk]; s != nil {
		delete(s, k)
		if len(s) == 0 {
			delete(r.subs, fk)
		}
	}
}

// ChangeRight 写入（rval 非 nil）或删除（nil）右表一行，
// 并按 k 字典序向 fk 队列追加所有订阅者的响应，返回追加条数。
// 调用方负责保证本次不是空操作。
func (r *RSide) ChangeRight(fk string, rval *string) int {
	if rval != nil {
		r.right[fk] = *rval
	} else {
		delete(r.right, fk)
	}
	s := r.subs[fk]
	r.lastChecked = len(s)
	keys := make([]string, 0, len(s))
	for k := range s {
		keys = append(keys, k)
	}
	// 按 k 字典序生成响应，保证同一右表变更的响应顺序可复现。
	sort.Strings(keys)
	for _, k := range keys {
		resp := &Response{K: k, Hash: s[k], RVal: rval}
		if rval != nil {
			vv := *rval
			resp.RVal = &vv
		}
		r.queues[fk] = append(r.queues[fk], resp)
	}
	return len(keys)
}

// Pop 取出 fk 队列队首；空队列返回 ok=false。
func (r *RSide) Pop(fk string) (*Response, bool) {
	q := r.queues[fk]
	if len(q) == 0 {
		return nil, false
	}
	resp := q[0]
	copy(q, q[1:])
	r.queues[fk] = q[:len(q)-1]
	return resp, true
}

// Pending 返回全部队列待投递响应总数。
func (r *RSide) Pending() int {
	n := 0
	for _, q := range r.queues {
		n += len(q)
	}
	return n
}

// SubsLen 返回 sub[fk] 当前订阅者数（不生成任何响应）。
func (r *RSide) SubsLen(fk string) int { return len(r.subs[fk]) }

// QueueLen 返回指定 fk 的队列长度。
func (r *RSide) QueueLen(fk string) int { return len(r.queues[fk]) }

// NextQueuedFK 返回任意一个非空队列的 fk。
func (r *RSide) NextQueuedFK() (string, bool) {
	for fk, q := range r.queues {
		if len(q) > 0 {
			return fk, true
		}
	}
	return "", false
}
