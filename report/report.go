// Package report 汇总每个任务的终态与原因，并做确定性序列化。
package report

import (
	"sort"
	"strings"

	"ontology/fail"
)

// Result 是单个任务的最终结果。
type Result struct {
	State fail.State
	Cause string // 指向最初失败任务的 ID；无则空
	Err   error  // 原始错误（含 *fail.PanicError）；无则 nil
}

// Report 是整张图的执行报告。
type Report struct {
	Results map[string]Result
}

// New 创建空报告。
func New() *Report {
	return &Report{Results: map[string]Result{}}
}

// Get 返回某任务的结果。
func (r *Report) Get(id string) Result {
	return r.Results[id]
}

// Count 统计某终态的任务数。
func (r *Report) Count(s fail.State) int {
	n := 0
	for _, res := range r.Results {
		if res.State == s {
			n++
		}
	}
	return n
}

// String 按任务 ID 字典序序列化，输出与完成顺序无关、逐字节确定。
func (r *Report) String() string {
	ids := make([]string, 0, len(r.Results))
	for id := range r.Results {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var b strings.Builder
	for _, id := range ids {
		res := r.Results[id]
		errStr := ""
		if res.Err != nil {
			errStr = res.Err.Error()
		}
		b.WriteString(id + " " + res.State.String() + " " + res.Cause + " " + errStr + "\n")
	}
	return b.String()
}
