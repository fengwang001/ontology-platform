package eventproc

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// String 渲染快照，便于单测日志打印：
// 队列、当前批次（含已处理位置）、保存现场栈与已处理序列。
func (s Snapshot) String() string {
	var b strings.Builder

	priorities := make([]int, 0, len(s.Queued))
	for pr := range s.Queued {
		priorities = append(priorities, pr)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(priorities)))

	b.WriteString("queued=[")
	for i, pr := range priorities {
		if i > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "p%d:%s", pr, formatItems(s.Queued[pr]))
	}
	b.WriteByte(']')

	b.WriteString(" current=")
	if s.Current == nil {
		b.WriteString("<none>")
	} else {
		fmt.Fprintf(&b, "p%d(cursor=%d):%s",
			s.Current.Priority, s.Current.Cursor, formatItems(s.Current.Events))
	}

	b.WriteString(" stack=[")
	for i, f := range s.Stack {
		if i > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "(p%d,cursor=%d)", f.Priority, f.Cursor)
	}
	b.WriteByte(']')

	fmt.Fprintf(&b, " processed=%v", s.Processed)
	return b.String()
}

func formatItems(items []BatchItem) string {
	parts := make([]string, len(items))
	for i, it := range items {
		parts[i] = strconv.Quote(it.ID)
	}
	return "[" + strings.Join(parts, ",") + "]"
}
