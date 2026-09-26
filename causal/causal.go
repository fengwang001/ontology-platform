// Package causal 实现多节点因果广播：各节点向量时钟、Broadcast 生成消息、
// Deliver 判定与分量推进、待投递缓冲。依赖 vc，不依赖 api。
package causal

import (
	"errors"
	"sync"

	"ontology/vc"
)

// 三类可判定哨兵错误，互不相同。
var (
	ErrNodeOutOfRange = errors.New("causal: node index out of range")
	ErrUnknownMessage = errors.New("causal: message not in pending buffer")
	ErrSelfDelivery   = errors.New("causal: cannot deliver a message to its broadcaster")
)

// Msg 是一条广播消息：From 为发送者，TS 为发送者广播此刻的整份向量时钟副本。
type Msg struct {
	From int
	TS   vc.Vector
}

// key 是消息身份：(发送者, 发送者分量)，同一发送者的消息按序号唯一。
type key struct{ from, seq int }

// System 是 n 个节点的因果广播系统，状态全部在进程内存。
type System struct {
	mu      sync.RWMutex
	clocks  []vc.Vector
	pending map[key]Msg
	// lastChecked 记录最近一次 Deliver 为定位/校验消息检查过的待投递消息个数。
	// 非导出，不出现在任何公开接口。
	lastChecked int
}

// New 创建 n 节点系统，所有向量时钟为零向量。
func New(n int) *System {
	s := &System{clocks: make([]vc.Vector, n), pending: make(map[key]Msg)}
	for i := range s.clocks {
		s.clocks[i] = vc.New(n)
	}
	return s
}

// Broadcast 让节点 from 广播一条消息：自身分量加 1，消息携带此刻的整份时钟副本。
func (s *System) Broadcast(from int) (Msg, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if from < 0 || from >= len(s.clocks) {
		return Msg{}, ErrNodeOutOfRange
	}
	s.clocks[from][from]++
	m := Msg{From: from, TS: s.clocks[from].Clone()}
	s.pending[key{from, m.TS[from]}] = m
	return m, nil
}

// Deliver 尝试把消息 m 投递到节点 node。投递成功返回 (true, nil)；
// 不满足投递条件返回 (false, nil)（阻塞，状态不变）；非法输入返回哨兵错误。
// 所有校验先于任何状态写入，失败不留痕。
func (s *System) Deliver(node int, m Msg) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastChecked = 0
	if node < 0 || node >= len(s.clocks) {
		return false, ErrNodeOutOfRange
	}
	if m.From < 0 || m.From >= len(s.clocks) || len(m.TS) != len(s.clocks) {
		return false, ErrUnknownMessage
	}
	s.lastChecked++ // 按消息身份直接索引待投递缓冲，O(1)
	if _, ok := s.pending[key{m.From, m.TS[m.From]}]; !ok {
		return false, ErrUnknownMessage
	}
	if m.From == node {
		return false, ErrSelfDelivery
	}
	if !vc.Deliverable(s.clocks[node], m.TS, m.From) {
		return false, nil
	}
	s.clocks[node][m.From]++ // 只推进发送者分量
	return true, nil
}

// VC 返回节点 node 当前向量时钟的副本。
func (s *System) VC(node int) (vc.Vector, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if node < 0 || node >= len(s.clocks) {
		return nil, ErrNodeOutOfRange
	}
	return s.clocks[node].Clone(), nil
}

// N 返回节点数。
func (s *System) N() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.clocks)
}
