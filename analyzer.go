package deadcode

import (
	"fmt"
	"sort"
)

func (s *Session) Solve() (*Result, error) {
	if err := validateSnapshot(s); err != nil {
		return nil, err
	}
	if err := validateEntryModules(s); err != nil {
		return nil, err
	}

	resolver := newExportResolver(s)
	resolver.build()

	analysis := &analysis{
		session:    s,
		resolver:   resolver,
		bindings:   make(map[string]map[string]bindingLocation),
		included:   make(map[string]bool),
		seenModule: make(map[string]bool),
		reasons:    make(map[DeclarationID]KeepReason),
		seenDecl:   make(map[DeclarationID]bool),
	}

	for _, entry := range s.entries {
		analysis.includeModule(entry)
		analysis.keepEntryExports(entry)
	}
	analysis.run()

	if err := analysis.validateIncludedBindings(); err != nil {
		return nil, err
	}
	return analysis.result(), nil
}

type bindingLocation struct {
	targetModule string
	importIndex  int
	bindingIndex int
	binding      ImportBinding
}

type analysis struct {
	session  *Session
	resolver *exportResolver
	bindings map[string]map[string]bindingLocation

	moduleQueue []string
	seenModule  map[string]bool
	included    map[string]bool

	declQueue []DeclarationID
	seenDecl  map[DeclarationID]bool

	reasons map[DeclarationID]KeepReason
}

func (a *analysis) run() {
	for len(a.moduleQueue) > 0 || len(a.declQueue) > 0 {
		if len(a.moduleQueue) > 0 {
			moduleID := a.moduleQueue[0]
			a.moduleQueue = a.moduleQueue[1:]
			a.processModule(moduleID)
			continue
		}
		decl := a.declQueue[0]
		a.declQueue = a.declQueue[1:]
		a.processDeclaration(decl)
	}
}

func (a *analysis) includeModule(moduleID string) {
	a.included[moduleID] = true
	if !a.seenModule[moduleID] {
		a.seenModule[moduleID] = true
		a.moduleQueue = append(a.moduleQueue, moduleID)
	}
}

func (a *analysis) processModule(moduleID string) {
	module := a.session.modules[moduleID]
	if moduleHasSideEffects(module) {
		for _, declaration := range module.Declarations {
			if declaration.HasSideEffects {
				a.keepDeclaration(DeclarationID{ModuleID: moduleID, Name: declaration.Name}, ReasonSideEffect)
			}
		}
	}

	for _, statement := range module.Imports {
		target := a.session.modules[statement.TargetModule]
		if moduleHasSideEffects(target) {
			a.includeModule(statement.TargetModule)
		}
	}
}

func (a *analysis) keepEntryExports(moduleID string) {
	module := a.session.modules[moduleID]
	for _, export := range module.Exports {
		if export.Kind == LocalDeclarationExport || export.Kind == NamedReexport {
			resolved := a.resolver.resolveNamed(moduleID, export.Name)
			if resolved.status == resolutionResolved {
				a.keepDeclaration(resolved.decl, ReasonEntryExport)
			}
		}
	}

	wildcard := a.resolver.wildcardExports(moduleID)
	for name, decl := range wildcard.decls {
		if _, ambiguous := wildcard.ambiguous[name]; ambiguous {
			continue
		}
		a.keepDeclaration(decl, ReasonEntryExport)
	}
}

func (a *analysis) keepDeclaration(decl DeclarationID, reason KeepReason) {
	if existing, exists := a.reasons[decl]; !exists || reasonStronger(reason, existing) {
		a.reasons[decl] = reason
	}
	if !a.seenDecl[decl] {
		a.seenDecl[decl] = true
		a.declQueue = append(a.declQueue, decl)
	}

	target := a.session.modules[decl.ModuleID]
	if !moduleHasSideEffects(target) {
		a.includeModule(decl.ModuleID)
	}
}

func (a *analysis) processDeclaration(decl DeclarationID) {
	module := a.session.modules[decl.ModuleID]
	declaration := findDeclaration(module, decl.Name)

	for _, reference := range declaration.References {
		if local := findDeclaration(module, reference); local != nil {
			a.keepDeclaration(DeclarationID{ModuleID: decl.ModuleID, Name: reference}, ReasonReferenced)
			continue
		}

		binding, ok := a.lookupBinding(decl.ModuleID, reference)
		if !ok {
			continue
		}
		if binding.binding.ImportedName == WildcardImportName {
			wildcard := a.resolver.wildcardExports(binding.targetModule)
			for name, targetDecl := range wildcard.decls {
				if _, ambiguous := wildcard.ambiguous[name]; ambiguous {
					continue
				}
				a.keepDeclaration(targetDecl, ReasonReferenced)
			}
			continue
		}

		resolved := a.resolver.resolveNamed(binding.targetModule, binding.binding.ImportedName)
		if resolved.status == resolutionResolved {
			a.keepDeclaration(resolved.decl, ReasonReferenced)
		}
	}
}

