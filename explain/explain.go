// Package explain 把计划渲染成可逐行核对的文本，
// 每个步骤带估计基数、累计代价与异常标注。
package explain

import (
	"fmt"
	"strings"

	"ontology/catalog"
	"ontology/plan"
)

// Render 渲染计划：后序编号每个步骤，附估计基数与代价；
// 统计异常（缺失/过期）与假设（独立性/笛卡尔积）就地标注。
func Render(best *plan.Plan, v *catalog.View, warnings []error) string {
	sel := map[catalog.Predicate]float64{}
	for _, p := range v.Preds {
		sel[p.Pred] = p.Sel
	}
	stale := map[string]bool{}
	for _, t := range v.Tables {
		stale[t.Name] = t.Stale
	}
	var b strings.Builder
	step := 0
	var walk func(p *plan.Plan)
	walk = func(p *plan.Plan) {
		if p.Kind == plan.Join {
			walk(p.Left)
			walk(p.Right)
		}
		step++
		switch p.Kind {
		case plan.Scan:
			fmt.Fprintf(&b, "%d. Scan %s card=%.6g cost=%.6g", step, p.Table, p.Card, p.Cost)
			if stale[p.Table] {
				b.WriteString(" [统计过期: 已按目录行数校正]")
			}
		case plan.Join:
			fmt.Fprintf(&b, "%d. Join(%s,%s) card=%.6g cost=%.6g", step, p.Left, p.Right, p.Card, p.Cost)
			if p.Cross {
				b.WriteString(" [笛卡尔积]")
			}
			for _, pred := range p.Preds {
				fmt.Fprintf(&b, " pred=%s=%s sel=%.4g", pred.Left, pred.Right, sel[pred])
			}
			if len(p.Preds) > 1 {
				fmt.Fprintf(&b, " [独立性假设: %d个谓词选择率相乘]", len(p.Preds))
			}
			if p.Unreliable {
				b.WriteString(" [估计不可靠: 缺列统计, 已回退默认选择率]")
			}
		}
		b.WriteByte('\n')
	}
	walk(best)
	fmt.Fprintf(&b, "== 总代价 %.6g, 估计基数 %.6g\n", best.Cost, best.Card)
	for _, w := range warnings {
		fmt.Fprintf(&b, "! %v\n", w)
	}
	return b.String()
}
