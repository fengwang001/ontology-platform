package ontology

import (
	"sync"
)

// ChangeOp 描述一次源头变更的类型。
type ChangeOp int

const (
	OpUpsert ChangeOp = iota
	OpDelete
)

// ChangeEvent 是上游变更数据捕获事件。更新与删除都会产生新事件，
// 删除事件表示墓碑；重新插入后版本继续在墓碑版本之上递增。
type ChangeEvent struct {
	Key     string
	Version int64
	Op      ChangeOp
}

// StateToken 是源头状态令牌：读取第一步从源头取得的一次性快照。
// 字段不对外暴露，调用方只能在 CompleteRead/Backfill 中整体交回。
type StateToken struct {
	key     string
	version int64
	exists  bool
	value   string
	id      uint64
}

// Version 返回令牌对应的源头版本，便于日志与测试判定。
func (t StateToken) Version() int64 { return t.version }

// Key 返回令牌对应的键。
func (t StateToken) Key() string { return t.key }

type sourceRow struct {
	version int64
	exists  bool
	value   string
}

// Source 是带版本号的源头存储。每个键保留墓碑行，
// 因此删除后再次插入仍能拿到严格递增的版本号。
type Source struct {
	mu        sync.Mutex
	rows      map[string]sourceRow
	events    []ChangeEvent
	outgoing  map[uint64]struct{}
	nextID    uint64
	readCount int64
}

func NewSource() *Source {
	return &Source{
		rows:     make(map[string]sourceRow),
		outgoing: make(map[uint64]struct{}),
	}
}

// Upsert 更新或插入一行，版本加一，并把变更追加到事件队列。
func (s *Source) Upsert(key, value string) (ChangeEvent, error) {
	if key == "" {
		return ChangeEvent{}, ErrEmptyKey
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	row := s.rows[key]
	row.version++
	row.exists = true
	row.value = value
	s.rows[key] = row

	ev := ChangeEvent{Key: key, Version: row.version, Op: OpUpsert}
	s.events = append(s.events, ev)
	return ev, nil
}

// Delete 删除一行：源头记录墓碑且版本继续加一。删除不存在的行
// （从未写入或已被墓碑化）整体拒绝，不产生事件、不动版本。
func (s *Source) Delete(key string) (ChangeEvent, error) {
	if key == "" {
		return ChangeEvent{}, ErrEmptyKey
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	row, ok := s.rows[key]
	if !ok || !row.exists {
		return ChangeEvent{}, ErrDeleteMissing
	}
	row.version++
	row.exists = false
	row.value = ""
	s.rows[key] = row

	ev := ChangeEvent{Key: key, Version: row.version, Op: OpDelete}
	s.events = append(s.events, ev)
	return ev, nil
}

// GetForRead 是两阶段读取的第一步：从源头取状态快照并登记为
// 未完成的一次性令牌，同时递增“读源头次数”。
func (s *Source) GetForRead(key string) (StateToken, error) {
	if key == "" {
		return StateToken{}, ErrEmptyKey
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	s.nextID++
	row := s.rows[key]
	tok := StateToken{
		key:     key,
		version: row.version,
		exists:  row.exists,
		value:   row.value,
		id:      s.nextID,
	}
	s.outgoing[tok.id] = struct{}{}
	s.readCount++
	return tok, nil
}

// ConsumeToken 兑现令牌。未知或已兑现的令牌返回 ErrUnknownToken，
// 且不改变任何状态（包括读计数）。
func (s *Source) ConsumeToken(tok StateToken) (value string, exists bool, version int64, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if tok.id == 0 || tok.key == "" {
		return "", false, 0, ErrUnknownToken
	}
	if _, ok := s.outgoing[tok.id]; !ok {
		return "", false, 0, ErrUnknownToken
	}
	delete(s.outgoing, tok.id)
	return tok.value, tok.exists, tok.version, nil
}

// HasOutstandingToken 报告令牌当前是否仍登记在源头。
func (s *Source) HasOutstandingToken(tok StateToken) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.outgoing[tok.id]
	return ok
}

// DirectGet 是无缓存参照读法：直接读取源头当前状态。
func (s *Source) DirectGet(key string) (string, bool) {
	if key == "" {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[key]
	if !ok || !row.exists {
		return "", false
	}
	return row.value, true
}

// Events 返回尚未被投递消费的事件队列快照。
func (s *Source) Events() []ChangeEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ChangeEvent, len(s.events))
	copy(out, s.events)
	return out
}

// PendingEventCount 返回队列中待投递事件数。
func (s *Source) PendingEventCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.events)
}

// PopEvent 取出并移除队首事件；队列为空时 ok 为 false。
func (s *Source) PopEvent() (ev ChangeEvent, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.events) == 0 {
	return ChangeEvent{}, false
	}
	ev = s.events[0]
	s.events = s.events[1:]
	return ev, true
}

// ReadCount 返回 GetForRead 成功取令牌的次数（无缓存直读不计入）。
func (s *Source) ReadCount() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readCount
}
