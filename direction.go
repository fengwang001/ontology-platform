package ontology

// Direction controls which Score dimension ranks "better".
// It never affects the tie-breaking rule: equal scores are always
// broken by ID in ascending lexicographic order.
type Direction int

const (
	// Desc keeps the K elements with the largest Score values.
	Desc Direction = iota
	// Asc keeps the K elements with the smallest Score values.
	Asc
)

func (d Direction) valid() bool {
	return d == Desc || d == Asc
}
