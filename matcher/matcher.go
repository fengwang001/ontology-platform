// Package matcher 提供带类型登记与按序增臂的模式匹配穷尽性与冗余检查。
package matcher

import (
	"fmt"
	"sync"
)

// 资源上限。
const (
	maxNameBytes        = 32
	maxCtorsPerType     = 8
	maxFieldsPerCtor    = 4
	maxTypes            = 64
	maxSessions         = 1000
	maxArmsPerSession   = 200
	maxOrAlts           = 8
	maxPatDepth         = 16
	maxArmExpansion     = 4096
	maxSessionExpansion = 20000
)

// ErrKind 区分拒绝原因类别。
type ErrKind int

const (
	// ErrInvalidArgument 参数非法（名字、数量、深度、各类上限、无有限值、字段类型未登记等）。
	ErrInvalidArgument ErrKind = iota + 1
	// ErrDuplicateName DefineType 的类型名或构造子名重名。
	ErrDuplicateName
	// ErrNoSuchSession 会话号不存在。
	ErrNoSuchSession
	// ErrInvalidPattern AddArm 的模式校验失败。
	ErrInvalidPattern
	// ErrExpansionLimit 展开行数超限。
	ErrExpansionLimit
)

// Error 是可区分类别的拒绝错误。
type Error struct {
	Kind ErrKind
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

func newError(kind ErrKind, format string, args ...any) *Error {
	return &Error{Kind: kind, Msg: fmt.Sprintf(format, args...)}
}

// PatKind 模式节点类别。
type PatKind int

const (
	WildPat PatKind = iota
	CtorPat
	OrPat
)

// Pat 是未校验的模式语法树。
type Pat struct {
	Kind PatKind
	Name string // CtorPat 的构造子名
	Args []*Pat // CtorPat 的子模式
	Alts []*Pat // OrPat 的分支
}

// W 构造通配模式。
func W() *Pat { return &Pat{Kind: WildPat} }

// C 构造构造子模式。
func C(name string, args ...*Pat) *Pat { return &Pat{Kind: CtorPat, Name: name, Args: args} }

// Or 构造选择模式。
func Or(alts ...*Pat) *Pat { return &Pat{Kind: OrPat, Alts: alts} }

// Ctor 是 DefineType 的构造子声明。
type Ctor struct {
	Name   string
	Fields []string
}

type typeInfo struct {
	name  string
	ctors []*ctorInfo
}

type ctorInfo struct {
	name   string
	parent *typeInfo
	fields []*typeInfo
}

type session struct {
	typ           *typeInfo
	armCount      int
	rows          [][]*node // 全部不带守卫臂展开后的覆盖行
	expandedTotal int
	coverAll      bool // 不带守卫臂的行已覆盖全部值（冗余判定短路）
	cacheValid    bool
	cacheExh      bool
	cacheCE       string
}

// Engine 持有全部类型与会话，方法可并发调用。
type Engine struct {
	mu           sync.Mutex
	types        map[string]*typeInfo
	ctors        map[string]*ctorInfo
	sessions     []*session
	missingCalls int // 非导出计数器：Missing 的实际计算次数（验证 Check 缓存）
}

// NewEngine 返回空引擎。
func NewEngine() *Engine {
	return &Engine{
		types: make(map[string]*typeInfo),
		ctors: make(map[string]*ctorInfo),
	}
}

// DefineType 登记一个代数数据类型。被拒绝时不改变任何状态。
func (e *Engine) DefineType(name string, ctors []Ctor) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	// 第一类：参数非法。
	if len(name) == 0 || len(name) > maxNameBytes {
		return newError(ErrInvalidArgument, "类型名 %q 为空或超过 32 字节", name)
	}
	if len(ctors) < 1 || len(ctors) > maxCtorsPerType {
		return newError(ErrInvalidArgument, "构造子数 %d 不在 1..8", len(ctors))
	}
	for _, c := range ctors {
		if len(c.Name) == 0 || len(c.Name) > maxNameBytes {
			return newError(ErrInvalidArgument, "构造子名 %q 为空或超过 32 字节", c.Name)
		}
		if len(c.Fields) > maxFieldsPerCtor {
			return newError(ErrInvalidArgument, "构造子 %q 字段数 %d 超过 4", c.Name, len(c.Fields))
		}
		for _, f := range c.Fields {
			if f == name {
				continue
			}
			if _, ok := e.types[f]; !ok {
				return newError(ErrInvalidArgument, "字段类型 %q 未登记", f)
			}
		}
	}
	finite := false
	for _, c := range ctors {
		selfRef := false
		for _, f := range c.Fields {
			if f == name {
				selfRef = true
				break
			}
		}
		if !selfRef {
			finite = true
			break
		}
	}
	if !finite {
		return newError(ErrInvalidArgument, "类型 %q 没有有限值：每个构造子都引用自身", name)
	}
	if len(e.types) >= maxTypes {
		return newError(ErrInvalidArgument, "类型数超过 64")
	}

	// 第二类：重名。
	if _, ok := e.types[name]; ok {
		return newError(ErrDuplicateName, "类型名 %q 已登记", name)
	}
	seen := make(map[string]bool, len(ctors))
	for _, c := range ctors {
		if seen[c.Name] {
			return newError(ErrDuplicateName, "构造子名 %q 在本类型内重复", c.Name)
		}
		seen[c.Name] = true
		if _, ok := e.ctors[c.Name]; ok {
			return newError(ErrDuplicateName, "构造子名 %q 已被其他类型使用", c.Name)
		}
	}

	// 提交。
	t := &typeInfo{name: name}
	for _, c := range ctors {
		ci := &ctorInfo{name: c.Name, parent: t}
		for _, f := range c.Fields {
			if f == name {
				ci.fields = append(ci.fields, t)
			} else {
				ci.fields = append(ci.fields, e.types[f])
			}
		}
		t.ctors = append(t.ctors, ci)
	}
	e.types[name] = t
	for _, ci := range t.ctors {
		e.ctors[ci.name] = ci
	}
	return nil
}

