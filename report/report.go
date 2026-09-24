// Package report 生成按任务 ID 字典序排列的确定性执行报告。
package report

import (
	"fmt"
	"sort"
	"strings"

	"ontology/fail"
)

// Entry 是单个任务的最终状态记录。
type Entry struct {
	ID     string
	Status fail.Status
	Cause  string // Skipped 时指向最初失败的任务
	Err    error
}

// Report 是不可变的执行报告，条目按 ID 升序。
type Report struct {
	entries []Entry
	byID    map[string]Entry
}

// New 对条目按 ID 排序后构造报告。
func New(entries []Entry) *Report {
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
	r := &Report{entries: entries, byID: make(map[string]Entry, len(entries))}
	for _, e := range entries {
		r.byID[e.ID] = e
	}
	return r
}

func (r *Report) Entries() []Entry { return r.entries }

func (r *Report) Get(id string) (Entry, bool) {
	e, ok := r.byID[id]
	return e, ok
}

// String 输出确定性的逐行文本，不受完成顺序影响。
func (r *Report) String() string {
	var b strings.Builder
	for _, e := range r.entries {
		fmt.Fprintf(&b, "%s %s", e.ID, e.Status)
		if e.Cause != "" {
			fmt.Fprintf(&b, " cause=%s", e.Cause)
		}
		if e.Err != nil {
			fmt.Fprintf(&b, " err=%q", e.Err.Error())
		}
		b.WriteByte('\n')
	}
	return b.String()
}
