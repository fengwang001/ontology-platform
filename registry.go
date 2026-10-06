package ontology

import (
	"errors"
	"sort"
	"sync"
)

type Registry struct {
	mu     sync.RWMutex
	config Config
	types  map[string]*typeRecord
}

func NewRegistry(config Config) *Registry {
	return &Registry{config: config, types: make(map[string]*typeRecord)}
}

func (r *Registry) Register(spec TypeSpec) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.types[spec.Name]; exists {
		return typeError(ErrDuplicateField, spec.Name, errors.New("type already registered"))
	}
	record, err := r.prepareRecord(spec, nil, nil)
	if err != nil {
		r.log("register "+spec.Name, "rejected: "+err.Error(), "validation failed")
		return err
	}
	record.version = 1
	for dep := range record.directDeps {
		r.types[dep].dependents[spec.Name] = true
	}
	r.types[spec.Name] = record
	r.log("register "+spec.Name, "accepted version 1", "validation and layout completed")
	return nil
}

func (r *Registry) Modify(name string, spec TypeSpec) (ChangeResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	oldRecord, exists := r.types[name]
	if !exists {
		err := typeError(ErrUndefinedType, name, errUndefinedType)
		r.log("modify "+name, "rejected: "+err.Error(), "target type does not exist")
		return ChangeResult{}, err
	}
	spec.Name = name

	rootCandidate, err := r.prepareRecord(spec, nil, nil)
	if err != nil {
		r.log("modify "+name, "rejected: "+err.Error(), "candidate root validation failed")
		return ChangeResult{}, err
	}

	oldRootLayout := cloneLayout(oldRecord.layout)
	rootCompatibility := compareLayouts(oldRootLayout, rootCandidate.layout)
	rootChanged := layoutIdentitiesDiffer(oldRootLayout, rootCandidate.layout)

	result := ChangeResult{
		RootCompatibility: rootCompatibility,
		Compatibility:     map[string]Compatibility{name: rootCompatibility},
		Recalculated:      []string{name},
		OldLayout:         &oldRootLayout,
		NewLayout:         cloneLayoutPtr(rootCandidate.layout),
	}

	candidates := map[string]*typeRecord{name: rootCandidate}
	layouts := map[string]Layout{name: rootCandidate.layout}
	changed := map[string]bool{name: rootCompatibility != FullyCompatible}

	if rootCompatibility != FullyCompatible {
		work := map[string]bool{}
		for dependent := range oldRecord.dependents {
			work[dependent] = true
		}
		for len(work) > 0 {
			currentName := readyDependent(work, r.types)
			delete(work, currentName)
			currentRecord := r.types[currentName]
			hasChangedDependency := false
			for dep := range currentRecord.directDeps {
				if changed[dep] {
					hasChangedDependency = true
					break
				}
			}
			if !hasChangedDependency {
				continue
			}

			currentCandidate, err := r.recomputeRecord(currentRecord, layouts)
			if err != nil {
				if typed, ok := err.(*TypeError); ok && typed.Kind == ErrSizeExceeded {
					typed.Kind = ErrDependentSizeExceeded
					typed.Err = errDependentSize
				}
				r.log("modify "+name, "rejected: "+err.Error(), "dependent recomputation failed")
				return ChangeResult{}, err
			}
			compatibility := compareLayouts(currentRecord.layout, currentCandidate.layout)
			result.Compatibility[currentName] = compatibility
			result.Recalculated = append(result.Recalculated, currentName)
			candidates[currentName] = currentCandidate
			layouts[currentName] = currentCandidate.layout
			changed[currentName] = compatibility != FullyCompatible
			if compatibility != FullyCompatible {
				for dependent := range currentRecord.dependents {
					work[dependent] = true
				}
			}
		}
	}

	for dep := range oldRecord.directDeps {
		delete(r.types[dep].dependents, name)
	}
	for dep := range rootCandidate.directDeps {
		r.types[dep].dependents[name] = true
	}
	rootCandidate.dependents = oldRecord.dependents
	rootCandidate.version = oldRecord.version
	if rootChanged {
		rootCandidate.version++
	}
	rootCandidate.layout.Version = rootCandidate.version
	rootCandidate.recalculations = oldRecord.recalculations + 1
	r.types[name] = rootCandidate

	for currentName, candidate := range candidates {
		if currentName == name {
			continue
		}
		old := r.types[currentName]
		candidate.dependents = old.dependents
		candidate.version = old.version
		if layoutIdentitiesDiffer(old.layout, candidate.layout) {
			candidate.version++
		}
		candidate.layout.Version = candidate.version
		candidate.recalculations = old.recalculations + 1
		r.types[currentName] = candidate
	}

	r.log(
		"modify "+name,
		"accepted: "+rootCompatibility.String(),
		compatibilityReason(rootCompatibility, oldRootLayout, rootCandidate.layout),
	)
	return result, nil
}

func (r *Registry) Layout(name string) (Layout, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	record, ok := r.types[name]
	if !ok {
		return Layout{}, false
	}
	return cloneLayout(record.layout), true
}

func (r *Registry) View(name string) (View, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	record, ok := r.types[name]
	if !ok {
		return View{}, false
	}
	return View{
		Name:           record.spec.Name,
		Version:        record.version,
		Size:           record.layout.Size,
		Align:          record.layout.Align,
		Dependents:     len(record.dependents),
		Recalculations: record.recalculations,
	}, true
}

