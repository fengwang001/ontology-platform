package audit

import (
	"fmt"
	"sync"
)

// MemoryAnchorStore 是线程安全的内存锚点存储，锚点只增不改。
type MemoryAnchorStore struct {
	mu      sync.Mutex
	anchors []Anchor
}

// NewMemoryAnchorStore 创建空的内存锚点存储。
func NewMemoryAnchorStore() *MemoryAnchorStore {
	return &MemoryAnchorStore{}
}

// Publish 原子发布锚点；序号必须严格递增，否则拒绝。
func (s *MemoryAnchorStore) Publish(a Anchor) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n := len(s.anchors); n > 0 && a.Seq <= s.anchors[n-1].Seq {
		return fmt.Errorf("audit: 锚点序号必须严格递增，已有 %d，收到 %d", s.anchors[n-1].Seq, a.Seq)
	}
	s.anchors = append(s.anchors, a)
	return nil
}

// Snapshot 返回按序号升序的锚点快照。
func (s *MemoryAnchorStore) Snapshot() []Anchor {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Anchor(nil), s.anchors...)
}
