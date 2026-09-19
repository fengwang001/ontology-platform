package ontology

import (
	"fmt"
	"sort"
)

// sortedKeys 返回排序后的键集合，保证遍历确定性。
func sortedKeys[V any](m map[ObjectKey]V) []ObjectKey {
	keys := make([]ObjectKey, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Type != keys[j].Type {
			return keys[i].Type < keys[j].Type
		}
		return keys[i].ID < keys[j].ID
	})
	return keys
}

func (s *Store) sortedLinkTypeNames() []string {
	names := make([]string, 0, len(s.linkTypes))
	for n := range s.linkTypes {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func (s *Store) objectsOfTypeLocked(typeName string) []ObjectKey {
	var out []ObjectKey
	for k := range s.objects {
		if k.Type == typeName {
			out = append(out, k)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (s *Store) hasLinkLocked(ltName string, src, tgt ObjectKey) bool {
	_, ok := s.fwd[ltName][src][tgt]
	return ok
}

// addLinkLocked 同时写正反向索引，调用方必须持写锁。
func (s *Store) addLinkLocked(ltName string, src, tgt ObjectKey) {
	if s.fwd[ltName] == nil {
		s.fwd[ltName] = make(map[ObjectKey]map[ObjectKey]struct{})
	}
	if s.fwd[ltName][src] == nil {
		s.fwd[ltName][src] = make(map[ObjectKey]struct{})
	}
	s.fwd[ltName][src][tgt] = struct{}{}

	if s.bwd[ltName] == nil {
		s.bwd[ltName] = make(map[ObjectKey]map[ObjectKey]struct{})
	}
	if s.bwd[ltName][tgt] == nil {
		s.bwd[ltName][tgt] = make(map[ObjectKey]struct{})
	}
	s.bwd[ltName][tgt][src] = struct{}{}
}

// removeLinkLocked 同时删正反向索引，调用方必须持写锁。
func (s *Store) removeLinkLocked(ltName string, src, tgt ObjectKey) {
	if bySrc := s.fwd[ltName]; bySrc != nil {
		if set := bySrc[src]; set != nil {
			delete(set, tgt)
			if len(set) == 0 {
				delete(bySrc, src)
			}
		}
	}
	if byTgt := s.bwd[ltName]; byTgt != nil {
		if set := byTgt[tgt]; set != nil {
			delete(set, src)
			if len(set) == 0 {
				delete(byTgt, tgt)
			}
		}
	}
}

// InvariantViolation 描述一次索引不一致。
type InvariantViolation struct {
	LinkType string
	Side     string // "forward"：正向有反向缺；"reverse"：反向有正向缺；"dangling"：端点对象不存在
	Source   ObjectKey
	Target   ObjectKey
	Detail   string
}

func (v InvariantViolation) String() string {
	return fmt.Sprintf("%s [%s] %s -> %s: %s", v.LinkType, v.Side, v.Source, v.Target, v.Detail)
}

// CheckInvariant 自检双向索引镜像一致性，返回全部违例（空切片表示一致）。
// 结果按 LinkType 与端点排序，可被测试直接调用。
func (s *Store) CheckInvariant() []InvariantViolation {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []InvariantViolation
	names := make([]string, 0, len(s.fwd)+len(s.bwd))
	seen := map[string]bool{}
	for n := range s.fwd {
		seen[n] = true
		names = append(names, n)
	}
	for n := range s.bwd {
		if !seen[n] {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	for _, lt := range names {
		for _, src := range sortedKeys(s.fwd[lt]) {
			for _, tgt := range sortedKeys(s.fwd[lt][src]) {
				if _, ok := s.bwd[lt][tgt][src]; !ok {
					out = append(out, InvariantViolation{lt, "forward", src, tgt,
						"present in forward index, missing in reverse index"})
				}
				if _, ok := s.objects[src]; !ok {
					out = append(out, InvariantViolation{lt, "dangling", src, tgt,
						"source object does not exist"})
				}
				if _, ok := s.objects[tgt]; !ok {
					out = append(out, InvariantViolation{lt, "dangling", src, tgt,
						"target object does not exist"})
				}
			}
		}
		for _, tgt := range sortedKeys(s.bwd[lt]) {
			for _, src := range sortedKeys(s.bwd[lt][tgt]) {
				if _, ok := s.fwd[lt][src][tgt]; !ok {
					out = append(out, InvariantViolation{lt, "reverse", src, tgt,
						"present in reverse index, missing in forward index"})
				}
			}
		}
	}
	return out
}
