package ontology

import (
	"fmt"
	"sync"
)

// ErrKind 是可区分的裁决类别，声明顺序即报错优先次序。
type ErrKind int

const (
	ErrInvalid          ErrKind = iota // 参数非法：空文本、非法字符、导航段文法错误
	ErrSymrefCycle                     // 符号引用成环（仅设置时）
	ErrRefIDAmbiguous                  // 引用与标识歧义
	ErrPrefixAmbiguous                 // 前缀歧义（附候选个数）
	ErrNotExist                        // 不存在
	ErrNotARef                         // 非引用不可用日志
	ErrNoSuchLogEntry                  // 记录不存在
	ErrTypeNotNavigable                // 类型不可导航
	ErrNoSuchParent                    // 父不存在
	ErrBeyondHistory                   // 超出历史
	ErrTypeMismatch                    // 类型不符
)

func (k ErrKind) String() string {
	switch k {
	case ErrInvalid:
		return "invalid"
	case ErrSymrefCycle:
		return "symref-cycle"
	case ErrRefIDAmbiguous:
		return "ref-id-ambiguous"
	case ErrPrefixAmbiguous:
		return "prefix-ambiguous"
	case ErrNotExist:
		return "not-exist"
	case ErrNotARef:
		return "not-a-ref"
	case ErrNoSuchLogEntry:
		return "no-such-log-entry"
	case ErrTypeNotNavigable:
		return "type-not-navigable"
	case ErrNoSuchParent:
		return "no-such-parent"
	case ErrBeyondHistory:
		return "beyond-history"
	case ErrTypeMismatch:
		return "type-mismatch"
	default:
		return "unknown"
	}
}

// Error 是一次被拒绝的解析/设置。Segment 是失败的导航段下标（0 起），
// -1 表示失败发生在基址解析或最终结果类型检查。
type Error struct {
	Kind       ErrKind
	Detail     string
	Candidates int // 仅 ErrPrefixAmbiguous：候选对象个数
	Segment    int
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Kind, e.Detail)
}

// Config 是服务配置。
type Config struct {
	MinAbbrev  int      // 参与标识匹配的最短缩写长度，默认 4
	Namespaces []string // 短名补全命名空间（有序），默认 refs/heads/、refs/tags/
}

func (c Config) withDefaults() Config {
	if c.MinAbbrev <= 0 {
		c.MinAbbrev = 4
	}
	if c.MinAbbrev > idLen {
		c.MinAbbrev = idLen
	}
	if len(c.Namespaces) == 0 {
		c.Namespaces = []string{"refs/heads/", "refs/tags/"}
	}
	return c
}

// Query 是结果类型约束：Type 为 TypeAny 表示不约束；AutoPeel 允许在检查时
// 自动剥去标签以满足约束。
type Query struct {
	Type     ObjType
	AutoPeel bool
}

// Result 是一次成功解析的结果。
type Result struct {
	ID   string
	Type ObjType
	Ref  string // 基址经引用解析且未被日志导航消费时，为末端引用全名
}

// Service 是修订表达式解析与消歧服务。对象写入、引用更新与解析可任意并发；
// 解析全程持读锁，等价于看到某一瞬间的完整状态，全体操作等价于某串行顺序。
type Service struct {
	mu   sync.RWMutex
	cfg  Config
	objs *objectStore
	refs *refStore
}

func NewService(cfg Config) *Service {
	c := cfg.withDefaults()
	return &Service{cfg: c, objs: newObjectStore(), refs: newRefStore(c.Namespaces)}
}

