package deadcode

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

func TestRandomGraphsAgainstNaiveModel(t *testing.T) {
	seed := int64(1609)
	random := rand.New(rand.NewSource(seed))

	for iteration := 0; iteration < 80; iteration++ {
		modules, entries := generateRandomGraph(random, 3+random.Intn(10))
		t.Run(fmt.Sprintf("seed=%d-iteration=%d", seed, iteration), func(t *testing.T) {
			t.Logf("input modules=%#v entries=%#v basis=independent naive closure generation", modules, entries)

			registry := NewRegistry()
			for _, module := range modules {
				if err := registry.Register(module); err != nil {
					t.Fatalf("register: %v", err)
				}
			}

			result, err := registry.NewSession(entries).Solve()
			if err != nil {
				t.Fatalf("random valid graph produced error: %v", err)
			}
			naive := naiveSolve(modules, entries)
			t.Logf("output included=%#v kept=%#v basis=compare worklist result against repeated set closure", result.IncludedModules, result.Reasons)

			if !reflect.DeepEqual(result.IncludedModules, naive.included) {
				t.Fatalf("included = %#v, want %#v", result.IncludedModules, naive.included)
			}
			if !reflect.DeepEqual(result.Reasons, naive.reasons) {
				t.Fatalf("reasons = %#v, want %#v", result.Reasons, naive.reasons)
			}
		})
	}
}

type naiveState struct {
	included []string
	reasons  map[DeclarationID]KeepReason
}

func naiveSolve(modules []Module, entries []string) naiveState {
	byID := make(map[string]Module)
	for _, module := range modules {
		byID[module.ID] = module
	}

	included := make(map[string]bool)
	kept := make(map[DeclarationID]KeepReason)

	keep := func(decl DeclarationID, reason KeepReason) {
		current, exists := kept[decl]
		if !exists || reasonRank(reason) < reasonRank(current) {
			kept[decl] = reason
		}
	}

	exports := func(moduleID string, name string) (resolutionStatus, DeclarationID) {
		return naiveResolve(byID, moduleID, name, map[nameKey]struct{}{})
	}

	for {
		changed := false

		include := func(id string) {
			if !included[id] {
				included[id] = true
				changed = true
			}
		}

		for _, entry := range entries {
			include(entry)
		}

		for moduleID := range included {
			module := byID[moduleID]
			for _, statement := range module.Imports {
				if naiveSideEffects(byID[statement.TargetModule]) {
					include(statement.TargetModule)
				}
			}
		}

		for decl := range kept {
			module := byID[decl.ModuleID]
			if !naiveSideEffects(module) {
				include(decl.ModuleID)
			}
		}

		for _, entry := range entries {
			module := byID[entry]
			for _, export := range module.Exports {
				if export.Kind == LocalDeclarationExport || export.Kind == NamedReexport {
					if status, decl := exports(entry, export.Name); status == resolutionResolved {
						before := len(kept)
						keep(decl, ReasonEntryExport)
						if len(kept) != before || kept[decl] != ReasonEntryExport {
							changed = true
						}
					}
				}
			}
			wildcard := naiveWildcard(byID, entry, true, map[string]struct{}{})
			for name, decl := range wildcard.decls {
				if _, ambiguous := wildcard.ambiguous[name]; ambiguous {
					continue
				}
				if current, exists := kept[decl]; !exists || current != ReasonEntryExport {
					keep(decl, ReasonEntryExport)
					changed = true
				}
			}
		}

		for moduleID := range included {
			module := byID[moduleID]
			if naiveSideEffects(module) {
				for _, declaration := range module.Declarations {
					if declaration.HasSideEffects {
						decl := DeclarationID{ModuleID: moduleID, Name: declaration.Name}
						if current, exists := kept[decl]; !exists || reasonRank(ReasonSideEffect) < reasonRank(current) {
							keep(decl, ReasonSideEffect)
							changed = true
						}
					}
				}
			}
		}

		for decl := range kept {
			module := byID[decl.ModuleID]
			declaration := naiveFindDeclaration(module, decl.Name)
			for _, reference := range declaration.References {
				if local := naiveFindDeclaration(module, reference); local != nil {
					target := DeclarationID{ModuleID: decl.ModuleID, Name: reference}
					if current, exists := kept[target]; !exists || reasonRank(ReasonReferenced) < reasonRank(current) {
						keep(target, ReasonReferenced)
						changed = true
					}
					continue
				}
				targetModule, importedName, ok := naiveFindBinding(module, reference)
				if !ok {
					continue
				}
				if importedName == WildcardImportName {
					wildcard := naiveWildcard(byID, targetModule, true, map[string]struct{}{})
					for name, target := range wildcard.decls {
						if _, ambiguous := wildcard.ambiguous[name]; ambiguous {
							continue
						}
						if current, exists := kept[target]; !exists || reasonRank(ReasonReferenced) < reasonRank(current) {
							keep(target, ReasonReferenced)
							changed = true
						}
					}
					continue
				}
				if status, target := exports(targetModule, importedName); status == resolutionResolved {
					if current, exists := kept[target]; !exists || reasonRank(ReasonReferenced) < reasonRank(current) {
						keep(target, ReasonReferenced)
						changed = true
					}
				}
			}
		}

		if !changed {
			break
		}
	}

	moduleIDs := make([]string, 0, len(included))
	for id := range included {
		moduleIDs = append(moduleIDs, id)
	}
	sort.Strings(moduleIDs)
	return naiveState{included: moduleIDs, reasons: kept}
}

