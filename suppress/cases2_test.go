package suppress

import (
	"reflect"
	"testing"
)

// TestRepeatedDisableAndOrphanEnable 重复禁用与孤立启用不影响已有区间。
func TestRepeatedDisableAndOrphanEnable(t *testing.T) {
	dirs := []Directive{
		{Line: 1, Kind: KindDisable, Labels: []string{"A"}, Reason: "r"},
		{Line: 2, Kind: KindDisable, Labels: []string{"A"}, Reason: "r"},
		{Line: 2, Kind: KindEnable, Labels: []string{"B"}, Reason: "r"},
		{Line: 3, Kind: KindEnable, Labels: []string{"A"}, Reason: "r"},
		{Line: 4, Kind: KindEnable, Labels: []string{"A"}, Reason: "r"},
	}
	j := runCase(t, "重复禁用与孤立启用", 5, rulesA, false,
		[]Diagnostic{{Line: 2, Column: 1, Rule: "A"}, {Line: 4, Column: 1, Rule: "A"}}, dirs)
	if got := suppressedKeys(j); !reflect.DeepEqual(got, []string{"2:1:A"}) {
		t.Fatalf("只有行2在区间内: %v", got)
	}
	if !findIssue(j, 2, "A", IssueRepeatedDisable) ||
		!findIssue(j, 2, "B", IssueOrphanEnable) ||
		!findIssue(j, 4, "A", IssueOrphanEnable) {
		t.Fatalf("配对问题缺失: %v", issueStrings(j))
	}

	// 同一行先启用再禁用：按登记次序，启用孤立、禁用开启新区间（最终未闭合）。
	j2 := runCase(t, "同行启用再禁用", 3, rulesA, false,
		[]Diagnostic{{Line: 1, Column: 1, Rule: "A"}},
		[]Directive{
			{Line: 1, Kind: KindEnable, Labels: []string{"A"}, Reason: "r"},
			{Line: 1, Kind: KindDisable, Labels: []string{"A"}, Reason: "r"},
		})
	if len(j2.Suppressed) != 1 ||
		!findIssue(j2, 1, "A", IssueOrphanEnable) ||
		!findIssue(j2, 1, "", IssueUnclosedRange) {
		t.Fatalf("同行启用/禁用配对错误: %v", issueStrings(j2))
	}
}

// TestUnused 各类作用范围在没有归属诊断时报未使用。
func TestUnused(t *testing.T) {
	dirs := []Directive{
		{Line: 1, Kind: KindDisable, Labels: []string{"A"}, Reason: "r"},
		{Line: 3, Kind: KindEnable, Labels: []string{"A"}, Reason: "r"},
	}
	j := runCase(t, "区间未使用", 4, rulesA, false,
		[]Diagnostic{{Line: 2, Column: 1, Rule: "B"}}, dirs)
	if !findIssue(j, 1, "A", IssueUnused) || len(j.Kept) != 1 {
		t.Fatalf("区间未使用判定错误: issues=%v kept=%v", issueStrings(j), keptKeys(j))
	}
	j2 := runCase(t, "区间已使用", 4, rulesA, false,
		[]Diagnostic{{Line: 2, Column: 1, Rule: "A"}}, dirs)
	if findIssue(j2, 1, "A", IssueUnused) {
		t.Fatalf("区间抑制过诊断不应报未使用: %v", issueStrings(j2))
	}
	j3 := runCase(t, "全文件未使用", 3, rulesA, false,
		[]Diagnostic{{Line: 1, Column: 1, Rule: "B"}},
		[]Directive{{Line: 2, Kind: KindWholeFile, Labels: []string{"A"}, Reason: "r"}})
	if !findIssue(j3, 2, "A", IssueUnused) {
		t.Fatalf("全文件标签全程未命中应报未使用: %v", issueStrings(j3))
	}
	j4 := runCase(t, "本行未使用", 3, rulesA, false,
		[]Diagnostic{{Line: 2, Column: 1, Rule: "A"}},
		[]Directive{{Line: 1, Kind: KindThisLine, Labels: []string{"A"}, Reason: "r"}})
	if !findIssue(j4, 1, "A", IssueUnused) {
		t.Fatalf("本行指令未命中应报未使用: %v", issueStrings(j4))
	}
	j5 := runCase(t, "启用不报未使用", 3, rulesA, false, nil,
		[]Directive{{Line: 1, Kind: KindEnable, Labels: []string{"A"}, Reason: "r"}})
	if !findIssue(j5, 1, "A", IssueOrphanEnable) || findIssue(j5, 1, "A", IssueUnused) {
		t.Fatalf("启用指令问题错误: %v", issueStrings(j5))
	}
}

