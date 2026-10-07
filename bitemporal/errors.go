// Package bitemporal implements a bi-temporal snapshot exporter for the
// ontology platform.
//
// Every record carries a half-open valid-time interval (business truth) and a
// transaction time (when the system recorded it). Exports freeze a consistent
// slice at a single transaction-time point T: for every object and every
// valid-time point, only the record with the greatest transaction time not
// exceeding T is visible; points with no covering record are explicitly
// "unknown" rather than default-filled.
package bitemporal

// ErrorCode identifies an export failure category. Codes are part of the
// public contract so callers can distinguish the four mandated error classes.
type ErrorCode int

const (
	// CodeRetention: requested transaction time predates the retained horizon.
	CodeRetention ErrorCode = iota + 1
	// CodeSchemaUndefined: object type was not yet defined at time T.
	CodeSchemaUndefined
	// CodeInvalidRange: export range/parameter is malformed.
	CodeInvalidRange
	// CodeInconsistency: corrupted or contradictory record detected during export.
	CodeInconsistency
)

// ExportError is the structured error returned for every rejected export.
type ExportError struct {
	Code ErrorCode
	Msg  string
	err  error
}

func (e *ExportError) Error() string {
	if e == nil {
		return ""
	}
	if e.err != nil {
		return e.Code.String() + ": " + e.Msg + ": " + e.err.Error()
	}
	return e.Code.String() + ": " + e.Msg
}

// Unwrap exposes the underlying corruption cause, if any.
func (e *ExportError) Unwrap() error { return e.err }

// String maps codes to stable wire names.
func (c ErrorCode) String() string {
	switch c {
	case CodeRetention:
		return "RETENTION_RANGE_EXCEEDED"
	case CodeSchemaUndefined:
		return "SCHEMA_UNDEFINED_AT_T"
	case CodeInvalidRange:
		return "INVALID_EXPORT_RANGE"
	case CodeInconsistency:
		return "INCONSISTENT_RECORD"
	default:
		return "UNKNOWN_ERROR"
	}
}

// AsExportError extracts an *ExportError from err, if present.
func AsExportError(err error) (*ExportError, bool) {
	ee, ok := err.(*ExportError)
	return ee, ok
}