func naiveResolve(byID map[string]Module, moduleID, name string, active map[nameKey]struct{}) (resolutionStatus, DeclarationID) {
	key := nameKey{module: moduleID, name: name}
	if _, cycling := active[key]; cycling {
		return resolutionCycle, DeclarationID{}
	}

	module := byID[moduleID]
	for _, export := range module.Exports {
		if export.Kind != LocalDeclarationExport && export.Kind != NamedReexport || export.Name != name {
			continue
		}
		if export.Kind == LocalDeclarationExport {
			return resolutionResolved, DeclarationID{ModuleID: moduleID, Name: export.SourceName}
		}
		active[key] = struct{}{}
		return naiveResolve(byID, export.SourceModule, export.SourceName, active)
	}

	wildcard := naiveWildcard(byID, moduleID, true, map[string]struct{}{})
	if decl, exists := wildcard.decls[name]; exists {
		return resolutionResolved, decl
	}
	if _, ambiguous := wildcard.ambiguous[name]; ambiguous {
		return resolutionAmbiguous, DeclarationID{}
	}
	return resolutionMissing, DeclarationID{}
}

func naiveWildcard(byID map[string]Module, moduleID string, isStart bool, active map[string]struct{}) wildcardResult {
	if _, cycling := active[moduleID]; cycling {
		return newWildcardResult()
	}
	active[moduleID] = struct{}{}
	result := newWildcardResult()
	module := byID[moduleID]

	for _, export := range module.Exports {
		if export.Kind != LocalDeclarationExport && export.Kind != NamedReexport {
			continue
		}
		if export.Name == "default" && !isStart {
			continue
		}
		if status, decl := naiveResolve(byID, moduleID, export.Name, map[nameKey]struct{}{}); status == resolutionResolved {
			addWildcardCandidate(result, export.Name, decl)
		}
	}

	for _, export := range module.Exports {
		if export.Kind != WildcardReexport {
			continue
		}
		incoming := naiveWildcard(byID, export.SourceModule, false, active)
		for name := range incoming.ambiguous {
			if naiveHasExplicit(module, name) {
				continue
			}
			result.ambiguous[name] = struct{}{}
			delete(result.decls, name)
		}
		names := make([]string, 0, len(incoming.decls))
		for name := range incoming.decls {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if name == "default" || naiveHasExplicit(module, name) {
				continue
			}
			addWildcardCandidate(result, name, incoming.decls[name])
		}
	}
	return result
}

func naiveHasExplicit(module Module, name string) bool {
	for _, export := range module.Exports {
		if (export.Kind == LocalDeclarationExport || export.Kind == NamedReexport) && export.Name == name {
			return true
		}
	}
	return false
}

func naiveFindDeclaration(module Module, name string) *Declaration {
	for index := range module.Declarations {
		if module.Declarations[index].Name == name {
			return &module.Declarations[index]
		}
	}
	return nil
}

