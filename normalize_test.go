package ontology

import "testing"

func nameStore(norm Normalize) *Store {
	return NewStore(norm, Constraint{Name: "uq_name", Cols: []string{"name"}})
}

// TestNormalizeFourCombos covers all four toggle combinations with the
// pair "  Alice" / "alice": only trim+fold together makes them clash.
func TestNormalizeFourCombos(t *testing.T) {
	cases := []struct {
		name string
		norm Normalize
		want bool // conflict expected
	}{
		{"none", Normalize{TrimSpace: false, CaseFold: false}, false},
		{"trim_only", Normalize{TrimSpace: true, CaseFold: false}, false},
		{"fold_only", Normalize{TrimSpace: false, CaseFold: true}, false},
		{"trim_and_fold", Normalize{TrimSpace: true, CaseFold: true}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := nameStore(tc.norm)
			mustNoErr(t, s.Insert("a", rec("name", "  Alice")))
			err := s.Insert("b", rec("name", "alice"))
			if got := err != nil; got != tc.want {
				t.Fatalf("conflict=%v, want %v (err=%v)", got, tc.want, err)
			}
		})
	}
}

// TestFoldNonASCII proves folding is Unicode-aware, not ASCII-only.
func TestFoldNonASCII(t *testing.T) {
	norm := Normalize{CaseFold: true}
	conflict := []struct{ a, b string }{
		{"STRASSE", "straße"}, // German ß folds to ss
		{"ß", "SS"},           // sharp s vs double s
		{"İ", "i\u0307"},      // Turkish dotted capital I -> i + U+0307
		{"ΣΣ", "σς"},          // Greek final sigma folds to σ
		{"ΟΔΟΣ", "οδος"},      // word-final sigma in context
		{"Alice", "ALICE"},    // plain ASCII still folds
	}
	for _, tc := range conflict {
		s := nameStore(norm)
		mustNoErr(t, s.Insert("a", rec("name", tc.a)))
		if err := s.Insert("b", rec("name", tc.b)); err == nil {
			t.Errorf("expected conflict between %q and %q", tc.a, tc.b)
		}
	}

	// Turkish İ must NOT fold to bare "i" (full folding keeps U+0307).
	s := nameStore(norm)
	mustNoErr(t, s.Insert("a", rec("name", "İ")))
	if err := s.Insert("b", rec("name", "i")); err != nil {
		t.Errorf("İ and i must not conflict under full folding: %v", err)
	}
}

// TestStoredValueUnchanged verifies normalization never alters storage.
func TestStoredValueUnchanged(t *testing.T) {
	s := nameStore(Normalize{TrimSpace: true, CaseFold: true})
	raw := "  Åliceß "
	mustNoErr(t, s.Insert("a", rec("name", raw)))
	got, ok := s.Get("a")
	if !ok {
		t.Fatal("record missing")
	}
	if got["name"].Str != raw {
		t.Fatalf("stored %q, want byte-identical %q", got["name"].Str, raw)
	}
}
