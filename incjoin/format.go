package incjoin

import (
	"strconv"
	"strings"
)

// formatRows 把一批输入行格式化为 [r1 r2 ...]，空批显示为 -。
func formatRows(rows []Row) string {
	if len(rows) == 0 {
		return "-"
	}
	parts := make([]string, len(rows))
	for i, r := range rows {
		parts[i] = r.String()
	}
	return "[" + strings.Join(parts, " ") + "]"
}

// formatEntries 把输出差分（或快照条目）格式化，空切片显示为 -。
func formatEntries(entries []DiffEntry) string {
	if len(entries) == 0 {
		return "-"
	}
	parts := make([]string, len(entries))
	for i, e := range entries {
		parts[i] = e.String()
	}
	return "[" + strings.Join(parts, " ") + "]"
}

func formatDeleteMsg(side, key, val string, old, delta int64) string {
	return side + " delete would make multiplicity negative: " +
		"key=" + strconv.Quote(key) + " val=" + strconv.Quote(val) +
		" current=" + strconv.FormatInt(old, 10) +
		" delete_delta=" + strconv.FormatInt(delta, 10)
}

func formatMulMsg(key, lv, rv string, lm, rm int64) string {
	return "join multiplicity product overflows int64: key=" + strconv.Quote(key) +
		" leftMult=" + strconv.FormatInt(lm, 10) +
		" rightMult=" + strconv.FormatInt(rm, 10)
}

func formatTotalLimit(before, delta, limit int64) string {
	return "post-batch result tuple count " + strconv.FormatInt(before+delta, 10) +
		" exceeds limit " + strconv.FormatInt(limit, 10)
}
