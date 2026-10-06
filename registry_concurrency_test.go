package deadcode

import (
	"sync"
	"testing"
)

func TestRegistryConcurrentRegistrationAndSnapshot(t *testing.T) {
	registry := NewRegistry()
	base := Module{
		ID: "entry",
		Declarations: []Declaration{
			{Name: "seed", References: []string{"dep"}},
		},
		Imports: []Import{{
			TargetModule: "dep",
			Bindings:     []ImportBinding{{LocalName: "dep", ImportedName: "dep"}},
		}},
		Exports: []Export{LocalExport("seed", "seed")},
	}
	if err := registry.Register(base); err != nil {
		t.Fatal(err)
	}

	session := registry.NewSession([]string{"entry"})

	var wait sync.WaitGroup
	for index := 0; index < 32; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			id := "m" + string(rune('a'+index%26)) + string(rune('a'+(index/26)%26))
			_ = registry.Register(Module{ID: id})
		}(index)
	}
	wait.Wait()

	_, err := session.Solve()
	if err == nil || err.(*AnalysisError).Code != UnknownModule {
		t.Fatalf("snapshot solve error = %v, want unknown module from frozen snapshot", err)
	}

	if err := registry.Register(Module{
		ID:           "dep",
		Declarations: []Declaration{{Name: "dep"}},
		Exports:      []Export{LocalExport("dep", "dep")},
	}); err != nil {
		t.Fatal(err)
	}

	result, err := registry.NewSession([]string{"entry"}).Solve()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("input base=%#v concurrentWriters=32 output=%#v basis=session reads its creation-time deep-copy snapshot", base, result)
	if got := result.Reasons[DeclarationID{ModuleID: "dep", Name: "dep"}]; got != ReasonReferenced {
		t.Fatalf("dep reason = %q, want %q", got, ReasonReferenced)
	}
}
