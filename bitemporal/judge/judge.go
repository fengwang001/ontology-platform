// Package judge 编排双时态快照兼容性判定：按固定优先级依次执行记录自洽性校验、
// 边界语义比较、版本间信息留存规则判定与版本可识别性检查，并负责版本迁移。
package judge

import (
	"fmt"

	"ontology/bitemporal"
	"ontology/bitemporal/boundary"
	"ontology/bitemporal/interval"
	"ontology/bitemporal/registry"
)

// Verdict 是单条记录的总体判定类别。
type Verdict string

const (
	// VerdictRejected：记录自身时态区间不自洽，在任何格式兼容性判定之前先行拒绝。
	VerdictRejected Verdict = "rejected"
	// VerdictIncompatible：边界开闭语义差异会使同一查询时点的归属判定不同。
	VerdictIncompatible Verdict = "incompatible"
	// VerdictLossy：迁移可以进行，但目标版本不记录某条源端存在的时间轴，信息必然损失。
	VerdictLossy Verdict = "lossy"
	// VerdictCompatible：信息可完整保留，且无归属语义差异。
	VerdictCompatible Verdict = "compatible"
	// VerdictUnknownVersion：源或目标版本号超出目录可识别范围。
	VerdictUnknownVersion Verdict = "unknown_version"
)

// FillRuleEntireTimeline 是升级填充的唯一固定规则标识：
// 源端缺失有效时间轴时，填充为覆盖整条时间轴的区间 (-∞, +∞)，
// 语义为“该事实在所有时点均有效（未知即不限制）”。
const FillRuleEntireTimeline = "fill_valid_time_entire_timeline"

// AxisLoss 报告一条时间轴因“目标版本不记录该轴”而发生的确定性信息损失。
type AxisLoss struct {
	Axis    bitemporal.Axis
	Message string
}

// AxisFill 报告一条时间轴在目标版本中存在、但源格式不记录时所采用的固定填充规则。
// 规则全局统一，不允许、也不接受按实例逐一人工指定。
type AxisFill struct {
	Axis    bitemporal.Axis
	Rule    string
	Message string
}

// BoundaryIssue 报告一条时间轴上的边界语义差异。
type BoundaryIssue struct {
	Axis              bitemporal.Axis
	ChangesMembership bool
	WitnessPoint      int64
	HasWitness        bool
	Message           string
}

// Result 是单条记录从 src 版本迁移到 dst 版本的判定结果。
//
// 四类判定互斥，按固定优先级产生：记录自身不自洽 > 边界语义差异 > 轴信息损失；
// 版本号不可识别作为元数据缺省，仅在记录自洽且无法进行版本级判定时给出。
// Fills 只在 VerdictCompatible / VerdictLossy 下列出，填充不属于损失。
type Result struct {
	Verdict        Verdict
	RecordID       string
	SourceVersion  string
	TargetVersion  string
	RejectErrors   []interval.AxisError
	BoundaryIssues []BoundaryIssue
	Losses         []AxisLoss
	Fills          []AxisFill
	UnknownReasons []string
}

// Engine 是判定与迁移的入口。它只持有只读依赖，无可变状态，所有方法可并发调用。
type Engine struct {
	reg       *registry.Registry
	validator interval.Validator
	comparer  boundary.Comparator
}

// NewEngine 构造判定引擎。
func NewEngine(reg *registry.Registry) *Engine {
	return &Engine{reg: reg, validator: interval.Validator{}, comparer: boundary.Comparator{}}
}

