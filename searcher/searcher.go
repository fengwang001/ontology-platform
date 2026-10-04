// Package searcher 提供只在刷新后可见的搜索视图。
//
// 视图只保存存活文档（删除即移除），不感知事务日志与落盘水位。
// 调用方（engine）负责按序号顺序应用操作，并保证并发安全。
package searcher

import (
	"bytes"
	"sort"
)

// Doc 是搜索视图中的一条存活文档。
type Doc struct {
	ID   []byte
	Body []byte
	Seq  int64
}

// View 是某一时刻可搜索的文档集合。
type View struct {
	docs map[string]Doc
}

// New 返回一个空视图。
func New() *View {
	return &View{docs: make(map[string]Doc)}
}

// ApplyIndex 把一次索引操作并入视图。
func (v *View) ApplyIndex(id, body []byte, seq int64) {
	v.docs[string(id)] = Doc{
		ID:   append([]byte(nil), id...),
		Body: append([]byte(nil), body...),
		Seq:  seq,
	}
}

// ApplyDelete 把一次删除操作并入视图。
func (v *View) ApplyDelete(id []byte) {
	delete(v.docs, string(id))
}

// Get 返回 id 在视图中的存活文档。
func (v *View) Get(id []byte) (Doc, bool) {
	doc, ok := v.docs[string(id)]
	return doc, ok
}

// List 按 id 字节序返回视图中全部存活文档。
func (v *View) List() []Doc {
	out := make([]Doc, 0, len(v.docs))
	for _, doc := range v.docs {
		out = append(out, doc)
	}
	sort.Slice(out, func(i, j int) bool {
		return bytes.Compare(out[i].ID, out[j].ID) < 0
	})
	return out
}

// Len 返回视图中存活文档数。
func (v *View) Len() int {
	return len(v.docs)
}

// Snapshot 返回视图的深拷贝，用作提交点。
func (v *View) Snapshot() *View {
	cp := &View{docs: make(map[string]Doc, len(v.docs))}
	for key, doc := range v.docs {
		cp.docs[key] = Doc{
			ID:   append([]byte(nil), doc.ID...),
			Body: append([]byte(nil), doc.Body...),
			Seq:  doc.Seq,
		}
	}
	return cp
}
