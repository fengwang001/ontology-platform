package auditlog

import (
	"fmt"
	"sync"
)

// AnchorStore 是外部锚点存储的抽象：只增不改，按序号单调发布。
type AnchorStore interface {
	// Publish 发布（序号，摘要）；序号必须严格递增。
	Publish(seq uint64, d Digest) error
	// MaxSeq 返回已发布的最大锚点序号，无锚点时返回 0。
	MaxSeq() uint64
	// Get 查询指定序号的锚点摘要。
	Get(seq uint64) (Digest, bool)
	// List 按序号升序返回全部锚点。
	List() []Anchor
}

// MemoryAnchorStore 是并发安全的内存锚点存储，用于本地验证与测试。
type MemoryAnchorStore struct {
	mu      sync.RWMutex
	anchors []Anchor
}

// NewMemoryAnchorStore 创建空的内存锚点存储。
func NewMemoryAnchorStore() *MemoryAnchorStore {
	return &MemoryAnchorStore{}
}

// Publish 实现 AnchorStore。
func (s *MemoryAnchorStore) Publish(seq uint64, d Digest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n := len(s.anchors); n > 0 && seq <= s.anchors[n-1].Seq {
		return fmt.Errorf("auditlog: 锚点序号必须严格递增，已发布至 %d，收到 %d", s.anchors[n-1].Seq, seq)
	}
	s.anchors = append(s.anchors, Anchor{Seq: seq, Digest: d})
	return nil
}

// MaxSeq 实现 AnchorStore。
func (s *MemoryAnchorStore) MaxSeq() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if n := len(s.anchors); n > 0 {
		return s.anchors[n-1].Seq
	}
	return 0
}

// Get 实现 AnchorStore。
func (s *MemoryAnchorStore) Get(seq uint64) (Digest, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, a := range s.anchors {
		if a.Seq == seq {
			return a.Digest, true
		}
	}
	return Digest{}, false
}

// List 实现 AnchorStore。
func (s *MemoryAnchorStore) List() []Anchor {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Anchor, len(s.anchors))
	copy(out, s.anchors)
	return out
}
