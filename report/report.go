// Package report 汇总每个任务的最终状态与原因，输出逐字节确定的报告。
package report

import (
	"fmt"
	"sort"
	"strings"

	"ontology/fail"
	"ontology/graph"
)

// Entry 是单个任务的最终记录。
type Entry struct {
	Status  fail.Status
	Started bool     // 是否真正开始执行过（Canceled 与 Skipped 的旁证）
	Origin  graph.ID // Skipped 时指向最初失败的任务；Failed 时为自身
	Err     error    // Failed 时的原始错误（含 *fail.PanicError）
}

// Report 按任务 ID 索引最终条目。
type Report struct {
	Entries map[graph.ID]*Entry
}

// New 创建空报告。
func New() *Report { return &Report{Entries: map[graph.ID]*Entry{}} }

// Get 返回某任务的条目。
func (r *Report) Get(id graph.ID) (*Entry, bool) {
	e, ok := r.Entries[id]
	return e, ok
}

// Set 写入某任务的最终条目。
func (r *Report) Set(id graph.ID, e Entry) { r.Entries[id] = &e }

// Count 统计处于某状态的任务数。
func (r *Report) Count(s fail.Status) int {
	n := 0
	for _, e := range r.Entries {
		if e.Status == s {
			n++
		}
	}
	return n
}

// String 按任务 ID 字典序逐行输出；与完成顺序、加边顺序无关，逐字节确定。
func (r *Report) String() string {
	ids := make([]string, 0, len(r.Entries))
	for id := range r.Entries {
		ids = append(ids, string(id))
	}
	sort.Strings(ids)
	var b strings.Builder
	for _, id := range ids {
		e := r.Entries[graph.ID(id)]
		fmt.Fprintf(&b, "%s status=%s started=%t", id, e.Status, e.Started)
		if e.Origin != "" {
			fmt.Fprintf(&b, " origin=%s", e.Origin)
		}
		if e.Err != nil {
			fmt.Fprintf(&b, " err=%q", e.Err.Error())
		}
		b.WriteByte('\n')
	}
	return b.String()
}
