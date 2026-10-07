package docsync

// RejectKind classifies an operation rejection. Earlier kinds have higher
// precedence per the protocol: stale > reversed range > out of bounds >
// surrogate split > overlapping edits.
type RejectKind int

const (
	RejectStale RejectKind = iota
	RejectReversedRange
	RejectOutOfBounds
	RejectSurrogateSplit
	RejectOverlappingEdits
)

// RejectError describes why a change or diagnostic registration was refused.
type RejectError struct {
	Kind RejectKind
	// EditIndex is -1 when the error does not refer to an edit/diagnostic index.
	EditIndex int
	Pos       Position
	Msg       string
}

func (e *RejectError) Error() string {
	return e.Msg
}

func reject(kind RejectKind, idx int, pos Position, msg string) *RejectError {
	return &RejectError{Kind: kind, EditIndex: idx, Pos: pos, Msg: msg}
}

var _ error = (*RejectError)(nil)
