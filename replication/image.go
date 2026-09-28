package replication

import (
	"sort"
	"strconv"
)

// 本文件负责行镜像比较与事件合法性校验。
//
// 镜像按"列名 -> 值"的集合整体比较：列集合必须相同，且每个列的值必须相同；
// 因此缺列（map 中无此 key）与空串（key 存在但值为 ""）不相等。

// diffKind 标识单个列上的镜像差异类型。
type diffKind string

const (
	diffMissingInCurrent diffKind = "MISSING_IN_CURRENT" // 前像有该列、当前行没有（缺列）
	diffMissingInBefore  diffKind = "MISSING_IN_BEFORE"  // 当前行有该列、前像没有（缺列）
	diffValueMismatch    diffKind = "VALUE_MISMATCH"     // 两镜像都有该列但值不同
)

// imageDiff 描述两个行镜像之间的一处差异，用于给出判定依据。
type imageDiff struct {
	column  string
	kind    diffKind
	before  string // 前像侧的值（缺列时为 "<missing>"）
	current string // 当前侧的值（缺列时为 "<missing>"）
}

// missingMarker 用于日志中表示"该列缺失"，区别于真实的空串值 ""。
const missingMarker = "<missing>"

// rowsEqual 报告两个行镜像是否完全一致：
// 列集合相同（缺列不算空串）且每列值相同。
func rowsEqual(a, b Row) bool {
	if len(a) != len(b) {
		return false
	}
	for col, va := range a {
		vb, ok := b[col]
		if !ok || vb != va {
			return false
		}
	}
	return true
}

// firstDiff 返回两个镜像按列名字典序的第一处差异；完全一致时返回 nil。
// 稳定的列序保证同一输入序列的日志输出完全相同。
func firstDiff(before, current Row) *imageDiff {
	cols := make(map[string]struct{}, len(before)+len(current))
	for c := range before {
		cols[c] = struct{}{}
	}
	for c := range current {
		cols[c] = struct{}{}
	}
	ordered := make([]string, 0, len(cols))
	for c := range cols {
		ordered = append(ordered, c)
	}
	sort.Strings(ordered)

	for _, c := range ordered {
		vb, inBefore := before[c]
		vc, inCurrent := current[c]
		switch {
		case inBefore && !inCurrent:
			return &imageDiff{column: c, kind: diffMissingInCurrent, before: vb, current: missingMarker}
		case !inBefore && inCurrent:
			return &imageDiff{column: c, kind: diffMissingInBefore, before: missingMarker, current: vc}
		case vb != vc:
			return &imageDiff{column: c, kind: diffValueMismatch, before: vb, current: vc}
		}
	}
	return nil
}

// describeDiff 把差异渲染为日志中使用的判定依据。
// strconv.Quote 使空串显示为 ""，缺列显示为 <missing>，二者可区分。
func describeDiff(d *imageDiff) string {
	if d == nil {
		return ""
	}
	switch d.kind {
	case diffMissingInCurrent:
		return "column " + strconv.Quote(d.column) + " present in before-image with value " + strconv.Quote(d.before) +
			" but missing (" + missingMarker + ") in current row (a missing column is not the empty string)"
	case diffMissingInBefore:
		return "column " + strconv.Quote(d.column) + " missing (" + missingMarker + ") in before-image " +
			"but present in current row with value " + strconv.Quote(d.current) +
			" (a missing column is not the empty string)"
	default:
		return "column " + strconv.Quote(d.column) + " mismatch: before-image=" + strconv.Quote(d.before) + " current=" + strconv.Quote(d.current)
	}
}

// validateEvent 校验单条事件的结构合法性（不涉及副本当前状态）。
// 合法返回空串，非法返回说明依据。规则：
//   - 序号必须为正；
//   - 主键非空；
//   - 操作必须为 INSERT/UPDATE/DELETE 之一；
//   - 镜像中的列名非空；
//   - INSERT：前像必须为 nil（行不存在），后像必须非 nil 且至少一列；
//   - UPDATE：前像、后像均必须非 nil 且至少一列；
//   - DELETE：前像必须非 nil 且至少一列，后像必须为 nil。
func validateEvent(e Event) string {
	if e.Seq <= 0 {
		return "seq must be positive, got " + strconv.FormatInt(e.Seq, 10)
	}
	if e.Key == "" {
		return "key must not be empty"
	}
	switch e.Op {
	case OpInsert:
		if e.Before != nil {
			return "INSERT requires nil before-image (row must not exist), got " + strconv.Itoa(len(e.Before)) + " columns"
		}
		if len(e.After) == 0 {
			return "INSERT requires a non-empty after-image"
		}
		if reason := validateColumns("after-image", e.After); reason != "" {
			return reason
		}
	case OpUpdate:
		if len(e.Before) == 0 {
			return "UPDATE requires a non-empty before-image"
		}
		if len(e.After) == 0 {
			return "UPDATE requires a non-empty after-image"
		}
		if reason := validateColumns("before-image", e.Before); reason != "" {
			return reason
		}
		if reason := validateColumns("after-image", e.After); reason != "" {
			return reason
		}
	case OpDelete:
		if len(e.Before) == 0 {
			return "DELETE requires a non-empty before-image"
		}
		if e.After != nil {
			return "DELETE requires nil after-image, got " + strconv.Itoa(len(e.After)) + " columns"
		}
		if reason := validateColumns("before-image", e.Before); reason != "" {
			return reason
		}
	default:
		return "unknown op " + strconv.Quote(string(e.Op))
	}
	return ""
}

// validateColumns 拒绝空列名；which 标明镜像位置用于依据说明。
func validateColumns(which string, r Row) string {
	for c := range r {
		if c == "" {
			return which + " contains a column with empty name"
		}
	}
	return ""
}
