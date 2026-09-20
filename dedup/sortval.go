package dedup

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// Sort ranks keep different kinds of values in separate, ordered bands.
const (
	rankMissing = iota
	rankNil
	rankBool
	rankNumber
	rankString
	rankNaN
	rankOther
)

// sortVal is the canonical sortable form of one dedup column value.
type sortVal struct {
	rank  int
	b     bool
	i     int64
	f     float64
	isInt bool
	s     string
}

// sortVals extracts the canonical sortable form of each dedup column.
func sortVals(columns []string, row map[string]any) []sortVal {
	out := make([]sortVal, len(columns))
	for idx, col := range columns {
		v, ok := row[col]
		if !ok {
			out[idx] = sortVal{rank: rankMissing}
			continue
		}
		out[idx] = sortValOf(v)
	}
	return out
}

func sortValOf(v any) sortVal {
	switch t := v.(type) {
	case nil:
		return sortVal{rank: rankNil}
	case bool:
		return sortVal{rank: rankBool, b: t}
	case string:
		return sortVal{rank: rankString, s: t}
	case int:
		return sortVal{rank: rankNumber, i: int64(t), isInt: true}
	case int8:
		return sortVal{rank: rankNumber, i: int64(t), isInt: true}
	case int16:
		return sortVal{rank: rankNumber, i: int64(t), isInt: true}
	case int32:
		return sortVal{rank: rankNumber, i: int64(t), isInt: true}
	case int64:
		return sortVal{rank: rankNumber, i: t, isInt: true}
	case uint:
		return uintSortVal(uint64(t))
	case uint8:
		return uintSortVal(uint64(t))
	case uint16:
		return uintSortVal(uint64(t))
	case uint32:
		return uintSortVal(uint64(t))
	case uint64:
		return uintSortVal(t)
	case float32:
		return sortValOf(float64(t))
	case float64:
		if math.IsNaN(t) {
			return sortVal{rank: rankNaN}
		}
		return sortVal{rank: rankNumber, f: t}
	}
	return sortVal{rank: rankOther}
}

func uintSortVal(u uint64) sortVal {
	if u <= math.MaxInt64 {
		return sortVal{rank: rankNumber, i: int64(u), isInt: true}
	}
	return sortVal{rank: rankNumber, f: float64(u)}
}

// rowTie renders a row deterministically so groups whose dedup columns
// compare equal (e.g. two NaN rows) still sort independently of arrival.
func rowTie(row map[string]any) string {
	keys := make([]string, 0, len(row))
	for k := range row {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&sb, "%s=%T:%v;", k, row[k], row[k])
	}
	return sb.String()
}

func copyRow(row map[string]any) map[string]any {
	out := make(map[string]any, len(row))
	for k, v := range row {
		out[k] = v
	}
	return out
}
