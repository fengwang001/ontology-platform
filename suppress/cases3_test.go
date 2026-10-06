package suppress

import "testing"

// TestAttributionWinner 多条同时命中时的归属选择：
// 指令行号最小优先；并列取登记次序；同一指令具体规则标签先于“全部”。
func TestAttributionWinner(t *testing.T) {
	// 行1的全文件 vs 行2的本行：行1更小，归属全文件指令。
	j := runCase(t, "归属取最小指令行", 4, rulesA, false,
		[]Diagnostic{{Line: 2, Column: 1, Rule: "A"}},
		[]Directive{
			{Line: 1, Kind: KindWholeFile, Labels: []string{AllTag}, Reason: "r"},
			{Line: 2, Kind: KindThisLine, Labels: []string{"A"}, Reason: "r"},
		})
	if len(j.Suppressed) != 1 || j.Suppressed[0].Attribution.DirectiveOrder != 0 {
		t.Fatalf("应归属行1的全文件指令: %+v", j.Suppressed)
	}

	// 同一指令同时含具体标签与“全部”：具体规则先于全部。
	j2 := runCase(t, "同指令具体先于全部", 2, rulesA, false,
		[]Diagnostic{{Line: 1, Column: 1, Rule: "A"}},
		[]Directive{{Line: 1, Kind: KindThisLine, Labels: []string{AllTag, "A"}, Reason: "r"}})
	if len(j2.Suppressed) != 1 || j2.Suppressed[0].Attribution.Label != "A" {
		t.Fatalf("同指令应优先具体标签: %+v", j2.Suppressed)
	}

	// 归属者使用计数：只有归属者算使用，被同一条诊断命中但未归属的标签报未使用。
	j3 := runCase(t, "未归属命中者算未使用", 2, rulesA, false,
		[]Diagnostic{{Line: 1, Column: 1, Rule: "A"}},
		[]Directive{
			{Line: 1, Kind: KindThisLine, Labels: []string{"A"}, Reason: "r"},
			{Line: 1, Kind: KindThisLine, Labels: []string{AllTag}, Reason: "r"},
		})
	if len(j3.Suppressed) != 1 || j3.Suppressed[0].Attribution.Label != "A" {
		t.Fatalf("具体标签应归属: %+v", j3.Suppressed)
	}
	if !findIssue(j3, 1, AllTag, IssueUnused) {
		t.Fatalf("未归属的全部标签应报未使用: %v", issueStrings(j3))
	}
	if findIssue(j3, 1, "A", IssueUnused) {
		t.Fatalf("归属者不应报未使用: %v", issueStrings(j3))
	}
}

