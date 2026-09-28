package ontology

import "sync"

// row 是源头中的一行。墓碑（tomb == true）表示该键已被删除，
// 但版本号仍然保留并继续递增。
type row struct {
	version int64
	value   string
	tomb    bool
}

// Source 是带版本号的维表源头（system of record）。
// 更新与删除都使版本加一；删除留下墓碑。每次写操作都会把
// 对应变更事件追加到 FIFO 队列，等待 Cache 投递消费。
type Source struct {
	mu     sync.Mutex
	rows   map[string]row
	queue  []Event
	reads  int64
}

func NewSource() *Source {
	return &Source{rows: make(map[string]row)}
}

// Update 插入或更新一行，版本加一，并投递一条 upsert 事件。
// 对已被删除（墓碑）的键执行更新等同于“复活”，版本继续递增。
func (s *Source) Update(key, value string) (int64, error) {
	if key == "" {
		return 0, ErrEmptyKey
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	r := s.rows[key]
	r.version++
	r.value = value
	r.tomb = false
	s.rows[key] = r
	s.queue = append(s.queue, Event{Key: key, Version: r.version, Value: value, Tomb: false})
	return r.version, nil
}

// Delete 删除一行：不存在的键整体拒绝；键存在（含墓碑）时版本加一，
// 写入墓碑并投递一条删除事件。
func (s *Source) Delete(key string) (int64, error) {
	if key == "" {
		return 0, ErrEmptyKey
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	r, ok := s.rows[key]
	if !ok {
		return 0, ErrDeleteNotFound
	}
	r.version++
	r.value = ""
	r.tomb = true
	s.rows[key] = r
	s.queue = append(s.queue, Event{Key: key, Version: r.version, Tomb: true})
	return r.version, nil
}

// snapshot 读取源头当前状态并累计“直接读源头”次数。
// 键不存在或为墓碑时都返回 tomb == true、version 为已观察到的版本
// （从未见过的键版本为 0）。
func (s *Source) snapshot(key string) (version int64, value string, tomb bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads++
	r, ok := s.rows[key]
	if !ok {
		return 0, "", true
	}
	return r.version, r.value, r.tomb
}

// popEvent 取出队列头部事件；队列为空时返回 false。
func (s *Source) popEvent() (Event, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queue) == 0 {
		return Event{}, false
	}
	e := s.queue[0]
	s.queue = s.queue[1:]
	return e, true
}

// queuedLen 返回尚未投递的事件数。
func (s *Source) queuedLen() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.queue)
}

// SourceReadCount 返回源头被直接读取的累计次数（无缓存参照不经过它）。
func (s *Source) SourceReadCount() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reads
}

// DirectGet 是无缓存参照路径：直接读源头当前状态，不经过缓存、
// 不计入缓存发起的源头读取计数，供测试与对账使用。
func (s *Source) DirectGet(key string) (value string, found bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rows[key]
	if !ok || r.tomb {
		return "", false
	}
	return r.value, true
}
