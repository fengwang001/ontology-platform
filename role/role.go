package role

import (
	"errors"
	"fmt"
	"sync"
)

// Value 为 int64 或 string。
type Value any

// ExprKind 标识表达式种类。
type ExprKind int

const (
	KindTerm ExprKind = iota + 1
	KindRange
	KindAnd
	KindOr
	KindNot
)

// Expr 为过滤/查询共用表达式。
type Expr struct {
	Kind  ExprKind
	Field string
	Value Value
	Lo    int64
	Hi    int64
	Args  []Expr
}

// 名称、id、字段名均为 1 到 64 字节；表达式最大深度与子式数。
const (
	maxNameLen = 64
	maxEntries = 16
	maxDepth   = 8
	maxArgs    = 8
)

// Term 构造字段等值表达式。
func Term(field string, v Value) Expr {
	return Expr{Kind: KindTerm, Field: field, Value: v}
}

// Range 构造闭区间表达式（仅 int64）。
func Range(field string, lo, hi int64) Expr {
	return Expr{Kind: KindRange, Field: field, Lo: lo, Hi: hi}
}

// And 构造合取（1–8 个子式）。
func And(args ...Expr) Expr { return Expr{Kind: KindAnd, Args: args} }

// Or 构造析取（1–8 个子式）。
func Or(args ...Expr) Expr { return Expr{Kind: KindOr, Args: args} }

// Not 构造取反。
func Not(arg Expr) Expr { return Expr{Kind: KindNot, Args: []Expr{arg}} }

// FieldAuth 为字段授权：Unrestricted 为无限制。
type FieldAuth struct {
	Unrestricted bool
	Grant        []string
	Except       []string
}

// Entry 为一条授权条目。
type Entry struct {
	IndexPattern string
	Filter       *Expr
	Fields       FieldAuth
}

// Role 为角色（1–16 条）。
type Role struct {
	Name    string
	Entries []Entry
}

// 哨兵错误。
var (
	ErrInvalid     = errors.New("invalid argument")
	ErrNoUser      = errors.New("user not found")
	ErrNoRole      = errors.New("role not found")
	ErrRoleInUse   = errors.New("role in use")
	ErrNoPerm      = errors.New("permission denied")
	ErrDocNotFound = errors.New("document not found")
)

// Store 管理角色与用户绑定。
type Store struct {
	mu        sync.RWMutex
	roles     map[string]Role
	userRoles map[string][]string
}

// NewStore 创建角色存储。
func NewStore() *Store {
	return &Store{roles: map[string]Role{}, userRoles: map[string][]string{}}
}

// PutRole 整体替换角色。
func (s *Store) PutRole(name string, entries []Entry) error {
	if !validName(name) {
		return invalidf("role name")
	}
	if len(entries) < 1 || len(entries) > maxEntries {
		return invalidf("role entries count")
	}
	for i := range entries {
		if err := entries[i].validate(); err != nil {
			return err
		}
	}
	copied := cloneEntries(entries)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.roles[name] = Role{Name: name, Entries: copied}
	return nil
}

