// Package docsync implements versioned text document synchronization with
// incremental edits and migrating/invalidating diagnostics.
package docsync

// Position is a zero-based (line, UTF-16 code unit column) position.
type Position struct {
	Line      int
	Character int
}

// Range is a half-open interval [Start, End); Start must not be after End.
type Range struct {
	Start Position
	End   Position
}

// Severity of a diagnostic.
type Severity int

const (
	SeverityError Severity = iota + 1
	SeverityWarning
	SeverityInformation
	SeverityHint
)

// Edit replaces Range with Text; an empty Range inserts, empty Text deletes.
type Edit struct {
	Range Range
	Text  string
}

// Diagnostic is a range annotation attached to a document version.
type Diagnostic struct {
	Range    Range
	Severity Severity
	Message  string
}

// DiagnosticEntry is an active diagnostic with its registration order.
type DiagnosticEntry struct {
	Diagnostic
	RegisteredAt int64
}

// InvalidatedEntry records a diagnostic that ceased to apply at a version.
type InvalidatedEntry struct {
	Diagnostic
	RegisteredAt       int64
	InvalidatedVersion int64
}

// Snapshot is a consistent, non-tearing view of one document version.
type Snapshot struct {
	Version     int64
	Text        string
	Diagnostics []DiagnosticEntry
	Invalidated []InvalidatedEntry
}