func (r *Registry) Views() []View {
	r.mu.RLock()
	defer r.mu.RUnlock()
	views := make([]View, 0, len(r.types))
	for _, record := range r.types {
		views = append(views, View{
			Name:           record.spec.Name,
			Version:        record.version,
			Size:           record.layout.Size,
			Align:          record.layout.Align,
			Dependents:     len(record.dependents),
			Recalculations: record.recalculations,
		})
	}
	sort.Slice(views, func(i, j int) bool { return views[i].Name < views[j].Name })
	return views
}

func (r *Registry) log(input, output, reason string) {
	if r.config.Logger != nil {
		r.config.Logger.Log(input, output, reason)
	}
}

func (r *Registry) prepareRecord(spec TypeSpec, selfLayout *Layout, overrides map[string]Layout) (*typeRecord, error) {
	spec = cloneSpec(spec)
	record := &typeRecord{
		spec:       spec,
		directDeps: make(map[string]bool),
		dependents: map[string]bool{},
	}

	if spec.Basic {
		if !allowedAlignment(spec.Alignment, r.config.AllowedAlignments) {
			return nil, typeError(ErrInvalidAlignment, spec.Name, errInvalidAlignment)
		}
		record.layout = Layout{Name: spec.Name, Size: spec.Size, Align: spec.Alignment, Version: 1}
		if record.layout.Size > r.config.MaxSize {
			return nil, typeError(ErrSizeExceeded, spec.Name, errSizeExceeded)
		}
		return record, nil
	}

	lookup := func(name string) (Layout, bool) {
		if selfLayout != nil && name == spec.Name {
			return *selfLayout, true
		}
		if layout, ok := overrides[name]; ok {
			return layout, true
		}
		existing, ok := r.types[name]
		if !ok {
			return Layout{}, false
		}
		return existing.layout, true
	}

	for _, field := range spec.Fields {
		if field.Pointer {
			continue
		}
		if selfLayout == nil && field.Type == spec.Name {
			continue
		}
		if _, ok := lookup(field.Type); !ok {
			return nil, typeError(ErrUndefinedType, spec.Name, errUndefinedType)
		}
	}

	if !allowedAlignment(r.config.PointerAlignment, r.config.AllowedAlignments) ||
		!allowedAlignment(r.config.MinCompositeAlign, r.config.AllowedAlignments) {
		return nil, typeError(ErrInvalidAlignment, spec.Name, errInvalidAlignment)
	}
	if spec.MaxAlign != 0 && !allowedAlignment(spec.MaxAlign, r.config.AllowedAlignments) {
		return nil, typeError(ErrInvalidAlignment, spec.Name, errInvalidAlignment)
	}

	for _, field := range spec.Fields {
		if field.Compact {
			continue
		}
		if selfLayout == nil && field.Type == spec.Name {
			continue
		}
		alignment := r.config.PointerAlignment
		if !field.Pointer {
			target, ok := lookup(field.Type)
			if !ok {
				return nil, typeError(ErrUndefinedType, spec.Name, errUndefinedType)
			}
			alignment = target.Align
		}
		alignment = cappedAlignment(alignment, spec.MaxAlign)
		if !allowedAlignment(alignment, r.config.AllowedAlignments) {
			return nil, typeError(ErrInvalidAlignment, spec.Name, errInvalidAlignment)
		}
	}

	seenFields := make(map[string]bool)
	for _, field := range spec.Fields {
		if seenFields[field.Name] {
			return nil, typeError(ErrDuplicateField, spec.Name, errDuplicateField)
		}
		seenFields[field.Name] = true
	}

	directDeps := make(map[string]bool)
	for _, field := range spec.Fields {
		if !field.Pointer {
			directDeps[field.Type] = true
		}
	}
	if hasDirectCycle(spec.Name, directDeps, func(name string) map[string]bool {
		if selfLayout != nil && name == spec.Name {
			return directDeps
		}
		if existing, ok := r.types[name]; ok {
			return existing.directDeps
		}
		return nil
	}) {
		return nil, typeError(ErrDirectCycle, spec.Name, errDirectCycle)
	}

	layout, err := calculateLayout(r.config, spec, lookup)
	if err != nil {
		return nil, err
	}
	if !allowedAlignment(layout.Align, r.config.AllowedAlignments) {
		return nil, typeError(ErrInvalidAlignment, spec.Name, errInvalidAlignment)
	}
	if layout.Size > r.config.MaxSize {
		return nil, typeError(ErrSizeExceeded, spec.Name, errSizeExceeded)
	}
	record.directDeps = directDeps
	record.layout = layout
	record.layout.Version = 1
	return record, nil
}

func hasDirectCycle(root string, rootDeps map[string]bool, deps func(string) map[string]bool) bool {
	const (
		visiting = 1
		done     = 2
	)
	state := map[string]int{}
	var visit func(string) bool
	visit = func(name string) bool {
		if name == root && state[name] == visiting {
			return true
		}
		if state[name] != 0 {
			return false
		}
		state[name] = visiting
		for dep := range deps(name) {
			if dep == root || visit(dep) {
				return true
			}
		}
		state[name] = done
		return false
	}
	for dep := range rootDeps {
		if dep == root || visit(dep) {
			return true
		}
	}
	return false
}

func (r *Registry) recomputeRecord(record *typeRecord, layouts map[string]Layout) (*typeRecord, error) {
	return r.prepareRecord(record.spec, nil, layouts)
}

func cloneLayoutPtr(layout Layout) *Layout {
	clone := cloneLayout(layout)
	return &clone
}

func layoutIdentitiesDiffer(oldLayout, newLayout Layout) bool {
	return compareLayouts(oldLayout, newLayout) != FullyCompatible
}
