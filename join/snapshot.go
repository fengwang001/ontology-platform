package join

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// Snapshot 返回某一时刻完整、逐行一致的左外连接视图（调用期间并发写入被阻塞，
// 因而不会读到中间状态）。每个条目 Kind 均为 Insert，按 (LeftID, RightID) 字典序
// 排列；空填充行的 RightID 为空串，排在同一左行的配对行之前。
func (j *Joiner) Snapshot() []Entry {
	j.mu.RLock()
	defer j.mu.RUnlock()

	entries := make([]Entry, 0, j.resultRowCount())
	for _, lr := range sortedRows(j.left) {
		rids := j.right.keyIDs(lr.Key)
		if len(rids) == 0 {
			entries = append(entries, paddedEntry(Insert, lr))
			continue
		}
		for _, rid := range rids {
			entries = append(entries, pairedEntry(Insert, lr, j.right.rows[rid]))
		}
	}
	return entries
}

func (j *Joiner) resultRowCount() int {
	n := 0
	for _, lr := range j.left.rows {
		c := len(j.right.byKey[lr.Key])
		if c == 0 {
			c = 1
		}
		n += c
	}
	return n
}

func sortedRows(t table) []Row {
	rows := make([]Row, 0, len(t.rows))
	for _, r := range t.rows {
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, k int) bool { return rows[i].ID < rows[k].ID })
	return rows
}

// DecisionLog 返回已成功落库的全部判定记录（按时间顺序）的副本。
// 被拒绝的批不会追加记录，因此“已产生的日志”不会因非法输入而改变。
func (j *Joiner) DecisionLog() []Decision {
	j.mu.RLock()
	defer j.mu.RUnlock()
	out := make([]Decision, len(j.log))
	copy(out, j.log)
	return out
}

// writeText 把一条判定记录格式化为人类可读文本：打印输入、输出条目与判定依据。
func (j *Joiner) writeText(d Decision) {
	var b strings.Builder
	if d.Accepted {
		fmt.Fprintf(&b, "op[%d] ACCEPT %s %s id=%q key=%q value=%q\n",
			d.Index, d.Op.Side, d.Op.Kind, d.Op.Row.ID, d.Op.Row.Key, d.Op.Row.Value)
	} else {
		fmt.Fprintf(&b, "op[%d] REJECT %s %s id=%q key=%q value=%q reason=%s\n",
			d.Index, d.Op.Side, d.Op.Kind, d.Op.Row.ID, d.Op.Row.Key, d.Op.Row.Value, d.Reason)
	}
	fmt.Fprintf(&b, "    basis: %s\n", d.Detail)
	for _, e := range d.Entries {
		if e.IsPadded() {
			fmt.Fprintf(&b, "    out %s key=%q left=%q(empty padded) right=<none>\n",
				e.Kind, e.Key, e.LeftID)
		} else {
			fmt.Fprintf(&b, "    out %s key=%q left=%q right=%q\n",
				e.Kind, e.Key, e.LeftID, e.RightID)
		}
	}
	if _, err := io.WriteString(j.out, b.String()); err != nil {
		// 日志写入失败不应影响连接结果；此处仅保证不 panic。
		_ = err
	}
}
