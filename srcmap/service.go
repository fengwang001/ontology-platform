package srcmap

import "sync"

// Service 以名字管理不可变映射，登记/合成/查询均可并发调用。
type Service struct {
	mu       sync.RWMutex
	mappings map[string]*Mapping
}

// NewService 创建空服务。
func NewService() *Service {
	return &Service{mappings: make(map[string]*Mapping)}
}

// Register 校验并以名字登记一张新映射。
func (s *Service) Register(name string, sourceCount int, lines []Line) error {
	// 参数非法优先：先在锁外完成所有校验，拒绝时不触碰状态。
	m, err := New(sourceCount, lines)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.mappings[name]; exists {
		return duplicatef("名字 %q 已被占用", name)
	}
	s.mappings[name] = m
	return nil
}

// Compose 以 m2Name（最终→中间）和 m1Name（中间→原始）合成结果并登记。
func (s *Service) Compose(m2Name, m1Name, resultName string) error {
	// 快照必须取自操作开始时刻已登记的同一组状态：在读锁内同时取两映射。
	s.mu.RLock()
	m1, ok1 := s.mappings[m1Name]
	m2, ok2 := s.mappings[m2Name]
	_, resultExists := s.mappings[resultName]
	s.mu.RUnlock()
	if !ok1 || !ok2 {
		missing := m1Name
		if !ok2 {
			missing = m2Name
		}
		return notFoundf("名字 %q 未登记", missing)
	}
	if resultExists {
		return duplicatef("结果名字 %q 已被占用", resultName)
	}
	// M2 单源约束属于参数非法；合成内部可能报位置溢出，均不改变状态。
	composed, err := compose(m2, m1)
	if err != nil {
		return err
	}
	// 提交：与并发登记/合成串行化，复查结果名避免竞态。
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.mappings[resultName]; exists {
		return duplicatef("结果名字 %q 已被占用", resultName)
	}
	s.mappings[resultName] = composed
	return nil
}

// Query 查询已登记映射。
func (s *Service) Query(name string, genLine, genCol int) (LookupResult, error) {
	if err := checkGenPos(genLine, genCol); err != nil {
		return LookupResult{}, err
	}
	s.mu.RLock()
	m, ok := s.mappings[name]
	s.mu.RUnlock()
	if !ok {
		return LookupResult{}, notFoundf("名字 %q 未登记", name)
	}
	return m.Lookup(genLine, genCol)
}

// Get 返回已登记映射的只读视图（源数量与规范行），名字不存在返回未找到。
func (s *Service) Get(name string) (sourceCount int, lines []Line, err error) {
	s.mu.RLock()
	m, ok := s.mappings[name]
	s.mu.RUnlock()
	if !ok {
		return 0, nil, notFoundf("名字 %q 未登记", name)
	}
	return m.sourceCount, m.lines, nil
}
