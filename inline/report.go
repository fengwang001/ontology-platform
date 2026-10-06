package inline

import (
	"fmt"
	"strings"
)

// RejectReason 是调用点被拒绝内联的原因，声明顺序即优先级：
// 一个调用点只报告第一个成立的原因。
type RejectReason int

const (
	RejectNone RejectReason = iota
	// RejectUndefined 被调函数未定义。
	RejectUndefined
	// RejectNoInline 被调函数带有禁止内联标记。
	RejectNoInline
	// RejectDirectRecursion 调用点直接递归（被调函数即包含该调用点的函数）。
	RejectDirectRecursion
	// RejectNonInlinableStructure 被调函数包含不可内联的结构。
	RejectNonInlinableStructure
	// RejectChainLimit 同一函数在当前展开链上出现次数超限。
	RejectChainLimit
	// RejectBudget 超出增长预算或全局尺寸上限。
	RejectBudget
)

func (r RejectReason) String() string {
	switch r {
	case RejectNone:
		return "无"
	case RejectUndefined:
		return "被调函数未定义"
	case RejectNoInline:
		return "禁止内联标记"
	case RejectDirectRecursion:
		return "直接递归"
	case RejectNonInlinableStructure:
		return "结构不可内联"
	case RejectChainLimit:
		return "展开链超限"
	case RejectBudget:
		return "预算不足"
	}
	return fmt.Sprintf("RejectReason(%d)", int(r))
}

// CallDecision 是一个被考察调用点的最终去向。
type CallDecision struct {
	// ID 唯一标识调用点，如 "F#0" 或 "F#0/G#1"（经 F#0 内联进来的 G 的 1 号调用点）。
	ID string
	// Callee 是被调函数名。
	Callee string
	// Hotness 是考察时刻该调用点的热度（已按外层比例缩放）。
	Hotness float64
	// Path 是考察时刻的展开链（根在前）。
	Path []string
	// Inlined 为 true 表示已内联；否则 Reason 给出拒绝原因。
	Inlined bool
	Reason  RejectReason
}

// FunctionReport 是一个函数作为展开根的决策报告。
// 其中数值来自决策过程中实时记录的状态，非事后重算。
type FunctionReport struct {
	Name        string
	InitialSize int64
	FinalSize   int64
	// DeepestPath 是展开过程中达到的最深展开链（根在前）。
	DeepestPath []string
	// Decisions 按考察顺序记录每个调用点的去向。
	Decisions []CallDecision

	// surviving 是最终函数体中幸存的（未被内联的）调用点，
	// 按最终函数体顺序排列，供后续根内联本函数时复制。
	surviving []bodySite
}

// Stats 记录决策过程的判定工作量，用于验证复杂度承诺。
type Stats struct {
	// BudgetChecks 是预算判定次数。
	BudgetChecks int64
	// ChainScanSteps 是展开链扫描的总步数（只随链长增长）。
	ChainScanSteps int64
}

// Report 是一次决策任务的完整报告，函数按登记顺序排列。
type Report struct {
	Functions []FunctionReport
	Stats     Stats
}

// Func 按名取报告。
func (r *Report) Func(name string) (FunctionReport, bool) {
	for _, fr := range r.Functions {
		if fr.Name == name {
			return fr, true
		}
	}
	return FunctionReport{}, false
}

// String 渲染为人类可读的决策报告。
func (r *Report) String() string {
	var b strings.Builder
	for _, fr := range r.Functions {
		fmt.Fprintf(&b, "函数 %s: 初始尺寸 %d -> 最终尺寸 %d, 最深展开链 %s\n",
			fr.Name, fr.InitialSize, fr.FinalSize, strings.Join(fr.DeepestPath, " -> "))
		for _, d := range fr.Decisions {
			if d.Inlined {
				fmt.Fprintf(&b, "  调用点 %s (-> %s, 热度 %.4g): 已内联\n", d.ID, d.Callee, d.Hotness)
			} else {
				fmt.Fprintf(&b, "  调用点 %s (-> %s, 热度 %.4g): 拒绝, 原因: %s\n",
					d.ID, d.Callee, d.Hotness, d.Reason)
			}
		}
	}
	return b.String()
}