// NewSession 在已登记类型上开启会话，返回从 1 递增的会话号。
func (e *Engine) NewSession(typeName string) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, ok := e.types[typeName]
	if !ok {
		return 0, newError(ErrInvalidArgument, "类型 %q 未登记", typeName)
	}
	if len(e.sessions) >= maxSessions {
		return 0, newError(ErrInvalidArgument, "会话数超过 1000")
	}
	e.sessions = append(e.sessions, &session{typ: t})
	return len(e.sessions), nil
}

// shapeDepth 校验模式形状（Or 分支数 1..8、深度不超过 16），返回深度。
// 深度定义：通配为 1；构造子/Or 为 1 + 子模式最大深度。
func shapeDepth(p *Pat) (int, error) {
	if p == nil {
		return 0, newError(ErrInvalidArgument, "模式为空")
	}
	var d int
	switch p.Kind {
	case WildPat:
		d = 1
	case OrPat:
		if len(p.Alts) < 1 || len(p.Alts) > maxOrAlts {
			return 0, newError(ErrInvalidArgument, "Or 分支数 %d 不在 1..8", len(p.Alts))
		}
		for _, a := range p.Alts {
			da, err := shapeDepth(a)
			if err != nil {
				return 0, err
			}
			if da > d {
				d = da
			}
		}
		d++
	case CtorPat:
		for _, a := range p.Args {
			da, err := shapeDepth(a)
			if err != nil {
				return 0, err
			}
			if da > d {
				d = da
			}
		}
		d++
	default:
		return 0, newError(ErrInvalidArgument, "未知的模式类别 %d", p.Kind)
	}
	if d > maxPatDepth {
		return 0, newError(ErrInvalidArgument, "模式深度 %d 超过 16", d)
	}
	return d, nil
}

// resolve 按先序从左到右校验模式并解析构造子。
func (e *Engine) resolve(p *Pat, t *typeInfo) (*rpat, error) {
	switch p.Kind {
	case WildPat:
		return &rpat{kind: WildPat}, nil
	case OrPat:
		rp := &rpat{kind: OrPat, alts: make([]*rpat, 0, len(p.Alts))}
		for _, a := range p.Alts {
			ra, err := e.resolve(a, t)
			if err != nil {
				return nil, err
			}
			rp.alts = append(rp.alts, ra)
		}
		return rp, nil
	default: // CtorPat
		c := e.ctors[p.Name]
		if c == nil {
			return nil, newError(ErrInvalidPattern, "构造子名 %q 不存在", p.Name)
		}
		if c.parent != t {
			return nil, newError(ErrInvalidPattern, "构造子 %q 不属于期望类型 %q", p.Name, t.name)
		}
		if len(p.Args) != len(c.fields) {
			return nil, newError(ErrInvalidPattern, "构造子 %q 需要 %d 个子模式，实际 %d 个", p.Name, len(c.fields), len(p.Args))
		}
		rp := &rpat{kind: CtorPat, ctor: c, args: make([]*rpat, 0, len(p.Args))}
		for i, a := range p.Args {
			ra, err := e.resolve(a, c.fields[i])
			if err != nil {
				return nil, err
			}
			rp.args = append(rp.args, ra)
		}
		return rp, nil
	}
}

