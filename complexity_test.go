package ontology

import (
	"strconv"
	"testing"
)

func BenchmarkHitStableAsRegistryGrows(b *testing.B) {
	for _, size := range []int{1000, 4000} {
		b.Run(strconv.Itoa(size/1000)+"k", func(b *testing.B) {
			registry := NewRegistry(Config{})
			if err := registry.RegisterDefinition(Definition{Name: "Leaf", Params: []Param{{Name: "t"}}, Body: leafBody}); err != nil {
				b.Fatal(err)
			}
			for i := 0; i < size; i++ {
				if _, err := registry.Instantiate("Leaf", Named("filler"+strconv.Itoa(i))); err != nil {
					b.Fatal(err)
				}
			}
			hot, err := registry.Instantiate("Leaf", Named("hot"))
			if err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				result, err := registry.Instantiate("Leaf", Alias("hot-alias", Named("hot")))
				if err != nil || !result.Hit || result.Instance.ID != hot.Instance.ID {
					b.Fatalf("invalid hot hit: %+v %v", result, err)
				}
			}
		})
	}
}

func BenchmarkUpdateOnlyTouchesAffectedGraph(b *testing.B) {
	for _, unrelated := range []int{1000, 4000} {
		b.Run(strconv.Itoa(unrelated/1000)+"k-unrelated", func(b *testing.B) {
			registry := NewRegistry(Config{})
			for _, def := range []Definition{
				{Name: "Filler", Params: []Param{{Name: "t"}}, Body: leafBody},
				{Name: "Leaf", Params: []Param{{Name: "t"}}, Body: leafBody},
				{Name: "Box", Params: []Param{{Name: "t"}}, Body: boxBody},
			} {
				if err := registry.RegisterDefinition(def); err != nil {
					b.Fatal(err)
				}
			}
			for i := 0; i < unrelated; i++ {
				if _, err := registry.Instantiate("Filler", Named("filler"+strconv.Itoa(i))); err != nil {
					b.Fatal(err)
				}
			}
			affected, err := registry.Instantiate("Box", Named("affected"))
			if err != nil {
				b.Fatal(err)
			}
			updated := Definition{Name: "Leaf", Params: []Param{{Name: "t"}}, Body: leafBody}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := registry.UpdateDefinition(updated); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			instance, ok := registry.Get(affected.Instance.ID)
			if !ok || !instance.Stale {
				b.Fatalf("affected instance was not marked: %+v", instance)
			}
		})
	}
}
