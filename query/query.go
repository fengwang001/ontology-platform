package query

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"ontology/dls"
	"ontology/role"
)

// ErrNotFound 表示文档不存在；不可见文档与真不存在不可区分。
var ErrNotFound = errors.New("document not found")

const (
	maxNameBytes = 64
	maxSize      = 1000
)

// Doc 是返回给用户的文档：只含该用户可见字段（已拷贝）。
type Doc struct {
	ID     string
	Fields map[string]role.Value
}

// AggItem 是单个聚合计数。
type AggItem struct {
	Value role.Value
	Count int
}

type storedDoc struct {
	id     string
	fields map[string]role.Value
}

type indexData struct {
	docs map[string]*storedDoc
}

// Store 持有全部索引数据、角色注册表与权限解析器，所有方法可并发。
type Store struct {
	mu     sync.RWMutex
	indexs map[string]*indexData
	reg    *role.Registry
	rv     *dls.Resolver
}

func NewStore() *Store {
	reg := role.NewRegistry()
	return &Store{
		indexs: map[string]*indexData{},
		reg:    reg,
		rv:     dls.NewResolver(reg),
	}
}

func (s *Store) Registry() *role.Registry { return s.reg }
func (s *Store) Resolver() *dls.Resolver  { return s.rv }

func validName(n string) bool { return len(n) >= 1 && len(n) <= maxNameBytes }

func validateFields(fields map[string]role.Value) error {
	for name, v := range fields {
		if !validName(name) {
			return fmt.Errorf("%w: field name", role.ErrInvalid)
		}
		switch v.(type) {
		case int64, string:
		default:
			return fmt.Errorf("%w: field value must be int64 or string", role.ErrInvalid)
		}
	}
	return nil
}

