package hpack

// Header is one name/value pair.
type Header struct {
	Name  string
	Value string
}

// Block is one encoded header block: negotiated size updates followed by
// representation instructions.
type Block struct {
	Updates []int
	Instrs  []Instruction
}

// Instruction is one of Indexed, LiteralIndexed or LiteralNever.
type Instruction interface {
	instruction()
}

// Indexed represents an exact match against the static or dynamic table.
type Indexed struct{ Index int }

// LiteralIndexed represents a literal whose name may refer to a table entry;
// the decoder inserts the resulting entry. NameIdx 0 carries the literal Name.
type LiteralIndexed struct {
	NameIdx int
	Name    string
	Value   string
}

// LiteralNever represents a sensitive literal: never inserted by either side.
type LiteralNever struct {
	NameIdx int
	Name    string
	Value   string
}

func (Indexed) instruction()        {}
func (LiteralIndexed) instruction() {}
func (LiteralNever) instruction()   {}

// Error class returned by the decoder.
type Error string

const (
	ErrSyntax Error = "ErrSyntax"
	ErrIndex  Error = "ErrIndex"
	ErrUpdate Error = "ErrUpdate"
)

func (e Error) Error() string { return string(e) }
