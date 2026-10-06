package suppress

import (
	"errors"
	"reflect"
	"testing"
)

func TestRangeBoundariesAndSameLineDirectives(t *testing.T) {
	processor := NewProcessor(5, []string{"A", "B"}, false)
	mustRegisterDiagnostic(t, processor, Diagnostic{Line: 1, Column: 1, Rule: "A"})
	mustRegisterDiagnostic(t, processor, Diagnostic{Line: 2, Column: 1, Rule: "A"})
	mustRegisterDiagnostic(t, processor, Diagnostic{Line: 3, Column: 1, Rule: "A"})
	mustRegisterDiagnostic(t, processor, Diagnostic{Line: 4, Column: 1, Rule: "A"})
	mustRegisterDirective(t, processor, Directive{Line: 1, Kind: KindDisable, Tags: []string{"A"}})
	mustRegisterDirective(t, processor, Directive{Line: 4, Kind: KindEnable, Tags: []string{"A"}})
	mustRegisterDirective(t, processor, Directive{Line: 2, Kind: KindLine, Tags: []string{"B"}})
	mustRegisterDirective(t, processor, Directive{Line: 3, Kind: KindNextLine, Tags: []string{"A"}})

	report := processor.Decide()
	assertDiagnostics(t, report.Suppressed, []SuppressedDiagnostic{
		{Diagnostic: Diagnostic{Line: 1, Column: 1, Rule: "A"}, DirectiveLine: 1, Tag: "A"},
		{Diagnostic: Diagnostic{Line: 2, Column: 1, Rule: "A"}, DirectiveLine: 1, Tag: "A"},
		{Diagnostic: Diagnostic{Line: 3, Column: 1, Rule: "A"}, DirectiveLine: 1, Tag: "A"},
		{Diagnostic: Diagnostic{Line: 4, Column: 1, Rule: "A"}, DirectiveLine: 3, Tag: "A"},
	})
	assertDiagnostics(t, report.Kept, []Diagnostic(nil))
	assertHasIssue(t, report, DirectiveIssue{DirectiveLine: 2, Tag: "B", Category: IssueUnused})
}

func TestAllAndSpecificTagsDoNotInterfere(t *testing.T) {
	processor := NewProcessor(3, []string{"A", "B"}, false)
	mustRegisterDiagnostic(t, processor, Diagnostic{Line: 1, Column: 1, Rule: "A"})
	mustRegisterDiagnostic(t, processor, Diagnostic{Line: 2, Column: 1, Rule: "A"})
	mustRegisterDirective(t, processor, Directive{Line: 1, Kind: KindLine, Tags: []string{"A"}})
	mustRegisterDirective(t, processor, Directive{Line: 1, Kind: KindLine, Tags: []string{AllRules}})
	mustRegisterDirective(t, processor, Directive{Line: 2, Kind: KindDisable, Tags: []string{"A"}})
	mustRegisterDirective(t, processor, Directive{Line: 2, Kind: KindDisable, Tags: []string{AllRules}})
	mustRegisterDirective(t, processor, Directive{Line: 3, Kind: KindEnable, Tags: []string{"A"}})

	report := processor.Decide()
	if len(report.Suppressed) != 2 || report.Suppressed[0].Tag != "A" {
		t.Fatalf("specific tag must win on line 1, got %+v", report.Suppressed)
	}
	assertHasIssue(t, report, DirectiveIssue{DirectiveLine: 1, Tag: AllRules, Category: IssueUnused})
	assertHasIssue(t, report, DirectiveIssue{DirectiveLine: 2, Tag: "", Category: IssueUnclosedRange, InstructionLevel: true})
}

