package patch

import "sync"

// Commit 是提交日志中的一条成功记录。
type Commit struct {
	Doc     string
	Version int
	Patch   []byte
}

type doc struct {
	content []byte
	version int
}

// Store 是带版本号与提交日志的多文档存储；所有方法并发安全。
type Store struct {
	mu   sync.Mutex
	docs map[string]*doc
	log  []Commit
}

// NewStore 创建空存储。
func NewStore() *Store {
	return &Store{docs: map[string]*doc{}}
}

// Put 写入文档初始内容，版本为 0。
func (s *Store) Put(name string, content []byte) {
	s.mu.Lock()
	s.docs[name] = &doc{content: append([]byte(nil), content...)}
	s.mu.Unlock()
}

// Get 返回文档当前内容与版本。
func (s *Store) Get(name string) ([]byte, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.docs[name]
	if d == nil {
		return nil, 0
	}
	return append([]byte(nil), d.content...), d.version
}

// ApplyDoc 在一致快照上尝试应用；成功则原子替换、版本加一并记录提交日志。
func (s *Store) ApplyDoc(name string, p []byte, opts Options) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.docs[name]
	if d == nil {
		d = &doc{}
		s.docs[name] = d
	}
	out, err := Apply(d.content, p, opts)
	if err != nil {
		return err
	}
	d.content = out
	d.version++
	s.log = append(s.log, Commit{Doc: name, Version: d.version, Patch: append([]byte(nil), p...)})
	return nil
}

// Log 返回成功提交的日志副本。
func (s *Store) Log(name string) []Commit {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Commit
	for _, c := range s.log {
		if c.Doc == name {
			out = append(out, Commit{Doc: c.Doc, Version: c.Version, Patch: append([]byte(nil), c.Patch...)})
		}
	}
	return out
}

// Replay 从 initial 出发按日志顺序串行重放，返回最终文本。
func Replay(initial []byte, log []Commit) []byte {
	cur := append([]byte(nil), initial...)
	for _, c := range log {
		next, err := Apply(cur, c.Patch, Options{})
		if err != nil {
			panic(err)
		}
		cur = next
	}
	return cur
}
