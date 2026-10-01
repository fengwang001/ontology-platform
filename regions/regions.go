package regions

import (
	"sort"
	"sync"
)

// Set 是一个并发安全的区域集合。
type Set struct {
	mu      sync.RWMutex
	regions map[string]Region
}

func NewSet() *Set {
	return &Set{regions: make(map[string]Region)}
}

// contains 实现区域的点归属规则：
// 点在外环闭区域内，并且不在洞的开区域内（洞的边与顶点仍属于区域）。
func containsRegion(r Region, p Point) bool {
	if !ringContainsClosed(r.Outer, p) {
		return false
	}
	if len(r.Hole) > 0 && ringContainsOpen(r.Hole, p) {
		return false
	}
	return true
}

func cloneRegion(r Region) Region {
	out := r
	out.Outer = append(Ring(nil), r.Outer...)
	if r.Hole != nil {
		out.Hole = append(Ring(nil), r.Hole...)
	}
	return out
}

// Put 新增一个区域；几何检查全部先于 id 冲突检查，任何失败都不改变集合。
func (s *Set) Put(r Region) error {
	if err := validateRegion(r); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.regions[r.ID]; ok {
		return ErrIDExists
	}
	s.regions[r.ID] = cloneRegion(r)
	return nil
}

// Replace 原子地替换同 id 的已有区域；失败（含几何校验失败或 id 不存在）时原区域保留。
func (s *Set) Replace(r Region) error {
	if err := validateRegion(r); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.regions[r.ID]; !ok {
		return ErrIDNotFound
	}
	s.regions[r.ID] = cloneRegion(r)
	return nil
}

// Remove 删除区域；id 不存在时报错且不改变集合。
func (s *Set) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.regions[id]; !ok {
		return ErrIDNotFound
	}
	delete(s.regions, id)
	return nil
}

// Locate 返回命中的全部区域，按优先级降序、id 升序排列。
// 结果对应读锁下某一时刻的完整集合快照。
func (s *Set) Locate(x, y int64) ([]Region, error) {
	if x < -maxCoord || x > maxCoord || y < -maxCoord || y > maxCoord {
		return nil, ErrCoordOutOfRange
	}
	p := Point{X: x, Y: y}
	s.mu.RLock()
	hits := make([]Region, 0)
	for _, r := range s.regions {
		if containsRegion(r, p) {
			hits = append(hits, cloneRegion(r))
		}
	}
	s.mu.RUnlock()
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Priority != hits[j].Priority {
			return hits[i].Priority > hits[j].Priority
		}
		return hits[i].ID < hits[j].ID
	})
	return hits, nil
}

func (s *Set) Best(x, y int64) (Region, bool, error) {
	hits, err := s.Locate(x, y)
	if err != nil {
		return Region{}, false, err
	}
	if len(hits) == 0 {
		return Region{}, false, nil
	}
	return hits[0], true, nil
}
