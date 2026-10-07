package ontology

import (
	"fmt"
	"strings"
	"sync"
)

// Store 是多租户命名空间权限模块的并发安全入口。
// 所有并发调用的结果等价于某个串行顺序逐条处理这些调用得到的结果（可线性化）。
//
// 并发与原子性设计：
//   - 全部状态保存在不可变的 state 快照中，任何变更整体构建新快照后原子替换指针；
//   - 租户撤销覆盖、全局默认规则切换都是单次指针替换，并发判定观察不到中间状态；
//   - 判定在写锁内基于同一快照完成并追加审计，保证所有调用间存在全局串行序。
type Store struct {
	mu sync.Mutex
	st *state
}

type ruleKey struct {
	typ   string
	class string
}

type overrideKey struct {
	tenant string
	typ    string
	class  string
}

type state struct {
	tenants       map[string]bool
	types         map[string]ObjectTypeDef
	basisRequired map[string]bool
	defaults      map[ruleKey][]Statement
	overrides     map[overrideKey][]Statement
	audit         []AuditRecord
	seq           uint64
}

// NewStore 创建空 Store。
func NewStore() *Store {
	return &Store{st: &state{
		tenants:       map[string]bool{},
		types:         map[string]ObjectTypeDef{},
		basisRequired: map[string]bool{},
		defaults:      map[ruleKey][]Statement{},
		overrides:     map[overrideKey][]Statement{},
	}}
}

func cloneStatements(in []Statement) []Statement {
	if in == nil {
		return nil
	}
	out := make([]Statement, len(in))
	for i, s := range in {
		out[i] = s
		if s.Scope.Members != nil {
			out[i].Scope.Members = append([]string(nil), s.Scope.Members...)
		}
	}
	return out
}

// clone 深拷贝状态（写时复制）。审计切片共享底层数组但永不原地修改。
func (s *state) clone() *state {
	ns := &state{
		tenants:       make(map[string]bool, len(s.tenants)),
		types:         make(map[string]ObjectTypeDef, len(s.types)),
		basisRequired: make(map[string]bool, len(s.basisRequired)),
		defaults:      make(map[ruleKey][]Statement, len(s.defaults)),
		overrides:     make(map[overrideKey][]Statement, len(s.overrides)),
		audit:         s.audit,
		seq:           s.seq,
	}
	for k, v := range s.tenants {
		ns.tenants[k] = v
	}
	for k, v := range s.types {
		ns.types[k] = v
	}
	for k, v := range s.basisRequired {
		ns.basisRequired[k] = v
	}
	for k, v := range s.defaults {
		ns.defaults[k] = v
	}
	for k, v := range s.overrides {
		ns.overrides[k] = v
	}
	return ns
}

func (s *state) appendAudit(op, input, output string, basis map[string]string) {
	s.seq++
	s.audit = append(s.audit, AuditRecord{
		Seq:       s.seq,
		Op:        op,
		Input:     input,
		Output:    output,
		RuleBasis: basis,
	})
}

