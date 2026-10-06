package suppress

import (
	"math/rand"
	"reflect"
	"strconv"
	"sync"
	"testing"
)

func TestConcurrentRegistrationAndConsistentDecisions(t *testing.T) {
	processor := NewProcessor(200, []string{"A", "B", "C"}, false)
	var waitGroup sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		waitGroup.Add(1)
		go func(worker int) {
			defer waitGroup.Done()
			for i := 0; i < 50; i++ {
				line := 1 + (worker*50+i)%200
				_ = processor.RegisterDiagnostic(Diagnostic{Line: line, Column: 1 + worker, Rule: []string{"A", "B", "C"}[worker%3]})
				_ = processor.RegisterDirective(Directive{
					Line:   line,
					Kind:   []DirectiveKind{KindLine, KindNextLine, KindDisable, KindEnable, KindFile}[worker%5],
					Tags:   []string{[]string{"A", "B", "C", AllRules}[worker%4]},
					Reason: "concurrent registration",
				})
			}
		}(worker)
	}

	for reader := 0; reader < 4; reader++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			if processor.Decide() == nil {
				t.Error("Decide returned nil during concurrent registration")
			}
		}()
	}
	waitGroup.Wait()
	first := processor.Decide()
	if !reflect.DeepEqual(first, processor.Decide()) {
		t.Fatal("Decide is not stable after registration completes")
	}
}

func TestRepeatedDecisionsAreStable(t *testing.T) {
	input := generateRandomInput(rand.New(rand.NewSource(99)))
	processor := NewProcessor(input.totalLines, input.knownRules, input.requireReason)
	for _, diagnostic := range input.diagnostics {
		if err := processor.RegisterDiagnostic(diagnostic); err != nil {
			t.Fatal(err)
		}
	}
	for _, directive := range input.directives {
		_ = processor.RegisterDirective(directive)
	}
	first := processor.Decide()
	for i := 0; i < 10; i++ {
		if !reflect.DeepEqual(first, processor.Decide()) {
			t.Fatal("Decide is not repeatable")
		}
	}
}

func BenchmarkDecide(b *testing.B) {
	for _, size := range []int{100, 1000, 10000} {
		b.Run("size="+strconv.Itoa(size), func(b *testing.B) {
			processor := NewProcessor(size, []string{"A", "B", "C"}, false)
			for i := 0; i < size; i++ {
				line := 1 + i%size
				_ = processor.RegisterDiagnostic(Diagnostic{Line: line, Column: 1, Rule: []string{"A", "B", "C"}[i%3]})
				kind := []DirectiveKind{KindLine, KindNextLine, KindDisable, KindEnable, KindFile}[i%5]
				_ = processor.RegisterDirective(Directive{Line: line, Kind: kind, Tags: []string{AllRules}, Reason: "benchmark"})
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = processor.Decide()
			}
		})
	}
}