func naiveFindBinding(module Module, localName string) (string, string, bool) {
	for _, statement := range module.Imports {
		for _, binding := range statement.Bindings {
			if binding.LocalName == localName {
				return statement.TargetModule, binding.ImportedName, true
			}
		}
	}
	return "", "", false
}

func naiveSideEffects(module Module) bool {
	return module.SideEffects != SideEffectsNo
}

func generateRandomGraph(random *rand.Rand, moduleCount int) ([]Module, []string) {
	modules := make([]Module, 0, moduleCount)
	for index := 0; index < moduleCount; index++ {
		id := fmt.Sprintf("m%02d", index)
		declarations := make([]Declaration, 0, 2+random.Intn(3))
		for declIndex := 0; declIndex < 1+random.Intn(4); declIndex++ {
			declarations = append(declarations, Declaration{
				Name:           fmt.Sprintf("d%d", declIndex),
				HasSideEffects: random.Intn(3) == 0,
			})
		}
		modules = append(modules, Module{
			ID:           id,
			Declarations: declarations,
			SideEffects:  []SideEffectSetting{SideEffectsDefault, SideEffectsYes, SideEffectsNo}[random.Intn(3)],
		})
	}

	for moduleIndex := range modules {
		module := &modules[moduleIndex]
		module.Exports = append(module.Exports, LocalExport("d0", "d0"))
		for declarationIndex := 1; declarationIndex < len(module.Declarations); declarationIndex++ {
			if random.Intn(2) == 0 {
				name := module.Declarations[declarationIndex].Name
				module.Exports = append(module.Exports, LocalExport(name, name))
			}
		}
	}

	for moduleIndex := range modules {
		module := &modules[moduleIndex]
		for targetIndex := 0; targetIndex < moduleCount; targetIndex++ {
			if random.Intn(3) != 0 {
				continue
			}
			target := modules[targetIndex]
			exportedDeclarations := make([]string, 0, len(target.Exports))
			for _, export := range target.Exports {
				if export.Kind == LocalDeclarationExport {
					exportedDeclarations = append(exportedDeclarations, export.Name)
				}
			}
			statement := Import{TargetModule: target.ID}
			count := 1 + random.Intn(2)
			for bindingIndex := 0; bindingIndex < count && bindingIndex < len(exportedDeclarations); bindingIndex++ {
				name := exportedDeclarations[bindingIndex]
				imported := name
				if random.Intn(4) == 0 {
					imported = WildcardImportName
				}
				statement.Bindings = append(statement.Bindings, ImportBinding{
					LocalName:    fmt.Sprintf("from_%s_%s", target.ID, name),
					ImportedName: imported,
				})
			}
			module.Imports = append(module.Imports, statement)

			if random.Intn(2) == 0 {
				binding := statement.Bindings[random.Intn(len(statement.Bindings))]
				declarationIndex := random.Intn(len(module.Declarations))
				module.Declarations[declarationIndex].References = appendUnique(module.Declarations[declarationIndex].References, binding.LocalName)
			}
		}

		if random.Intn(2) == 0 && len(module.Declarations) > 1 {
			from := random.Intn(len(module.Declarations))
			to := random.Intn(len(module.Declarations))
			module.Declarations[from].References = appendUnique(module.Declarations[from].References, module.Declarations[to].Name)
		}

		exportedNames := make(map[string]bool)
		for _, export := range module.Exports {
			exportedNames[export.Name] = true
		}
		if random.Intn(3) == 0 && moduleIndex+1 < moduleCount {
			target := modules[(moduleIndex+1)%moduleCount]
			renamed := target.Declarations[0].Name + "Renamed"
			if len(target.Declarations) > 0 && !exportedNames[renamed] {
				module.Exports = append(module.Exports, NamedExport(
					renamed,
					target.ID,
					target.Declarations[0].Name,
				))
			}
		}
		if random.Intn(4) == 0 && moduleIndex+1 < moduleCount {
			module.Exports = append(module.Exports, WildcardExport(modules[moduleIndex+1].ID))
		}
	}

	entries := []string{modules[0].ID}
	if random.Intn(2) == 0 {
		entries = append(entries, modules[random.Intn(moduleCount)].ID)
	}
	return modules, entries
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