// TestOutputOrdering 输出排序：诊断按（行，列，规则）；
// 完全相同诊断保持登记次序；问题按（行，标签，优先级）且指令级在前。
func TestOutputOrdering(t *testing.T) {
	j := runCase(t, "输出排序", 6, rulesA, false,
		[]Diagnostic{
			{Line: 2, Column: 9, Rule: "C"},
			{Line: 1, Column: 1, Rule: "B"},
			{Line: 1, Column: 1, Rule: "B"}, // 完全相同
			{Line: 1, Column: 1, Rule: "A"},
		},
		[]Directive{
			{Line: 5, Kind: KindDisable, Labels: []string{"A", AllTag}, Reason: "r"}, // 未闭合
			{Line: 1, Kind: KindEnable, Labels: []string{"C"}, Reason: "r"},          // 孤立
		})
	wantKept := []string{"1:1:A", "1:1:B", "1:1:B", "2:9:C"}
	if got := keptKeys(j); !equalStrings(got, wantKept) {
		t.Fatalf("保留排序错误: %v", got)
	}
	if j.Kept[1].Index >= j.Kept[2].Index {
		t.Fatalf("完全相同诊断应保持登记次序: %+v", j.Kept)
	}
	// 问题次序：行1标签C(孤立) 先于 行5；行5指令级 UNCLOSED 排在其标签级问题之前。
	if len(j.Issues) < 1 || j.Issues[0].Code != IssueOrphanEnable ||
		j.Issues[0].DirectiveLine != 1 || j.Issues[0].Label != "C" {
		t.Fatalf("问题排序头部错误: %v", issueStrings(j))
	}
	posUnclosed, posA, posAll := -1, -1, -1
	for i, is := range j.Issues {
		if is.DirectiveLine == 5 && is.Code == IssueUnclosedRange {
			posUnclosed = i
		}
		if is.DirectiveLine == 5 && is.Label == "A" {
			posA = i
		}
		if is.DirectiveLine == 5 && is.Label == AllTag {
			posAll = i
		}
	}
	if posUnclosed < 0 || posUnclosed > posA || posUnclosed > posAll {
		t.Fatalf("未闭合应为指令级且排在标签级之前: %v", issueStrings(j))
	}
	if posA < 0 || posAll < 0 || posAll < posA {
		t.Fatalf("标签应按字典序（A < 全部）: %v", issueStrings(j))
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestRegistrationErrors 登记错误：参数非法优先于重复；被拒绝登记不改变状态。
func TestRegistrationErrors(t *testing.T) {
	s := NewSession(3, rulesA, false)
	mustInvalid := func(name string, err *RegError) {
		t.Helper()
		if err == nil || err.Code != ErrInvalidParameter {
			t.Fatalf("%s: 期望参数非法, got %v", name, err)
		}
	}
	mustInvalid("诊断行0", s.AddDiagnostic(Diagnostic{Line: 0, Column: 1, Rule: "A"}))
	mustInvalid("诊断行超界", s.AddDiagnostic(Diagnostic{Line: 4, Column: 1, Rule: "A"}))
	mustInvalid("诊断列0", s.AddDiagnostic(Diagnostic{Line: 1, Column: 0, Rule: "A"}))
	mustInvalid("诊断空规则", s.AddDiagnostic(Diagnostic{Line: 1, Column: 1, Rule: ""}))
	mustInvalid("指令行0", s.AddDirective(Directive{Line: 0, Kind: KindThisLine}))
	mustInvalid("未知种类", s.AddDirective(Directive{Line: 1, Kind: Kind(99)}))

	base := Directive{Line: 1, Kind: KindThisLine, Labels: []string{"A", "A", "B"}, Reason: "why"}
	if err := s.AddDirective(base); err != nil {
		t.Fatalf("首次登记应接受: %v", err)
	}
	// 完全相同（标签集合相同、次序无关、重复标签去重）应判重复。
	dup := Directive{Line: 1, Kind: KindThisLine, Labels: []string{"B", "A"}, Reason: "why"}
	if err := s.AddDirective(dup); err == nil || err.Code != ErrDuplicate {
		t.Fatalf("标签集合相同应判重复, got %v", err)
	}
	// 理由不同则不是重复。
	if err := s.AddDirective(Directive{Line: 1, Kind: KindThisLine, Labels: []string{"A", "B"}, Reason: "other"}); err != nil {
		t.Fatalf("理由不同不应判重复: %v", err)
	}
	// 参数非法优先于重复：即使内容与已接受者相同，越界也报参数非法。
	bad := base
	bad.Line = 9
	if err := s.AddDirective(bad); err == nil || err.Code != ErrInvalidParameter {
		t.Fatalf("非法参数应优先于重复判定, got %v", err)
	}

	// 被拒绝登记不得改变状态：只有 2 条被接受指令、0 条诊断。
	j := s.Evaluate()
	if len(j.Kept) != 0 || len(j.Suppressed) != 0 {
		t.Fatalf("被拒绝诊断不应入会话: %+v", j)
	}
	if countAcceptedDirectives(s) != 2 {
		t.Fatalf("应有且仅有 2 条接受指令")
	}

	// 判定可重复调用且结果一致。
	j2 := s.Evaluate()
	if len(j.Issues) != len(j2.Issues) {
		t.Fatalf("重复判定结果应一致")
	}
}

func countAcceptedDirectives(s *Session) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.directives)
}
