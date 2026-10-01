package cursor

import (
	"strconv"
	"strings"
)

func intToString(v int64) string { return strconv.FormatInt(v, 10) }

func quoteString(s string) string { return strconv.Quote(s) }

func rowsToString(rows []int64) string {
	if rows == nil {
		return "[]"
	}
	parts := make([]string, len(rows))
	for i, r := range rows {
		parts[i] = strconv.FormatInt(r, 10)
	}
	return "[" + strings.Join(parts, ",") + "]"
}
