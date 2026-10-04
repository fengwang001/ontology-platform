// Package cursor 提供复合游标 (ts,id) 的字典序比较与回看起点计算。
package cursor

// Cursor 是复合游标，按 (TS, ID) 字典序比较。
type Cursor struct {
	TS int64
	ID int64
}

// Compare 返回 -1/0/+1，表示 c 小于/等于/大于 o。
func (c Cursor) Compare(o Cursor) int {
	switch {
	case c.TS != o.TS:
		if c.TS < o.TS {
			return -1
		}
		return 1
	case c.ID != o.ID:
		if c.ID < o.ID {
			return -1
		}
		return 1
	default:
		return 0
	}
}

// After 报告 c 是否严格大于 o（字典序）。
func (c Cursor) After(o Cursor) bool { return c.Compare(o) > 0 }

// Max 返回两者中较大的游标。
func Max(a, b Cursor) Cursor {
	if b.After(a) {
		return b
	}
	return a
}

// LookbackStart 计算回看起点：(max(cur.TS-B, 0), 0)。
// ID 取 0 使 ts 等于起点值的全部行（id>=1）都被包含。
func LookbackStart(cur Cursor, B int64) Cursor {
	ts := cur.TS - B
	if ts < 0 {
		ts = 0
	}
	return Cursor{TS: ts, ID: 0}
}
