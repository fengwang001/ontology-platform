// Package ingest 负责文档校验、按序遍历、原子提交与存取。
package ingest

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"ontology/coerce"
	"ontology/mapping"
)

const (
	maxIDLen  = 512
	maxKeyLen = 64
	maxDepth  = 8
)

// Engine 是文档入库器，所有方法可并发调用（内部串行化）。
type Engine struct {
	mu   sync.Mutex
	m    *mapping.Mapping
	dyn  mapping.Dynamic
	docs map[string]map[string]any
}

// New 创建入库器；dynamic 取 True/False/Strict，fmax 为字段总数上限。
func New(dyn mapping.Dynamic, fmax int) (*Engine, error) {
	if !dyn.Valid() {
		return nil, fmt.Errorf("%w: dynamic %d", mapping.ErrInvalidArgument, dyn)
	}
	m, err := mapping.New(fmax)
	if err != nil {
		return nil, err
	}
	return &Engine{m: m, dyn: dyn, docs: make(map[string]map[string]any)}, nil
}

// Index 校验并入库文档，返回规范化文档、Ignored 列表与当前 mv。
// 拒绝是全有或全无：不留下任何新字段，mv 不变。
func (e *Engine) Index(id string, doc map[string]any) (map[string]any, []string, int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := validateID(id); err != nil {
		return nil, nil, e.m.MV(), err
	}
	if err := validateDoc(doc); err != nil {
		return nil, nil, e.m.MV(), err
	}
	st := &walkState{}
	out, err := e.walkObject(e.m.Root(), nil, doc, st)
	if err != nil {
		for i := len(st.added) - 1; i >= 0; i-- {
			e.m.RemoveChild(st.added[i].parent, st.added[i].key)
		}
		return nil, nil, e.m.MV(), err
	}
	if len(st.added) > 0 {
		e.m.BumpMV()
	}
	e.docs[id] = out
	sort.Strings(st.ignored)
	return deepCopyDoc(out), st.ignored, e.m.MV(), nil
}

// Get 返回 id 的规范化文档，不存在报 ErrDocNotFound。
func (e *Engine) Get(id string) (map[string]any, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	doc, ok := e.docs[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", mapping.ErrDocNotFound, id)
	}
	return deepCopyDoc(doc), nil
}

// PutMapping 显式加字段，路径上缺失的父节点自动建为 object。
// 已存在且类型相同为空操作，mv 不变。
func (e *Engine) PutMapping(path string, typ mapping.Type) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !typ.Valid() {
		return e.m.MV(), fmt.Errorf("%w: type %d", mapping.ErrInvalidArgument, typ)
	}
	segs := strings.Split(path, ".")
	for _, s := range segs {
		if err := validateKey(s); err != nil {
			return e.m.MV(), err
		}
	}
	n := e.m.Root()
	i := 0
	for ; i < len(segs); i++ {
		c := e.m.Child(n, segs[i])
		if c == nil {
			break
		}
		if i == len(segs)-1 {
			if e.m.TypeOf(c) == typ {
				return e.m.MV(), nil
			}
			return e.m.MV(), &mapping.PathError{Path: path, Err: mapping.ErrTypeConflict,
				Detail: "field exists with different type"}
		}
		if e.m.TypeOf(c) != mapping.Object {
			return e.m.MV(), &mapping.PathError{Path: strings.Join(segs[:i+1], "."), Err: mapping.ErrTypeConflict,
				Detail: "parent is a leaf field"}
		}
		n = c
	}
	if missing := len(segs) - i; e.m.Count()+missing > e.m.Fmax() {
		return e.m.MV(), &mapping.PathError{Path: path, Err: mapping.ErrFieldLimit}
	}
	for ; i < len(segs); i++ {
		t := mapping.Object
		if i == len(segs)-1 {
			t = typ
		}
		n = e.m.AddChild(n, segs[i], t)
	}
	e.m.BumpMV()
	return e.m.MV(), nil
}

// MV 返回当前映射版本。
func (e *Engine) MV() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.m.MV()
}

// Fields 导出全部字段路径到类型的快照。
func (e *Engine) Fields() map[string]mapping.Type {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.m.Fields()
}

type addedNode struct {
	parent *mapping.Node
	key    string
}

type walkState struct {
	added   []addedNode
	ignored []string
}

func (e *Engine) walkObject(node *mapping.Node, prefix []string, src map[string]any, st *walkState) (map[string]any, error) {
	out := make(map[string]any, len(src))
	keys := make([]string, 0, len(src))
	for k := range src {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := src[k]
		path := joinPath(prefix, k)
		switch x := v.(type) {
		case nil:
			out[k] = nil
		case map[string]any:
			child, ignored, err := e.resolve(node, k, path, mapping.Object, st)
			if err != nil {
				return nil, err
			}
			if ignored {
				continue
			}
			if e.m.TypeOf(child) != mapping.Object {
				return nil, &mapping.PathError{Path: path, Err: mapping.ErrTypeConflict,
					Detail: "leaf field rejects object"}
			}
			sub, err := e.walkObject(child, append(prefix, k), x, st)
			if err != nil {
				return nil, err
			}
			out[k] = sub
		case []any:
			res, ignored, err := e.walkArray(node, k, path, x, st)
			if err != nil {
				return nil, err
			}
			if ignored {
				continue
			}
			out[k] = res
		default:
			child, ignored, err := e.resolve(node, k, path, infer(v), st)
			if err != nil {
				return nil, err
			}
			if ignored {
				continue
			}
			t := e.m.TypeOf(child)
			if t == mapping.Object {
				return nil, &mapping.PathError{Path: path, Err: mapping.ErrTypeConflict,
					Detail: "object field rejects scalar"}
			}
			c, err := coerce.Coerce(t, v)
			if err != nil {
				return nil, &mapping.PathError{Path: path, Err: mapping.ErrTypeConflict, Detail: err.Error()}
			}
			out[k] = c
		}
	}
	return out, nil
}

