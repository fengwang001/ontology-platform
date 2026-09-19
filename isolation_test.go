package projection

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
)

func TestProjectDoesNotMutateInput(t *testing.T) {
	obj := map[string]any{
		"name": "Alice",
		"addr": map[string]any{"city": "NYC", "geo": map[string]any{"lat": 1.0}},
		"tags": []any{"a", "b"},
	}
	want := deepCopyForTest(obj)
	rs, _ := Compile(Config{DefaultAllow: true, Deny: []string{"addr.city", "tags"}})
	out, err := rs.Project(obj, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(obj, want) {
		t.Fatalf("input was mutated:\n got %#v\nwant %#v", obj, want)
	}
	// Mutating every level of the result must not reach the input.
	out["name"] = "Bob"
	out["addr"].(map[string]any)["geo"].(map[string]any)["lat"] = 99.0
	if obj["name"] != "Alice" {
		t.Fatal("top-level result mutation reached input")
	}
	if obj["addr"].(map[string]any)["geo"].(map[string]any)["lat"] != 1.0 {
		t.Fatal("nested result mutation reached input")
	}
}

func TestResultMapsAndSlicesIndependent(t *testing.T) {
	obj := map[string]any{
		"addr": map[string]any{"geo": map[string]any{"lat": 1.0}},
		"tags": []any{map[string]any{"k": "v"}},
	}
	rs, _ := Compile(Config{DefaultAllow: true})
	a, _ := rs.Project(obj, nil)
	b, _ := rs.Project(obj, nil)
	a["addr"].(map[string]any)["geo"].(map[string]any)["lat"] = 2.0
	a["tags"].([]any)[0].(map[string]any)["k"] = "changed"
	if b["addr"].(map[string]any)["geo"].(map[string]any)["lat"] != 1.0 {
		t.Fatal("two projections share nested map storage")
	}
	if b["tags"].([]any)[0].(map[string]any)["k"] != "v" {
		t.Fatal("two projections share nested slice storage")
	}
}

func TestConcurrentProjectionWithTwoRulesets(t *testing.T) {
	obj := map[string]any{
		"name":  "Alice",
		"email": "a@example.com",
		"ssn":   "000",
		"addr":  map[string]any{"city": "NYC", "zip": "10001"},
	}
	rsA, _ := Compile(Config{Allow: []string{"name", "addr.city", "addr.zip"}})
	rsB, _ := Compile(Config{DefaultAllow: true, Deny: []string{"ssn", "addr.zip"}})

	const goroutines = 64
	const iterations = 50
	var wg sync.WaitGroup
	errs := make(chan error, goroutines*2)

	project := func(rs *Ruleset, check func(map[string]any) error) {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			out, err := rs.Project(obj, nil)
			if err != nil {
				errs <- err
				return
			}
			if err := check(out); err != nil {
				errs <- err
				return
			}
			// Mutate the result aggressively; no other projection may see it.
			for k := range out {
				out[k] = "polluted"
			}
		}
	}

	for g := 0; g < goroutines; g++ {
		wg.Add(2)
		go project(rsA, func(out map[string]any) error {
			if out["name"] != "Alice" {
				return fmt.Errorf("A: name missing or polluted: %v", out["name"])
			}
			if _, ok := out["email"]; ok {
				return fmt.Errorf("A: email must be hidden")
			}
			if _, ok := out["ssn"]; ok {
				return fmt.Errorf("A: ssn must be hidden")
			}
			return nil
		})
		go project(rsB, func(out map[string]any) error {
			if out["email"] != "a@example.com" {
				return fmt.Errorf("B: email must be visible: %v", out["email"])
			}
			if _, ok := out["ssn"]; ok {
				return fmt.Errorf("B: ssn must be hidden")
			}
			addr := out["addr"].(map[string]any)
			if _, ok := addr["zip"]; ok {
				return fmt.Errorf("B: addr.zip must be hidden")
			}
			if addr["city"] != "NYC" {
				return fmt.Errorf("B: addr.city polluted")
			}
			return nil
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

func TestConcurrentExplainSharesCompiledRuleset(t *testing.T) {
	rs, _ := Compile(Config{
		Allow: []string{"a.b.c"},
		Deny:  []string{"a", "a.b"},
	})
	var wg sync.WaitGroup
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				d := rs.Explain("a.b.c")
				if d.Visible || d.OverriddenBy != "a.b" {
					t.Errorf("unstable decision under concurrency: %+v", d)
					return
				}
			}
		}()
	}
	wg.Wait()
}

func deepCopyForTest(v any) any {
	switch t := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(t))
		for k, c := range t {
			m[k] = deepCopyForTest(c)
		}
		return m
	case []any:
		s := make([]any, len(t))
		for i, c := range t {
			s[i] = deepCopyForTest(c)
		}
		return s
	default:
		return v
	}
}
