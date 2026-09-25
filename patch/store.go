package patch

import (
	"sync"

	"ontology/udiff"
)

// Doc 是一个带版本号的内存文档。
type Doc struct {
	Text    []byte
	Version int
}

// Commit 是提交日志中的一条成功记录。
type Commit struct {
	Name    string
	Version int // 应用成功后的版本号
}

// Store 是多文档内存存储；每个文档独立加锁，整次「判断+替换」原子完成。
type Store struct {
	mu   sync.Mutex
	docs map[string]*docState
}

type docState struct {
	mu      sync.Mutex
	text    []byte
	version int
	log     []Commit
}

// NewStore 创建空存储并写入初始文档（版本 0）。
func NewStore(name string, initial []byte) *Store {
	s := &Store{docs: map[string]*docState{}}
	s.docs[name] = &docState{text: append([]byte(nil), initial...)}
	return s
}

// Get 返回文档当前快照（拷贝）与版本号。
func (s *Store) Get(name string) Doc {
	d := s.doc(name)
	d.mu.Lock()
	defer d.mu.Unlock()
	return Doc{Text: append([]byte(nil), d.text...), Version: d.version}
}

// Apply 在一致快照上尝试应用：成功则原子替换文本、版本加一并写提交日志；
// 失败则文本与版本零变化，返回错误。
func (s *Store) Apply(name string, f *udiff.File, opt Options) (Doc, error) {
	d := s.doc(name)
	d.mu.Lock()
	defer d.mu.Unlock()
	out, err := Apply(d.text, f, opt)
	if err != nil {
		return Doc{Text: append([]byte(nil), d.text...), Version: d.version}, err
	}
	d.text = out
	d.version++
	d.log = append(d.log, Commit{Name: name, Version: d.version})
	return Doc{Text: append([]byte(nil), out...), Version: d.version}, nil
}

// Log 返回成功应用的提交日志拷贝。
func (s *Store) Log(name string) []Commit {
	d := s.doc(name)
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]Commit(nil), d.log...)
}

func (s *Store) doc(name string) *docState {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.docs[name]
	if d == nil {
		d = &docState{}
		s.docs[name] = d
	}
	return d
}
