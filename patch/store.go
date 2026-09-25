package patch

import (
	"sync"

	"ontology/lines"
)

// Commit 是提交日志的一条记录。
type Commit struct {
	Doc     string
	Version int
	OK      bool
	Data    []byte
	Reverse bool
}

type doc struct {
	data []byte
	ver  int
}

// Store 是进程内多文档存储；每个文档带版本号与提交日志。
type Store struct {
	mu      sync.Mutex
	docs    map[string]*doc
	commits []Commit
}

// NewStore 创建空存储。
func NewStore() *Store { return &Store{docs: map[string]*doc{}} }

// Put 创建或整体替换一个文档，版本归零。
func (s *Store) Put(name string, data []byte) {
	s.mu.Lock()
	s.docs[name] = &doc{data: append([]byte(nil), data...)}
	s.mu.Unlock()
}

// Get 返回文档内容与版本的快照。
func (s *Store) Get(name string) ([]byte, int, bool) {
	s.mu.Lock()
	d, ok := s.docs[name]
	if !ok {
		s.mu.Unlock()
		return nil, 0, false
	}
	data := append([]byte(nil), d.data...)
	v := d.ver
	s.mu.Unlock()
	return data, v, true
}

// Apply 在一致快照上判断并原子应用；失败状态零变化，日志均留痕。
func (s *Store) Apply(name string, p []byte, opts Options, rev bool) (int, error) {
	s.mu.Lock()
	d, ok := s.docs[name]
	if !ok {
		d = &doc{}
		s.docs[name] = d
	}
	snap := append([]byte(nil), d.data...)
	parsed, err := check(p, opts)
	if err == nil {
		pp := parsed
		if rev {
			pp = reverse(parsed)
		}
		var out []byte
		out, err = applyParsed(lines.Split(snap), pp, opts, rev)
		if err == nil {
			d.data = out
			d.ver++
		}
	}
	ver := d.ver
	s.commits = append(s.commits, Commit{Doc: name, Version: ver, OK: err == nil,
		Data: append([]byte(nil), p...), Reverse: rev})
	s.mu.Unlock()
	return ver, err
}

// Replay 按提交日志把成功补丁从空初始内容开始串行重放。
func (s *Store) Replay(name string, initial []byte, opts Options) ([]byte, error) {
	s.mu.Lock()
	log := append([]Commit(nil), s.commits...)
	s.mu.Unlock()
	cur := append([]byte(nil), initial...)
	var err error
	for _, c := range log {
		if c.Doc != name || !c.OK {
			continue
		}
		if c.Reverse {
			cur, err = Reverse(cur, c.Data, opts)
		} else {
			cur, err = Apply(cur, c.Data, opts)
		}
		if err != nil {
			return nil, err
		}
	}
	return cur, nil
}
