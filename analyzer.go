package ontology

import (
	"context"
	"fmt"
	"sort"
)

// PointStatus 描述查询点的可达性。
type PointStatus int

const (
	PointReachable PointStatus = iota
	PointUnreachable
)

// PointResult 是一个程序点上一个变量的窄化结果。
type PointResult struct {
	Status PointStatus
	Type   *Type
}

// Result 是一次成功分析产生的不可变结果。
//
// 内部为“程序点 ID -> 每变量规范化窄化类型”的扁平快照，构建完成后不再写入；
// 因此可被任意多个调用方并发查询。Query 的代价是一次 map 查询加上对“被查询
// 变量窄化类型规模”线性的公开类型还原，不随程序语句总数增长，也不重新分析。
type Result struct {
	declared map[string]*normType
	entry    map[string]env
	vars     []string
}

type analyzer struct {
	prog  *Program
	decls map[string]*normType
	vars  []string
	entry map[string]env
	errs  pendingError
	order int
	ids   map[string]bool
}

// validateStatic 只检查与控制流无关的参数非法：程序形状、ID 唯一性、
// 声明类型畸形（含对象重复属性）、条件结构与字面量形状、变量是否声明。
func (a *analyzer) validateStatic() *AnalysisError {
	p := a.prog
	if p == nil {
		return &AnalysisError{Kind: ErrInvalidArgument, Detail: "程序为 nil"}
	}
	if p.Decls == nil {
		return &AnalysisError{Kind: ErrInvalidArgument, Detail: "缺少变量声明映射"}
	}
	declOrder := make([]string, 0, len(p.Decls))
	for name := range p.Decls {
		declOrder = append(declOrder, name)
	}
	sort.Strings(declOrder)
	for _, name := range declOrder {
		n, err := normalize(p.Decls[name])
		if err != nil {
			return &AnalysisError{Kind: ErrInvalidArgument, Var: name, Detail: "声明类型畸形: " + err.Error()}
		}
		a.decls[name] = n
		a.vars = append(a.vars, name)
	}

	a.walkStmts(p.Stmts, func(s *Stmt) {
		if s == nil {
			a.recordInvalid(a.nextOrder(), "", "", "语句为 nil")
			return
		}
		if s.ID == "" {
			a.recordInvalid(a.nextOrder(), "", "", "语句缺少唯一标识")
			return
		}
		if a.ids[s.ID] {
			a.recordInvalid(a.nextOrder(), s.ID, "", "语句标识重复")
			return
		}
		a.ids[s.ID] = true

		switch s.K {
		case StmtAssign:
			if s.Var == "" {
				a.recordInvalid(a.nextOrder(), s.ID, "", "赋值缺少变量名")
			} else if _, ok := a.decls[s.Var]; !ok {
				a.recordInvalid(a.nextOrder(), s.ID, s.Var, "赋值引用未声明变量")
			}
			if s.Value == nil {
				a.recordInvalid(a.nextOrder(), s.ID, s.Var, "赋值缺少类型")
			} else if _, err := normalize(s.Value); err != nil {
				a.recordInvalid(a.nextOrder(), s.ID, s.Var, "赋值类型畸形: "+err.Error())
			}
		case StmtReturn:
			// 返回语句无额外字段要求
		case StmtIf:
			if s.Cond == nil {
				a.recordInvalid(a.nextOrder(), s.ID, "", "条件分支缺少条件")
			} else {
				a.validateCondStatic(s, s.Cond)
			}
		default:
			a.recordInvalid(a.nextOrder(), s.ID, "", fmt.Sprintf("未知语句形态 %d", s.K))
		}
	})

	return a.errs.err
}

func (a *analyzer) validateCondStatic(s *Stmt, c *Cond) {
	switch c.K {
	case CondIsType, CondEqNull, CondEqUndefined, CondLooseNull, CondTruthy:
		a.requireDeclared(s, c.Var)
	case CondEqLiteral:
		a.requireDeclared(s, c.Var)
		if c.Lit == nil {
			a.recordInvalid(a.nextOrder(), s.ID, c.Var, "字面量相等条件缺少字面量")
			return
		}
		n, err := normalize(c.Lit)
		if err != nil {
			a.recordInvalid(a.nextOrder(), s.ID, c.Var, "字面量类型畸形: "+err.Error())
			return
		}
		if !isSingleLiteral(n) {
			a.recordInvalid(a.nextOrder(), s.ID, c.Var, "严格相等的右值必须是单一字面量类型")
		}
	case CondDiscrim:
		a.requireDeclared(s, c.Var)
		if c.Prop == "" {
			a.recordInvalid(a.nextOrder(), s.ID, c.Var, "判别属性条件缺少属性名")
		}
		if c.Lit == nil {
			a.recordInvalid(a.nextOrder(), s.ID, c.Var, "判别属性条件缺少字面量")
			return
		}
		n, err := normalize(c.Lit)
		if err != nil {
			a.recordInvalid(a.nextOrder(), s.ID, c.Var, "判别字面量类型畸形: "+err.Error())
			return
		}
		if !isSingleLiteral(n) {
			a.recordInvalid(a.nextOrder(), s.ID, c.Var, "判别属性比较值必须是单一字面量类型")
		}
	case CondNot:
		if c.Inner == nil {
			a.recordInvalid(a.nextOrder(), s.ID, c.Var, "取反条件缺少内部条件")
		} else {
			a.validateCondStatic(s, c.Inner)
		}
	case CondAnd, CondOr:
		if c.Left == nil || c.Right == nil {
			a.recordInvalid(a.nextOrder(), s.ID, c.Var, "逻辑条件缺少操作数")
		} else {
			a.validateCondStatic(s, c.Left)
			a.validateCondStatic(s, c.Right)
		}
	default:
		a.recordInvalid(a.nextOrder(), s.ID, c.Var, fmt.Sprintf("未知条件形态 %d", c.K))
	}
}