// AddArm 把一条匹配臂追加到会话末尾，返回臂序号（从 1 起）、
// 每个顶层分支是否冗余、以及整条臂是否冗余。
func (e *Engine) AddArm(sessionID int, p *Pat, guarded bool) (int, []bool, bool, error) {
	// 第一类：参数非法（不依赖会话的部分先查）。
	if _, err := shapeDepth(p); err != nil {
		return 0, nil, false, err
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	// 第三类：会话号不存在。
	if sessionID < 1 || sessionID > len(e.sessions) {
		return 0, nil, false, newError(ErrNoSuchSession, "会话号 %d 不存在", sessionID)
	}
	sess := e.sessions[sessionID-1]
	if sess.armCount >= maxArmsPerSession {
		return 0, nil, false, newError(ErrInvalidArgument, "会话 %d 的臂数超过 200", sessionID)
	}

	// 第四类：模式校验。
	rp, err := e.resolve(p, sess.typ)
	if err != nil {
		return 0, nil, false, err
	}

	// 第五类：展开行数超限。
	cnt := expCount(rp)
	if cnt > maxArmExpansion {
		return 0, nil, false, newError(ErrExpansionLimit, "模式展开数 %d 超过 4096", cnt)
	}
	if sess.expandedTotal+cnt > maxSessionExpansion {
		return 0, nil, false, newError(ErrExpansionLimit, "会话累计展开行数超过 20000")
	}

	// 顶层分支列表：顶层 Or 的各分支，否则只有模式本身。
	var branches []*rpat
	if rp.kind == OrPat {
		branches = rp.alts
	} else {
		branches = []*rpat{rp}
	}

	// 逐分支判定冗余：覆盖集 = 此前不带守卫臂的行 + 本臂更前分支的行。
	results := make([]bool, len(branches))
	allRedundant := true
	var armRows [][]*node
	var earlier [][]*node
	if !sess.coverAll && covered(sess.rows, []*node{wildNode}, []*typeInfo{sess.typ}) {
		sess.coverAll = true
	}
	if sess.coverAll {
		// 既有覆盖已穷尽，任何分支都冗余。
		for i := range results {
			results[i] = true
		}
		for _, b := range branches {
			for _, r := range expandPat(b) {
				armRows = append(armRows, []*node{r})
			}
		}
	} else {
		for i, b := range branches {
			branchRows := expandPat(b)
			cov := make([][]*node, 0, len(sess.rows)+len(earlier))
			cov = append(cov, sess.rows...)
			cov = append(cov, earlier...)
			redundant := true
			for _, r := range branchRows {
				if !covered(cov, []*node{r}, []*typeInfo{sess.typ}) {
					redundant = false
					break
				}
			}
			results[i] = redundant
			if !redundant {
				allRedundant = false
			}
			for _, r := range branchRows {
				row := []*node{r}
				earlier = append(earlier, row)
				armRows = append(armRows, row)
			}
		}
	}

	// 提交。
	sess.armCount++
	sess.expandedTotal += cnt
	if !guarded {
		sess.rows = append(sess.rows, armRows...)
		if !allRedundant {
			// 只有含非冗余分支的不带守卫臂才改变覆盖，使缓存失效。
			sess.cacheValid = false
		}
	}
	return sess.armCount, results, allRedundant, nil
}

// Check 返回会话是否穷尽；不穷尽时返回规范反例文本。
func (e *Engine) Check(sessionID int) (bool, string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if sessionID < 1 || sessionID > len(e.sessions) {
		return false, "", newError(ErrNoSuchSession, "会话号 %d 不存在", sessionID)
	}
	sess := e.sessions[sessionID-1]
	if sess.cacheValid {
		return sess.cacheExh, sess.cacheCE, nil
	}
	e.missingCalls++
	ce, found := missing(sess.rows, []*typeInfo{sess.typ})
	sess.cacheExh = !found
	sess.cacheCE = ""
	if found {
		sess.cacheCE = renderNode(ce[0])
	}
	sess.cacheValid = true
	return sess.cacheExh, sess.cacheCE, nil
}
