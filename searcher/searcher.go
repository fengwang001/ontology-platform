// Package searcher 提供只在刷新后可见的只读搜索视图及其内存段/提交点管理。
package searcher

import "sort"

// Doc 是搜索视图中的一条存活文档。
type Doc struct {
	ID      string
	Body    []byte
	Seq     int64
	Deleted bool
}

// Searcher 管理一个提交点与一个并入中的内存段。
type Searcher struct {
	committed map[string]Doc // 提交点
	segment   map[string]Doc // 最近一次 Commit 后由 Apply 并入的内存段（含墓碑）
}

// New 创建空搜索视图。
func New() *Searcher {
	return &Searcher{committed: map[string]Doc{}, segment: map[string]Doc{}}
}

// Apply 把操作（含删除墓碑）并入内存段。
func (s *Searcher) Apply(seq int64, deleted bool, id string, body []byte) {
	s.segment[id] = Doc{ID: id, Body: append([]byte(nil), body...), Seq: seq, Deleted: deleted}
}

// Search 返回提交点与内存段合并后存活的文档，按 id 字节序排列。
func (s *Searcher) Search() []Doc {
	merged := s.snapshot()
	ids := make([]string, 0, len(merged))
	for id, doc := range merged {
		if !doc.Deleted {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	out := make([]Doc, 0, len(ids))
	for _, id := range ids {
		doc := merged[id]
		doc.Body = append([]byte(nil), doc.Body...)
		out = append(out, doc)
	}
	return out
}

// Lookup 返回合并视图中该 id 的文档；ok 为 false 表示视图中完全没有该 id。
func (s *Searcher) Lookup(id string) (Doc, bool) {
	if doc, ok := s.segment[id]; ok {
		return doc, true
	}
	doc, ok := s.committed[id]
	return doc, ok
}

// Commit 把内存段并入提交点，随后清空内存段。
func (s *Searcher) Commit() {
	for id, doc := range s.segment {
		s.committed[id] = doc
	}
	s.segment = map[string]Doc{}
}

// Snapshot 返回提交点中存活文档的深拷贝，供恢复时装回。
func (s *Searcher) Snapshot() map[string]Doc {
	out := make(map[string]Doc, len(s.committed))
	for id, doc := range s.committed {
		if !doc.Deleted {
			doc.Body = append([]byte(nil), doc.Body...)
			out[id] = doc
		}
	}
	return out
}

// Load 用给定提交点快照替换提交点，并清空内存段。
func (s *Searcher) Load(committed map[string]Doc) {
	s.committed = map[string]Doc{}
	for id, doc := range committed {
		doc.Body = append([]byte(nil), doc.Body...)
		s.committed[id] = doc
	}
	s.segment = map[string]Doc{}
}

func (s *Searcher) snapshot() map[string]Doc {
	merged := make(map[string]Doc, len(s.committed)+len(s.segment))
	for id, doc := range s.committed {
		merged[id] = doc
	}
	for id, doc := range s.segment {
		merged[id] = doc
	}
	return merged
}
