// Package gate 实现滚动升级期间的对象元数据格式兼容闸门：
// 统一串行化节点注册、元数据读写、集群格式级别的提升（两阶段）与回退。
package gate

import (
	"fmt"
	"sync"

	"ontology/cluster"
	"ontology/meta"
)

var errBadAck = fmt.Errorf("gate: ack fn is nil")

// AckFn / ReleaseFn 由调用方注入；Raise 对快照节点按名升序 Ack，失败时反序 Release。
type AckFn func(node string, target int) error
type ReleaseFn func(node string, target int)

// Stats 是集群状态快照：G 与级别 1..3 的记录条数。
type Stats struct {
	G     int
	Count [4]int
}

// Service 是兼容闸门服务。所有方法可并发调用，等价于某个串行顺序。
type Service struct {
	mu      sync.Mutex
	g       int
	nodes   *cluster.Registry
	store   *meta.Store
	ack     AckFn
	release ReleaseFn
}

// New 创建服务；ack 必须非空，release 可为 nil。
func New(ack AckFn, release ReleaseFn) (*Service, error) {
	if ack == nil {
		return nil, errBadAck
	}
	if release == nil {
		release = func(string, int) {}
	}
	return &Service{
		g:       1,
		nodes:   cluster.New(),
		store:   meta.New(),
		ack:     ack,
		release: release,
	}, nil
}

// Join 注册节点。
func (s *Service) Join(name string, maxLevel int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.nodes.Join(name, maxLevel); err != nil {
		return mapErr("Join", err, "")
	}
	return nil
}

// Leave 移除节点，不改变 G。
func (s *Service) Leave(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.nodes.Leave(name); err != nil {
		return mapErr("Leave", err, "")
	}
	return nil
}

// Put 以当前 G 写入记录；f 中 nil 指针表示该字段缺省。
func (s *Service) Put(node, key string, f meta.Fields) error {
	if node == "" || key == "" || f.Size < 0 || f.Size > 1e12 {
		return gateErr("Put", ErrInvalidArg, "")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.nodes.Get(node)
	if !ok {
		return gateErr("Put", ErrNotExist, "节点 "+node)
	}
	if n.Max < s.g {
		return gateErr("Put", ErrReadOnly, "节点 "+node)
	}
	if f.Etag != nil && s.g < meta.LevelEtag {
		return gateErr("Put", ErrUnsupported, "etag")
	}
	if f.Tags != nil && s.g < meta.LevelTags {
		return gateErr("Put", ErrUnsupported, "tags")
	}
	s.store.Put(key, s.g, f)
	return nil
}

// Get 按节点能力降级读取。
func (s *Service) Get(node, key string) (meta.View, error) {
	if node == "" || key == "" {
		return meta.View{}, gateErr("Get", ErrInvalidArg, "")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.nodes.Get(node)
	if !ok {
		return meta.View{}, gateErr("Get", ErrNotExist, "节点 "+node)
	}
	v, ok := s.store.Read(key, n.Max)
	if !ok {
		return meta.View{}, gateErr("Get", ErrNotExist, "键 "+key)
	}
	return v, nil
}

// Delete 删除记录；任何在册节点可执行。
func (s *Service) Delete(node, key string) error {
	if node == "" || key == "" {
		return gateErr("Delete", ErrInvalidArg, "")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.nodes.Get(node); !ok {
		return gateErr("Delete", ErrNotExist, "节点 "+node)
	}
	if !s.store.Delete(key) {
		return gateErr("Delete", ErrNotExist, "键 "+key)
	}
	return nil
}

// Raise 单步提升集群级别（两阶段确认）。
func (s *Service) Raise(target int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if target < 1 || target > 3 || target != s.g+1 {
		return gateErr("Raise", ErrInvalidArg, "")
	}
	snap := s.nodes.Snapshot()
	if len(snap) == 0 {
		return gateErr("Raise", ErrNoNode, "")
	}
	min := 4
	laggard := ""
	for _, n := range snap {
		if n.Max < min || (n.Max == min && (laggard == "" || n.Name < laggard)) {
			min = n.Max
			laggard = n.Name
		}
	}
	if target > min {
		return gateErr("Raise", ErrNodeLagging, "节点 "+laggard)
	}
	acked := make([]string, 0, len(snap))
	for _, n := range snap {
		if err := s.ack(n.Name, target); err != nil {
			failed := n.Name
			for i := len(acked) - 1; i >= 0; i-- {
				s.release(acked[i], target)
			}
			return gateErr("Raise", ErrAckFailed, "节点 "+failed)
		}
		acked = append(acked, n.Name)
	}
	s.g = target
	return nil
}

// Rollback 检查残留后回退集群级别。
func (s *Service) Rollback(target int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if target < 1 || target >= s.g {
		return gateErr("Rollback", ErrInvalidArg, "")
	}
	count, highest := s.store.Residue(target)
	if count > 0 {
		return gateErr("Rollback", ErrResidue,
			fmt.Sprintf("残留 %d 条，最高级别 %d", count, highest))
	}
	s.g = target
	return nil
}

// Stats 返回 G 与各级别记录条数。
func (s *Service) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Stats{G: s.g, Count: s.store.CountByLevel()}
}