// TestUnknownRuleMixed 未知标签忽略，同指令其他标签照常生效。
func TestUnknownRuleMixed(t *testing.T) {
	j := runCase(t, "未知规则混合", 3, rulesA, false,
		[]Diagnostic{{Line: 1, Column: 1, Rule: "A"}, {Line: 1, Column: 2, Rule: "B"}},
		[]Directive{{Line: 1, Kind: KindThisLine, Labels: []string{"A", "ZZ", "B"}, Reason: "r"}})
	if len(j.Suppressed) != 2 {
		t.Fatalf("A、B 应被抑制，ZZ 被忽略: sup=%v", suppressedKeys(j))
	}
	if !findIssue(j, 1, "ZZ", IssueUnknownRule) {
		t.Fatalf("应报 ZZ 未知规则: %v", issueStrings(j))
	}
	if findIssue(j, 1, "ZZ", IssueUnused) || findIssue(j, 1, "A", IssueUnknownRule) {
		t.Fatalf("未知标签不应报未使用，已知标签不应报未知: %v", issueStrings(j))
	}
}

// TestMissingReason 缺理由使整条指令失效。
func TestMissingReason(t *testing.T) {
	j := runCase(t, "缺理由禁用失效", 3, rulesA, true,
		[]Diagnostic{{Line: 2, Column: 1, Rule: "A"}},
		[]Directive{
			{Line: 1, Kind: KindDisable, Labels: []string{"A", "B"}, Reason: "  \t "},
			{Line: 2, Kind: KindEnable, Labels: []string{"A"}, Reason: "ok"},
		})
	if len(j.Suppressed) != 0 {
		t.Fatalf("缺理由指令不得抑制任何诊断: %v", suppressedKeys(j))
	}
	if !findIssue(j, 1, "A", IssueMissingReason) || !findIssue(j, 1, "B", IssueMissingReason) {
		t.Fatalf("缺理由应覆盖所有标签: %v", issueStrings(j))
	}
	if !findIssue(j, 2, "A", IssueOrphanEnable) {
		t.Fatalf("缺理由禁用不开启区间，后续启用应孤立: %v", issueStrings(j))
	}
	if findIssue(j, 1, "A", IssueUnused) || findIssue(j, 1, "A", IssueUnclosedRange) {
		t.Fatalf("缺理由槽位不应再有其他问题: %v", issueStrings(j))
	}
	j2 := runCase(t, "不要求理由时空白有效", 2, rulesA, false,
		[]Diagnostic{{Line: 1, Column: 1, Rule: "A"}},
		[]Directive{{Line: 1, Kind: KindThisLine, Labels: []string{"A"}, Reason: ""}})
	if len(j2.Suppressed) != 1 {
		t.Fatalf("不要求理由时空白理由应生效: kept=%v", keptKeys(j2))
	}
}

// TestNextLineLastLine 下一行指令在最后一行：无目标行，不生效。
func TestNextLineLastLine(t *testing.T) {
	j := runCase(t, "下一行位于末行", 3, rulesA, false,
		[]Diagnostic{{Line: 3, Column: 1, Rule: "A"}},
		[]Directive{{Line: 3, Kind: KindNextLine, Labels: []string{"A"}, Reason: "r"}})
	if len(j.Suppressed) != 0 || !findIssue(j, 3, "A", IssueNoTargetLine) {
		t.Fatalf("末行下一行应报无目标行且不生效: sup=%v issues=%v",
			suppressedKeys(j), issueStrings(j))
	}
	j2 := runCase(t, "下一行正常", 3, rulesA, false,
		[]Diagnostic{{Line: 2, Column: 1, Rule: "A"}},
		[]Directive{{Line: 1, Kind: KindNextLine, Labels: []string{"A"}, Reason: "r"}})
	if len(j2.Suppressed) != 1 {
		t.Fatalf("下一行应抑制行2: %v", suppressedKeys(j2))
	}
}
