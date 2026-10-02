package ontology

// Encoding marks how a page's Data is encoded.
type Encoding uint8

const (
	// EncPlain stores raw little-endian uint32 values.
	EncPlain Encoding = iota
	// EncDict stores a dictionary-encoded hybrid RLE / bit-packed page.
	EncDict
)

// Page is one flushed column page.
type Page struct {
	Enc     Encoding
	Rows    int
	Width   int
	DictLen int
	Data    []byte
}

// Package-level errors.
var (
	// ErrParam is returned for invalid constructor parameters.
	ErrParam = errOntology("ontology: invalid parameter")
	// ErrFull is returned when appending to a full page buffer.
	ErrFull = errOntology("ontology: page buffer full")
	// ErrEmpty is returned when flushing an empty buffer.
	ErrEmpty = errOntology("ontology: page buffer empty")
	// ErrCorrupt is returned for malformed pages.
	ErrCorrupt = errOntology("ontology: corrupt page")
)

type errOntology string

func (e errOntology) Error() string { return string(e) }