// DeleteRole 删除未被绑定的角色；角色不存在报 ErrNoRole，仍被绑定报 ErrRoleInUse。
func (s *Store) DeleteRole(name string) error {
	if !validName(name) {
		return invalidf("role name")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.roles[name]; !ok {
		return ErrNoRole
	}
	for _, bound := range s.userRoles {
		for _, r := range bound {
			if r == name {
				return ErrRoleInUse
			}
		}
	}
	delete(s.roles, name)
	return nil
}

// BindUser 整体替换用户角色集（1–16 个，不得重复，角色须存在）。
func (s *Store) BindUser(user string, roles []string) error {
	if !validName(user) {
		return invalidf("user name")
	}
	if len(roles) < 1 || len(roles) > maxEntries {
		return invalidf("user roles count")
	}
	seen := make(map[string]struct{}, len(roles))
	for _, r := range roles {
		if !validName(r) {
			return invalidf("role name")
		}
		if _, dup := seen[r]; dup {
			return invalidf("duplicate role binding")
		}
		seen[r] = struct{}{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range roles {
		if _, ok := s.roles[r]; !ok {
			return ErrNoRole
		}
	}
	bound := append([]string(nil), roles...)
	s.userRoles[user] = bound
	return nil
}

// UnbindUser 删除用户绑定；不存在时为空操作。
func (s *Store) UnbindUser(user string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.userRoles, user)
}

// UserEntries 返回用户各角色的条目快照及触碰条目数。
// 用户不存在返回 ErrNoUser；只遍历该用户绑定的角色，与全局角色数无关。
func (s *Store) UserEntries(user string) ([][]Entry, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	names, ok := s.userRoles[user]
	if !ok {
		return nil, 0, ErrNoUser
	}
	result := make([][]Entry, 0, len(names))
	touched := 0
	for _, name := range names {
		role, ok := s.roles[name]
		if !ok {
			continue
		}
		touched += len(role.Entries)
		result = append(result, cloneEntries(role.Entries))
	}
	return result, touched, nil
}

func (e Entry) validate() error {
	if !validPattern(e.IndexPattern) {
		return invalidf("index pattern")
	}
	if e.Filter != nil {
		if err := validateExpr(*e.Filter, 1); err != nil {
			return err
		}
	}
	f := e.Fields
	if f.Unrestricted {
		return nil
	}
	if len(f.Grant) < 1 || len(f.Grant) > maxEntries {
		return invalidf("field grant list")
	}
	if len(f.Except) > maxEntries {
		return invalidf("field except list")
	}
	for _, p := range f.Grant {
		if !validPattern(p) {
			return invalidf("field grant pattern")
		}
	}
	for _, p := range f.Except {
		if !validPattern(p) {
			return invalidf("field except pattern")
		}
	}
	return nil
}

func validateExpr(e Expr, depth int) error {
	if depth > maxDepth {
		return invalidf("expression depth")
	}
	if !validName(e.Field) && e.Kind != KindAnd && e.Kind != KindOr && e.Kind != KindNot {
		return invalidf("field name")
	}
	switch e.Kind {
	case KindTerm:
		if !validScalar(e.Value) {
			return invalidf("term value")
		}
	case KindRange:
		if e.Lo > e.Hi {
			return invalidf("range bounds")
		}
	case KindAnd, KindOr:
		if len(e.Args) < 1 || len(e.Args) > maxArgs {
			return invalidf("boolean args count")
		}
		for _, sub := range e.Args {
			if err := validateExpr(sub, depth+1); err != nil {
				return err
			}
		}
	case KindNot:
		if len(e.Args) != 1 {
			return invalidf("not args count")
		}
		if err := validateExpr(e.Args[0], depth+1); err != nil {
			return err
		}
	default:
		return invalidf("expression kind")
	}
	return nil
}

// ValidName 判断名字、id、字段名是否满足 1 到 64 字节。
func ValidName(s string) bool { return validName(s) }

// ValidateExpr 校验表达式（深度 ≤ 8，子式 1–8，值为 int64/string）。
func ValidateExpr(e Expr) error { return validateExpr(e, 1) }

// ValidValue 判断值是否为 int64 或 string。
func ValidValue(v Value) bool { return validScalar(v) }

// Eval 在一份完整文档上求值表达式。
// 文档缺该字段或值类型不符时 Term/Range 为假（Not 因此为真）。
func Eval(e Expr, doc map[string]Value) bool {
	switch e.Kind {
	case KindTerm:
		got, ok := doc[e.Field]
		if !ok {
			return false
		}
		return equalValue(got, e.Value)
	case KindRange:
		got, ok := doc[e.Field]
		if !ok {
			return false
		}
		n, ok := got.(int64)
		if !ok {
			return false
		}
		return n >= e.Lo && n <= e.Hi
	case KindAnd:
		for _, sub := range e.Args {
			if !Eval(sub, doc) {
				return false
			}
		}
		return true
	case KindOr:
		for _, sub := range e.Args {
			if Eval(sub, doc) {
				return true
			}
		}
		return false
	case KindNot:
		return !Eval(e.Args[0], doc)
	default:
		return false
	}
}

// MatchPattern 判断名称是否匹配模式：精确名或以单个 * 结尾的前缀，"*" 匹配全部。
func MatchPattern(pattern, name string) bool {
	if pattern == "*" {
		return true
	}
	if len(pattern) > 0 && pattern[len(pattern)-1] == '*' {
		prefix := pattern[:len(pattern)-1]
		return len(name) >= len(prefix) && name[:len(prefix)] == prefix
	}
	return pattern == name
}

// FieldVisible 判断字段授权下某字段是否可见。
func (f FieldAuth) FieldVisible(field string) bool {
	if f.Unrestricted {
		return true
	}
	granted := false
	for _, p := range f.Grant {
		if MatchPattern(p, field) {
			granted = true
			break
		}
	}
	if !granted {
		return false
	}
	for _, p := range f.Except {
		if MatchPattern(p, field) {
			return false
		}
	}
	return true
}

func validName(s string) bool {
	return len(s) >= 1 && len(s) <= maxNameLen
}

func validPattern(s string) bool {
	if !validName(s) {
		return false
	}
	star := -1
	for i := 0; i < len(s); i++ {
		if s[i] == '*' {
			if star >= 0 {
				return false
			}
			star = i
		}
	}
	return star == -1 || star == len(s)-1
}

func validScalar(v Value) bool {
	switch v.(type) {
	case int64, string:
		return true
	default:
		return false
	}
}

func equalValue(a, b Value) bool {
	// 类型必须一致；int 与 string 不可比较。
	switch av := a.(type) {
	case int64:
		bv, ok := b.(int64)
		return ok && av == bv
	case string:
		bv, ok := b.(string)
		return ok && av == bv
	default:
		return false
	}
}

func cloneEntries(in []Entry) []Entry {
	out := make([]Entry, len(in))
	copy(out, in)
	return out
}

func invalidf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}