func (a *analyzer) requireDeclared(s *Stmt, variable string) {
	if variable == "" {
		a.recordInvalid(a.nextOrder(), s.ID, "", "条件缺少变量名")
		return
	}
	if _, ok := a.decls[variable]; !ok {
		a.recordInvalid(a.nextOrder(), s.ID, variable, "条件引用未声明变量")
	}
}

func isSingleLiteral(n *normType) bool {
	if len(n.members) != 1 {
		return false
	}
	switch n.members[0].kind {
	case lkNumLit, lkStrLit, lkTrue, lkFalse, lkNull, lkUndefined:
		return true
	}
	return false
}

func (a *analyzer) recordInvalid(order int, stmtID, variable, detail string) {
	a.errs.record(ErrInvalidArgument, order, stmtID, variable, detail)
}

func (a *analyzer) nextOrder() int {
	a.order++
	return a.order
}

// walkStmts 按程序次序（深度优先、先本句后分支）遍历全部语句。
func (a *analyzer) walkStmts(stmts []*Stmt, f func(*Stmt)) {
	var walk func([]*Stmt)
	walk = func(ss []*Stmt) {
		for _, s := range ss {
			f(s)
			if s != nil && s.K == StmtIf {
				walk(s.Then)
				walk(s.Else)
			}
		}
	}
	walk(stmts)
}

// execBlock 顺序执行语句块，返回块出口环境；nil 表示块所有路径都已终止。
func (a *analyzer) execBlock(stmts []*Stmt, incoming env) env {
	cur := incoming
	for _, s := range stmts {
		if cur == nil {
			// 后续语句在所有路径上不可达：逐一显式登记，仍可被查询区分。
			a.markUnreachable(stmts[indexOf(stmts, s):])
			break
		}
		cur = a.execStmt(s, cur)
	}
	return cur
}

func indexOf(stmts []*Stmt, target *Stmt) int {
	for i, s := range stmts {
		if s == target {
			return i
		}
	}
	return len(stmts)
}

// markUnreachable 为以不可达环境进入的语句登记 nil 快照（不执行其分支体）。
// 分支体语句同样不可达，需要递归标记，使每个查询点都有确定答复。
func (a *analyzer) markUnreachable(stmts []*Stmt) {
	var walk func([]*Stmt)
	walk = func(ss []*Stmt) {
		for _, s := range ss {
			if s == nil {
				continue
			}
			a.entry[s.ID] = nil
			if s.K == StmtIf {
				walk(s.Then)
				walk(s.Else)
			}
		}
	}
	walk(stmts)
}

func (a *analyzer) execStmt(s *Stmt, cur env) env {
	a.entry[s.ID] = cloneEnv(cur)

	switch s.K {
	case StmtReturn:
		return nil

	case StmtAssign:
		valueN, _ := normalize(s.Value)
		declN := a.decls[s.Var]
		if !assignable(valueN, declN) {
			a.errs.record(ErrNotAssignable, a.nextOrder(), s.ID, s.Var,
				fmt.Sprintf("所赋类型 %s 含有不属于声明类型 %s 的成员", normKey(valueN), normKey(declN)))
		}
		return updateVar(cur, s.Var, valueN)

	case StmtIf:
		te, fe := splitEnv(cur, s.Cond)
		a.checkCondFlow(s, s.Cond, cur)

		var thenOut, elseOut env
		if te == nil {
			a.markUnreachable(s.Then)
		}
		if fe == nil {
			a.markUnreachable(s.Else)
		}
		if te != nil && len(s.Then) > 0 {
			thenOut = a.execBlock(s.Then, te)
		} else {
			thenOut = te
		}
		if fe != nil && len(s.Else) > 0 {
			elseOut = a.execBlock(s.Else, fe)
		} else {
			elseOut = fe
		}
		return unionEnv(thenOut, elseOut)
	}
	return cur
}

