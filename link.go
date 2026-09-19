package ontology

import "fmt"

// Link 建立一条链，即时校验端点类型、对象存在性、重复与基数约束。
func (s *Store) Link(ltName string, src, tgt ObjectKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.linkLocked(ltName, src, tgt)
}

func (s *Store) linkLocked(ltName string, src, tgt ObjectKey) error {
	lt, ok := s.linkTypes[ltName]
	if !ok {
		return fmt.Errorf("link type %q not declared", ltName)
	}
	if src.Type != lt.SourceType || tgt.Type != lt.TargetType {
		return &EndpointTypeError{
			LinkType:       ltName,
			Source:         src,
			Target:         tgt,
			WantSourceType: lt.SourceType,
			WantTargetType: lt.TargetType,
		}
	}
	if _, ok := s.objects[src]; !ok {
		return &ObjectNotFoundError{LinkType: ltName, Source: src, Target: tgt, Side: "source"}
	}
	if _, ok := s.objects[tgt]; !ok {
		return &ObjectNotFoundError{LinkType: ltName, Source: src, Target: tgt, Side: "target"}
	}
	if s.hasLinkLocked(ltName, src, tgt) {
		return &DuplicateLinkError{LinkType: ltName, Source: src, Target: tgt}
	}
	switch lt.Cardinality {
	case OneToOne:
		if len(s.fwd[ltName][src]) > 0 {
			return &CardinalityError{LinkType: ltName, Source: src, Target: tgt, Kind: ViolationOneToOneSource}
		}
		if len(s.bwd[ltName][tgt]) > 0 {
			return &CardinalityError{LinkType: ltName, Source: src, Target: tgt, Kind: ViolationOneToOneTarget}
		}
	case OneToMany:
		if len(s.bwd[ltName][tgt]) > 0 {
			return &CardinalityError{LinkType: ltName, Source: src, Target: tgt, Kind: ViolationOneToManyTarget}
		}
	}
	s.addLinkLocked(ltName, src, tgt)
	return nil
}

// Unlink 断开一条链；链不存在时返回 LinkNotFoundError。
func (s *Store) Unlink(ltName string, src, tgt ObjectKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.unlinkLocked(ltName, src, tgt)
}

func (s *Store) unlinkLocked(ltName string, src, tgt ObjectKey) error {
	if _, ok := s.linkTypes[ltName]; !ok {
		return fmt.Errorf("link type %q not declared", ltName)
	}
	if !s.hasLinkLocked(ltName, src, tgt) {
		return &LinkNotFoundError{LinkType: ltName, Source: src, Target: tgt}
	}
	s.removeLinkLocked(ltName, src, tgt)
	return nil
}

// LinksFrom 正向遍历：返回源对象沿 LinkType 到达的全部目标，排序稳定。
func (s *Store) LinksFrom(ltName string, src ObjectKey) []ObjectKey {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return sortedKeys(s.fwd[ltName][src])
}

// LinksTo 反向遍历：返回沿 LinkType 指向目标对象的全部源，排序稳定。
func (s *Store) LinksTo(ltName string, tgt ObjectKey) []ObjectKey {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return sortedKeys(s.bwd[ltName][tgt])
}

// Links 返回某 LinkType 的全部链，排序稳定。
func (s *Store) Links(ltName string) []Link {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Link
	for _, src := range sortedKeys(s.fwd[ltName]) {
		for _, tgt := range sortedKeys(s.fwd[ltName][src]) {
			out = append(out, Link{LinkType: ltName, Source: src, Target: tgt})
		}
	}
	return out
}
