package ontology

import (
	"fmt"
	"math/rand/v2"
	"sort"
	"testing"
)

type naiveState struct {
	specs    map[string]TypeSpec
	layouts  map[string]Layout
	versions map[string]int
	recalcs  map[string]int
}

func newNaiveState(config Config) *naiveState {
	return &naiveState{
		specs:    map[string]TypeSpec{},
		layouts:  map[string]Layout{},
		versions: map[string]int{},
		recalcs:  map[string]int{},
	}
}

func (s *naiveState) recompute(config Config) {
	remaining := map[string]map[string]bool{}
	for name, spec := range s.specs {
		deps := map[string]bool{}
		if !spec.Basic {
			for _, field := range spec.Fields {
				if !field.Pointer {
					deps[field.Type] = true
				}
			}
		}
		remaining[name] = deps
	}
	layouts := make(map[string]Layout, len(s.specs))
	for len(remaining) > 0 {
		var readyName string
		var readySpec TypeSpec
		progress := false
		for name, deps := range remaining {
			ready := true
			for dep := range deps {
				if _, done := layouts[dep]; !done {
					ready = false
					break
				}
			}
			if ready {
				readyName = name
				readySpec = s.specs[name]
				progress = true
				break
			}
		}
		if progress {
			var layout Layout
			if readySpec.Basic {
				layout = Layout{Name: readyName, Size: readySpec.Size, Align: readySpec.Alignment}
			} else {
				lookup := func(target string) (Layout, bool) {
					targetLayout, ok := layouts[target]
					return targetLayout, ok
				}
				var err error
				layout, err = calculateLayout(config, readySpec, lookup)
				if err != nil {
					panic(err)
				}
			}
			layout.Version = s.versions[readyName]
			layouts[readyName] = layout
			delete(remaining, readyName)
		}
		if !progress {
			panic("direct embedding cycle in accepted model")
		}
	}
	s.layouts = layouts
}

func (s *naiveState) register(config Config, spec TypeSpec) {
	s.specs[spec.Name] = spec
	s.recompute(config)
	s.versions[spec.Name] = 1
	s.layouts[spec.Name] = s.layouts[spec.Name]
}

func (s *naiveState) modify(config Config, root string, spec TypeSpec) map[string]Compatibility {
	oldLayouts := map[string]Layout{}
	for name, layout := range s.layouts {
		oldLayouts[name] = layout
	}
	oldVersions := map[string]int{}
	for name, version := range s.versions {
		oldVersions[name] = version
	}
	oldRecalcs := map[string]int{}
	for name, count := range s.recalcs {
		oldRecalcs[name] = count
	}

	s.specs[root] = spec
	s.versions = oldVersions
	s.recalcs = oldRecalcs
	s.recompute(config)
	results := map[string]Compatibility{root: compareLayouts(oldLayouts[root], s.layouts[root])}
	changed := map[string]bool{root: results[root] != FullyCompatible}
	s.recalcs[root]++
	if results[root] != FullyCompatible {
		s.versions[root]++
	}

	order := naiveDependentsOrder(root, s.specs)
	for _, name := range order {
		hasChangedDependency := false
		for _, field := range s.specs[name].Fields {
			if !field.Pointer && changed[field.Type] {
				hasChangedDependency = true
				break
			}
		}
		if !hasChangedDependency {
			continue
		}
		result := compareLayouts(oldLayouts[name], s.layouts[name])
		results[name] = result
		changed[name] = result != FullyCompatible
		s.recalcs[name]++
		if result != FullyCompatible {
			s.versions[name]++
		}
	}
	for name := range s.layouts {
		layout := s.layouts[name]
		layout.Version = s.versions[name]
		s.layouts[name] = layout
	}
	return results
}

func naiveDependentsOrder(root string, specs map[string]TypeSpec) []string {
	closure := map[string]bool{root: true}
	frontier := []string{root}
	for len(frontier) > 0 {
		current := frontier[0]
		frontier = frontier[1:]
		for name, spec := range specs {
			if closure[name] || spec.Basic {
				continue
			}
			for _, field := range spec.Fields {
				if !field.Pointer && field.Type == current {
					closure[name] = true
					frontier = append(frontier, name)
					break
				}
			}
		}
	}
	remaining := map[string]map[string]bool{}
	for name := range closure {
		deps := map[string]bool{}
		if !specs[name].Basic {
			for _, field := range specs[name].Fields {
				if !field.Pointer && closure[field.Type] {
					deps[field.Type] = true
				}
			}
		}
		remaining[name] = deps
	}
	order := []string{}
	for len(remaining) > 0 {
		ready := make([]string, 0)
		for name, deps := range remaining {
			if len(deps) == 0 {
				ready = append(ready, name)
			}
		}
		sort.Strings(ready)
		current := ready[0]
		order = append(order, current)
		delete(remaining, current)
		for name := range remaining {
			delete(remaining[name], current)
		}
	}
	return order
}