func (a *analysis) lookupBinding(moduleID, localName string) (bindingLocation, bool) {
	byName, ok := a.bindings[moduleID]
	if !ok {
		byName = make(map[string]bindingLocation)
		module := a.session.modules[moduleID]
		for importIndex, statement := range module.Imports {
			for bindingIndex, binding := range statement.Bindings {
				byName[binding.LocalName] = bindingLocation{
					targetModule: statement.TargetModule,
					importIndex:  importIndex,
					bindingIndex: bindingIndex,
					binding:      binding,
				}
			}
		}
		a.bindings[moduleID] = byName
	}
	binding, ok := byName[localName]
	return binding, ok
}

func (a *analysis) validateIncludedBindings() error {
	moduleIDs := make([]string, 0, len(a.included))
	for moduleID := range a.included {
		moduleIDs = append(moduleIDs, moduleID)
	}
	sort.Strings(moduleIDs)

	for _, moduleID := range moduleIDs {
		module := a.session.modules[moduleID]
		for importIndex, statement := range module.Imports {
			for bindingIndex, binding := range statement.Bindings {
				if binding.ImportedName == WildcardImportName {
					continue
				}
				resolved := a.resolver.resolveNamed(statement.TargetModule, binding.ImportedName)
				if resolved.status == resolutionResolved {
					continue
				}
				return &AnalysisError{
					Code:           bindingErrorCode(resolved.status),
					ModuleID:       moduleID,
					ImportIndex:    importIndex,
					BindingIndex:   bindingIndex,
					LocalName:      binding.LocalName,
					ImportedName:   binding.ImportedName,
					TargetModuleID: statement.TargetModule,
					Message:        bindingErrorMessage(statement.TargetModule, binding.ImportedName, resolved.status),
				}
			}
		}
	}
	return nil
}

func (a *analysis) result() *Result {
	moduleIDs := make([]string, 0, len(a.included))
	for moduleID := range a.included {
		moduleIDs = append(moduleIDs, moduleID)
	}
	sort.Strings(moduleIDs)

	declarations := make([]DeclarationID, 0, len(a.reasons))
	for decl := range a.reasons {
		declarations = append(declarations, decl)
	}
	sort.Slice(declarations, func(i, j int) bool {
		if declarations[i].ModuleID != declarations[j].ModuleID {
			return declarations[i].ModuleID < declarations[j].ModuleID
		}
		return declarations[i].Name < declarations[j].Name
	})

	reasons := make(map[DeclarationID]KeepReason, len(a.reasons))
	for decl, reason := range a.reasons {
		reasons[decl] = reason
	}

	return &Result{
		IncludedModules:  moduleIDs,
		KeptDeclarations: declarations,
		Reasons:          reasons,
	}
}

func validateEntryModules(s *Session) error {
	entries := append([]string(nil), s.entries...)
	sort.Strings(entries)
	for _, entry := range entries {
		if _, exists := s.modules[entry]; !exists {
			return &AnalysisError{
				Code:           UnknownModule,
				ModuleID:       entry,
				TargetModuleID: entry,
				Message:        fmt.Sprintf("entry module %q is not registered", entry),
			}
		}
	}
	return nil
}

func moduleHasSideEffects(module Module) bool {
	return module.SideEffects != SideEffectsNo
}

func findDeclaration(module Module, name string) *Declaration {
	for index := range module.Declarations {
		if module.Declarations[index].Name == name {
			return &module.Declarations[index]
		}
	}
	return nil
}

func reasonStronger(candidate, existing KeepReason) bool {
	return reasonRank(candidate) < reasonRank(existing)
}

func reasonRank(reason KeepReason) int {
	switch reason {
	case ReasonEntryExport:
		return 0
	case ReasonReferenced:
		return 1
	default:
		return 2
	}
}

func bindingErrorCode(status resolutionStatus) ErrorCode {
	switch status {
	case resolutionAmbiguous:
		return AmbiguousExport
	case resolutionCycle:
		return ReexportCycle
	default:
		return MissingExport
	}
}

func bindingErrorMessage(targetModule, name string, status resolutionStatus) string {
	switch status {
	case resolutionAmbiguous:
		return fmt.Sprintf("export %q from module %q is ambiguous", name, targetModule)
	case resolutionCycle:
		return fmt.Sprintf("reexport chain for %q from module %q contains a cycle", name, targetModule)
	default:
		return fmt.Sprintf("export %q is missing from module %q", name, targetModule)
	}
}
