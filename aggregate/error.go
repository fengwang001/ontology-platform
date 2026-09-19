package aggregate

import (
	"fmt"
	"strconv"
	"strings"
)

// OverflowError is returned (as *OverflowError) from Snapshot when one or
// more groups consisting entirely of int64 values have a sum outside the
// int64 range. It never silently wraps and never widens such a sum to
// float64; callers decide what to do. Groups lists every overflowing group
// key in the same stable order as Snapshot results.
type OverflowError struct {
	Groups []GroupKey
}

func (e *OverflowError) Error() string {
	names := make([]string, len(e.Groups))
	for i, g := range e.Groups {
		names[i] = FormatKey(g)
	}
	return "aggregate: int64 sum overflow in group(s): " + strings.Join(names, ", ")
}

// FormatKey renders a GroupKey as a stable, human-readable descriptor that
// keeps the three missing situations distinguishable, e.g.
// [region=us,status=<absent>] or [region=<null>,status=<empty_string>].
func FormatKey(k GroupKey) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, col := range k.Columns {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(col.Name)
		b.WriteByte('=')
		switch col.Kind {
		case Absent:
			b.WriteString("<absent>")
		case Null:
			b.WriteString("<nil>")
		case EmptyString:
			b.WriteString(`""`)
		default:
			switch v := col.Value.(type) {
			case string:
				b.WriteString(strconv.Quote(v))
			default:
				b.WriteString(fmt.Sprintf("%v", v))
			}
		}
	}
	b.WriteByte(']')
	return b.String()
}
