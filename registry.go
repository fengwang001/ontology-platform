package deadcode

import (
	"fmt"
	"sort"
	"sync"
)

const WildcardImportName = "*"

type Registry struct {
	mu      sync.RWMutex
	modules map[string]Module
}

func NewRegistry() *Registry {
	return &Registry{modules: make(map[string]Module)}
}

func (r *Registry) Register(module Module) error {
	if err := validateModuleShape(module); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.modules[module.ID]; exists {
		return &AnalysisError{
			Code:     DuplicateModule,
			ModuleID: module.ID,
			Message:  fmt.Sprintf("module %q is already registered", module.ID),
		}
	}
	r.modules[module.ID] = cloneModule(module)
	return nil
}

func (r *Registry) NewSession(entryModules []string) *Session {
	r.mu.RLock()
	defer r.mu.RUnlock()

	modules := make(map[string]Module, len(r.modules))
	for id, module := range r.modules {
		modules[id] = cloneModule(module)
	}
	entries := append([]string(nil), entryModules...)
	return &Session{modules: modules, entries: entries}
}

type Session struct {
	modules map[string]Module
	entries []string
}

func validateModuleShape(module Module) error {
	if module.ID == "" {
		return invalidError("", "module identifier is empty")
	}

	declarations := make(map[string]struct{}, len(module.Declarations))
	for index, declaration := range module.Declarations {
		if declaration.Name == "" {
			return invalidError(module.ID, fmt.Sprintf("declaration at index %d has an empty name", index))
		}
		if _, duplicate := declarations[declaration.Name]; duplicate {
			return invalidError(module.ID, fmt.Sprintf("duplicate declaration name %q", declaration.Name))
		}
		declarations[declaration.Name] = struct{}{}
		for refIndex, ref := range declaration.References {
			if ref == "" {
				return invalidError(module.ID, fmt.Sprintf("declaration %q has an empty reference at index %d", declaration.Name, refIndex))
			}
		}
	}

	localBindings := make(map[string]struct{})
	for importIndex, statement := range module.Imports {
		if statement.TargetModule == "" {
			return invalidError(module.ID, fmt.Sprintf("import at index %d has an empty target", importIndex))
		}
		for bindingIndex, binding := range statement.Bindings {
			if binding.LocalName == "" {
				return invalidError(module.ID, fmt.Sprintf("import %d binding %d has an empty local name", importIndex, bindingIndex))
			}
			if binding.ImportedName == "" {
				return invalidError(module.ID, fmt.Sprintf("import binding %q has an empty imported name", binding.LocalName))
			}
			if _, duplicate := localBindings[binding.LocalName]; duplicate {
				return invalidError(module.ID, fmt.Sprintf("duplicate import local name %q", binding.LocalName))
			}
			localBindings[binding.LocalName] = struct{}{}
		}
	}

	exportNames := make(map[string]struct{}, len(module.Exports))
	for exportIndex, export := range module.Exports {
		switch export.Kind {
		case LocalDeclarationExport:
			if export.Name == "" || export.SourceName == "" || export.SourceModule != "" {
				return invalidError(module.ID, fmt.Sprintf("local export at index %d is invalid", exportIndex))
			}
			if _, duplicate := exportNames[export.Name]; duplicate {
				return invalidError(module.ID, fmt.Sprintf("duplicate export name %q", export.Name))
			}
			exportNames[export.Name] = struct{}{}
		case NamedReexport:
			if export.Name == "" || export.SourceModule == "" || export.SourceName == "" {
				return invalidError(module.ID, fmt.Sprintf("named reexport at index %d is invalid", exportIndex))
			}
			if _, duplicate := exportNames[export.Name]; duplicate {
				return invalidError(module.ID, fmt.Sprintf("duplicate export name %q", export.Name))
			}
			exportNames[export.Name] = struct{}{}
		case WildcardReexport:
			if export.SourceModule == "" || export.Name != "" || export.SourceName != "" {
				return invalidError(module.ID, fmt.Sprintf("wildcard reexport at index %d is invalid", exportIndex))
			}
		default:
			return invalidError(module.ID, fmt.Sprintf("export at index %d has unknown kind", exportIndex))
		}
	}

	return nil
}