func copyFields(in map[string]role.Value) map[string]role.Value {
	out := make(map[string]role.Value, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// PutDoc 是管理操作，不经权限判定；索引在首次 PutDoc 时出现。
func (s *Store) PutDoc(index, id string, fields map[string]role.Value) {
	if !validName(index) || !validName(id) {
		panic(fmt.Sprintf("%v: PutDoc name out of range", role.ErrInvalid))
	}
	if err := validateFields(fields); err != nil {
		panic(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ix, ok := s.indexs[index]
	if !ok {
		ix = &indexData{docs: map[string]*storedDoc{}}
		s.indexs[index] = ix
	}
	ix.docs[id] = &storedDoc{id: id, fields: copyFields(fields)}
}

// DeleteDoc 是管理操作；目标不存在时为空操作（幂等）。
func (s *Store) DeleteDoc(index, id string) {
	if !validName(index) || !validName(id) {
		panic(fmt.Sprintf("%v: DeleteDoc name out of range", role.ErrInvalid))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if ix, ok := s.indexs[index]; ok {
		delete(ix.docs, id)
	}
}

// snapshotIndex 返回索引内文档的快照切片（拷贝字段 map），并在锁内完成。
func (s *Store) snapshotIndex(index string) []*storedDoc {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ix, ok := s.indexs[index]
	if !ok {
		return nil
	}
	out := make([]*storedDoc, 0, len(ix.docs))
	for _, d := range ix.docs {
		out = append(out, &storedDoc{id: d.id, fields: copyFields(d.fields)})
	}
	return out
}

// indexExists 仅在解析视图后使用：不存在索引统一对外报无权。
func (s *Store) indexExists(index string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.indexs[index]
	return ok
}

// prune 在裁剪视图上生成只含可见字段的新字段映射；不可见字段被抹去。
func prune(fields map[string]role.Value, v *dls.View) map[string]role.Value {
	out := make(map[string]role.Value, len(fields))
	for name, val := range fields {
		if v.FieldVisible(name) {
			out[name] = val
		}
	}
	return out
}

// resolveView 落实拒绝次序：参数非法 > 用户不存在 > 无权
// （索引不存在与无匹配条目统一无权）。
func (s *Store) resolveView(user, index string) (*dls.View, error) {
	if !validName(user) || !validName(index) {
		return nil, fmt.Errorf("%w: user/index name", role.ErrInvalid)
	}
	v, err := s.rv.Resolve(user, index)
	if err != nil {
		return nil, err
	}
	if !s.indexExists(index) {
		return nil, fmt.Errorf("%w: index %s", dls.ErrForbidden, index)
	}
	return v, nil
}

// Search 在裁剪后的视图上求值 q：q 引用不可见字段时按缺字段处理
// （Term/Range 假、Not 真）。返回按 id 字节序前 size 条与匹配总数。
func (s *Store) Search(user, index string, q role.Expr, size int) ([]Doc, int, error) {
	if size < 1 || size > maxSize {
		return nil, 0, fmt.Errorf("%w: size must be 1..1000", role.ErrInvalid)
	}
	if err := role.ValidateExpr(q); err != nil {
		return nil, 0, err
	}
	v, err := s.resolveView(user, index)
	if err != nil {
		return nil, 0, err
	}
	docs := s.snapshotIndex(index)
	var matched []*storedDoc
	total := 0
	for _, d := range docs {
		if !v.DocVisible(d.fields) {
			continue
		}
		if !role.Eval(q, prune(d.fields, v)) {
			continue
		}
		total++
		matched = append(matched, d)
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].id < matched[j].id })
	out := make([]Doc, 0, min(size, len(matched)))
	for _, d := range matched[:min(size, len(matched))] {
		out = append(out, Doc{ID: d.id, Fields: prune(d.fields, v)})
	}
	return out, total, nil
}

// Get 返回单个可见文档（只含可见字段）；不可见与真不存在统一 ErrNotFound。
func (s *Store) Get(user, index, id string) (Doc, error) {
	if !validName(user) || !validName(index) || !validName(id) {
		return Doc{}, fmt.Errorf("%w: user/index/id name", role.ErrInvalid)
	}
	v, err := s.resolveView(user, index)
	if err != nil {
		return Doc{}, err
	}
	s.mu.RLock()
	ix, exists := s.indexs[index]
	var d *storedDoc
	ok := false
	if exists {
		d, ok = ix.docs[id]
	}
	var full map[string]role.Value
	if ok {
		full = copyFields(d.fields)
	}
	s.mu.RUnlock()
	if !ok || !v.DocVisible(full) {
		return Doc{}, fmt.Errorf("%w: %s/%s", ErrNotFound, index, id)
	}
	return Doc{ID: id, Fields: prune(full, v)}, nil
}

// Agg 对可见文档按该字段值计数（缺该字段不计）；字段不可见时返回空列表。
// 返回按值排序：int64 先于 string，各自升序。
func (s *Store) Agg(user, index, field string) ([]AggItem, error) {
	if !validName(user) || !validName(index) || !validName(field) {
		return nil, fmt.Errorf("%w: user/index/field name", role.ErrInvalid)
	}
	v, err := s.resolveView(user, index)
	if err != nil {
		return nil, err
	}
	if !v.FieldVisible(field) {
		return []AggItem{}, nil
	}
	docs := s.snapshotIndex(index)
	counts := map[role.Value]int{}
	for _, d := range docs {
		if !v.DocVisible(d.fields) {
			continue
		}
		val, ok := d.fields[field]
		if !ok {
			continue
		}
		counts[val]++
	}
	var ints []int64
	var strs []string
	for val := range counts {
		switch x := val.(type) {
		case int64:
			ints = append(ints, x)
		case string:
			strs = append(strs, x)
		}
	}
	sort.Slice(ints, func(i, j int) bool { return ints[i] < ints[j] })
	sort.Strings(strs)
	out := make([]AggItem, 0, len(ints)+len(strs))
	for _, x := range ints {
		out = append(out, AggItem{Value: x, Count: counts[x]})
	}
	for _, x := range strs {
		out = append(out, AggItem{Value: x, Count: counts[x]})
	}
	return out, nil
}
