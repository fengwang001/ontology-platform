// Package report 汇总每个任务的最终状态与原因，输出确定性的逐字节报告。
package report

import (
	"fmt"
	"sort"
	"strings"

	"ontology/fail"
)

// Entry 是单个任务的终态记录：Err 为失败原始错误，Cause 为跳过根因任务 ID。
type Entry struct {
	ID    string
	State fail.State
	Err   error
	Cause string
}

// Report 按任务 ID 索引终态，Finalize 后按字典序输出。
type Report struct {
	entries map[string]*Entry
	order   []string
}

// New 创建空报告。
func New() *Report {
	return &Report{entries: map[string]*Entry{}}
}

// Set 写入一个任务的终态；终态不可逆，重复写入被忽略。
func (r *Report) Set(e Entry) {
	if _, ok := r.entries[e.ID]; ok {
		return
	}
	cp := e
	r.entries[e.ID] = &cp
}

// Get 查询某任务的终态。
func (r *Report) Get(id string) (Entry, bool) {
	e, ok := r.entries[id]
	if !ok {
		return Entry{}, false
	}
	return *e, true
}

// Len 返回已记录的任务数。
func (r *Report) Len() int { return len(r.entries) }

// Finalize 固定输出顺序（任务 ID 字典序）。
func (r *Report) Finalize() {
	r.order = r.order[:0]
	for id := range r.entries {
		r.order = append(r.order, id)
	}
	sort.Strings(r.order)
}

// String 输出确定性文本报告，同一批终态逐字节相同。
func (r *Report) String() string {
	var b strings.Builder
	for _, id := range r.order {
		e := r.entries[id]
		fmt.Fprintf(&b, "%s: %s", e.ID, e.State)
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
