package query

import (
	"fmt"
	"sort"
	"sync"

	"ontology/dls"
	"ontology/role"
)

// Doc 为一篇文档。
type Doc struct {
	ID     string
	Fields map[string]role.Value
}

// SearchResult 为搜索结果。
type SearchResult struct {
	Docs  []Doc
	Total int
}

// Bucket 为聚合的一个值计数。
type Bucket struct {
	Value role.Value
	Count int
}

// Engine 持有索引文档并执行权限化查询。
type Engine struct {
	store *role.Store

	mu     sync.RWMutex
	indexs map[string]map[string]map[string]role.Value
}

// NewEngine 创建查询引擎。
func NewEngine(store *role.Store) *Engine {
	return &Engine{store: store, indexs: map[string]map[string]map[string]role.Value{}}
}

// PutDoc 管理操作：写入文档，索引在首次 PutDoc 时出现；不经权限判定。
func (e *Engine) PutDoc(index, id string, fields map[string]role.Value) error {
	if !role.ValidName(index) || !role.ValidName(id) {
		return invalidf("index or doc id")
	}
	cloned := make(map[string]role.Value, len(fields))
	for name, value := range fields {
		if !role.ValidName(name) || !role.ValidValue(value) {
			return invalidf("field name or value")
		}
		cloned[name] = value
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	docs, ok := e.indexs[index]
	if !ok {
		docs = map[string]map[string]role.Value{}
		e.indexs[index] = docs
	}
	docs[id] = cloned
	return nil
}

// DeleteDoc 管理操作：删除文档；文档不存在为空操作，不经权限判定。
func (e *Engine) DeleteDoc(index, id string) error {
	if !role.ValidName(index) || !role.ValidName(id) {
		return invalidf("index or doc id")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if docs, ok := e.indexs[index]; ok {
		delete(docs, id)
	}
	return nil
}

// Search 在裁剪后的视图上求值 q。
// 拒绝次序：参数非法 > 用户不存在 > 无权 > 文档不存在（此处无文档级错误）。
// q 引用不可见字段时按文档缺该字段处理，不报错。
func (e *Engine) Search(user, index string, q role.Expr, size int) (*SearchResult, error) {
	if !role.ValidName(user) || !role.ValidName(index) {
		return nil, invalidf("user or index")
	}
	if err := role.ValidateExpr(q); err != nil {
		return nil, err
	}
	if size < 1 || size > 1000 {
		return nil, invalidf("size")
	}
	view, err := e.resolve(user, index)
	if err != nil {
		return nil, err
	}
	docs := e.snapshot(index)

	type hit struct {
		id     string
		fields dls.Fields
	}
	matches := make([]hit, 0)
	for id, full := range docs {
		if !view.DocVisible(full) {
			continue
		}
		stripped := view.StripFields(full)
		// 查询在裁剪后的视图上求值：不可见字段等同缺失。
		if !role.Eval(q, map[string]role.Value(stripped)) {
			continue
		}
		matches = append(matches, hit{id: id, fields: stripped})
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].id < matches[j].id })

	total := len(matches)
	if total > size {
		matches = matches[:size]
	}
	result := &SearchResult{Docs: make([]Doc, 0, len(matches)), Total: total}
	for _, match := range matches {
		result.Docs = append(result.Docs, Doc{ID: match.id, Fields: map[string]role.Value(match.fields)})
	}
	return result, nil
}

// Get 取单篇可见文档；不可见文档与真不存在统一报 role.ErrDocNotFound。
func (e *Engine) Get(user, index, id string) (*Doc, error) {
	if !role.ValidName(user) || !role.ValidName(index) || !role.ValidName(id) {
		return nil, invalidf("user, index or doc id")
	}
	view, err := e.resolve(user, index)
	if err != nil {
		return nil, err
	}
	full := e.snapshotOne(index, id)
	if full == nil || !view.DocVisible(full) {
		return nil, role.ErrDocNotFound
	}
	return &Doc{ID: id, Fields: map[string]role.Value(view.StripFields(full))}, nil
}

// Agg 在可见文档上按字段值计数；字段不可见或文档缺该字段均不计，
// 字段不可见返回空列表而不报错。int64 先于 string，各自升序。
func (e *Engine) Agg(user, index, field string) ([]Bucket, error) {
	if !role.ValidName(user) || !role.ValidName(index) || !role.ValidName(field) {
		return nil, invalidf("user, index or field")
	}
	view, err := e.resolve(user, index)
	if err != nil {
		return nil, err
	}
	if !view.FieldVisible(field) {
		return []Bucket{}, nil
	}
	docs := e.snapshot(index)
	intCounts := map[int64]int{}
	strCounts := map[string]int{}
	for _, full := range docs {
		if !view.DocVisible(full) {
			continue
		}
		value, ok := full[field]
		if !ok {
			continue
		}
		switch v := value.(type) {
		case int64:
			intCounts[v]++
		case string:
			strCounts[v]++
		}
	}
	ints := make([]int64, 0, len(intCounts))
	for key := range intCounts {
		ints = append(ints, key)
	}
	sort.Slice(ints, func(i, j int) bool { return ints[i] < ints[j] })
	strs := make([]string, 0, len(strCounts))
	for key := range strCounts {
		strs = append(strs, key)
	}
	sort.Strings(strs)

	buckets := make([]Bucket, 0, len(ints)+len(strs))
	for _, value := range ints {
		buckets = append(buckets, Bucket{Value: value, Count: intCounts[value]})
	}
	for _, value := range strs {
		buckets = append(buckets, Bucket{Value: value, Count: strCounts[value]})
	}
	return buckets, nil
}

// resolve 完成“用户存在 → M 非空”的判定。索引不存在与 M 为空不可区分。
func (e *Engine) resolve(user, index string) (*dls.View, error) {
	groups, _, err := e.store.UserEntries(user)
	if err != nil {
		return nil, err
	}
	// 索引不存在与 M 为空统一报无权，不暴露索引是否存在。
	if !e.indexExists(index) {
		return nil, role.ErrNoPerm
	}
	return dls.Resolve(groups, index)
}

func (e *Engine) indexExists(index string) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	_, ok := e.indexs[index]
	return ok
}

// snapshot 返回某索引全部文档的深拷贝快照；索引不存在返回空映射。
func (e *Engine) snapshot(index string) map[string]map[string]role.Value {
	e.mu.RLock()
	defer e.mu.RUnlock()
	docs, ok := e.indexs[index]
	if !ok {
		return map[string]map[string]role.Value{}
	}
	out := make(map[string]map[string]role.Value, len(docs))
	for id, fields := range docs {
		copied := make(map[string]role.Value, len(fields))
		for name, value := range fields {
			copied[name] = value
		}
		out[id] = copied
	}
	return out
}

func (e *Engine) snapshotOne(index, id string) map[string]role.Value {
	e.mu.RLock()
	defer e.mu.RUnlock()
	docs, ok := e.indexs[index]
	if !ok {
		return nil
	}
	fields, ok := docs[id]
	if !ok {
		return nil
	}
	copied := make(map[string]role.Value, len(fields))
	for name, value := range fields {
		copied[name] = value
	}
	return copied
}

func invalidf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", role.ErrInvalid, fmt.Sprintf(format, args...))
}
