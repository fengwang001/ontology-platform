package certselector

import (
	"fmt"
	"testing"
)

func populateBenchmarkSelector(b *testing.B, unrelated int, sameName int) *Selector {
	b.Helper()
	selector := New()
	for i := 0; i < unrelated; i++ {
		name := fmt.Sprintf("host-%06d.example.net", i)
		if err := selector.Add(cert(fmt.Sprintf("unrelated-%06d", i), []string{name}, KeyTypeRSA, 0, 1_000_000)); err != nil {
			b.Fatalf("Add(unrelated): %v", err)
		}
	}
	for i := 0; i < sameName; i++ {
		if err := selector.Add(cert(fmt.Sprintf("matched-%06d", i), []string{"target.example.org"}, KeyTypeRSA, int64(i), int64(i+1_000_000))); err != nil {
			b.Fatalf("Add(matched): %v", err)
		}
	}
	return selector
}

func benchmarkSelectWithPopulation(b *testing.B, unrelated int, sameName int) {
	selector := populateBenchmarkSelector(b, unrelated, sameName)
	input := SelectInput{Name: "target.example.org", KeyTypes: keyTypes(KeyTypeRSA), Now: 500_000}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := selector.Select(input); err != nil {
			b.Fatalf("Select(): %v", err)
		}
	}
}

func BenchmarkSelectSmallPopulation(b *testing.B) {
	benchmarkSelectWithPopulation(b, 100, 20)
}

func BenchmarkSelectLargePopulation(b *testing.B) {
	benchmarkSelectWithPopulation(b, 100_000, 20)
}

func BenchmarkSelectLargeMatchedGroup(b *testing.B) {
	benchmarkSelectWithPopulation(b, 100_000, 2_000)
}
