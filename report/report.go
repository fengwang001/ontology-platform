// Package report 把终态集合渲染成逐字节确定的文本报告。
package report

import (
	"fmt"
	"sort"
	"strings"

	"ontology/fail"
)

// Render 按任务 ID 字典序逐行输出：ID 状态 [附加原因]。
// 同图同终态必然产出逐字节相同的字符串，与完成时序无关。
func Render(states []fail.State) string {
	ordered := append([]fail.State{}, states...)
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].ID < ordered[j].ID
	})
	var b strings.Builder
	for _, st := range ordered {
		fmt.Fprintf(&b, "%s\t%s", st.ID, st.Status)
		switch st.Status {
		case fail.StatusFailed:
			if st.Err != nil {
				fmt.Fprintf(&b, "\terr=%s", oneLine(st.Err.Error()))
			}
		case fail.StatusSkipped:
			fmt.Fprintf(&b, "\treason=%s", st.ReasonID)
		case fail.StatusCanceled:
			if st.Started {
				b.WriteString("\tstarted=true")
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	return s
}