func (e *Engine) walkArray(node *mapping.Node, key, path string, arr []any, st *walkState) ([]any, bool, error) {
	var first any
	for _, el := range arr {
		if el != nil {
			first = el
			break
		}
	}
	if first == nil {
		out := make([]any, len(arr))
		copy(out, arr)
		return out, false, nil
	}
	child, ignored, err := e.resolve(node, key, path, infer(first), st)
	if err != nil {
		return nil, false, err
	}
	if ignored {
		return nil, true, nil
	}
	t := e.m.TypeOf(child)
	if t == mapping.Object {
		return nil, false, &mapping.PathError{Path: path, Err: mapping.ErrTypeConflict,
			Detail: "object field rejects array"}
	}
	out := make([]any, len(arr))
	for i, el := range arr {
		if el == nil {
			continue
		}
		c, err := coerce.Coerce(t, el)
		if err != nil {
			return nil, false, &mapping.PathError{Path: path, Err: mapping.ErrTypeConflict, Detail: err.Error()}
		}
		out[i] = c
	}
	return out, false, nil
}

// resolve 返回 key 对应节点；没有字段时按 dynamic 处理（True 以 want 建字段）。
// ignored=true 表示 dynamic=False 忽略该键连同整棵子树。
func (e *Engine) resolve(parent *mapping.Node, key, path string, want mapping.Type, st *walkState) (n *mapping.Node, ignored bool, err error) {
	if e.dyn == mapping.True {
		n, created, err := e.m.ChildOrAdd(parent, key, want)
		if err != nil {
			return nil, false, &mapping.PathError{Path: path, Err: err}
		}
		if created {
			st.added = append(st.added, addedNode{parent, key})
		}
		return n, false, nil
	}
	if n := e.m.Child(parent, key); n != nil {
		return n, false, nil
	}
	if e.dyn == mapping.False {
		st.ignored = append(st.ignored, path)
		return nil, true, nil
	}
	return nil, false, &mapping.PathError{Path: path, Err: mapping.ErrStrict, Detail: "unknown field"}
}

func infer(v any) mapping.Type {
	switch v.(type) {
	case bool:
		return mapping.Bool
	case int64:
		return mapping.Long
	case float64:
		return mapping.Double
	case string:
		return mapping.Keyword
	}
	return mapping.Object
}

func joinPath(prefix []string, k string) string {
	if len(prefix) == 0 {
		return k
	}
	return strings.Join(prefix, ".") + "." + k
}

func validateID(id string) error {
	if len(id) == 0 || len(id) > maxIDLen {
		return fmt.Errorf("%w: id length %d out of [1, %d]", mapping.ErrInvalidArgument, len(id), maxIDLen)
	}
	return nil
}

func validateKey(k string) error {
	if len(k) == 0 || len(k) > maxKeyLen || strings.Contains(k, ".") {
		return fmt.Errorf("%w: key %q", mapping.ErrInvalidArgument, k)
	}
	return nil
}

// validateDoc 在动任何映射之前整份检查文档结构。
func validateDoc(doc map[string]any) error {
	return checkObject(doc, 1)
}

func checkObject(obj map[string]any, depth int) error {
	if depth > maxDepth {
		return fmt.Errorf("%w: object nesting deeper than %d", mapping.ErrInvalidArgument, maxDepth)
	}
	for k, v := range obj {
		if err := validateKey(k); err != nil {
			return err
		}
		switch x := v.(type) {
		case nil, bool, int64, float64, string:
		case map[string]any:
			if err := checkObject(x, depth+1); err != nil {
				return err
			}
		case []any:
			for _, el := range x {
				switch el.(type) {
				case nil, bool, int64, float64, string:
				default:
					return fmt.Errorf("%w: array element %T at key %q", mapping.ErrInvalidArgument, el, k)
				}
			}
		default:
			return fmt.Errorf("%w: value %T at key %q", mapping.ErrInvalidArgument, v, k)
		}
	}
	return nil
}

func deepCopyDoc(doc map[string]any) map[string]any {
	out := make(map[string]any, len(doc))
	for k, v := range doc {
		switch x := v.(type) {
		case map[string]any:
			out[k] = deepCopyDoc(x)
		case []any:
			c := make([]any, len(x))
			copy(c, x)
			out[k] = c
		default:
			out[k] = v
		}
	}
	return out
}
