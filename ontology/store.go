package ontology

// cloneAttrs 对入参做深拷贝，避免返回内容或内部状态与调用方传入的切片、map 别名。
func cloneAttrs(attrs map[string][]string) map[string][]string {
	cloned := make(map[string][]string, len(attrs))
	for dim, values := range attrs {
		copied := make([]string, len(values))
		copy(copied, values)
		cloned[dim] = copied
	}
	return cloned
}

// Add 登记一篇新文档。参数非法先于重复文档判定。
func (s *Store) Add(docID string, attrs map[string][]string) error {
	if docID == "" {
		return ErrInvalidArgument
	}
	if err := validateAttrs(attrs); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.docs == nil {
		s.docs = make(map[string]*document)
	}
	if _, exists := s.docs[docID]; exists {
		return ErrDuplicateDocument
	}
	s.docs[docID] = &document{nodes: documentNodes(attrs)}
	return nil
}

// Replace 原子替换一篇已有文档。参数非法先于文档不存在判定。
// 替换在单个临界区内完成，计数不会同时观察到新旧内容。
func (s *Store) Replace(docID string, attrs map[string][]string) error {
	if docID == "" {
		return ErrInvalidArgument
	}
	if err := validateAttrs(attrs); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.docs[docID]; !exists {
		return ErrDocumentNotFound
	}
	s.docs[docID] = &document{nodes: documentNodes(attrs)}
	return nil
}

// Delete 删除一篇文档。
func (s *Store) Delete(docID string) error {
	if docID == "" {
		return ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.docs[docID]; !exists {
		return ErrDocumentNotFound
	}
	delete(s.docs, docID)
	return nil
}

// Link 声明维度 child 的生效依赖 parent。
// child 至多一个 parent，声明后不可更改；自环与成环被拒绝。
func (s *Store) Link(child, parent string) error {
	if child == "" || parent == "" {
		return ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.parent == nil {
		s.parent = make(map[string]string)
	}
	if existing, ok := s.parent[child]; ok {
		// 重复声明：即便 parent 相同也拒绝。
		_ = existing
		return ErrDuplicateLink
	}
	if child == parent {
		return ErrCyclicDependency
	}
	// 从 parent 沿依赖链向上，若到达 child，则新增边会成环。
	cur := parent
	for {
		if cur == child {
			return ErrCyclicDependency
		}
		next, ok := s.parent[cur]
		if !ok {
			break
		}
		cur = next
	}
	s.parent[child] = parent
	return nil
}
