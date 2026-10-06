package revision

import (
	"fmt"
	"sync"
)

// Store 组合对象库与引用集合，提供并发安全的修订解析服务。
type Store struct {
	mu   sync.RWMutex
	db   *ObjectDB
	refs *RefStore
	cfg  Config
	ns   []string
}

// NewStore 创建服务。namespaces 为短名补全的命名空间次序。
func NewStore(cfg Config, namespaces []string) *Store {
	return &Store{
		db:   NewObjectDB(cfg),
		refs: newRefStore(namespaces),
		cfg:  cfg,
		ns:   namespaces,
	}
}

// AddObject 写入对象。
func (s *Store) AddObject(obj Object) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db.Add(obj)
}

// SetRef 设置直接引用（targetID 必须为库中已有对象），并追加 reflog。
func (s *Store) SetRef(name, targetID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.db.Get(targetID); !ok {
		return fmt.Errorf("ref %q target %s not in object db", name, targetID)
	}
	return s.refs.setTarget(name, targetID, targetID)
}

// SetSymbolicRef 设置符号引用；成环则整体拒绝。
func (s *Store) SetSymbolicRef(name, target string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.refs.setSymbolic(name, target)
}

// Result 是一次成功解析的结果。
type Result struct {
	Object  Object
	FromRef string // 首段经引用命中时的引用全名
}

// ResolveOption 配置结果类型约束。
type ResolveOption struct {
	WantType ObjType
	Require  bool // 是否要求结果必须为 WantType
	AutoPeel bool // 允许自动剥标签以满足类型要求
}