// Put 追加对象。标识须为 40 位小写十六进制且不重复；提交的父与树、标签的
// 目标须已存在且类型匹配。对象只增不删。
func (s *Service) Put(o Object) *Error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(o.ID) != idLen || !isHex(o.ID) {
		return invalidf("malformed object id %q", o.ID)
	}
	if _, dup := s.objs.get(o.ID); dup {
		return invalidf("duplicate object %s", o.ID)
	}
	switch o.Type {
	case TypeCommit:
		for _, p := range o.Parents {
			po, ok := s.objs.get(p)
			if !ok || po.Type != TypeCommit {
				return invalidf("commit %s has unknown/non-commit parent %s", o.ID, p)
			}
		}
		to, ok := s.objs.get(o.Tree)
		if !ok || to.Type != TypeTree {
			return invalidf("commit %s has unknown/non-tree tree %s", o.ID, o.Tree)
		}
	case TypeTag:
		if _, ok := s.objs.get(o.Target); !ok {
			return invalidf("tag %s targets unknown object %s", o.ID, o.Target)
		}
	case TypeTree, TypeBlob:
	default:
		return invalidf("unknown object type %d", o.Type)
	}
	s.objs.put(o)
	return nil
}

// SetRef 把全名设为指向 id 的直接引用，并追加引用日志。
func (s *Service) SetRef(name, id string) *Error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !isValidRefName(name) {
		return invalidf("malformed ref name %q", name)
	}
	if _, ok := s.objs.get(id); !ok {
		return invalidf("ref %s targets unknown object %s", name, id)
	}
	s.refs.set(name, id)
	return nil
}

// SetSymbolic 把 name 设为指向 target 的符号引用；成环则整体拒绝。
func (s *Service) SetSymbolic(name, target string) *Error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !isValidRefName(name) || !isValidRefName(target) {
		return invalidf("malformed symbolic ref %q -> %q", name, target)
	}
	if !s.refs.setSymbolic(name, target) {
		return &Error{Kind: ErrSymrefCycle, Detail: fmt.Sprintf("symbolic ref %q -> %q would create a cycle", name, target), Segment: -1}
	}
	return nil
}

// ShortestAbbrev 返回 id 在当前对象库中唯一且不短于配置下限的最短前缀。
func (s *Service) ShortestAbbrev(id string) (string, *Error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(id) != idLen || !isHex(id) {
		return "", invalidf("malformed object id %q", id)
	}
	abbr, _, ok := s.objs.shortestAbbrev(id, s.cfg.MinAbbrev)
	if !ok {
		return "", &Error{Kind: ErrNotExist, Detail: fmt.Sprintf("no object %s", id), Segment: -1}
	}
	return abbr, nil
}

// Resolve 把表达式唯一解析为一个对象；被拒绝时不改变任何状态。
func (s *Service) Resolve(text string, q Query) (Result, *Error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, perr := parseExpr(text)
	if perr != nil {
		return Result{}, perr
	}
	cur, refName, rerr := s.resolveBase(e.base)
	if rerr != nil {
		return Result{}, rerr
	}
	for i, sg := range e.segs {
		var serr *Error
		cur, refName, serr = s.applySeg(cur, refName, sg)
		if serr != nil {
			serr.Segment = i
			return Result{}, serr
		}
	}
	if q.Type != TypeAny {
		if cur.Type == TypeTag && q.AutoPeel {
			cur = s.peel(cur)
		}
		if cur.Type != q.Type {
			return Result{}, &Error{
				Kind:    ErrTypeMismatch,
				Detail:  fmt.Sprintf("want %s, got %s", q.Type, cur.Type),
				Segment: -1,
			}
		}
	}
	return Result{ID: cur.ID, Type: cur.Type, Ref: refName}, nil
}

