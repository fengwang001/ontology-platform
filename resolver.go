package deadcode

import "sort"

type resolutionStatus int

const (
	resolutionResolved resolutionStatus = iota
	resolutionMissing
	resolutionAmbiguous
	resolutionCycle
)

type resolutionResult struct {
	status resolutionStatus
	decl   DeclarationID
}

type nameKey struct {
	module string
	name   string
}

type wildcardResult struct {
	decls     map[string]DeclarationID
	ambiguous map[string]struct{}
}

type exportResolver struct {
	session        *Session
	explicit       map[string]Export
	wildcards      map[string][]Export
	explicitStatus map[nameKey]resolutionStatus
	explicitDecl   map[nameKey]DeclarationID
	wildcardCache  map[string]wildcardResult
}

func newExportResolver(session *Session) *exportResolver {
	return &exportResolver{
		session:        session,
		explicit:       map[string]Export{},
		wildcards:      map[string][]Export{},
		explicitStatus: map[nameKey]resolutionStatus{},
		explicitDecl:   map[nameKey]DeclarationID{},
		wildcardCache:  map[string]wildcardResult{},
	}
}

func (r *exportResolver) build() {
	for moduleID, module := range r.session.modules {
		for _, export := range module.Exports {
			switch export.Kind {
			case LocalDeclarationExport, NamedReexport:
				r.explicit[moduleID+"\x00"+export.Name] = export
			case WildcardReexport:
				r.wildcards[moduleID] = append(r.wildcards[moduleID], export)
			}
		}
	}
	r.cacheAllExplicitResolutions()
}

func (r *exportResolver) resolveNamed(moduleID, name string) resolutionResult {
	key := nameKey{module: moduleID, name: name}
	if status, ok := r.explicitStatus[key]; ok {
		return resolutionResult{status: status, decl: r.explicitDecl[key]}
	}
	return r.resolvePath(key, map[nameKey]struct{}{})
}

func (r *exportResolver) wildcardExports(moduleID string) wildcardResult {
	if result, ok := r.wildcardCache[moduleID]; ok {
		return result
	}
	result := r.collectWildcard(moduleID, map[string]bool{})
	r.wildcardCache[moduleID] = result
	return result
}

func (r *exportResolver) resolvePath(key nameKey, active map[nameKey]struct{}) resolutionResult {
	if status, ok := r.explicitStatus[key]; ok {
		return resolutionResult{status: status, decl: r.explicitDecl[key]}
	}
	if _, cycling := active[key]; cycling {
		return resolutionResult{status: resolutionCycle}
	}

	export, ok := r.explicit[key.module+"\x00"+key.name]
	if !ok {
		wildcard := r.wildcardExports(key.module)
		if decl, exists := wildcard.decls[key.name]; exists {
			return resolutionResult{status: resolutionResolved, decl: decl}
		}
		if _, exists := wildcard.ambiguous[key.name]; exists {
			return resolutionResult{status: resolutionAmbiguous}
		}
		return resolutionResult{status: resolutionMissing}
	}

	active[key] = struct{}{}
	var result resolutionResult
	if export.Kind == LocalDeclarationExport {
		result = resolutionResult{status: resolutionResolved, decl: DeclarationID{ModuleID: key.module, Name: export.SourceName}}
	} else {
		result = r.resolvePath(nameKey{module: export.SourceModule, name: export.SourceName}, active)
	}
	if result.status != resolutionCycle {
		r.explicitStatus[key] = result.status
		r.explicitDecl[key] = result.decl
	}
	return result
}

func (r *exportResolver) cacheAllExplicitResolutions() {
	for _, moduleID := range r.sortedModuleIDs() {
		for _, export := range r.session.modules[moduleID].Exports {
			if export.Kind == LocalDeclarationExport || export.Kind == NamedReexport {
				r.resolveNamed(moduleID, export.Name)
			}
		}
	}
}

func (r *exportResolver) collectWildcard(moduleID string, active map[string]bool) wildcardResult {
	if result, ok := r.wildcardCache[moduleID]; ok {
		return result
	}
	if active[moduleID] {
		return newWildcardResult()
	}

	active[moduleID] = true
	defer delete(active, moduleID)

	result := newWildcardResult()
	for _, export := range r.wildcards[moduleID] {
		mergeForwardedWildcard(result, r.collectWildcard(export.SourceModule, active))
	}
	r.applyOwnExports(result, moduleID)
	r.wildcardCache[moduleID] = result
	return result
}

func (r *exportResolver) applyOwnExports(result wildcardResult, moduleID string) {
	for _, export := range r.session.modules[moduleID].Exports {
		if export.Kind != LocalDeclarationExport && export.Kind != NamedReexport {
			continue
		}
		key := nameKey{module: moduleID, name: export.Name}
		if status, exists := r.explicitStatus[key]; exists && status == resolutionResolved {
			delete(result.ambiguous, export.Name)
			result.decls[export.Name] = r.explicitDecl[key]
		}
	}
}

func mergeForwardedWildcard(dst, incoming wildcardResult) {
	for name := range incoming.ambiguous {
		if name != "default" {
			dst.ambiguous[name] = struct{}{}
			delete(dst.decls, name)
		}
	}
	for name, decl := range incoming.decls {
		if name != "default" {
			addWildcardCandidate(dst, name, decl)
		}
	}
}

func newWildcardResult() wildcardResult {
	return wildcardResult{decls: map[string]DeclarationID{}, ambiguous: map[string]struct{}{}}
}

func addWildcardCandidate(result wildcardResult, name string, decl DeclarationID) {
	if _, ambiguous := result.ambiguous[name]; ambiguous {
		return
	}
	if existing, exists := result.decls[name]; exists {
		if existing != decl {
			result.ambiguous[name] = struct{}{}
			delete(result.decls, name)
		}
		return
	}
	result.decls[name] = decl
}

func (r *exportResolver) sortedModuleIDs() []string {
	moduleIDs := make([]string, 0, len(r.session.modules))
	for moduleID := range r.session.modules {
		moduleIDs = append(moduleIDs, moduleID)
	}
	sort.Strings(moduleIDs)
	return moduleIDs
}
