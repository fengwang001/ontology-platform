package deadcode

type SideEffectSetting int

const (
	SideEffectsDefault SideEffectSetting = iota
	SideEffectsYes
	SideEffectsNo
)

type Declaration struct {
	Name           string
	HasSideEffects bool
	References     []string
}

type ImportBinding struct {
	LocalName    string
	ImportedName string
}

type Import struct {
	TargetModule string
	Bindings     []ImportBinding
}

type ExportKind int

const (
	LocalDeclarationExport ExportKind = iota
	NamedReexport
	WildcardReexport
)

type Export struct {
	Kind         ExportKind
	Name         string
	SourceModule string
	SourceName   string
}

func LocalExport(name, declarationName string) Export {
	return Export{Kind: LocalDeclarationExport, Name: name, SourceName: declarationName}
}

func NamedExport(name, sourceModule, sourceName string) Export {
	return Export{Kind: NamedReexport, Name: name, SourceModule: sourceModule, SourceName: sourceName}
}

func WildcardExport(sourceModule string) Export {
	return Export{Kind: WildcardReexport, SourceModule: sourceModule}
}

type Module struct {
	ID           string
	Declarations []Declaration
	Imports      []Import
	Exports      []Export
	SideEffects  SideEffectSetting
}

type DeclarationID struct {
	ModuleID string
	Name     string
}

type KeepReason string

const (
	ReasonEntryExport KeepReason = "entry-export"
	ReasonReferenced  KeepReason = "referenced"
	ReasonSideEffect  KeepReason = "side-effect"
)

type ErrorCode string

const (
	InvalidArgument    ErrorCode = "invalid-argument"
	DuplicateModule    ErrorCode = "duplicate-module"
	UnknownModule      ErrorCode = "unknown-module"
	UndefinedReference ErrorCode = "undefined-reference"
	MissingExport      ErrorCode = "missing-export"
	AmbiguousExport    ErrorCode = "ambiguous-export"
	ReexportCycle      ErrorCode = "reexport-cycle"
)

type AnalysisError struct {
	Code           ErrorCode
	ModuleID       string
	Declaration    string
	ImportIndex    int
	BindingIndex   int
	RecordOrder    int
	LocalName      string
	ImportedName   string
	ExportIndex    int
	TargetModuleID string
	Message        string
}

func (e *AnalysisError) Error() string { return e.Message }

type Result struct {
	IncludedModules  []string
	KeptDeclarations []DeclarationID
	Reasons          map[DeclarationID]KeepReason
}