// resolveBase 解析基址：全名精确命中（豁免歧义）> 命名空间补全（与有效
// 标识前缀并存报引用与标识歧义）> 标识前缀匹配（唯一命中/前缀歧义/不存在）。
func (s *Service) resolveBase(base string) (Object, string, *Error) {
	if hit, ok := s.refs.lookup(base); ok {
		if hit.dangling {
			return Object{}, "", &Error{Kind: ErrNotExist, Detail: fmt.Sprintf("ref %q is dangling", base), Segment: -1}
		}
		o, ok := s.objs.get(hit.id)
		if !ok {
			return Object{}, "", &Error{Kind: ErrNotExist, Detail: fmt.Sprintf("ref %q targets unknown object", base), Segment: -1}
		}
		if !hit.exact && s.isUsableIDPrefix(base) {
			if n, _ := s.objs.prefixCount(base); n > 0 {
				return Object{}, "", &Error{Kind: ErrRefIDAmbiguous, Detail: fmt.Sprintf("%q is both a ref and an object id prefix", base), Segment: -1}
			}
		}
		return o, hit.terminal, nil
	}
	if s.isUsableIDPrefix(base) {
		n, _ := s.objs.prefixCount(base)
		switch {
		case n == 0:
			return Object{}, "", &Error{Kind: ErrNotExist, Detail: fmt.Sprintf("no object matches %q", base), Segment: -1}
		case n > 1:
			return Object{}, "", &Error{Kind: ErrPrefixAmbiguous, Detail: fmt.Sprintf("prefix %q is ambiguous", base), Candidates: n, Segment: -1}
		}
		id, _ := s.objs.prefixFirst(base)
		o, _ := s.objs.get(id)
		return o, "", nil
	}
	return Object{}, "", &Error{Kind: ErrNotExist, Detail: fmt.Sprintf("%q is neither a ref nor an object id prefix", base), Segment: -1}
}

// isUsableIDPrefix 报告 base 是否达到下限、长度合法且为纯小写十六进制，
// 即是否参与标识匹配。
func (s *Service) isUsableIDPrefix(base string) bool {
	return len(base) >= s.cfg.MinAbbrev && len(base) <= idLen && isHex(base)
}

// peel 剥去标签直至非标签对象。
func (s *Service) peel(o Object) Object {
	for o.Type == TypeTag {
		o, _ = s.objs.get(o.Target)
	}
	return o
}

// applySeg 求值一个导航段；成功后结果都是对象（refName 清空）。
func (s *Service) applySeg(cur Object, refName string, sg segment) (Object, string, *Error) {
	switch sg.kind {
	case segParent:
		if cur.Type != TypeCommit {
			return cur, refName, &Error{Kind: ErrTypeNotNavigable, Detail: fmt.Sprintf("%s on %s", sg.text, cur.Type)}
		}
		if sg.n > len(cur.Parents) {
			return cur, refName, &Error{Kind: ErrNoSuchParent, Detail: fmt.Sprintf("commit has %d parents, want #%d", len(cur.Parents), sg.n)}
		}
		o, _ := s.objs.get(cur.Parents[sg.n-1])
		return o, "", nil
	case segAncestor:
		if cur.Type != TypeCommit {
			return cur, refName, &Error{Kind: ErrTypeNotNavigable, Detail: fmt.Sprintf("%s on %s", sg.text, cur.Type)}
		}
		for k := 0; k < sg.n; k++ {
			if len(cur.Parents) == 0 {
				return cur, refName, &Error{Kind: ErrBeyondHistory, Detail: fmt.Sprintf("no first parent at step %d of %s", k+1, sg.text)}
			}
			cur, _ = s.objs.get(cur.Parents[0])
		}
		return cur, "", nil
	case segReflog:
		if refName == "" {
			return cur, refName, &Error{Kind: ErrNotARef, Detail: fmt.Sprintf("%s requires a ref, not an object id", sg.text)}
		}
		log := s.refs.reflog(refName)
		if sg.n >= len(log) {
			return cur, refName, &Error{Kind: ErrNoSuchLogEntry, Detail: fmt.Sprintf("ref %s has %d log entries, want @{%d}", refName, len(log), sg.n)}
		}
		o, _ := s.objs.get(log[len(log)-1-sg.n])
		return o, "", nil
	case segPeel:
		return s.peel(cur), "", nil
	case segTree:
		if cur.Type != TypeCommit {
			return cur, refName, &Error{Kind: ErrTypeNotNavigable, Detail: fmt.Sprintf("%s on %s", sg.text, cur.Type)}
		}
		o, _ := s.objs.get(cur.Tree)
		return o, "", nil
	}
	return cur, refName, &Error{Kind: ErrInvalid, Detail: "unknown segment"}
}
