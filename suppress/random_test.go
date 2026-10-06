package suppress

import (
	"errors"
	"math/rand"
	"reflect"
	"testing"
)

type randomInput struct {
	totalLines    int
	knownRules    []string
	requireReason bool
	diagnostics   []Diagnostic
	directives    []Directive
}

func TestRandomMatchesNaiveModel(t *testing.T) {
	seed := timeNowNanos()
	random := rand.New(rand.NewSource(seed))
	t.Logf("random seed: %d", seed)

	for iteration := 0; iteration < 200; iteration++ {
		input := generateRandomInput(random)
		processor := NewProcessor(input.totalLines, input.knownRules, input.requireReason)
		for _, diagnostic := range input.diagnostics {
			if err := processor.RegisterDiagnostic(diagnostic); err != nil {
				t.Fatalf("seed=%d iteration=%d diagnostic %+v: %v", seed, iteration, diagnostic, err)
			}
		}
		registeredDirectives := make([]Directive, 0, len(input.directives))
		for _, directive := range input.directives {
			err := processor.RegisterDirective(directive)
			if err == nil {
				registeredDirectives = append(registeredDirectives, directive)
				continue
			}
			if !errorsIs(err, ErrDuplicateDirective) {
				t.Fatalf("seed=%d iteration=%d directive %+v: %v", seed, iteration, directive, err)
			}
		}

		got := processor.Decide()
		want := naiveDecide(buildTestSnapshot(input.totalLines, input.knownRules, input.requireReason, input.diagnostics, registeredDirectives))
		logRandomCase(t, iteration, input, registeredDirectives, got, want)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("seed=%d iteration=%d report mismatch\ngot:  %+v\nwant: %+v", seed, iteration, *got, *want)
		}
	}
}

func generateRandomInput(random *rand.Rand) randomInput {
	totalLines := 1 + random.Intn(10)
	knownRules := []string{"A", "B", "C"}
	diagnosticCount := random.Intn(12)
	directiveCount := random.Intn(10)
	input := randomInput{
		totalLines:    totalLines,
		knownRules:    knownRules,
		requireReason: random.Intn(2) == 0,
	}
	for i := 0; i < diagnosticCount; i++ {
		input.diagnostics = append(input.diagnostics, Diagnostic{
			Line:   1 + random.Intn(totalLines),
			Column: 1 + random.Intn(5),
			Rule:   knownRules[random.Intn(len(knownRules))],
		})
	}
	kinds := []DirectiveKind{KindLine, KindNextLine, KindDisable, KindEnable, KindFile}
	for i := 0; i < directiveCount; i++ {
		tagPool := append([]string{AllRules}, knownRules...)
		if random.Intn(5) == 0 {
			tagPool = append(tagPool, "BAD")
		}
		tagCount := random.Intn(3)
		var tags []string
		for j := 0; j < tagCount; j++ {
			tags = append(tags, tagPool[random.Intn(len(tagPool))])
		}
		reason := "reason"
		if random.Intn(4) == 0 {
			reason = "  "
		}
		line := 1 + random.Intn(totalLines)
		kind := kinds[random.Intn(len(kinds))]
		directive := Directive{Line: line, Kind: kind, Tags: tags, Reason: reason}
		input.directives = append(input.directives, directive)
		if random.Intn(7) == 0 {
			input.directives = append(input.directives, directive)
		}
	}
	return input
}

func buildTestSnapshot(totalLines int, knownRules []string, requireReason bool, diagnostics []Diagnostic, directives []Directive) snapshot {
	ruleSet := make(map[string]struct{}, len(knownRules))
	for _, rule := range knownRules {
		ruleSet[rule] = struct{}{}
	}
	snap := snapshot{
		totalLines:    totalLines,
		requireReason: requireReason,
		knownRules:    ruleSet,
		diagnostics:   append([]Diagnostic(nil), diagnostics...),
	}
	for sequence, directive := range directives {
		snap.directives = append(snap.directives, acceptedDirective{
			line:     directive.Line,
			kind:     directive.Kind,
			tags:     normalizeTags(directive.Tags),
			reason:   directive.Reason,
			sequence: sequence,
		})
	}
	return snap
}

func logRandomCase(t *testing.T, iteration int, input randomInput, accepted []Directive, got, want *Report) {
	t.Helper()
	t.Logf("iteration=%d totalLines=%d knownRules=%v requireReason=%v", iteration, input.totalLines, input.knownRules, input.requireReason)
	for _, diagnostic := range input.diagnostics {
		t.Logf("input diagnostic line=%d column=%d rule=%s", diagnostic.Line, diagnostic.Column, diagnostic.Rule)
	}
	for _, directive := range accepted {
		t.Logf("input directive line=%d kind=%s tags=%v reason=%q", directive.Line, directive.Kind, normalizeTags(directive.Tags), directive.Reason)
	}
	t.Logf("decision kept=%+v", got.Kept)
	for _, diagnostic := range got.Suppressed {
		t.Logf("decision suppressed (%d,%d,%s) <- directive=%d tag=%s", diagnostic.Line, diagnostic.Column, diagnostic.Rule, diagnostic.DirectiveLine, diagnostic.Tag)
	}
	for _, issue := range got.Issues {
		t.Logf("decision issue directive=%d tag=%s category=%s instructionLevel=%v", issue.DirectiveLine, issue.Tag, issue.Category, issue.InstructionLevel)
	}
	t.Logf("reference kept=%+v suppressed=%+v issues=%+v", want.Kept, want.Suppressed, want.Issues)
}

func errorsIs(err error, target error) bool {
	return errors.Is(err, target)
}
