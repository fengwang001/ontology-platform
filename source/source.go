// Package source 实现带全局序号的源索引。
//
// 每个被接受的 Put/Delete 占一个全局序号 seq（1,2,3…），被拒绝的不占号。
// Running 期间 reindex 通过 Forward 钩子在同一原子步内把写入双写到 dest。
package source

import (
	"errors"
	"sort"
	"sync"
)

var (
	// ErrInvalidID 表示 id 不是 1 到 512 字节。
	ErrInvalidID = errors.New("source: invalid id")
	// ErrInvalidBody 表示 body 超过 65536 字节。
	ErrInvalidBody = errors.New("source: invalid body")
	// ErrDocNotFound 表示 Delete 的 id 不存在。
	ErrDocNotFound = errors.New("source: document not found")
	// ErrSwitched 表示重建已切换，源不再接受写入。
	ErrSwitched = errors.New("source: reindex switched")
)

const (
	// MaxIDLen 是 id 的最大字节数。
	MaxIDLen = 512
	// MaxBodyLen 是 body 的最大字节数。
	MaxBodyLen = 65536
)

// Doc 是源中的一条存活文档。
type Doc struct {
	ID   string
	Body []byte
	Seq  uint64
}

// Forward 是双写钩子，在源锁内被调用；del 为真表示删除。
type Forward func(del bool, id string, body []byte, seq uint64)

// Source 是带全局序号的源索引，可并发使用。
type Source struct {
	mu       sync.Mutex
	seq      uint64
	docs     map[string]Doc
	switched bool
	fwd      Forward
}

// New 返回一个空源索引。
func New() *Source {
	return &Source{docs: make(map[string]Doc)}
}

// Lock 暴露内部互斥锁，供 reindex 协调器把 dest 侧操作与
// "源写 + 双写"纳入同一临界区，保证整体可线性化。
func (s *Source) Lock() { s.mu.Lock() }

// Unlock 见 Lock。
func (s *Source) Unlock() { s.mu.Unlock() }

func validID(id string) bool { return len(id) >= 1 && len(id) <= MaxIDLen }

// Put 写入文档（存在则覆盖），返回占用的全局序号。
// 拒绝次序：参数非法 > 已切换。
func (s *Source) Put(id string, body []byte) (uint64, error) {
	if !validID(id) {
		return 0, ErrInvalidID
	}
	if len(body) > MaxBodyLen {
		return 0, ErrInvalidBody
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.switched {
		return 0, ErrSwitched
	}
	s.seq++
	seq := s.seq
	s.docs[id] = Doc{ID: id, Body: append([]byte(nil), body...), Seq: seq}
	if s.fwd != nil {
		s.fwd(false, id, body, seq)
	}
	return seq, nil
}

// Delete 删除文档，返回占用的全局序号。
// 拒绝次序：参数非法 > 已切换 > 文档不存在。
func (s *Source) Delete(id string) (uint64, error) {
	if !validID(id) {
		return 0, ErrInvalidID
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.switched {
		return 0, ErrSwitched
	}
	if _, ok := s.docs[id]; !ok {
		return 0, ErrDocNotFound
	}
	s.seq++
	seq := s.seq
	delete(s.docs, id)
	if s.fwd != nil {
		s.fwd(true, id, nil, seq)
	}
	return seq, nil
}

// Get 返回 id 对应的存活文档。
func (s *Source) Get(id string) (Doc, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.docs[id]
	return d, ok
}

// LiveDocs 返回当前存活文档，按 id 字节序排列。
func (s *Source) LiveDocs() []Doc {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.liveDocsLocked()
}

func (s *Source) liveDocsLocked() []Doc {
	docs := make([]Doc, 0, len(s.docs))
	for _, d := range s.docs {
		docs = append(docs, d)
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].ID < docs[j].ID })
	return docs
}

// BeginForward 冻结此刻的存活快照（按 id 字节序）并安装双写钩子。
// 调用方必须已持有 Lock()。
func (s *Source) BeginForward(fwd Forward) []Doc {
	s.fwd = fwd
	return s.liveDocsLocked()
}

// EndForward 移除双写钩子；switched 为真时源进入已切换状态。
// 调用方必须已持有 Lock()。
func (s *Source) EndForward(switched bool) {
	s.fwd = nil
	if switched {
		s.switched = true
	}
}
