package suppress

import (
	"bytes"
	"fmt"
	"testing"
)

// rulesA 为多数用例共享的已知规则集合。
var rulesA = []string{"A", "B", "C"}

// logJudgment 将每次输入、输出与判定依据写入测试日志，满足可审计要求。
func logJudgment(t *testing.T, name string, total int, requireReason bool,
	diags []Diagnostic, dirs []Directive, j Judgment) {
	t.Helper()
	var b bytes.Buffer
	fmt.Fprintf(&b, "\n==== %s ====\n", name)
	fmt.Fprintf(&b, "输入: totalLines=%d requireReason=%v\n", total, requireReason)
	for i, d := range diags {
		fmt.Fprintf(&b, "  诊断#%d: 行=%d 列=%d 规则=%s\n", i, d.Line, d.Column, d.Rule)
	}
	for i, d := range dirs {
		fmt.Fprintf(&b, "  指令#%d: 行=%d 种类=%s 标签=%v 理由=%q\n",
			i, d.Line, d.Kind.String(), d.Labels, d.Reason)
	}
	fmt.Fprintf(&b, "判定依据:\n")
	for _, s := range j.Suppressed {
		fmt.Fprintf(&b, "  抑制: 行=%d 列=%d 规则=%s <- 指令#%d(行%d,%s) 标签=%s\n",
			s.Diagnostic.Line, s.Diagnostic.Column, s.Diagnostic.Rule,
			s.Attribution.DirectiveOrder, s.Attribution.DirectiveLine,
			s.Attribution.Kind.String(), s.Attribution.Label)
	}
	for _, k := range j.Kept {
		fmt.Fprintf(&b, "  保留: 行=%d 列=%d 规则=%s\n",
			k.Diagnostic.Line, k.Diagnostic.Column, k.Diagnostic.Rule)
	}
	for _, is := range j.Issues {
		fmt.Fprintf(&b, "  问题: 指令#%d(行%d) 标签=%q 类别=%s\n",
			is.DirectiveOrder, is.DirectiveLine, is.Label, is.Code.String())
	}
	t.Log(b.String())
}

func runCase(t *testing.T, name string, total int, rules []string, requireReason bool,
	diags []Diagnostic, dirs []Directive) Judgment {
	s := NewSession(total, rules, requireReason)
	for _, d := range diags {
		if err := s.AddDiagnostic(d); err != nil {
			t.Fatalf("%s: 诊断被意外拒绝: %v", name, err)
		}
	}
	for _, d := range dirs {
		if err := s.AddDirective(d); err != nil {
			t.Fatalf("%s: 指令被意外拒绝: %v", name, err)
		}
	}
	j := s.Evaluate()
	logJudgment(t, name, total, requireReason, diags, dirs, j)
	return j
}

func findIssue(j Judgment, line int, label string, code IssueCode) bool {
	for _, is := range j.Issues {
		if is.DirectiveLine == line && is.Label == label && is.Code == code {
			return true
		}
	}
	return false
}

func keptKeys(j Judgment) []string {
	out := make([]string, 0, len(j.Kept))
	for _, k := range j.Kept {
		out = append(out, fmt.Sprintf("%d:%d:%s", k.Diagnostic.Line, k.Diagnostic.Column, k.Diagnostic.Rule))
	}
	return out
}

func suppressedKeys(j Judgment) []string {
	out := make([]string, 0, len(j.Suppressed))
	for _, s := range j.Suppressed {
		out = append(out, fmt.Sprintf("%d:%d:%s", s.Diagnostic.Line, s.Diagnostic.Column, s.Diagnostic.Rule))
	}
	return out
}
