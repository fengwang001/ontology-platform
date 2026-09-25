// Package api 对外门面：New/AddNode/RemoveNode/Elect/SelfCheck。
// 依赖 elect（进而依赖 ring），反向依赖不存在。
package api

import (
	"fmt"
	"sync"

	"ontology/elect"
	"ontology/ring"
)

// 对外再导出三类可判定错误，便于调用方 errors.Is 判定。
var (
	ErrEmptyID  = ring.ErrEmptyID
	ErrDupID    = ring.ErrDupID
	ErrNotFound = ring.ErrNotFound
)

// System 是一个并发安全的选举系统，状态全在进程内存。
type System struct {
	mu sync.RWMutex
	r  *ring.Ring
}

func New() *System { return &System{r: ring.New()} }

// AddNode 加节点；空 ID、重复 ID 整体失败且不改状态。
func (s *System) AddNode(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.r.Add(id)
}

// RemoveNode 删节点；删除不存在的节点整体失败且不改状态。
func (s *System) RemoveNode(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.r.Remove(id)
}

// Size 返回当前节点数。
func (s *System) Size() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.r.Size()
}

// Elect 跑一次 Chang–Roberts 选举，返回胜者 ID 与总消息数。
func (s *System) Elect() (string, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	res, err := elect.Elect(s.r)
	return res.Winner, res.Messages, err
}

// SelfCheck 对内置节点序列核验四条不变量：正确性、终止性、
// 环闭合、失败不留痕。全部通过返回 nil，否则返回具体错误。
func SelfCheck() error {
	seqs := [][]string{
		{"3", "7", "2", "5"},
		{"9"},
		{"a", "b", "c", "d", "e", "f"},
		{"x1", "x9", "x5", "x2", "x8", "x3", "x7"},
	}
	for _, seq := range seqs {
		s := New()
		for _, id := range seq {
			if err := s.AddNode(id); err != nil {
				return fmt.Errorf("selfcheck build: %w", err)
			}
		}
		// 不变量 3：环闭合——从任意节点沿后继走恰好访问每个节点一次。
		s.mu.RLock()
		for _, start := range s.r.Order() {
			seen, cur := map[string]bool{}, start
			for i := 0; i < s.r.Size(); i++ {
				seen[cur] = true
				cur, _ = s.r.Successor(cur)
			}
			if cur != start || len(seen) != s.r.Size() {
				s.mu.RUnlock()
				return fmt.Errorf("selfcheck: ring not closed at %q", start)
			}
		}
		// 不变量 1+2：选举必返回且胜者恒等于朴素最大。
		max, _ := s.r.Max()
		res, err := elect.Elect(s.r)
		s.mu.RUnlock()
		if err != nil {
			return fmt.Errorf("selfcheck: elect did not terminate: %w", err)
		}
		if res.Winner != max {
			return fmt.Errorf("selfcheck: winner %q != naive max %q", res.Winner, max)
		}
		if res.Messages > len(seq)*len(seq) { // 每条消息至多绕环一圈
			return fmt.Errorf("selfcheck: message bound exceeded: %d", res.Messages)
		}
	}
	// 不变量 4：失败不留痕——被拒操作不改状态且系统仍可用。
	s := New()
	_ = s.AddNode("k1")
	before := s.Size()
	if s.AddNode("") == nil || s.AddNode("k1") == nil || s.RemoveNode("nope") == nil {
		return fmt.Errorf("selfcheck: invalid op not rejected")
	}
	if s.Size() != before {
		return fmt.Errorf("selfcheck: rejected op changed state")
	}
	w, _, err := s.Elect()
	if err != nil || w != "k1" {
		return fmt.Errorf("selfcheck: system unusable after rejection")
	}
	return nil
}
