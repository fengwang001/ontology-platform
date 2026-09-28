package ontology

import (
	"fmt"
	"sort"
	"strings"
)

// FormatChange 将一条变更格式化为紧凑的确定文本，如 +g/v、-g/v；
// 非法符号显示为 ?g/v。
func FormatChange(ch Change) string {
	sign := "?"
	switch ch.Kind {
	case KindInsert:
		sign = "+"
	case KindRetract:
		sign = "-"
	}
	return fmt.Sprintf("%s%s=%s", sign, ch.Group, ch.Value)
}

// FormatChanges 将一批输入条目按原顺序格式化。
func FormatChanges(changes []Change) string {
	parts := make([]string, len(changes))
	for i, ch := range changes {
		parts[i] = FormatChange(ch)
	}
	return "[" + strings.Join(parts, " ") + "]"
}

// FormatView 将去重计数视图按组名升序格式化为 {g:n ...}。
func FormatView(view map[string]int) string {
	groups := make([]string, 0, len(view))
	for g := range view {
		groups = append(groups, g)
	}
	sort.Strings(groups)
	parts := make([]string, 0, len(groups))
	for _, g := range groups {
		parts = append(parts, fmt.Sprintf("%s:%d", g, view[g]))
	}
	return "{" + strings.Join(parts, " ") + "}"
}

// FormatResult 将判定结果格式化为多行确定文本，
// 包含判定结论、拒绝原因/位置以及各组去重计数的净变化。
func FormatResult(res ApplyResult) string {
	var b strings.Builder
	if res.Accepted {
		b.WriteString("verdict=ACCEPT\n")
	} else {
		fmt.Fprintf(&b, "verdict=REJECT reason=%s entry_index=%d\n", res.Reason, res.EntryIndex)
	}
	for _, d := range res.Changes {
		fmt.Fprintf(&b, "  group=%s before=%d after=%d delta=%+d\n", d.Group, d.Before, d.After, d.Delta)
	}
	return strings.TrimRight(b.String(), "\n")
}

// FormatLogEntry 将一条判定日志格式化为多行确定文本，
// 打印输入批次、判定依据、输出净变化条目与批后视图。
func FormatLogEntry(e LogEntry) string {
	var b strings.Builder
	fmt.Fprintf(&b, "seq=%d ", e.Seq)
	b.WriteString(FormatResult(ApplyResult{
		Accepted:   e.Accepted,
		Reason:     e.Reason,
		EntryIndex: e.EntryIdx,
		Changes:    e.Changes,
	}))
	fmt.Fprintf(&b, "\n  input: %s", FormatChanges(e.Input))
	fmt.Fprintf(&b, "\n  view_after: %s", FormatView(e.ViewAfter))
	return b.String()
}
