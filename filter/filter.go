// Package filter 按角色可见列策略裁剪行与谓词（方案丙）。
// 谓词引用不可见列即整条拒绝；仅折叠为常量的 OR 子树内引用记为 elided。
package filter

import (
	"errors"
	"fmt"
	"sort"

	"ontology/policy"
	"ontology/predicate"
	"ontology/report"
)

var (
	ErrInvisibleColumn = errors.New("filter: predicate references invisible column")
	ErrUnknownRole     = errors.New("filter: unknown role")
	ErrEmptyRole       = errors.New("filter: empty visible set")
)

type (
	Row      map[string]string
	Node     = predicate.Node
	Counters struct {
		NodesVisited, CopyOps int
	}
	RejectError struct {
		Err  error
		Refs []report.Ref
	}
	Result struct {
		Rows     []Row
		Report   *report.Report
		counters *Counters
	}
	Filterer struct {
		pol      *policy.Policy
		counters Counters
	}
	analyzer struct {
		canSee       func(string) bool
		nodesVisited int
		rejected     map[string][]string // 路径以 pathSep 拼接，便于前缀判断
		elided       map[string][]string
	}
)

func (e *RejectError) Error() string { return e.Err.Error() }
func (e *RejectError) Unwrap() error { return e.Err }

func New(pol *policy.Policy) *Filterer { return &Filterer{pol: pol} }
func (f *Filterer) NodesVisited() int  { return f.counters.NodesVisited }
func (f *Filterer) CopyOps() int       { return f.counters.CopyOps }

func (a *analyzer) run(root *Node) {
	a.rejected = map[string][][]string{}
	a.elided = map[string][][]string{}
	if root != nil {
		a.walk(root, nil)
	}
}

const pathSep = "\x00"

func (a *analyzer) walk(node *Node, path string) (bool, bool) {
	a.nodesVisited++
	switch node.Kind {
	case predicate.KindConst:
		return node.Const, true
	case predicate.KindCompare:
		here := path + pathSep + "compare"
		if !a.canSee(node.Column) {
			a.rejected[node.Column] = append(a.rejected[node.Column], here)
		}
		return false, false
	case predicate.KindNot:
		cv, isc := a.walk(node.Children[0], path+pathSep+"not")
		return !cv, isc
	case predicate.KindOr:
		return a.junction(node, path, "or", true)
	case predicate.KindAnd:
		return a.junction(node, path, "and", false)
	}
	return false, false
}

func (a *analyzer) junction(node *Node, path, tag string, isOr bool) (bool, bool) {
	allConst, sawTrue := true, false
	for i, child := range node.Children {
		cv, c := a.walk(child, fmt.Sprintf("%s%s%s[%d]", path, pathSep, tag, i))
		if c {
			sawTrue = sawTrue || cv
			continue
		}
		allConst = false
	}
	// OR 任一子句常量真即短路；全部常量假则折叠为假。两种情况下
	// 其余子句运行时都不读取（保守折叠，AND 不享此例外）。
	isConst := sawTrue || allConst
	if isOr && isConst {
		a.reclassify(path, tag)
	}
	return sawTrue, isConst
}

// reclassify 把当前 OR 子树下的 rejected 移入 elided。
func (a *analyzer) reclassify(parentPath, tag string) {
	prefix := parentPath + pathSep + tag + "["
	for col, paths := range a.rejected {
		kept, moved := paths[:0], []string{}
		for _, p := range paths {
			if strings.HasPrefix(p, prefix) {
				moved = append(moved, p)
			} else {
				kept = append(kept, p)
			}
		}
		if len(kept) == 0 {
			delete(a.rejected, col)
		} else {
			a.rejected[col] = kept
		}
		a.elided[col] = append(a.elided[col], moved...)
	}
}

func (f *Filterer) Apply(role string, pred *Node, rows []Row) (*Result, error) {
	f.counters = Counters{}
	if !f.pol.Exists(role) {
		return nil, ErrUnknownRole
	}
	visible, _ := f.pol.Visible(role) // 空可见集是合法边界：行投影为空
	visSet := map[string]struct{}{}
	for _, col := range visible {
		visSet[col] = struct{}{}
	}
	az := &analyzer{canSee: func(c string) bool { _, ok := visSet[c]; return ok }}
	az.run(pred)
	f.counters.NodesVisited = az.nodesVisited

	rep := report.Build(droppedColumns(rows, visSet),
		refsOf(az.rejected), refsOf(az.elided), len(az.rejected) > 0)
	res := &Result{Report: rep, counters: &f.counters}
	if len(az.rejected) > 0 {
		err := error(&RejectError{Err: ErrInvisibleColumn, Refs: rep.Rejected})
		if len(visible) == 0 {
			err = ErrEmptyRole
		}
		return res, err
	}
	res.Rows = projectRows(rows, visSet, &f.counters)
	return res, nil
}

func refsOf(m map[string][][]string) []report.Ref {
	refs := make([]report.Ref, 0, len(m))
	for col, paths := range m {
		refs = append(refs, report.Ref{Column: col, Paths: paths})
	}
	return refs
}

func droppedColumns(rows []Row, visSet map[string]struct{}) []string {
	seen := map[string]struct{}{}
	for _, row := range rows {
		for col := range row {
			if _, ok := visSet[col]; !ok {
				seen[col] = struct{}{}
			}
		}
	}
	return sortedKeys(seen)
}

func projectRows(rows []Row, visSet map[string]struct{}, c *Counters) []Row {
	visible := sortedKeys(visSet)
	out := make([]Row, len(rows))
	for i, row := range rows {
		pr := make(Row, len(visible))
		for _, col := range visible {
			if v, ok := row[col]; ok {
				c.CopyOps++
				pr[col] = v
			}
		}
		out[i] = pr
	}
	return out
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
