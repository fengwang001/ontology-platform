package formulary

import "errors"

// ErrFrozen is returned for configuration changes after the first Submit.
var ErrFrozen = errors.New("formulary: configuration frozen")

// ErrInvalid is returned for malformed arguments.
var ErrInvalid = errors.New("formulary: invalid argument")

// ErrExists is returned when a drug id is registered twice.
var ErrExists = errors.New("formulary: drug already exists")

// Drug describes one drug product and its active ingredient.
type Drug struct {
	ID    string
	Ing   []byte
	Mg    int
	Level int
}

// Formulary is the drug catalogue with per-ingredient daily dose caps.
type Formulary struct {
	drugs  map[string]Drug
	max    map[string]int
	frozen bool
}

// New creates an empty formulary.
func New() *Formulary {
	return &Formulary{drugs: map[string]Drug{}, max: map[string]int{}}
}

// AddDrug registers a drug. Ingredient must be a non-empty byte string,
// mg in [1,1e6] and prescription level in [1,3].
func (f *Formulary) AddDrug(id string, ing []byte, mg, level int) error {
	if f.frozen {
		return ErrFrozen
	}
	if id == "" || len(ing) == 0 || mg < 1 || mg > 1_000_000 || level < 1 || level > 3 {
		return ErrInvalid
	}
	if _, ok := f.drugs[id]; ok {
		return ErrExists
	}
	stored := make([]byte, len(ing))
	copy(stored, ing)
	f.drugs[id] = Drug{ID: id, Ing: stored, Mg: mg, Level: level}
	return nil
}

// SetMax sets the per-day maximum dose (milligrams) for an ingredient.
// An unset cap means no limit. maxDay is in [1,1e9].
func (f *Formulary) SetMax(ing []byte, maxDay int) error {
	if f.frozen {
		return ErrFrozen
	}
	if len(ing) == 0 || maxDay < 1 || maxDay > 1_000_000_000 {
		return ErrInvalid
	}
	f.max[string(ing)] = maxDay
	return nil
}

// Freeze locks the catalogue at the first Submit.
func (f *Formulary) Freeze() {
	f.frozen = true
}

// Drug looks up a registered drug.
func (f *Formulary) Drug(id string) (Drug, bool) {
	d, ok := f.drugs[id]
	return d, ok
}

// Max returns the daily cap for an ingredient.
func (f *Formulary) Max(ing []byte) (int, bool) {
	m, ok := f.max[string(ing)]
	return m, ok
}
