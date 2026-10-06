package deadcode

import (
	"reflect"
	"testing"
)

func TestDeadCodeEliminationScenarios(t *testing.T) {
	tests := []struct {
		name     string
		modules  []Module
		entries  []string
		included []string
		kept     map[DeclarationID]KeepReason
	}{
		{
			name: "side effect module versus referenced side-effect-free module",
			modules: []Module{
				{
					ID: "entry",
					Declarations: []Declaration{
						{Name: "used", References: []string{"ns"}},
					},
					Imports: []Import{
						{TargetModule: "effectful"},
						{
							TargetModule: "pure",
							Bindings: []ImportBinding{
								{LocalName: "ns", ImportedName: WildcardImportName},
							},
						},
					},
					Exports: []Export{LocalExport("used", "used")},
				},
				{
					ID: "effectful",
					Declarations: []Declaration{
						{Name: "ignited", HasSideEffects: true},
						{Name: "quiet"},
					},
				},
				{
					ID:          "pure",
					SideEffects: SideEffectsNo,
					Declarations: []Declaration{
						{Name: "visible"},
						{Name: "hidden"},
					},
					Exports: []Export{LocalExport("visible", "visible")},
				},
				{
					ID:          "unreferenced-pure",
					SideEffects: SideEffectsNo,
					Declarations: []Declaration{
						{Name: "dead"},
					},
				},
			},
			entries:  []string{"entry"},
			included: []string{"effectful", "entry", "pure"},
			kept: map[DeclarationID]KeepReason{
				{ModuleID: "entry", Name: "used"}:        ReasonEntryExport,
				{ModuleID: "effectful", Name: "ignited"}: ReasonSideEffect,
				{ModuleID: "pure", Name: "visible"}:      ReasonReferenced,
			},
		},
		{
			name: "wildcard consistent, ambiguity, local precedence, default and rename chain",
			modules: []Module{
				pureModule(moduleWithExports("leaf", []Declaration{{Name: "shared"}, {Name: "def"}, {Name: "x"}},
					[]Export{LocalExport("shared", "shared"), LocalExport("default", "def"), LocalExport("x", "x")})),
				pureModule(moduleWithExports("same", []Declaration{{Name: "y"}},
					[]Export{WildcardExport("leaf"), LocalExport("y", "y")})),
				pureModule(moduleWithExports("different", []Declaration{{Name: "shared"}},
					[]Export{LocalExport("shared", "shared")})),
				pureModule(moduleWithExports("consistent", nil,
					[]Export{WildcardExport("leaf"), WildcardExport("same")})),
				pureModule(moduleWithExports("ambiguous", nil,
					[]Export{WildcardExport("leaf"), WildcardExport("different")})),
				pureModule(moduleWithExports("local-wins", []Declaration{{Name: "shared"}},
					[]Export{LocalExport("shared", "shared"), WildcardExport("different"), WildcardExport("leaf")})),
				pureModule(moduleWithExports("renamer", nil,
					[]Export{NamedExport("renamed", "leaf", "x")})),
				{
					ID: "main",
					Declarations: []Declaration{
						{Name: "keepShared", References: []string{"consistentShared"}},
						{Name: "keepLocal", References: []string{"localShared"}},
						{Name: "keepRenamed", References: []string{"renamed"}},
						{Name: "scan", References: []string{"consistentNamespace"}},
						{Name: "scanAmbiguous", References: []string{"ambiguousNamespace"}},
					},
					Imports: []Import{
						{Bindings: []ImportBinding{{LocalName: "consistentShared", ImportedName: "shared"}}, TargetModule: "consistent"},
						{Bindings: []ImportBinding{{LocalName: "localShared", ImportedName: "shared"}}, TargetModule: "local-wins"},
						{Bindings: []ImportBinding{{LocalName: "renamed", ImportedName: "renamed"}}, TargetModule: "renamer"},
						{Bindings: []ImportBinding{{LocalName: "consistentNamespace", ImportedName: WildcardImportName}}, TargetModule: "consistent"},
						{Bindings: []ImportBinding{{LocalName: "ambiguousNamespace", ImportedName: WildcardImportName}}, TargetModule: "ambiguous"},
					},
					Exports: []Export{
						LocalExport("keepShared", "keepShared"),
						LocalExport("keepLocal", "keepLocal"),
						LocalExport("keepRenamed", "keepRenamed"),
						LocalExport("scan", "scan"),
						LocalExport("scanAmbiguous", "scanAmbiguous"),
					},
				},
			},
			entries:  []string{"main"},
			included: []string{"leaf", "local-wins", "main", "same"},
			kept: map[DeclarationID]KeepReason{
				{ModuleID: "main", Name: "keepShared"}:    ReasonEntryExport,
				{ModuleID: "main", Name: "keepLocal"}:     ReasonEntryExport,
				{ModuleID: "main", Name: "keepRenamed"}:   ReasonEntryExport,
				{ModuleID: "main", Name: "scan"}:          ReasonEntryExport,
				{ModuleID: "main", Name: "scanAmbiguous"}: ReasonEntryExport,
				{ModuleID: "leaf", Name: "shared"}:        ReasonReferenced,
				{ModuleID: "leaf", Name: "x"}:             ReasonReferenced,
				{ModuleID: "same", Name: "y"}:             ReasonReferenced,
				{ModuleID: "local-wins", Name: "shared"}:  ReasonReferenced,
			},
		},
		{
			name: "import cycle is retained",
			modules: []Module{
				{
					ID: "a",
					Declarations: []Declaration{
						{Name: "aVal", References: []string{"bVal"}},
					},
					Imports: []Import{{
						TargetModule: "b",
						Bindings:     []ImportBinding{{LocalName: "bVal", ImportedName: "bVal"}},
					}},
					Exports: []Export{LocalExport("aVal", "aVal")},
				},
				{
					ID: "b",
					Declarations: []Declaration{
						{Name: "bVal", References: []string{"aVal"}},
					},
					Imports: []Import{{
						TargetModule: "a",
						Bindings:     []ImportBinding{{LocalName: "aVal", ImportedName: "aVal"}},
					}},
					Exports: []Export{LocalExport("bVal", "bVal")},
				},
			},
			entries:  []string{"a"},
			included: []string{"a", "b"},
			kept: map[DeclarationID]KeepReason{
				{ModuleID: "a", Name: "aVal"}: ReasonEntryExport,
				{ModuleID: "b", Name: "bVal"}: ReasonReferenced,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registry := NewRegistry()
			for _, module := range test.modules {
				if err := registry.Register(module); err != nil {
					t.Fatalf("register %s: %v", module.ID, err)
				}
			}
			t.Logf("input modules=%#v entries=%#v basis=%s", test.modules, test.entries, "entry exports seed referenced and side-effect closure")

			result, err := registry.NewSession(test.entries).Solve()
			if err != nil {
				t.Fatalf("solve: %v", err)
			}
			t.Logf("output included=%#v kept=%#v basis=%s", result.IncludedModules, result.Reasons, "least fixed point with reason precedence entry-export > referenced > side-effect")

			if !reflect.DeepEqual(result.IncludedModules, test.included) {
				t.Fatalf("included = %#v, want %#v", result.IncludedModules, test.included)
			}
			if !reflect.DeepEqual(result.Reasons, test.kept) {
				t.Fatalf("kept = %#v, want %#v", result.Reasons, test.kept)
			}
			if len(result.KeptDeclarations) != len(test.kept) {
				t.Fatalf("kept declaration list length = %d, want %d", len(result.KeptDeclarations), len(test.kept))
			}
		})
	}
}

