// Package report 把执行结果渲染成按任务 ID 字典序排列的确定性文本报告。
package report

import (
	"sort"
	"strings"

	"ontology/exec"
)

// Line 是一个任务的报告行。
type Line struct {
	ID     string
	Status string
	Reason string
}

// Lines 返回按 ID 升序排列的报告行。
func Lines(results []exec.TaskResult) []Line {
	sorted := append([]exec.TaskResult(nil), results...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	out := make([]Line, 0, len(sorted))
	for _, r := range sorted {
		l := Line{ID: r.ID, Status: r.Status.String()}
		if r.Err != nil {
			l.Reason = r.Err.Error()
		}
		out = append(out, l)
	}
	return out
}

// Render 渲染为逐字节确定的文本：每行 "ID STATUS REASON"，换行结尾。
func Render(results []exec.TaskResult) string {
	var b strings.Builder
	for _, l := range Lines(results) {
		b.WriteString(l.ID)
		b.WriteByte(' ')
		b.WriteString(l.Status)
		if l.Reason != "" {
			b.WriteByte(' ')
			b.WriteString(l.Reason)
		}
		b.WriteByte('\n')
	}
	return b.String()
}
