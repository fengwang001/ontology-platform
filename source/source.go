// Package source 维护带全局序号的源索引。
package source

import (
	"errors"
	"sort"
	"sync"
)

var (
	ErrInvalid  = errors.New("source: invalid argument")
	ErrSwitched = errors.New("source: already switched")
	ErrNotFound = errors.New("source: document not found")
)

// Doc 是一条源文档快照。
type Doc struct {
	ID   string
	Body string
	Seq  int64
}

// Source 是带全局序号的源索引。
type Source struct {
	mu       *sync.RWMutex
	seq      int64
	switched bool
	docs     map[string]Doc
	onPut    func(Doc)
	onDelete func(string, int64)
}

// New 创建源索引（内部自持锁，供独立使用）。
func New() *Source { return NewWithLock(&sync.RWMutex{}) }

// NewWithLock 创建使用外部共享锁的源索引（供 reindex 协调器线性化）。
func NewWithLock(mu *sync.RWMutex) *Source {
	return &Source{mu: mu, docs: map[string]Doc{}}
}

// Lock / Unlock 导出共享锁，供协调器在原子步内复用。
func (s *Source) Lock()    { s.mu.Lock() }
func (s *Source) Unlock()  { s.mu.Unlock() }
func (s *Source) RLock()   { s.mu.RLock() }
func (s *Source) RUnlock() { s.mu.RUnlock() }

// MarkSwitched 冻结源：此后 Put/Delete 报 ErrSwitched。
func (s *Source) MarkSwitched() { s.switched = true }

// SetHook 注册接受写入后的转发钩子（在持有共享锁的原子步内调用，不得重入锁）。
func (s *Source) SetHook(onPut func(Doc), onDelete func(string, int64)) {
	s.onPut = onPut
	s.onDelete = onDelete
}

// Seq 返回当前全局序号。
func (s *Source) Seq() int64 { return s.seq }

// Put 写入一条文档并占用下一个全局序号。
func (s *Source) Put(id, body string) (int64, error) {
	if len(id) < 1 || len(id) > 512 || len(body) > 65536 {
		return 0, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.switched {
		return 0, ErrSwitched
	}
	s.seq++
	doc := Doc{ID: id, Body: body, Seq: s.seq}
	s.docs[id] = doc
	if s.onPut != nil {
		s.onPut(doc)
	}
	return s.seq, nil
}

// Delete 删除文档；不存在则 ErrNotFound。
func (s *Source) Delete(id string) (int64, error) {
	if len(id) < 1 || len(id) > 512 {
		return 0, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.switched {
		return 0, ErrSwitched
	}
	if _, ok := s.docs[id]; !ok {
		return 0, ErrNotFound
	}
	s.seq++
	delete(s.docs, id)
	if s.onDelete != nil {
		s.onDelete(id, s.seq)
	}
	return s.seq, nil
}

// Snapshot 返回存活文档按 id 字节序排列的深拷贝。
func (s *Source) Snapshot() []Doc {
	ids := make([]string, 0, len(s.docs))
	for id := range s.docs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]Doc, 0, len(ids))
	for _, id := range ids {
		out = append(out, s.docs[id])
	}
	return out
}
