package agg

import (
	"fmt"
	"sort"
	"strings"
)

func formatRows(rows []Row) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, row := range rows {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "{op:%s group:%q value:%q amount:%d}", row.Op, row.Group, row.Value, row.Amount)
	}
	b.WriteByte(']')
	return b.String()
}

func formatDeltas(deltas map[string]int64) string {
	values := make([]string, 0, len(deltas))
	for value := range deltas {
		values = append(values, value)
	}
	sort.Strings(values)
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, fmt.Sprintf("%q:%d", value, deltas[value]))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func formatPartial(p *Partial) string {
	if p == nil {
		return "<nil>"
	}
	parts := make([]string, 0, len(p.Groups))
	for _, g := range p.Groups {
		parts = append(parts, fmt.Sprintf("{group:%q sumDelta:%d countDelta:%d valueDeltas:%s}",
			g.Group, g.SumDelta, g.CountDelta, formatDeltas(g.ValueDeltas)))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
