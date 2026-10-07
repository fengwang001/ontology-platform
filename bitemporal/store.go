package bitemporal

import "sync"

// Store 是只追加（append-only）的双时态记录存储，并发安全。
// 查询在一个读锁覆盖的一致性快照上完成全部遍历，
// 因而不可能观察到「区间已修正而写入记录尚未落定」的中间状态。
type Store struct {
	mu      sync.RWMutex
	objects map[string][]ObjectRecord
	links   map[string][]LinkRecord
	out     map[string][]string // 源对象 ID -> 链接 ID（去重）
}

// NewStore 创建空存储。
func NewStore() *Store {
	return &Store{
		objects: make(map[string][]ObjectRecord),
		links:   make(map[string][]LinkRecord),
		out:     make(map[string][]string),
	}
}

// AppendObject 追加一条对象写入记录；WrittenAt 必须严格晚于该对象已有最新记录。
func (s *Store) AppendObject(rec ObjectRecord) error {
	if err := validateObjectRecord(rec); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	versions := s.objects[rec.ID]
	if len(versions) > 0 {
		last := versions[len(versions)-1]
		if !rec.WrittenAt.After(last.WrittenAt) {
			return &WriteError{Message: "object record WrittenAt must be strictly later than the previous record"}
		}
		for _, v := range versions {
			if v.VersionID == rec.VersionID {
				return &WriteError{Message: "duplicate object VersionID"}
			}
		}
	}
	s.objects[rec.ID] = append(versions, rec)
	return nil
}

// AppendLink 追加一条链接写入记录；同一链接各版本端点必须一致，
// 且 WrittenAt 必须严格晚于该链接已有最新记录。
func (s *Store) AppendLink(rec LinkRecord) error {
	if err := validateLinkRecord(rec); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	versions := s.links[rec.ID]
	if len(versions) > 0 {
		first := versions[0]
		if first.SourceID != rec.SourceID || first.TargetID != rec.TargetID {
			return &WriteError{Message: "link endpoints must be consistent across versions"}
		}
		last := versions[len(versions)-1]
		if !rec.WrittenAt.After(last.WrittenAt) {
			return &WriteError{Message: "link record WrittenAt must be strictly later than the previous record"}
		}
		for _, v := range versions {
			if v.VersionID == rec.VersionID {
				return &WriteError{Message: "duplicate link VersionID"}
			}
		}
	}
	s.links[rec.ID] = append(versions, rec)
	s.addOutLink(rec.SourceID, rec.ID)
	return nil
}

// Snapshot 返回存储的一致性只读快照。
func (s *Store) Snapshot() *Snapshot {
	s.mu.RLock()
	return &Snapshot{
		objects: s.objects,
		links:   s.links,
		out:     s.out,
		release: s.mu.RUnlock,
	}
}

func (s *Store) addOutLink(sourceID, linkID string) {
	for _, id := range s.out[sourceID] {
		if id == linkID {
			return
		}
	}
	s.out[sourceID] = append(s.out[sourceID], linkID)
}

func validateObjectRecord(rec ObjectRecord) error {
	if rec.ID == "" {
		return &WriteError{Message: "object ID must not be empty"}
	}
	if rec.VersionID == "" {
		return &WriteError{Message: "object VersionID must not be empty"}
	}
	if rec.WrittenAt.IsZero() {
		return &WriteError{Message: "object WrittenAt must not be zero"}
	}
	if rec.Valid.Empty() {
		return &WriteError{Message: "object valid interval must be non-empty: [From, To) requires From < To"}
	}
	return nil
}

func validateLinkRecord(rec LinkRecord) error {
	if rec.ID == "" {
		return &WriteError{Message: "link ID must not be empty"}
	}
	if rec.VersionID == "" {
		return &WriteError{Message: "link VersionID must not be empty"}
	}
	if rec.SourceID == "" || rec.TargetID == "" {
		return &WriteError{Message: "link endpoints must not be empty"}
	}
	if rec.WrittenAt.IsZero() {
		return &WriteError{Message: "link WrittenAt must not be zero"}
	}
	if rec.Valid.Empty() {
		return &WriteError{Message: "link valid interval must be non-empty: [From, To) requires From < To"}
	}
	return nil
}
