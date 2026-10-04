package role

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

// Value 是文档字段值：仅允许 int64 或 string。
type Value = any

// 错误类别，跨包包装后仍可用 errors.Is 判定。
var (
	ErrInvalid      = errors.New("invalid argument")
	ErrRoleNotFound = errors.New("role not found")
	ErrRoleInUse    = errors.New("role in use")
	ErrUserNotFound = errors.New("user not found")
)

const (
	maxNameBytes = 64
	maxEntries   = 16
	maxRoles     = 16
	maxChildren  = 8
	maxDepth     = 8
)

func validName(s string) bool {
	return len(s) >= 1 && len(s) <= maxNameBytes
}

// validPattern 要求长度 1..64 字节；要么是精确名（不含星号），
// 要么是唯一一个星号结尾的前缀通配（"*" 匹配全部）。
func validPattern(p string) bool {
	if !validName(p) {
		return false
	}
	star := strings.IndexByte(p, '*')
	switch {
	case star < 0:
		return true
	case star != len(p)-1:
		return false
	default:
		return strings.IndexByte(p[:star], '*') < 0
	}
}

// MatchPattern 判定名字是否匹配模式。
func MatchPattern(pattern, name string) bool {
	if pattern == "*" {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(name, pattern[:len(pattern)-1])
	}
	return pattern == name
}

// Op 标识表达式节点类型。
type Op int

const (
	OpTerm Op = iota + 1
	OpRange
	OpAnd
	OpOr
	OpNot
)

// Expr 是过滤与查询共用的表达式。
// Term: Field + Value；Range: Field + Lo/Hi；
// And/Or: Children；Not: Children[0]。
type Expr struct {
	Op       Op
	Field    string
	Value    Value
	Lo       int64
	Hi       int64
	Children []Expr
}

func exprDepth(e Expr) int {
	d := 1
	for _, c := range e.Children {
		if cd := exprDepth(c) + 1; cd > d {
			d = cd
		}
	}
	return d
}

// ValidateExpr 校验表达式：字段名合法、值类型合法、子式个数 1..8、
// Not 恰一个子式、深度不超过 8、Range 两端 int64 且 lo<=hi。
func ValidateExpr(e Expr) error {
	if exprDepth(e) > maxDepth {
		return fmt.Errorf("%w: expr depth exceeds %d", ErrInvalid, maxDepth)
	}
	switch e.Op {
	case OpTerm:
		if !validName(e.Field) {
			return fmt.Errorf("%w: term field name", ErrInvalid)
		}
		switch e.Value.(type) {
		case int64, string:
		default:
			return fmt.Errorf("%w: term value must be int64 or string", ErrInvalid)
		}
	case OpRange:
		if !validName(e.Field) {
			return fmt.Errorf("%w: range field name", ErrInvalid)
		}
		if e.Lo > e.Hi {
			return fmt.Errorf("%w: range lo > hi", ErrInvalid)
		}
	case OpAnd, OpOr:
		if len(e.Children) < 1 || len(e.Children) > maxChildren {
			return fmt.Errorf("%w: and/or needs 1..8 children", ErrInvalid)
		}
		for _, c := range e.Children {
			if err := ValidateExpr(c); err != nil {
				return err
			}
		}
	case OpNot:
		if len(e.Children) != 1 {
			return fmt.Errorf("%w: not needs exactly 1 child", ErrInvalid)
		}
		if err := ValidateExpr(e.Children[0]); err != nil {
			return err
		}
	default:
		return fmt.Errorf("%w: unknown expr op", ErrInvalid)
	}
	return nil
}

// Eval 在一份字段映射上求值。缺字段或类型不符时 Term/Range 为假，
// 因此 Not 为真——裁剪后的视图直接复用同一语义。
func Eval(e Expr, fields map[string]Value) bool {
	switch e.Op {
	case OpTerm:
		v, ok := fields[e.Field]
		return ok && v == e.Value
	case OpRange:
		v, ok := fields[e.Field].(int64)
		return ok && v >= e.Lo && v <= e.Hi
	case OpAnd:
		for _, c := range e.Children {
			if !Eval(c, fields) {
				return false
			}
		}
		return true
	case OpOr:
		for _, c := range e.Children {
			if Eval(c, fields) {
				return true
			}
		}
		return false
	case OpNot:
		return !Eval(e.Children[0], fields)
	default:
		return false
	}
}

// Term/Range/And/Or/Not 为构造助手，非法输入返回包装了 ErrInvalid 的错误。

func Term(field string, value Value) (Expr, error) {
	e := Expr{Op: OpTerm, Field: field, Value: value}
	return e, ValidateExpr(e)
}

func Range(field string, lo, hi int64) (Expr, error) {
	e := Expr{Op: OpRange, Field: field, Lo: lo, Hi: hi}
	return e, ValidateExpr(e)
}

func And(children ...Expr) (Expr, error) {
	e := Expr{Op: OpAnd, Children: children}
	return e, ValidateExpr(e)
}

func Or(children ...Expr) (Expr, error) {
	e := Expr{Op: OpOr, Children: children}
	return e, ValidateExpr(e)
}

func Not(child Expr) (Expr, error) {
	e := Expr{Op: OpNot, Children: []Expr{child}}
	return e, ValidateExpr(e)
}