// Resolve 解析修订表达式（桩）。
// 解析全程持有读锁，因此看到的对象库与引用集合是某个瞬间的完整状态。
func (s *Store) Resolve(text string, opt ResolveOption) (Result, error) {
	exp, err := parseExpression(text)
	if err != nil {
		return Result{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	obj, fromRef, err := s.resolveHead(exp.head)
	if err != nil {
		return Result{}, err
	}
	for i, seg := range exp.segs {
		obj, fromRef, err = s.applySegment(obj, fromRef, seg, i)
		if err != nil {
			return Result{}, err
		}
	}
	if opt.Require {
		if obj.Type == TypeTag && opt.AutoPeel {
			obj = s.peelTag(obj)
		}
		if obj.Type != opt.WantType {
			return Result{}, &ResolutionError{Code: ErrTypeMismatch, SegIndex: len(exp.segs),
				Detail: fmt.Sprintf("result type %s does not match required %s",
					typeName(obj.Type), typeName(opt.WantType))}
		}
	}
	return Result{Object: obj, FromRef: fromRef}, nil
}

// ShortestAbbrev 返回对象在当前库中保证唯一的最短缩写（桩）。
func (s *Store) ShortestAbbrev(id string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.db.shortestAbbrev(id)
}

// resolveHead 在锁内完成首段裁决。
func (s *Store) resolveHead(head string) (Object, string, error) {
	refHit, refOK := s.refs.lookupShort(head, s.ns)
	hexPrefix := len(head) >= s.cfg.MinAbbrev && isHex(head)
	if refOK {
		direct, targetID, exists := s.refs.resolve(refHit.fullName)
		if !exists {
			// 指向缺失目标的悬空引用按不存在裁决。
			if hexPrefix {
				return Object{}, "", &ResolutionError{Code: ErrNotFound, SegIndex: -1,
					Detail: fmt.Sprintf("reference %q dangles and head %q is not a resolvable id prefix",
						refHit.fullName, head)}
			}
			return Object{}, "", &ResolutionError{Code: ErrNotFound, SegIndex: -1,
				Detail: fmt.Sprintf("reference %q dangles", refHit.fullName)}
		}
		obj, ok := s.db.Get(targetID)
		if !ok {
			return Object{}, "", &ResolutionError{Code: ErrNotFound, SegIndex: -1,
				Detail: fmt.Sprintf("reference %q target missing", direct)}
		}
		// 全名直接命中豁免「引用与标识歧义」；补全命中不豁免。
		if hexPrefix && !refHit.exact && s.db.countPrefix(head) > 0 {
			return Object{}, "", &ResolutionError{Code: ErrRefIDAmbiguous, SegIndex: -1,
				Detail: fmt.Sprintf("head %q is both a reference and an id prefix", head)}
		}
		return obj, refHit.fullName, nil
	}
	if hexPrefix {
		n := s.db.countPrefix(head)
		switch {
		case n == 1:
			if obj, ok := s.db.findByPrefix(head); ok {
				return obj, "", nil
			}
		case n > 1:
			return Object{}, "", &ResolutionError{Code: ErrPrefixAmbiguous, SegIndex: -1,
				Candidates: n,
				Detail:     fmt.Sprintf("id prefix %q is ambiguous (%d candidates)", head, n)}
		}
	}
	return Object{}, "", &ResolutionError{Code: ErrNotFound, SegIndex: -1,
		Detail: fmt.Sprintf("revision %q not found", head)}
}

// applySegment 在锁内逐段导航；返回错误时不修改任何状态（求值无副作用）。
func (s *Store) applySegment(obj Object, fromRef string, seg Segment, idx int) (Object, string, error) {
	fail := func(code ErrorCode, detail string) (Object, string, error) {
		return Object{}, "", &ResolutionError{Code: code, SegIndex: idx, Detail: detail}
	}
	switch seg.Kind {
	case SegReflog:
		// 引用日志只对引用名有效；经过任何导航（含上一段日志）后均不可用。
		if fromRef == "" {
			return fail(ErrReflogOnID, "reflog navigation requires a reference name")
		}
		direct, _, ok := s.refs.resolve(fromRef)
		if !ok {
			return fail(ErrReflogOnID, "reference chain unresolved for reflog")
		}
		id, ok := s.refs.reflogBack(direct, seg.N)
		if !ok {
			return fail(ErrReflogMissing, fmt.Sprintf(
				"reflog entry @{%d} does not exist for %q", seg.N, direct))
		}
		prev, ok := s.db.Get(id)
		if !ok {
			return fail(ErrNotFound, fmt.Sprintf("reflog target %s missing", id))
		}
		// 日志解析后结果已不再"来自引用名"，后续日志段将得到 ErrReflogOnID。
		return prev, "", nil
	case SegParent, SegAncestor:
		if obj.Type != TypeCommit {
			return fail(ErrNotNavigable, fmt.Sprintf(
				"%s navigation requires a commit, got %s",
				navName(seg.Kind), typeName(obj.Type)))
		}
		if seg.Kind == SegParent {
			if seg.N > len(obj.Parents) {
				return fail(ErrParentMissing, fmt.Sprintf(
					"commit has %d parent(s), parent %d requested", len(obj.Parents), seg.N))
			}
			parent, ok := s.db.Get(obj.Parents[seg.N-1])
			if !ok {
				return fail(ErrParentMissing, "parent object missing")
			}
			return parent, fromRef, nil
		}
		cur := obj
		for step := 0; step < seg.N; step++ {
			if cur.Type != TypeCommit {
				return fail(ErrNotNavigable, "ancestor walk reached non-commit")
			}
			if len(cur.Parents) == 0 {
				return fail(ErrBeyondHistory, fmt.Sprintf(
					"ancestor walk exceeds root after %d generation(s)", step))
			}
			cur, _ = s.db.Get(cur.Parents[0])
		}
		return cur, fromRef, nil
	case SegPeel:
		return s.peelTag(obj), fromRef, nil
	case SegTree:
		if obj.Type != TypeCommit {
			return fail(ErrNotNavigable, fmt.Sprintf(
				"^{tree} requires a commit, got %s", typeName(obj.Type)))
		}
		tree, ok := s.db.Get(obj.Tree)
		if !ok {
			return fail(ErrNotNavigable, "commit tree missing")
		}
		return tree, fromRef, nil
	}
	return obj, fromRef, nil
}

// peelTag 沿标签链剥至非标签对象；非标签对象原样返回（无副作用成功）。
func (s *Store) peelTag(obj Object) Object {
	for obj.Type == TypeTag {
		next, ok := s.db.Get(obj.Target)
		if !ok {
			return obj // 完整性保证不会到达
		}
		obj = next
	}
	return obj
}

func isHex(s string) bool {
	for i := 0; i < len(s); i++ {
		if _, ok := hexVal(s[i]); !ok {
			return false
		}
	}
	return true
}

func typeName(t ObjType) string {
	switch t {
	case TypeCommit:
		return "commit"
	case TypeTag:
		return "tag"
	case TypeTree:
		return "tree"
	case TypeBlob:
		return "blob"
	}
	return "unknown"
}

func navName(k SegmentKind) string {
	switch k {
	case SegParent:
		return "^N parent"
	case SegAncestor:
		return "~N ancestor"
	case SegReflog:
		return "@{N} reflog"
	case SegPeel:
		return "^{} peel"
	case SegTree:
		return "^{tree}"
	}
	return "unknown"
}
