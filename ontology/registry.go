package ontology

import (
	"log/slog"
	"sort"
	"sync"
)

// Registry 是对象类型的并发安全注册表。
// 所有写操作整体替换内部快照，读操作始终看到一致的全图状态。
type Registry struct {
	mu sync.RWMutex
	// types 以类型名为键的当前快照。
	types map[string]*ObjectType
	// invalidated 记录因重命名而失效的旧名。
	invalidated map[string]struct{}
	logger      *slog.Logger

	// 测试钩子：模拟重命名中断/状态损坏，验证校验与回滚路径。
	testCorruptCandidate func(next map[string]*ObjectType)
	testCorruptCommitted func(committed map[string]*ObjectType)
}

// NewRegistry 创建空注册表，logger 为 nil 时使用 slog.Default()。
func NewRegistry(logger *slog.Logger) *Registry {
	if logger == nil {
		logger = slog.Default()
	}
	return &Registry{
		types:       make(map[string]*ObjectType),
		invalidated: make(map[string]struct{}),
		logger:      logger,
	}
}

// AddType 注册一个对象类型；名字已存在或撞上已失效名时返回错误。
func (r *Registry) AddType(t *ObjectType) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.types[t.Name]; ok {
		return newError(CodeNameConflict, t.Name, "类型名已被占用")
	}
	if _, ok := r.invalidated[t.Name]; ok {
		return newError(CodeOldNameInvalidated, t.Name, "该名字已因重命名失效，不可复用")
	}
	r.types[t.Name] = t.clone()
	r.logger.Info("注册对象类型", "type", t.Name)
	return nil
}

// Resolve 按名字解析对象类型。
// 名字不存在返回 CodeTypeNotFound；名字已因重命名失效返回 CodeOldNameInvalidated。
func (r *Registry) Resolve(name string) (*ObjectType, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.resolveLocked(name)
}

func (r *Registry) resolveLocked(name string) (*ObjectType, error) {
	if t, ok := r.types[name]; ok {
		return t.clone(), nil
	}
	if _, ok := r.invalidated[name]; ok {
		return nil, newError(CodeOldNameInvalidated, name, "该名字已因重命名失效")
	}
	return nil, newError(CodeTypeNotFound, name, "类型未注册")
}

// Reference 描述图中一处对类型名的引用位置。
type Reference struct {
	HolderType string // 持有该引用的对象类型
	Kind       string // property_ref / link_source / link_target / action_param / action_return / action_property_ref
	Location   string // 具体位置描述，如属性名、链接名
	RefName    string // 被引用的类型名
}

// References 扫描全图，返回所有引用 name 的位置。
func (r *Registry) References(name string) []Reference {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return referencesLocked(r.types, name)
}

func referencesLocked(types map[string]*ObjectType, name string) []Reference {
	var refs []Reference
	for _, t := range types {
		for _, p := range t.Properties {
			if p.Type == PrimitiveObjectRef && p.RefType == name {
				refs = append(refs, Reference{HolderType: t.Name, Kind: "property_ref", Location: "property:" + p.Name, RefName: name})
			}
		}
		for _, l := range t.Links {
			if l.SourceType == name {
				refs = append(refs, Reference{HolderType: t.Name, Kind: "link_source", Location: "link:" + l.Name, RefName: name})
			}
			if l.TargetType == name {
				refs = append(refs, Reference{HolderType: t.Name, Kind: "link_target", Location: "link:" + l.Name, RefName: name})
			}
		}
		for _, a := range t.Actions {
			for _, param := range a.Params {
				if param.TypeRef == name {
					refs = append(refs, Reference{HolderType: t.Name, Kind: "action_param", Location: "action:" + a.Name + "/param:" + param.Name, RefName: name})
				}
			}
			for _, ret := range a.ReturnRefs {
				if ret == name {
					refs = append(refs, Reference{HolderType: t.Name, Kind: "action_return", Location: "action:" + a.Name, RefName: name})
				}
			}
			for _, pr := range a.PropertyRef {
				if pr.TypeName == name {
					refs = append(refs, Reference{HolderType: t.Name, Kind: "action_property_ref", Location: "action:" + a.Name + "/prop:" + pr.PropertyName, RefName: name})
				}
			}
		}
	}
	return refs
}

// List 返回按名字排序的全部类型快照。
func (r *Registry) List() []*ObjectType {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.types))
	for name := range r.types {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]*ObjectType, 0, len(names))
	for _, name := range names {
		out = append(out, r.types[name].clone())
	}
	return out
}

// Validate 在当前一致快照上校验全图不存在悬空引用。
func (r *Registry) Validate() error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if dangling := danglingReferences(r.types); len(dangling) > 0 {
		return newError(CodeDanglingReference, dangling[0].RefName, danglingReason(dangling))
	}
	return nil
}

// snapshot 返回当前全部类型的深拷贝。
func (r *Registry) snapshot() map[string]*ObjectType {
	out := make(map[string]*ObjectType, len(r.types))
	for name, t := range r.types {
		out[name] = t.clone()
	}
	return out
}
