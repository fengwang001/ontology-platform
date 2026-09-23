// Package plan 表示执行计划（叶子扫描 / 二元连接）并用按子集的
// 动态规划枚举选出代价最低的连接顺序。
package plan

import (
	"math"

	"ontology/catalog"
)

// relEps 是代价判等的相对阈值，依据见 DESIGN.md 第 2 节。
const relEps = 1e-9

// Kind 区分计划节点类型。
type Kind int

const (
	Scan Kind = iota // 叶子：单表扫描
	Join             // 二元连接（含笛卡尔积）
)

// Plan 是一棵二元计划树。Card 为估计基数，Cost 为累计代价。
type Plan struct {
	Kind  Kind
	Table string // Scan：表名

	Left, Right *Plan               // Join：两侧子计划
	Preds       []catalog.Predicate // Join：本步应用的横跨谓词
	Cross       bool                // Join：是否为笛卡尔积

	Card float64
	Cost float64

	Unreliable bool // 子树中使用了默认选择率（统计缺失）
	MultiPred  bool // 子树中组合了多个谓词（独立性假设）

	leafSeq string // 叶子中序表名序列（并列打破用）
	shape   string // 规范化树形串（最终兜底）
}

// LeafSeq 返回参与表名的中序序列。
func (p *Plan) LeafSeq() string { return p.leafSeq }

func newScan(table string, rows float64) *Plan {
	return &Plan{Kind: Scan, Table: table, Card: rows, Cost: rows,
		leafSeq: table, shape: table}
}

func newJoin(left, right *Plan, preds []catalog.Predicate, card, cost float64, unreliable bool) *Plan {
	return &Plan{
		Kind:       Join,
		Left:       left,
		Right:      right,
		Preds:      preds,
		Cross:      len(preds) == 0,
		Card:       card,
		Cost:       cost,
		Unreliable: unreliable || left.Unreliable || right.Unreliable,
		MultiPred:  len(preds) > 1 || left.MultiPred || right.MultiPred,
		leafSeq:    left.leafSeq + "\x00" + right.leafSeq,
		shape:      "(" + left.shape + "|" + right.shape + ")",
	}
}

// costEqual 按相对误差判定两个代价相等（吸收浮点累加的 ULP 漂移）。
func costEqual(a, b float64) bool {
	d := math.Abs(a - b)
	return d <= relEps*math.Max(1, math.Max(math.Abs(a), math.Abs(b)))
}

// better 判定 cand 是否应取代 cur：代价明显更低者胜；等代价时按
// 叶子表名序列字典序，再按树形串，保证与枚举顺序无关的全序。
func better(cand, cur *Plan) bool {
	if cur == nil {
		return true
	}
	if costEqual(cand.Cost, cur.Cost) {
		if cand.leafSeq != cur.leafSeq {
			return cand.leafSeq < cur.leafSeq
		}
		return cand.shape < cur.shape
	}
	return cand.Cost < cur.Cost
}

// CrossCount 统计计划树中笛卡尔积节点的个数（测试与演示用）。
func (p *Plan) CrossCount() int {
	if p.Kind == Scan {
		return 0
	}
	n := p.Left.CrossCount() + p.Right.CrossCount()
	if p.Cross {
		n++
	}
	return n
}

// String 返回便于核对的单行结构描述。
func (p *Plan) String() string {
	if p.Kind == Scan {
		return p.Table
	}
	op := "⋈"
	if p.Cross {
		op = "×"
	}
	return "(" + p.Left.String() + op + p.Right.String() + ")"
}
