package defassign

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// DiagKind 是诊断类别。
type DiagKind uint8

const (
	// DiagMaybeUnassigned：读取点在某条可到达路径上变量尚未赋值。
	DiagMaybeUnassigned DiagKind = iota + 1
	// DiagDeadAssign：赋值之后在所有路径上、被再次赋值前都未被读取。
	DiagDeadAssign
)

func (k DiagKind) String() string {
	switch k {
	case DiagMaybeUnassigned:
		return "maybe-unassigned"
	case DiagDeadAssign:
		return "dead-assign"
	}
	return "?"
}

// Diagnostic 是一条最终诊断。Witness 是判定依据：
// 对可能未赋值，列出可达且未赋值的路径标签（经规范化去重）。
type Diagnostic struct {
	Kind    DiagKind
	At      Pos
	Line    int
	Var     string
	Witness []string
}

func (d Diagnostic) String() string {
	return fmt.Sprintf("%s:%d:%s:%s", d.Kind, d.At, d.Var, strings.Join(d.Witness, "|"))
}

// Report 是一次检查的完整结果。
type Report struct {
	Program string
	Diags   []Diagnostic
}

// normalize 按位置、类别、变量排序；同点同类的诊断去重并合并依据。
func (r *Report) normalize() {
	sort.SliceStable(r.Diags, func(i, j int) bool {
		a, b := r.Diags[i], r.Diags[j]
		if a.At != b.At {
			return a.At < b.At
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.Var < b.Var
	})
	kept := r.Diags[:0]
	for _, d := range r.Diags {
		if len(kept) > 0 {
			last := &kept[len(kept)-1]
			if last.At == d.At && last.Kind == d.Kind && last.Var == d.Var {
				last.Witness = mergeWitness(last.Witness, canonTraces(d.Witness))
				continue
			}
		}
		d.Witness = mergeWitness(nil, canonTraces(d.Witness))
		kept = append(kept, d)
	}
	r.Diags = kept
}

func mergeWitness(a, b []string) []string {
	seen := map[string]bool{}
	for _, w := range a {
		seen[w] = true
	}
	for _, w := range b {
		seen[w] = true
	}
	out := make([]string, 0, len(seen))
	for w := range seen {
		out = append(out, w)
	}
	sort.Strings(out)
	return out
}

// WriteText 把报告以逐字节稳定的文本形式写出。
func (r *Report) WriteText(w io.Writer) {
	fmt.Fprintf(w, "program %s\n", r.Program)
	if len(r.Diags) == 0 {
		fmt.Fprintln(w, "  no diagnostics")
		return
	}
	for _, d := range r.Diags {
		fmt.Fprintf(w, "  line %d pos %d: %s %s via %s\n",
			d.Line, d.At, d.Kind, d.Var, strings.Join(d.Witness, ", "))
	}
}

// Text 返回报告文本（稳定字节序列）。
func (r *Report) Text() string {
	var sb strings.Builder
	r.WriteText(&sb)
	return sb.String()
}
