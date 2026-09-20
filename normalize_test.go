package ontology

import "testing"

var nameConstraint = Constraint{Name: "uniq_name", Columns: []string{"name"}}

// conflict reports whether inserting b after a fails.
func conflict(norm NormOptions, a, b string) bool {
	s := New(norm, nameConstraint)
	if err := s.Insert("r1", map[string]Value{"name": Str(a)}); err != nil {
		panic(err)
	}
	return s.Insert("r2", map[string]Value{"name": Str(b)}) != nil
}

func TestNormFourCombinations(t *testing.T) {
	cases := []struct {
		name string
		norm NormOptions
		a, b string
		want bool
	}{
		{"off_off_same", NormOptions{false, false}, "alice", "alice", true},
		{"off_off_space", NormOptions{false, false}, "  Alice", "Alice", false},
		{"off_off_case", NormOptions{false, false}, "Alice", "alice", false},
		{"trim_only_space", NormOptions{true, false}, "  Alice  ", "Alice", true},
		{"trim_only_case", NormOptions{true, false}, "  Alice", "alice", false},
		{"fold_only_case", NormOptions{false, true}, "Alice", "ALICE", true},
		{"fold_only_space", NormOptions{false, true}, "  Alice", "alice", false},
		{"both_space_case", NormOptions{true, true}, "  Alice", "alice", true},
		{"both_tab_nl", NormOptions{true, true}, "\tAlice\n", "ALICE", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := conflict(tc.norm, tc.a, tc.b); got != tc.want {
				t.Errorf("conflict(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func TestCaseFoldNonASCII(t *testing.T) {
	fold := NormOptions{CaseFold: true}
	cases := []struct {
		name string
		a, b string
		want bool
	}{
		{"german_eszett_ss", "Straße", "STRASSE", true},
		{"german_capital_eszett", "straße", "STRAẞE", true},
		{"turkish_dotted_I", "İstanbul", "i̇stanbul", true},
		{"greek_final_sigma", "ΟΔΥΣΣΕΥΣ", "οδυσσευς", true},
		{"greek_sigma_forms", "Σ", "ς", true},
		{"greek_mixed", "Σωκράτης", "σωκράτης", true},
		{"ascii_fold", "Alice", "ALICE", true},
		{"distinct", "alice", "bob", false},
		{"eszett_not_single_s", "ß", "s", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := conflict(fold, tc.a, tc.b); got != tc.want {
				t.Errorf("conflict(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// Without CaseFold, non-ASCII case variants must NOT collide.
func TestNoFoldNoNonASCIIEquivalence(t *testing.T) {
	plain := NormOptions{}
	for _, pair := range [][2]string{
		{"Straße", "STRASSE"},
		{"Σ", "ς"},
		{"İ", "i"},
	} {
		if conflict(plain, pair[0], pair[1]) {
			t.Errorf("conflict(%q, %q) = true without CaseFold, want false", pair[0], pair[1])
		}
	}
}

// TrimSpace must trim Unicode whitespace, not just ASCII spaces.
func TestTrimUnicodeSpace(t *testing.T) {
	norm := NormOptions{TrimSpace: true}
	if !conflict(norm, " alice ", "alice") {
		t.Error("U+00A0 no-break space should be trimmed")
	}
}