func validateSnapshot(s *Session) error {
	ids := make([]string, 0, len(s.modules))
	for id := range s.modules {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var firstUnknown *AnalysisError
	for _, id := range ids {
		module := s.modules[id]
		for importIndex, statement := range module.Imports {
			if _, exists := s.modules[statement.TargetModule]; !exists {
				err := &AnalysisError{
					Code:           UnknownModule,
					ModuleID:       id,
					ImportIndex:    importIndex,
					RecordOrder:    importIndex,
					TargetModuleID: statement.TargetModule,
					Message:        fmt.Sprintf("import target %q is not registered", statement.TargetModule),
				}
				firstUnknown = earlierError(firstUnknown, err)
			}
		}
		for exportIndex, export := range module.Exports {
			if export.Kind != NamedReexport && export.Kind != WildcardReexport {
				continue
			}
			if _, exists := s.modules[export.SourceModule]; exists {
				continue
			}
			err := &AnalysisError{
				Code:           UnknownModule,
				ModuleID:       id,
				ExportIndex:    exportIndex,
				RecordOrder:    len(module.Imports) + exportIndex,
				TargetModuleID: export.SourceModule,
				Message:        fmt.Sprintf("reexport target %q is not registered", export.SourceModule),
			}
			firstUnknown = earlierError(firstUnknown, err)
		}
	}
	if firstUnknown != nil {
		return firstUnknown
	}

	var firstUndefined *AnalysisError
	for _, id := range ids {
		module := s.modules[id]
		locals := make(map[string]struct{}, len(module.Declarations)+countImportBindings(module))
		for _, declaration := range module.Declarations {
			locals[declaration.Name] = struct{}{}
		}
		for _, statement := range module.Imports {
			for _, binding := range statement.Bindings {
				locals[binding.LocalName] = struct{}{}
			}
		}

		for declarationIndex, declaration := range module.Declarations {
			for _, reference := range declaration.References {
				if _, defined := locals[reference]; !defined {
					err := &AnalysisError{
						Code:        UndefinedReference,
						ModuleID:    id,
						Declaration: declaration.Name,
						RecordOrder: declarationIndex,
						Message:     fmt.Sprintf("declaration %q references undefined identifier %q", declaration.Name, reference),
					}
					firstUndefined = earlierError(firstUndefined, err)
				}
			}
		}

		for exportIndex, export := range module.Exports {
			if export.Kind == LocalDeclarationExport {
				if _, exists := locals[export.SourceName]; !exists {
					err := &AnalysisError{
						Code:        UndefinedReference,
						ModuleID:    id,
						Declaration: export.SourceName,
						ExportIndex: exportIndex,
						RecordOrder: len(module.Declarations) + exportIndex,
						Message:     fmt.Sprintf("local export %q refers to missing declaration %q", export.Name, export.SourceName),
					}
					firstUndefined = earlierError(firstUndefined, err)
				}
			}
		}
	}
	if firstUndefined == nil {
		return nil
	}
	return firstUndefined
}

func invalidError(moduleID, message string) *AnalysisError {
	return &AnalysisError{Code: InvalidArgument, ModuleID: moduleID, Message: message}
}

func earlierError(current, candidate *AnalysisError) *AnalysisError {
	if current == nil {
		return candidate
	}
	currentKey := errorOrderKey(current)
	candidateKey := errorOrderKey(candidate)
	if candidateKey < currentKey {
		return candidate
	}
	return current
}

func errorOrderKey(err *AnalysisError) string {
	return fmt.Sprintf("%s\x00%06d\x00%s",
		err.ModuleID,
		err.RecordOrder,
		err.Declaration)
}

func countImportBindings(module Module) int {
	total := 0
	for _, statement := range module.Imports {
		total += len(statement.Bindings)
	}
	return total
}

func cloneModule(module Module) Module {
	copy := module
	copy.Declarations = cloneDeclarations(module.Declarations)
	copy.Imports = cloneImports(module.Imports)
	copy.Exports = append([]Export(nil), module.Exports...)
	return copy
}

func cloneDeclarations(declarations []Declaration) []Declaration {
	copy := make([]Declaration, len(declarations))
	for index, declaration := range declarations {
		copy[index] = declaration
		copy[index].References = append([]string(nil), declaration.References...)
	}
	return copy
}

func cloneImports(imports []Import) []Import {
	copy := make([]Import, len(imports))
	for index, statement := range imports {
		copy[index].TargetModule = statement.TargetModule
		copy[index].Bindings = append([]ImportBinding(nil), statement.Bindings...)
	}
	return copy
}
