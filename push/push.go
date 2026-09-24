// Package push 把只依赖单表的顶层合取子谓词下推到对应表的扫描节点。
package push

import (
	"errors"
	"fmt"

	"ontology/ast"
)

// Scan 是一个表扫描节点，Pred 为下推到该节点的谓词（可为 nil）。
type Scan struct {
	Table string
	Pred  *ast.Node
}

// Plan 是简化的查询计划：顶层 WHERE 谓词 + 若干扫描节点。
type Plan struct {
	Where *ast.Node
	Scans []*Scan
}

// ErrUnknownTable 表示谓词引用了不存在的表，是可判定错误。
var ErrUnknownTable = errors.New("push: predicate references unknown table")

// conjuncts 拍平顶层合取项。
func conjuncts(n *ast.Node) []*ast.Node {
	if n.Kind == ast.And {
		var out []*ast.Node
		for _, k := range n.Kids {
			out = append(out, conjuncts(k)...)
		}
		return out
	}
	return []*ast.Node{n}
}

// Push 拆分顶层合取项：单表项并入对应 Scan，多表项（连接谓词）与
// 零表项（常量）留在顶层。引用不存在的表时报 ErrUnknownTable。
func Push(p *Plan) error {
	if p.Where == nil {
		return ast.ErrEmpty
	}
	byTable := make(map[string]*Scan, len(p.Scans))
	for _, s := range p.Scans {
		byTable[s.Table] = s
	}
	var remain []*ast.Node
	for _, c := range conjuncts(p.Where) {
		tabs := c.Tables()
		switch len(tabs) {
		case 1:
			s := byTable[tabs[0]]
			if s == nil {
				return fmt.Errorf("%w: %s", ErrUnknownTable, tabs[0])
			}
			if s.Pred == nil {
				s.Pred = c
			} else {
				s.Pred = ast.AndN(s.Pred, c)
			}
		default: // 0 个表（常量）或多表（连接谓词）留在顶层
			remain = append(remain, c)
		}
	}
	p.Where = ast.AndN(remain...)
	return nil
}

// Check 自检：每个 Scan 上的谓词只引用本表的列，无跨表引用残留。
func Check(p *Plan) error {
	for _, s := range p.Scans {
		if s.Pred == nil {
			continue
		}
		for _, t := range s.Pred.Tables() {
			if t != s.Table {
				return fmt.Errorf("push: scan %s keeps cross-table ref %s",
					s.Table, t)
			}
		}
	}
	return nil
}