// validateScopes 校验声明引用的属性与谓词在对象类型定义中存在。
func validateScopes(def *ObjectTypeDef, stmts []Statement) error {
	attrSet := make(map[string]bool, len(def.Attrs))
	for _, a := range def.Attrs {
		attrSet[a] = true
	}
	predSet := make(map[string]bool, len(def.Predicates))
	for _, p := range def.Predicates {
		predSet[p.ID] = true
	}
	check := func(kind ScopeKind, name string) error {
		switch kind {
		case ScopeAttr, ScopeAttrSet:
			if !attrSet[name] {
				return newError(ErrNotFound, "对象类型 %q 不存在属性 %q", def.Name, name)
			}
		case ScopePredicate, ScopePredicateSet:
			if !predSet[name] {
				return newError(ErrNotFound, "对象类型 %q 不存在谓词 %q", def.Name, name)
			}
		}
		return nil
	}
	for _, st := range stmts {
		switch st.Scope.Kind {
		case ScopeAttr, ScopePredicate:
			if err := check(st.Scope.Kind, st.Scope.Name); err != nil {
				return err
			}
		case ScopeAttrSet, ScopePredicateSet:
			for _, m := range st.Scope.Members {
				if err := check(st.Scope.Kind, m); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// validateLayer 校验单层规则内部不存在冲突或合并歧义（用于全局默认规则声明）。
func validateLayer(def *ObjectTypeDef, stmts []Statement) error {
	attrs, rows := splitByRow(stmts)
	attrUniverse := append([]string(nil), def.Attrs...)
	predUniverse := make([]string, 0, len(def.Predicates))
	for _, p := range def.Predicates {
		predUniverse = append(predUniverse, p.ID)
	}
	for _, a := range attrUniverse {
		if _, err := resolveLayer(attrs, attrUniverse, a, "default"); err != nil {
			return err
		}
	}
	for _, p := range predUniverse {
		if _, err := resolveLayer(rows, predUniverse, p, "default"); err != nil {
			return err
		}
	}
	return nil
}

// isLoosening 判定一条覆盖声明是否相对当前全局默认规则构成放宽。
func isLoosening(def *ObjectTypeDef, defaults []Statement, st Statement) bool {
	if st.Effect != Allow {
		return false
	}
	defAttrs, defRows := splitByRow(defaults)
	attrUniverse := append([]string(nil), def.Attrs...)
	predUniverse := make([]string, 0, len(def.Predicates))
	for _, p := range def.Predicates {
		predUniverse = append(predUniverse, p.ID)
	}
	var universe []string
	var layer []Statement
	if st.Scope.IsRow() {
		universe, layer = predUniverse, defRows
	} else {
		universe, layer = attrUniverse, defAttrs
	}
	for point := range scopeMembers(st.Scope, universe) {
		r, err := resolveLayer(layer, universe, point, "default")
		if err != nil || !r.found || r.effect == Deny {
			return true
		}
	}
	return false
}

// AddTenant 注册租户命名空间（幂等）。
func (s *Store) AddTenant(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ns := s.st.clone()
	ns.tenants[id] = true
	ns.appendAudit("add-tenant", "tenant="+id, "ok", nil)
	s.st = ns
}

// DefineObjectType 在全局命名空间定义对象类型，可被多个租户共享。
func (s *Store) DefineObjectType(def ObjectTypeDef) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ns := s.st.clone()
	ns.types[def.Name] = def
	ns.appendAudit("define-type", "type="+def.Name, "ok", nil)
	s.st = ns
}

// SetLooseningBasisRequired 声明某对象类型的放宽覆盖是否需要额外声明授权依据。
func (s *Store) SetLooseningBasisRequired(typeName string, required bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.st.types[typeName]; !ok {
		return newError(ErrNotFound, "对象类型 %q 不存在", typeName)
	}
	ns := s.st.clone()
	ns.basisRequired[typeName] = required
	ns.appendAudit("set-basis-required", fmt.Sprintf("type=%s required=%v", typeName, required), "ok", nil)
	s.st = ns
	return nil
}

// PutDefault 原子替换某对象类型在某主体类别上的全局默认规则。
// 未针对变更部分声明覆盖的租户立即感知新默认；已声明覆盖的租户不受影响。
func (s *Store) PutDefault(typeName, class string, stmts []Statement) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	def, ok := s.st.types[typeName]
	if !ok {
		return newError(ErrNotFound, "对象类型 %q 不存在", typeName)
	}
	stmts = cloneStatements(stmts)
	if err := validateScopes(&def, stmts); err != nil {
		return err
	}
	if err := validateLayer(&def, stmts); err != nil {
		return err
	}
	ns := s.st.clone()
	ns.defaults[ruleKey{typ: typeName, class: class}] = stmts
	ns.appendAudit("put-default", fmt.Sprintf("type=%s class=%s stmts=%d", typeName, class, len(stmts)), "ok", nil)
	s.st = ns
	return nil
}

// PutOverride 原子替换某租户对某对象类型在某主体类别上的覆盖规则。
// 放宽声明在系统要求时必须携带授权依据，否则整体拒绝且不影响任何状态与审计。
func (s *Store) PutOverride(tenant, typeName, class string, stmts []Statement) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.st.tenants[tenant] {
		return newError(ErrNotFound, "租户 %q 不存在", tenant)
	}
	def, ok := s.st.types[typeName]
	if !ok {
		return newError(ErrNotFound, "对象类型 %q 不存在", typeName)
	}
	stmts = cloneStatements(stmts)
	if err := validateScopes(&def, stmts); err != nil {
		return err
	}
	if s.st.basisRequired[typeName] {
		defaults := s.st.defaults[ruleKey{typ: typeName, class: class}]
		for _, st := range stmts {
			if st.Basis == "" && isLoosening(&def, defaults, st) {
				return newError(ErrMissingBasis,
					"覆盖声明 %q 相对全局默认规则构成放宽，但未声明授权依据", st.ID)
			}
		}
	}
	ns := s.st.clone()
	ns.overrides[overrideKey{tenant: tenant, typ: typeName, class: class}] = stmts
	ns.appendAudit("put-override",
		fmt.Sprintf("tenant=%s type=%s class=%s stmts=%d", tenant, typeName, class, len(stmts)), "ok", nil)
	s.st = ns
	return nil
}

// RevokeOverride 原子撤销某租户对某对象类型在某主体类别上的全部覆盖规则，
// 恢复为沿用全局默认规则。历史审计记录保持不变。
func (s *Store) RevokeOverride(tenant, typeName, class string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.st.tenants[tenant] {
		return newError(ErrNotFound, "租户 %q 不存在", tenant)
	}
	if _, ok := s.st.types[typeName]; !ok {
		return newError(ErrNotFound, "对象类型 %q 不存在", typeName)
	}
	ns := s.st.clone()
	delete(ns.overrides, overrideKey{tenant: tenant, typ: typeName, class: class})
	ns.appendAudit("revoke-override",
		fmt.Sprintf("tenant=%s type=%s class=%s", tenant, typeName, class), "ok", nil)
	s.st = ns
	return nil
}

// Decide 对一次访问请求进行判定。
// 判定依据实例归属租户（req.InstanceTenant）的覆盖规则，
// 与发起访问主体所属租户（req.SubjectTenant）的覆盖规则完全隔离。
func (s *Store) Decide(req AccessRequest) (Decision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.st.tenants[req.SubjectTenant] {
		return Decision{}, newError(ErrNotFound, "租户 %q 不存在", req.SubjectTenant)
	}
	if !s.st.tenants[req.InstanceTenant] {
		return Decision{}, newError(ErrNotFound, "租户 %q 不存在", req.InstanceTenant)
	}
	def, ok := s.st.types[req.Type]
	if !ok {
		return Decision{}, newError(ErrNotFound, "对象类型 %q 不存在", req.Type)
	}
	key := ruleKey{typ: req.Type, class: req.SubjectClass}
	ovrKey := overrideKey{tenant: req.InstanceTenant, typ: req.Type, class: req.SubjectClass}
	dec, err := evaluate(&def, s.st.defaults[key], s.st.overrides[ovrKey], req.Instance)
	if err != nil {
		return Decision{}, err
	}
	ns := s.st.clone()
	ns.appendAudit("decide", formatRequest(req), formatDecision(dec), dec.Trace.Basis)
	s.st = ns
	return dec, nil
}

// Audit 返回审计日志的只读副本。审计记录追加后不可变。
func (s *Store) Audit() []AuditRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]AuditRecord(nil), s.st.audit...)
}

func formatRequest(req AccessRequest) string {
	return fmt.Sprintf("subject=%s/%s type=%s instance=%s/%s",
		req.SubjectTenant, req.SubjectClass, req.Type, req.InstanceTenant, req.Instance.ID)
}

func formatDecision(dec Decision) string {
	var attrs []string
	for name, ok := range dec.Attrs {
		attrs = append(attrs, fmt.Sprintf("%s=%v", name, ok))
	}
	return fmt.Sprintf("row=%v attrs={%s} examined=%d+%d",
		dec.RowAllowed, strings.Join(attrs, ","),
		dec.Trace.DefaultEntriesExamined, dec.Trace.OverrideEntriesExamined)
}
