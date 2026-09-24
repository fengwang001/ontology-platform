// Package report 生成裁剪审计报告。
// 报告内容只依赖集合与路径、不依赖构造顺序，编码逐字节确定。
package report

import (
	"fmt"
	"sort"
	"strings"
)

// Ref 记录某个不可见列在谓词树中的全部引用路径。
// Path 是从根到该比较节点的步骤序列，如 ["or[1]", "not", "compare"]。
type Ref struct {
	Column string
	Paths  [][]string
}

// Report 是一次查询裁剪的审计结果。
type Report struct {
	DroppedColumns []string // 从行中移除的列，字典序
	Rejected       []Ref    // 导致整条拒绝的引用
	Elided         []Ref    // 被 OR 常量折叠消解、未被读取的引用
	RejectedQuery  bool     // 本次查询是否整条拒绝
}

// Build 归并输入并排序：同一列的多个路径合并为一条。
// 调用方可按任意顺序提供参数，输出确定。
func Build(dropped []string, rejected, elided []Ref, rejectedQuery bool) *Report {
	r := &Report{
		DroppedColumns: sortedUnique(dropped),
		Rejected:       normalize(refsAsMap(rejected)),
		Elided:         normalize(refsAsMap(elided)),
		RejectedQuery:  rejectedQuery,
	}
	return r
}

func refsAsMap(refs []Ref) map[string][][]string {
	m := make(map[string][][]string)
	for _, ref := range refs {
		m[ref.Column] = append(m[ref.Column], ref.Paths...)
	}
	return m
}

func normalize(m map[string][][]string) []Ref {
	cols := make([]string, 0, len(m))
	for col := range m {
		cols = append(cols, col)
	}
	sort.Strings(cols)
	out := make([]Ref, 0, len(cols))
	for _, col := range cols {
		paths := m[col]
		sort.Slice(paths, func(i, j int) bool {
			return strings.Join(paths[i], "/") < strings.Join(paths[j], "/")
		})
		out = append(out, Ref{Column: col, Paths: paths})
	}
	return out
}

func sortedUnique(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	for _, s := range in {
		seen[s] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// Encode 返回逐字节确定的文本表示。
func (r *Report) Encode() string {
	var b strings.Builder
	fmt.Fprintf(&b, "rejected_query=%v\n", r.RejectedQuery)
	fmt.Fprintf(&b, "dropped=%s\n", strings.Join(r.DroppedColumns, ","))
	writeRefs(&b, "rejected", r.Rejected)
	writeRefs(&b, "elided", r.Elided)
	return b.String()
}

func writeRefs(b *strings.Builder, tag string, refs []Ref) {
	if len(refs) == 0 {
		fmt.Fprintf(b, "%s=-\n", tag)
		return
	}
	for i, ref := range refs {
		paths := make([]string, len(ref.Paths))
		for j, p := range ref.Paths {
			paths[j] = strings.Join(p, ">")
		}
		fmt.Fprintf(b, "%s[%d]=%s|%s\n", tag, i, ref.Column, strings.Join(paths, ";"))
	}
}
