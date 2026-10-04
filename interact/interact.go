package interact

import "errors"

// ErrFrozen is returned for configuration changes after the first Submit.
var ErrFrozen = errors.New("interact: configuration frozen")

// ErrInvalid is returned for malformed arguments.
var ErrInvalid = errors.New("interact: invalid argument")

// Table stores symmetric ingredient interaction grades and patient allergies.
type Table struct {
	pairs   map[string]int
	allergy map[string]map[string]struct{}
	frozen  bool
}

// New creates an empty interaction table.
func New() *Table {
	return &Table{pairs: map[string]int{}, allergy: map[string]map[string]struct{}{}}
}

// pairKey stores one unordered pair under the lexicographically smaller key.
func pairKey(ingA, ingB []byte) string {
	if string(ingA) <= string(ingB) {
		return string(ingA) + "\x00" + string(ingB)
	}
	return string(ingB) + "\x00" + string(ingA)
}

// SetPair sets the symmetric grade between two distinct ingredients.
// grade is 1 (notice), 2 (caution) or 3 (contraindicated); unset is 0.
func (t *Table) SetPair(ingA, ingB []byte, grade int) error {
	if t.frozen {
		return ErrFrozen
	}
	if len(ingA) == 0 || len(ingB) == 0 || string(ingA) == string(ingB) || grade < 1 || grade > 3 {
		return ErrInvalid
	}
	t.pairs[pairKey(ingA, ingB)] = grade
	return nil
}

// SetAllergy registers an allergy of a patient to an ingredient.
func (t *Table) SetAllergy(patient string, ing []byte) error {
	if t.frozen {
		return ErrFrozen
	}
	if patient == "" || len(ing) == 0 {
		return ErrInvalid
	}
	set := t.allergy[patient]
	if set == nil {
		set = map[string]struct{}{}
		t.allergy[patient] = set
	}
	set[string(ing)] = struct{}{}
	return nil
}

// Freeze locks the table at the first Submit.
func (t *Table) Freeze() {
	t.frozen = true
}

// Grade returns the interaction grade (0 if unset).
func (t *Table) Grade(ingA, ingB []byte) int {
	if string(ingA) == string(ingB) {
		return 0
	}
	return t.pairs[pairKey(ingA, ingB)]
}

// Allergic reports whether a patient is allergic to an ingredient.
func (t *Table) Allergic(patient string, ing []byte) bool {
	_, ok := t.allergy[patient][string(ing)]
	return ok
}
