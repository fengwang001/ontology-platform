package snapshot

import "strings"

// Error categories. They are mutually exclusive per finding and never merged.
type ErrorKind string

const (
	KindOutOfScope      ErrorKind = "out_of_scope"
	KindChecksumFailed  ErrorKind = "checksum_failed"
	KindCountMismatch   ErrorKind = "count_mismatch"
	KindDanglingRef     ErrorKind = "dangling_reference"
	KindTargetUntrusted ErrorKind = "target_untrusted"
)

// Finding describes one concrete problem located on a specific block (or
// request) and its reason.
type Finding struct {
	Kind    ErrorKind
	Type    string // object type / block name; empty for request-level findings
	Record  string // record id when applicable
	Field   string // link field when applicable
	Target  string // referenced target type when applicable
	Message string
}

func (f Finding) String() string { return f.Message }

// LoadError is returned for rejected load or aggregate requests. It may carry
// several findings (ordered by rejection priority), but every finding has a
// single distinguishable Kind.
type LoadError struct {
	Findings []Finding
}

func (e *LoadError) Error() string {
	parts := make([]string, len(e.Findings))
	for i, f := range e.Findings {
		parts[i] = f.Message
	}
	return strings.Join(parts, "; ")
}

func (e *LoadError) Has(kind ErrorKind) bool {
	for _, f := range e.Findings {
		if f.Kind == kind {
			return true
		}
	}
	return false
}