func TestDuplicateDisableOrphanEnableEmptyRangeAndUnknownRule(t *testing.T) {
	processor := NewProcessor(4, []string{"A"}, false)
	mustRegisterDirective(t, processor, Directive{Line: 1, Kind: KindEnable, Tags: []string{"A"}})
	mustRegisterDirective(t, processor, Directive{Line: 2, Kind: KindDisable, Tags: []string{"A"}})
	mustRegisterDirective(t, processor, Directive{Line: 3, Kind: KindDisable, Tags: []string{"A"}})
	mustRegisterDirective(t, processor, Directive{Line: 4, Kind: KindEnable, Tags: []string{"A"}})
	mustRegisterDirective(t, processor, Directive{Line: 1, Kind: KindLine, Tags: []string{"A", "BAD"}})

	report := processor.Decide()
	assertHasIssue(t, report, DirectiveIssue{DirectiveLine: 1, Tag: "A", Category: IssueOrphanEnable})
	assertHasIssue(t, report, DirectiveIssue{DirectiveLine: 3, Tag: "A", Category: IssueRedundantDisable})
	assertHasIssue(t, report, DirectiveIssue{DirectiveLine: 1, Tag: "BAD", Category: IssueUnknownRule})
	assertHasIssue(t, report, DirectiveIssue{DirectiveLine: 2, Tag: "A", Category: IssueUnused})
}

func TestMissingReasonInvalidatesWholeDirectiveAndNextLineAtEOF(t *testing.T) {
	processor := NewProcessor(2, []string{"A", "B"}, true)
	mustRegisterDiagnostic(t, processor, Diagnostic{Line: 1, Column: 1, Rule: "A"})
	mustRegisterDiagnostic(t, processor, Diagnostic{Line: 2, Column: 1, Rule: "B"})
	mustRegisterDirective(t, processor, Directive{Line: 1, Kind: KindLine, Tags: []string{"A", "B"}, Reason: " \t"})
	mustRegisterDirective(t, processor, Directive{Line: 2, Kind: KindNextLine, Tags: nil, Reason: "ok"})

	report := processor.Decide()
	assertDiagnostics(t, report.Kept, []Diagnostic{
		{Line: 1, Column: 1, Rule: "A"},
		{Line: 2, Column: 1, Rule: "B"},
	})
	assertHasIssue(t, report, DirectiveIssue{DirectiveLine: 1, Tag: "A", Category: IssueMissingReason})
	assertHasIssue(t, report, DirectiveIssue{DirectiveLine: 1, Tag: "B", Category: IssueMissingReason})
	assertHasIssue(t, report, DirectiveIssue{DirectiveLine: 2, Tag: AllRules, Category: IssueNoTargetLine})
}

func TestUnclosedRangeAndRegistrationErrors(t *testing.T) {
	processor := NewProcessor(2, []string{"A"}, false)
	directive := Directive{Line: 1, Kind: KindDisable, Tags: []string{"A"}, Reason: "why"}
	mustRegisterDirective(t, processor, directive)
	if err := processor.RegisterDirective(directive); !errors.Is(err, ErrDuplicateDirective) {
		t.Fatalf("expected duplicate directive, got %v", err)
	}
	if err := processor.RegisterDirective(Directive{Line: 3, Kind: KindDisable}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected invalid line, got %v", err)
	}
	if err := processor.RegisterDirective(Directive{Line: 1, Kind: "bad"}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected invalid kind, got %v", err)
	}
	if err := processor.RegisterDiagnostic(Diagnostic{Line: 1, Column: 0, Rule: "A"}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected invalid column, got %v", err)
	}
	report := processor.Decide()
	assertHasIssue(t, report, DirectiveIssue{DirectiveLine: 1, Tag: "", Category: IssueUnclosedRange, InstructionLevel: true})
	assertHasIssue(t, report, DirectiveIssue{DirectiveLine: 1, Tag: "A", Category: IssueUnused})
}

func mustRegisterDiagnostic(t *testing.T, processor *Processor, diagnostic Diagnostic) {
	t.Helper()
	if err := processor.RegisterDiagnostic(diagnostic); err != nil {
		t.Fatalf("register diagnostic %+v: %v", diagnostic, err)
	}
}

func mustRegisterDirective(t *testing.T, processor *Processor, directive Directive) {
	t.Helper()
	if err := processor.RegisterDirective(directive); err != nil {
		t.Fatalf("register directive %+v: %v", directive, err)
	}
}

func assertDiagnostics[T any](t *testing.T, got, want []T) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("diagnostics mismatch\ngot:  %+v\nwant: %+v", got, want)
	}
}

func assertHasIssue(t *testing.T, report *Report, want DirectiveIssue) {
	t.Helper()
	for _, issue := range report.Issues {
		if reflect.DeepEqual(issue, want) {
			return
		}
	}
	t.Fatalf("issue %+v not found in %+v", want, report.Issues)
}
