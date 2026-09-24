// Package report 产出属性级裁剪的可审计报告；输出对输入构造顺序完全确定。
package report

import (
	"errors"
	"sort"
	"strings"

	"ontology/filter"
)

// Report 是一次查询的裁剪审计结果。
type Report struct {
	Role     string
	Removed  []string // 被裁掉的列名（字典序、去重）
	Rejected []filter.Ref
	Denied   bool
}

// Build 根据拒绝错误（可为 nil）与角色不可见列构造确定性报告。
// hidden 的传入顺序不影响输出：列名排序去重，引用按 (列名, 路径) 排序，
// 同一列出现多次时列名只出现一次、全部路径保留。
func Build(role string, hidden []string, err error) Report {
	r := Report{Role: role, Removed: sortedUnique(hidden)}
	if err != nil {
		var re *filter.RejectError
		if errors.As(err, &re) {
			r.Denied = true
			r.Rejected = sortRefs(append([]filter.Ref(nil), re.Refs...))
		}
	}
	return r
}

func sortedUnique(in []string) []string {
	set := make(map[string]struct{}, len(in))
	for _, s := range in {
		set[s] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func sortRefs(refs []filter.Ref) []filter.Ref {
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Col != refs[j].Col {
			return refs[i].Col < refs[j].Col
		}
		return refs[i].Path < refs[j].Path
	})
	return refs
}

// Marshal 生成确定性的逐字节文本表示，用于跨构造顺序比对。
func (r Report) Marshal() string {
	var b strings.Builder
	b.WriteString("role=" + r.Role + "\n")
	b.WriteString("denied=" + boolStr(r.Denied) + "\n")
	b.WriteString("removed=" + strings.Join(r.Removed, ",") + "\n")
	for _, ref := range r.Rejected {
		b.WriteString("reject:" + ref.Col + " " + ref.Path + "\n")
	}
	return b.String()
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