// FieldAuth 是单条目的字段授权。Unrestricted 为真时无限制；
// 否则可见字段 = 匹配 Grant 且不匹配 Except 者，Grant 至少 1 条。
type FieldAuth struct {
	Unrestricted bool
	Grant        []string
	Except       []string
}

func (fa FieldAuth) validate() error {
	if fa.Unrestricted {
		return nil
	}
	if len(fa.Grant) < 1 {
		return fmt.Errorf("%w: field grant needs at least 1 pattern", ErrInvalid)
	}
	for _, p := range fa.Grant {
		if !validPattern(p) {
			return fmt.Errorf("%w: bad grant pattern %q", ErrInvalid, p)
		}
	}
	for _, p := range fa.Except {
		if !validPattern(p) {
			return fmt.Errorf("%w: bad except pattern %q", ErrInvalid, p)
		}
	}
	return nil
}

// allowsField 判定本条目是否授权单个字段名。
func (fa FieldAuth) AllowsField(name string) bool {
	if fa.Unrestricted {
		return true
	}
	granted := false
	for _, p := range fa.Grant {
		if MatchPattern(p, name) {
			granted = true
			break
		}
	}
	if !granted {
		return false
	}
	for _, p := range fa.Except {
		if MatchPattern(p, name) {
			return false
		}
	}
	return true
}

// Entry 是一条授权：索引模式、可空 filter（nil 即文档无限制）、字段授权。
type Entry struct {
	Pattern string
	Filter  *Expr
	Fields  FieldAuth
}

func (en Entry) validate() error {
	if !validPattern(en.Pattern) {
		return fmt.Errorf("%w: bad index pattern %q", ErrInvalid, en.Pattern)
	}
	if en.Filter != nil {
		if err := ValidateExpr(*en.Filter); err != nil {
			return err
		}
	}
	return en.Fields.validate()
}

type roleDef struct {
	entries []Entry
}

// Registry 保存角色与用户绑定，所有方法可并发调用。
type Registry struct {
	mu    sync.RWMutex
	roles map[string]*roleDef
	users map[string][]string
}

func NewRegistry() *Registry {
	return &Registry{
		roles: map[string]*roleDef{},
		users: map[string][]string{},
	}
}

func cloneEntries(entries []Entry) []Entry {
	cp := make([]Entry, len(entries))
	copy(cp, entries)
	return cp
}

// PutRole 整体替换角色。
func (r *Registry) PutRole(name string, entries []Entry) error {
	if !validName(name) {
		return fmt.Errorf("%w: role name", ErrInvalid)
	}
	if len(entries) < 1 || len(entries) > maxEntries {
		return fmt.Errorf("%w: role needs 1..16 entries", ErrInvalid)
	}
	for _, en := range entries {
		if err := en.validate(); err != nil {
			return err
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.roles[name] = &roleDef{entries: cloneEntries(entries)}
	return nil
}

// DeleteRole 删除角色；角色不存在报 ErrRoleNotFound，仍被绑定报 ErrRoleInUse。
func (r *Registry) DeleteRole(name string) error {
	if !validName(name) {
		return fmt.Errorf("%w: role name", ErrInvalid)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.roles[name]; !ok {
		return fmt.Errorf("%w: %s", ErrRoleNotFound, name)
	}
	for _, bound := range r.users {
		for _, rn := range bound {
			if rn == name {
				return fmt.Errorf("%w: %s", ErrRoleInUse, name)
			}
		}
	}
	delete(r.roles, name)
	return nil
}

// BindUser 整体替换用户的角色集（1..16 个、不重复、必须存在）。
func (r *Registry) BindUser(user string, roles []string) error {
	if !validName(user) {
		return fmt.Errorf("%w: user name", ErrInvalid)
	}
	if len(roles) < 1 || len(roles) > maxRoles {
		return fmt.Errorf("%w: user needs 1..16 roles", ErrInvalid)
	}
	seen := make(map[string]struct{}, len(roles))
	for _, rn := range roles {
		if !validName(rn) {
			return fmt.Errorf("%w: role name in binding", ErrInvalid)
		}
		if _, dup := seen[rn]; dup {
			return fmt.Errorf("%w: duplicate role %s", ErrInvalid, rn)
		}
		seen[rn] = struct{}{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, rn := range roles {
		if _, ok := r.roles[rn]; !ok {
			return fmt.Errorf("%w: %s", ErrRoleNotFound, rn)
		}
	}
	bound := make([]string, len(roles))
	copy(bound, roles)
	r.users[user] = bound
	return nil
}

// userSnapshot 返回某用户绑定角色名的拷贝快照；不存在时 ok=false。
func (r *Registry) userSnapshot(user string) ([]string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	bound, ok := r.users[user]
	if !ok {
		return nil, false
	}
	cp := make([]string, len(bound))
	copy(cp, bound)
	return cp, true
}

// roleEntries 返回某角色条目的拷贝快照。
func (r *Registry) roleEntries(name string) ([]Entry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	def, ok := r.roles[name]
	if !ok {
		return nil, false
	}
	return cloneEntries(def.entries), true
}

// UserBound 返回用户绑定角色名的拷贝快照；用户不存在时 ok=false。
func (r *Registry) UserBound(user string) ([]string, bool) {
	return r.userSnapshot(user)
}

// RoleEntries 返回角色条目的拷贝快照。
func (r *Registry) RoleEntries(name string) ([]Entry, bool) {
	return r.roleEntries(name)
}
