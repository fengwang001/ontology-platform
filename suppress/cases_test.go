package suppress

import (
	"reflect"
	"testing"
)

func issueStrings(j Judgment) []string {
	out := make([]string, 0, len(j.Issues))
	for _, is := range j.Issues {
		out = append(out, is.Code.String()+"@"+itoa(is.DirectiveLine)+":"+is.Label)
	}
	return out
}

// TestDisableRangeBoundaries 禁用区间起止行取等边界：禁用行含；启用行不含；未闭合含末行。
func TestDisableRangeBoundaries(t *testing.T) {
	dirs := []Directive{
		{Line: 2, Kind: KindDisable, Labels: []string{"A"}, Reason: "r"},
		{Line: 4, Kind: KindEnable, Labels: []string{"A"}, Reason: "r"},
	}
	diags := []Diagnostic{
		{Line: 1, Column: 1, Rule: "A"},
		{Line: 2, Column: 1, Rule: "A"},
		{Line: 3, Column: 1, Rule: "A"},
		{Line: 4, Column: 1, Rule: "A"},
	}
	j := runCase(t, "禁用区间取等边界", 5, rulesA, false, diags, dirs)
	if got := suppressedKeys(j); !reflect.DeepEqual(got, []string{"2:1:A", "3:1:A"}) {
		t.Fatalf("被抑制诊断不符: %v", got)
	}
	if got := keptKeys(j); !reflect.DeepEqual(got, []string{"1:1:A", "4:1:A"}) {
		t.Fatalf("保留诊断不符: %v", got)
	}
	j2 := runCase(t, "未闭合含末行", 5, rulesA, false,
		[]Diagnostic{{Line: 5, Column: 2, Rule: "A"}},
		[]Directive{{Line: 3, Kind: KindDisable, Labels: []string{"A"}, Reason: "r"}})
	if len(j2.Suppressed) != 1 || !findIssue(j2, 3, "", IssueUnclosedRange) {
		t.Fatalf("未闭合区间判定错误: sup=%d issues=%v", len(j2.Suppressed), issueStrings(j2))
	}
}

// TestSameLineDirectives 同一行多条指令：归属取登记次序最早者。
func TestSameLineDirectives(t *testing.T) {
	dirs := []Directive{
		{Line: 2, Kind: KindThisLine, Labels: []string{"A"}, Reason: "first"},
		{Line: 2, Kind: KindThisLine, Labels: []string{"A"}, Reason: "second"},
	}
	j := runCase(t, "同行多指令", 3, rulesA, false,
		[]Diagnostic{{Line: 2, Column: 1, Rule: "A"}}, dirs)
	if len(j.Suppressed) != 1 || j.Suppressed[0].Attribution.DirectiveOrder != 0 {
		t.Fatalf("归属应取登记次序最早: %+v", j.Suppressed)
	}
}

// TestAllTagIndependence “全部”与具体规则标签互不影响、独立生效。
func TestAllTagIndependence(t *testing.T) {
	dirs := []Directive{
		{Line: 2, Kind: KindDisable, Labels: []string{AllTag}, Reason: "r"},
		{Line: 3, Kind: KindEnable, Labels: []string{"B"}, Reason: "r"},
	}
	diags := []Diagnostic{{Line: 3, Column: 1, Rule: "B"}, {Line: 3, Column: 2, Rule: "C"}}
	j := runCase(t, "全部与具体规则互不影响", 4, rulesA, false, diags, dirs)
	if len(j.Suppressed) != 2 {
		t.Fatalf("全部标签独立，两条都应被抑制: sup=%v kept=%v", suppressedKeys(j), keptKeys(j))
	}
	for _, s := range j.Suppressed {
		if s.Attribution.Label != AllTag {
			t.Fatalf("归属标签应为全部: %s", s.Attribution.Label)
		}
	}
	j2 := runCase(t, "空标签等同全部", 2, rulesA, false,
		[]Diagnostic{{Line: 1, Column: 1, Rule: "X"}},
		[]Directive{{Line: 1, Kind: KindThisLine, Labels: nil, Reason: "r"}})
	if len(j2.Suppressed) != 1 || j2.Suppressed[0].Attribution.Label != AllTag {
		t.Fatalf("空标签应作为全部生效: %+v", j2.Suppressed)
	}
}