// checkCondFlow 依据入口窄化类型判定不可访问属性/缺少判别属性。
// 逻辑操作按从左到右短路，右侧只在其实际被求值的环境下检查。
func (a *analyzer) checkCondFlow(s *Stmt, c *Cond, cur env) {
	switch c.K {
	case CondDiscrim:
		n := a.typeAt(cur, c.Var)
		hasNonObject := false
		for _, lf := range n.members {
			if lf.kind != lkObject {
				hasNonObject = true
				break
			}
		}
		if hasNonObject {
			a.errs.record(ErrPropertyNotAccessible, a.nextOrder(), s.ID, c.Var,
				"判别属性访问要求所有成员都是对象，存在非对象成员")
			return
		}
		for _, lf := range n.members {
			if _, ok := lf.props[c.Prop]; !ok {
				a.errs.record(ErrMissingDiscriminant, a.nextOrder(), s.ID, c.Var,
					fmt.Sprintf("存在缺少判别属性 %q 的对象成员", c.Prop))
				return
			}
		}
	case CondNot:
		a.checkCondFlow(s, c.Inner, cur)
	case CondAnd:
		a.checkCondFlow(s, c.Left, cur)
		lt, _ := splitEnv(cur, c.Left)
		if lt != nil {
			a.checkCondFlow(s, c.Right, lt)
		}
	case CondOr:
		a.checkCondFlow(s, c.Left, cur)
		_, lf := splitEnv(cur, c.Left)
		if lf != nil {
			a.checkCondFlow(s, c.Right, lf)
		}
	}
}

func (a *analyzer) typeAt(cur env, variable string) *normType {
	if cur != nil {
		if n, ok := cur[variable]; ok {
			return n
		}
	}
	return a.decls[variable]
}

// Analyze 对程序做一次完整窄化分析。任何错误都使整次分析失败，
// 只报告“优先级最高、同类中程序次序最先”的一处，不产生部分结果。
func Analyze(ctx context.Context, p *Program) (*Result, *AnalysisError) {
	if err := ctx.Err(); err != nil {
		return nil, &AnalysisError{Kind: ErrInvalidArgument, Detail: err.Error()}
	}
	a := &analyzer{
		prog:  p,
		decls: map[string]*normType{},
		entry: map[string]env{},
		ids:   map[string]bool{},
	}
	if e := a.validateStatic(); e != nil {
		return nil, e
	}

	init := make(env, len(a.vars))
	for _, name := range a.vars {
		init[name] = a.decls[name]
	}
	a.execBlock(p.Stmts, init)

	if a.errs.err != nil {
		return nil, a.errs.err
	}
	return &Result{declared: a.decls, entry: a.entry, vars: append([]string(nil), a.vars...)}, nil
}

// Snapshot 导出某点的全部变量窄化类型（主要供测试/验证与调用方诊断使用）。
// 第二个返回值为 PointUnreachable 时该点在所有路径上都不可达。
func (r *Result) Snapshot(stmtID string) (map[string]*Type, PointStatus, error) {
	e, ok := r.entry[stmtID]
	if !ok {
		return nil, PointReachable, &QueryError{msg: "未知程序点: " + stmtID}
	}
	if e == nil {
		return nil, PointUnreachable, nil
	}
	out := make(map[string]*Type, len(e))
	for _, name := range r.vars {
		if n, ok := e[name]; ok {
			out[name] = toPublic(n)
		}
	}
	return out, PointReachable, nil
}

// Query 查询某程序点入口处某变量被窄化后的类型。
func (r *Result) Query(stmtID, variable string) (PointResult, error) {
	e, ok := r.entry[stmtID]
	if !ok {
		return PointResult{}, &QueryError{msg: "未知程序点: " + stmtID}
	}
	if e == nil {
		return PointResult{Status: PointUnreachable, Type: TNever()}, nil
	}
	n, ok := e[variable]
	if !ok {
		return PointResult{}, &QueryError{msg: "未声明变量: " + variable}
	}
	return PointResult{Status: PointReachable, Type: toPublic(n)}, nil
}

// PointReachable 报告某程序点入口是否可达；不存在的点返回查询错误。
func (r *Result) PointReachable(stmtID string) (PointStatus, error) {
	e, ok := r.entry[stmtID]
	if !ok {
		return PointReachable, &QueryError{msg: "未知程序点: " + stmtID}
	}
	if e == nil {
		return PointUnreachable, nil
	}
	return PointReachable, nil
}

// DeclaredType 返回变量声明类型的归一化副本。
func (r *Result) DeclaredType(variable string) (*Type, error) {
	n, ok := r.declared[variable]
	if !ok {
		return nil, &QueryError{msg: "未声明变量: " + variable}
	}
	return toPublic(n), nil
}

func sortStrings(s []string) { sort.Strings(s) }

var _ = fmt.Sprintf
