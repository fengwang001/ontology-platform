// Package causal 实现多节点因果广播：各节点向量时钟、广播/投递与待投递缓冲，依赖 vc。
package causal

import (
	"errors"
	"sync"

	"ontology/vc"
)

// 三类可判定、互不相同的哨兵错误。
var (
	ErrNodeOutOfRange = errors.New("causal: node index out of range")
	ErrUnknownMessage = errors.New("causal: message not in pending buffer")
	ErrSelfDelivery   = errors.New("causal: delivering a message to its own broadcaster")
)

// msgID 是消息身份：(广播者, 该广播者的第几条)，全局唯一。
type msgID struct{ from, seq int }

// System 持有 n 个节点的向量时钟与每个节点的待投递缓冲。
type System struct {
	mu        sync.RWMutex
	n         int
	clocks    []vc.Clock             // clocks[p]：节点 p 当前的向量时钟
	pending   []map[msgID]vc.Message // pending[p]：节点 p 尚未投递的消息，按身份 O(1) 索引
	broadcast uint64                 // 已生成消息数（身份分配计数）
	// lastCheck 是非导出计数器：最近一次 Deliver 为定位/校验该消息而检查过的
	// 待投递消息个数。map 直接索引恒为 1，不随缓冲规模增长。不暴露给公开接口。
	lastCheck int
}

// New 创建 n 个节点、时钟全零的系统；n 为负按 0 处理。
func New(n int) *System {
	if n < 0 {
		n = 0
	}
	s := &System{n: n, clocks: make([]vc.Clock, n), pending: make([]map[msgID]vc.Message, n)}
	for p := 0; p < n; p++ {
		s.clocks[p] = make(vc.Clock, n)
		s.pending[p] = map[msgID]vc.Message{}
	}
	return s
}

// N 返回节点数（包内自检/测试使用）。
func (s *System) N() int { return s.n }

// Broadcast：from 的自身分量加 1，消息携带此刻时钟的独立副本；
// 广播者自己视作已投递，消息进入其余每个节点的待投递缓冲。
func (s *System) Broadcast(from int) (vc.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if from < 0 || from >= s.n { // 校验先于任何状态写入
		return vc.Message{}, ErrNodeOutOfRange
	}
	ts := cloneClock(s.clocks[from])
	ts[from]++
	s.clocks[from] = ts
	m := vc.Message{From: from, TS: cloneClock(ts)}
	id := msgID{from, ts[from]}
	for p := 0; p < s.n; p++ {
		if p != from {
			s.pending[p][id] = vc.Message{From: from, TS: cloneClock(ts)}
		}
	}
	s.broadcast++
	return m, nil
}

// Deliver：消息在 node 的缓冲中且满足投递条件时，只把发送者分量加 1 并移出缓冲；
// 条件不满足则阻塞（delivered=false、err=nil、状态不变）；引用非法则整体失败。
func (s *System) Deliver(node int, m vc.Message) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if node < 0 || node >= s.n { // 1) 下标越界
		return false, ErrNodeOutOfRange
	}
	if m.From < 0 || m.From >= s.n {
		return false, ErrNodeOutOfRange
	}
	if node == m.From { // 2) 自投递（优先于缓冲查询，保证错误可区分）
		return false, ErrSelfDelivery
	}
	if len(m.TS) != s.n { // 畸形时间戳不可能在缓冲中
		s.lastCheck = 0
		return false, ErrUnknownMessage
	}
	id := msgID{m.From, m.TS[m.From]}
	s.lastCheck = 1 // 直接按身份索引一次，不扫描缓冲
	got, ok := s.pending[node][id]
	if !ok { // 3) 未知消息
		return false, ErrUnknownMessage
	}
	if !vc.Deliverable(s.clocks[node], got.TS, got.From) { // 阻塞：状态不变
		return false, nil
	}
	s.clocks[node][got.From]++ // 只推进发送者分量
	delete(s.pending[node], id)
	return true, nil
}

// VC 返回节点 node 向量时钟的独立副本，只读路径使用读锁可并发。
func (s *System) VC(node int) (vc.Clock, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if node < 0 || node >= s.n {
		return nil, ErrNodeOutOfRange
	}
	return cloneClock(s.clocks[node]), nil
}

// cloneClock 返回时钟的独立副本。
func cloneClock(c vc.Clock) vc.Clock {
	out := make(vc.Clock, len(c))
	copy(out, c)
	return out
}
