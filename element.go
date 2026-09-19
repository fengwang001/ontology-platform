package ontology

// Direction controls only the Score dimension used by Selector.
type Direction int

const (
	// Desc keeps the K highest scores.
	Desc Direction = iota
	// Asc keeps the K lowest scores.
	Asc
)

// Element is one ranked ID and score.
type Element struct {
	ID    string
	Score float64
}