func TestResolutionErrors(t *testing.T) {
	tests := []struct {
		name    string
		modules []Module
		entries []string
		code    ErrorCode
	}{
		{
			name: "ambiguous named import",
			modules: []Module{
				moduleWithExports("a", []Declaration{{Name: "x"}}, []Export{LocalExport("x", "x")}),
				moduleWithExports("b", []Declaration{{Name: "x"}}, []Export{LocalExport("x", "x")}),
				moduleWithExports("bar", nil, []Export{WildcardExport("a"), WildcardExport("b")}),
				{
					ID: "foo",
					Imports: []Import{{
						TargetModule: "bar",
						Bindings:     []ImportBinding{{LocalName: "x", ImportedName: "x"}},
					}},
				},
			},
			entries: []string{"foo"},
			code:    AmbiguousExport,
		},
		{
			name: "reexport cycle",
			modules: []Module{
				moduleWithExports("a", nil, []Export{NamedExport("x", "b", "x")}),
				moduleWithExports("b", nil, []Export{NamedExport("x", "a", "x")}),
				{
					ID: "main",
					Imports: []Import{{
						TargetModule: "a",
						Bindings:     []ImportBinding{{LocalName: "x", ImportedName: "x"}},
					}},
				},
			},
			entries: []string{"main"},
			code:    ReexportCycle,
		},
		{
			name: "missing export",
			modules: []Module{
				{ID: "lib"},
				{
					ID: "main",
					Imports: []Import{{
						TargetModule: "lib",
						Bindings:     []ImportBinding{{LocalName: "x", ImportedName: "x"}},
					}},
				},
			},
			entries: []string{"main"},
			code:    MissingExport,
		},
		{
			name:    "unknown entry",
			modules: []Module{{ID: "main"}},
			entries: []string{"missing"},
			code:    UnknownModule,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registry := NewRegistry()
			for _, module := range test.modules {
				if err := registry.Register(module); err != nil {
					t.Fatal(err)
				}
			}
			t.Logf("input modules=%#v entries=%#v basis=resolution errors are checked after least fixed point", test.modules, test.entries)
			_, err := registry.NewSession(test.entries).Solve()
			if err == nil {
				t.Fatal("expected error")
			}
			analysisError, ok := err.(*AnalysisError)
			if !ok || analysisError.Code != test.code {
				t.Fatalf("error = %#v, want code %s", err, test.code)
			}
			t.Logf("output error=%#v basis=first module ID and binding registration order", analysisError)
		})
	}
}