// Judge 判定单条记录。纯函数式行为：不修改 rec，不修改任何引擎状态；
// 同一入参在任意并发强度下重复调用得到逐字段相等的结果。
func (e *Engine) Judge(rec bitemporal.Record, src, dst string) Result {
	r := Result{RecordID: rec.ID, SourceVersion: src, TargetVersion: dst}

	// 优先级 1：记录自身时态区间不自洽。与格式版本无关，必须最先判定并先行拒绝，
	// 从而与“格式版本本身不兼容”严格区分报告。
	if errs := e.validator.Validate(rec); len(errs) > 0 {
		r.Verdict = VerdictRejected
		r.RejectErrors = errs
		return r
	}

	srcFmt, srcOK := e.reg.Lookup(src)
	dstFmt, dstOK := e.reg.Lookup(dst)
	if !srcOK || !dstOK {
		r.Verdict = VerdictUnknownVersion
		if !srcOK {
			r.UnknownReasons = append(r.UnknownReasons,
				fmt.Sprintf("源格式版本 %q 超出可识别范围", src))
		}
		if !dstOK {
			r.UnknownReasons = append(r.UnknownReasons,
				fmt.Sprintf("目标格式版本 %q 超出可识别范围", dst))
		}
		return r
	}

	// 优先级 2：边界开闭语义差异导致归属结果可能不同 => 不兼容（区别于信息损失）。
	issues := e.compareBoundaries(rec, srcFmt, dstFmt)
	if len(issues) > 0 {
		r.Verdict = VerdictIncompatible
		r.BoundaryIssues = issues
		return r
	}

	// 优先级 3：轴记录与否差异。源有而目标不记录 => 确定性信息损失；
	// 目标记录而源格式不记录 => 按唯一固定规则填充（不存在未定义状态）。
	r.Losses, r.Fills = e.axisRetention(rec, srcFmt, dstFmt)
	if len(r.Losses) > 0 {
		r.Verdict = VerdictLossy
	} else {
		r.Verdict = VerdictCompatible
	}
	return r
}

// JudgeBatch 判定一批记录，开销与记录数成线性关系：
// 版本元数据只解析一次，单条记录的处理工作量为常数。
func (e *Engine) JudgeBatch(recs []bitemporal.Record, src, dst string) []Result {
	results := make([]Result, len(recs))
	for i := range recs {
		results[i] = e.Judge(recs[i], src, dst)
	}
	return results
}

func (e *Engine) compareBoundaries(rec bitemporal.Record, srcFmt, dstFmt registry.Format) []BoundaryIssue {
	issues := make([]BoundaryIssue, 0, 2)
	for _, axis := range bitemporal.Axes() {
		iv := axisInterval(rec, axis)
		// 只对“源记录携带、源与目标版本都记录”的轴比较边界语义；
		// 缺轴问题属于优先级 3，不在此处理。
		if iv == nil || !srcFmt.RecordsAxis(axis) || !dstFmt.RecordsAxis(axis) {
			continue
		}
		d := e.comparer.CompareAxis(axis, *iv, srcFmt.Convention(axis), dstFmt.Convention(axis))
		if d.ChangesMembership {
			issues = append(issues, BoundaryIssue{
				Axis:              axis,
				ChangesMembership: true,
				WitnessPoint:      d.WitnessPoint,
				HasWitness:        d.HasWitness,
				Message: fmt.Sprintf(
					"%s时间轴的端点开闭约定在源/目标格式间不一致，查询时点 %d 的归属判定会相反",
					axisName(axis), d.WitnessPoint),
			})
		}
	}
	return issues
}

func (e *Engine) axisRetention(rec bitemporal.Record, srcFmt, dstFmt registry.Format) ([]AxisLoss, []AxisFill) {
	losses := []AxisLoss{}
	fills := []AxisFill{}
	for _, axis := range bitemporal.Axes() {
		srcHas := srcFmt.RecordsAxis(axis)
		dstHas := dstFmt.RecordsAxis(axis)
		recHas := axisInterval(rec, axis) != nil
		switch {
		case srcHas && !dstHas && recHas:
			losses = append(losses, AxisLoss{
				Axis: axis,
				Message: fmt.Sprintf(
					"目标格式不记录%s时间轴，该轴信息在迁移后必然丢失",
					axisName(axis)),
			})
		case !srcHas && dstHas && axis == bitemporal.ValidTime:
			fills = append(fills, AxisFill{
				Axis: axis,
				Rule: FillRuleEntireTimeline,
				Message: "源格式不记录有效时间轴，按固定规则填充为 (-∞,+∞)（全时点有效），" +
					"不接受按实例逐一指定",
			})
		}
	}
	return losses, fills
}

func axisInterval(rec bitemporal.Record, axis bitemporal.Axis) *bitemporal.Interval {
	switch axis {
	case bitemporal.ValidTime:
		return rec.Valid
	case bitemporal.TransactionTime:
		return rec.Transaction
	default:
		return nil
	}
}

func axisName(axis bitemporal.Axis) string {
	switch axis {
	case bitemporal.ValidTime:
		return "有效"
	case bitemporal.TransactionTime:
		return "事务"
	default:
		return string(axis)
	}
}