func randomSpec(random *rand.Rand, name string, typeNames []string) TypeSpec {
	if random.IntN(3) == 0 {
		return basic(name, 1+random.IntN(8), []int{1, 2, 4, 8}[random.IntN(4)])
	}
	fieldCount := random.IntN(4)
	fields := make([]Field, fieldCount)
	for index := range fields {
		target := typeNames[random.IntN(len(typeNames))]
		fields[index] = Field{
			Name:    fmt.Sprintf("f%d", index),
			Type:    target,
			Pointer: random.IntN(5) < 3,
			Compact: random.IntN(4) == 0,
		}
	}
	return TypeSpec{Name: name, MaxAlign: []int{0, 0, 0, 2, 4}[random.IntN(5)], Fields: fields}
}

func TestRandomSequenceAgainstNaiveModel(t *testing.T) {
	config := testConfig(96)
	logger := &testLogger{}
	config.Logger = logger
	r := NewRegistry(config)
	model := newNaiveState(config)
	random := rand.New(rand.NewPCG(42, 99))
	typeNames := make([]string, 10)
	for index := range typeNames {
		typeNames[index] = fmt.Sprintf("T%d", index)
	}

	for iteration := 0; iteration < 300; iteration++ {
		name := typeNames[random.IntN(len(typeNames))]
		spec := randomSpec(random, name, typeNames)
		_, existed := r.View(name)
		var err error
		changeResult := ChangeResult{}
		if existed {
			changeResult, err = r.Modify(name, spec)
		} else {
			err = r.Register(spec)
		}
		if err == nil {
			if existed {
				model.modify(config, name, spec)
			} else {
				model.register(config, spec)
			}
		}

		if len(r.Views()) != len(model.specs) {
			t.Fatalf("iteration %d: accepted type count = %d, model %d", iteration, len(r.Views()), len(model.specs))
		}
		for typeName, modelLayout := range model.layouts {
			actual, ok := r.Layout(typeName)
			if !ok {
				t.Fatalf("iteration %d: %s missing", iteration, typeName)
			}
			modelLayout.Version = model.versions[typeName]
			if actual.Size != modelLayout.Size || actual.Align != modelLayout.Align || actual.Version != modelLayout.Version {
				t.Fatalf("iteration %d type %s layout = %+v, model %+v", iteration, typeName, actual, modelLayout)
			}
			if len(actual.Fields) != len(modelLayout.Fields) {
				t.Fatalf("iteration %d type %s field count", iteration, typeName)
			}
			for index := range actual.Fields {
				if actual.Fields[index] != modelLayout.Fields[index] {
					t.Fatalf("iteration %d type %s field %d = %+v, model %+v", iteration, typeName, index, actual.Fields[index], modelLayout.Fields[index])
				}
			}
			view, _ := r.View(typeName)
			if view.Version != model.versions[typeName] || view.Recalculations != model.recalcs[typeName] {
				t.Fatalf("iteration %d trigger=%s type %s view = %+v, model version %d recalc %d; result=%+v", iteration, name, typeName, view, model.versions[typeName], model.recalcs[typeName], changeResult)
			}
		}
	}
}

func BenchmarkSingleLayoutWithUnrelatedTypes(b *testing.B) {
	for _, unrelated := range []int{100, 10000} {
		b.Run(fmt.Sprintf("unrelated-%d", unrelated), func(b *testing.B) {
			r := NewRegistry(testConfig(1 << 20))
			for index := 0; index < unrelated; index++ {
				name := fmt.Sprintf("U%d", index)
				if err := r.Register(basic(name, 8, 8)); err != nil {
					b.Fatal(err)
				}
			}
			spec := TypeSpec{Name: "Target"}
			for index := 0; index < 16; index++ {
				name := fmt.Sprintf("F%d", index)
				if err := r.Register(basic(name, 4, 4)); err != nil {
					b.Fatal(err)
				}
				spec.Fields = append(spec.Fields, Field{Name: name, Type: name})
			}
			b.ResetTimer()
			for index := 0; index < b.N; index++ {
				if _, err := calculateLayout(r.config, spec, func(name string) (Layout, bool) {
					record, ok := r.types[name]
					return record.layout, ok
				}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkModifyWithUnrelatedTypes(b *testing.B) {
	for _, unrelated := range []int{100, 10000} {
		b.Run(fmt.Sprintf("unrelated-%d", unrelated), func(b *testing.B) {
			r := NewRegistry(testConfig(1 << 20))
			if err := r.Register(basic("u32", 4, 4)); err != nil {
				b.Fatal(err)
			}
			aOne := TypeSpec{Name: "A", Fields: []Field{{Name: "x", Type: "u32"}, {Name: "y", Type: "u32"}}}
			aTwo := TypeSpec{Name: "A", Fields: []Field{{Name: "y", Type: "u32"}, {Name: "x", Type: "u32"}}}
			if err := r.Register(aOne); err != nil {
				b.Fatal(err)
			}
			if err := r.Register(TypeSpec{Name: "B", Fields: []Field{{Name: "a", Type: "A"}}}); err != nil {
				b.Fatal(err)
			}
			for index := 0; index < unrelated; index++ {
				if err := r.Register(basic(fmt.Sprintf("U%d", index), 8, 8)); err != nil {
					b.Fatal(err)
				}
			}
			b.ResetTimer()
			for index := 0; index < b.N; index++ {
				spec := aOne
				if index%2 == 1 {
					spec = aTwo
				}
				if _, err := r.Modify("A", spec); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