func TestConstructionValidation(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(Module{}); err == nil || err.(*AnalysisError).Code != InvalidArgument {
		t.Fatalf("empty ID error = %v", err)
	}

	module := Module{
		ID: "m",
		Declarations: []Declaration{
			{Name: "a"},
			{Name: "a"},
		},
	}
	if err := registry.Register(module); err == nil || err.(*AnalysisError).Code != InvalidArgument {
		t.Fatalf("duplicate declaration error = %v", err)
	}

	module = Module{ID: "m", Exports: []Export{LocalExport("x", "x"), LocalExport("x", "x")}}
	if err := registry.Register(module); err == nil || err.(*AnalysisError).Code != InvalidArgument {
		t.Fatalf("duplicate export error = %v", err)
	}

	if err := registry.Register(Module{ID: "once"}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(Module{ID: "once"}); err == nil || err.(*AnalysisError).Code != DuplicateModule {
		t.Fatalf("duplicate module error = %v", err)
	}

	invalidRegistry := NewRegistry()
	err := invalidRegistry.Register(Module{
		ID:           "m",
		Declarations: []Declaration{{Name: "a", References: []string{"missing"}}},
	})
	if err != nil {
		t.Fatalf("invalid reference should be checked on snapshot solve: %v", err)
	}
	_, err = invalidRegistry.NewSession([]string{"m"}).Solve()
	if err == nil || err.(*AnalysisError).Code != UndefinedReference {
		t.Fatalf("undefined reference error = %v", err)
	}
}

func moduleWithExports(id string, declarations []Declaration, exports []Export) Module {
	return Module{
		ID:           id,
		Declarations: declarations,
		Exports:      exports,
	}
}

func pureModule(module Module) Module {
	module.SideEffects = SideEffectsNo
	return module
}
