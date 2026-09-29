// Package evolution provides a schema-evolution column mapper.
package evolution

import "fmt"

// RejectReason identifies why an operation or event was rejected.
// Each value is a distinct, machine-readable cause.
type RejectReason string

const (
	// ReasonInvalidType: a column declares a type the system does not know.
	ReasonInvalidType RejectReason = "invalid_type"
	// ReasonEmptyColumnName: a column name is "".
	ReasonEmptyColumnName RejectReason = "empty_column_name"
	// ReasonDuplicateColumn: two columns share the same name.
	ReasonDuplicateColumn RejectReason = "duplicate_column"
	// ReasonVersionExists: registering/evolving onto an already-used version.
	ReasonVersionExists RejectReason = "version_exists"
	// ReasonNoCurrentVersion: evolution requested before any initial schema.
	ReasonNoCurrentVersion RejectReason = "no_current_version"
	// ReasonUnknownColumn: an update/delete targets a missing column.
	ReasonUnknownColumn RejectReason = "unknown_column"
	// ReasonColumnExists: an add targets an existing column.
	ReasonColumnExists RejectReason = "column_exists"
	// ReasonInvalidChangeOp: the change operation kind is not add/update/delete.
	ReasonInvalidChangeOp RejectReason = "invalid_change_op"
	// ReasonVersionNotRegistered: projecting/querying an unregistered version.
	ReasonVersionNotRegistered RejectReason = "version_not_registered"
	// ReasonRequiredMissing: a required target column is absent in the event.
	ReasonRequiredMissing RejectReason = "required_column_missing"
	// ReasonConversionFailed: string->int on content that is not a legal integer.
	ReasonConversionFailed RejectReason = "conversion_failed"
	// ReasonInvalidValue: an event value's Go type cannot belong to its column.
	ReasonInvalidValue RejectReason = "invalid_value"
)

// RejectError is a rejection carrying a distinguishable reason.
type RejectError struct {
	Reason  RejectReason
	Message string
}

func (e *RejectError) Error() string {
	return string(e.Reason) + ": " + e.Message
}

func reject(why RejectReason, format string, args ...any) *RejectError {
	return &RejectError{Reason: why, Message: fmt.Sprintf(format, args...)}
}

// ColumnDecision is the per-column outcome category.
type ColumnDecision string

const (
	// DecisionAligned: column found by name and copied unchanged.
	DecisionAligned ColumnDecision = "aligned"
	// DecisionConverted: column found by name and type-converted.
	DecisionConverted ColumnDecision = "converted"
	// DecisionDefaulted: column missing in the event; zero value filled.
	DecisionDefaulted ColumnDecision = "defaulted"
	// DecisionDropped: an event column no longer exists; silently dropped.
	DecisionDropped ColumnDecision = "dropped"
	// DecisionRejected: processing this column caused the whole-event reject.
	DecisionRejected ColumnDecision = "rejected"
)

// ColumnResult records the per-column decision made while projecting.
// Even rejected projections carry the partial explanation up to the failure.
type ColumnResult struct {
	// Name is the target/event column name.
	Name string
	// Decision explains how this column was handled.
	Decision ColumnDecision
	// SourceType / TargetType are the declared types ("" when not applicable).
	SourceType ColumnType
	TargetType ColumnType
	// SourceValue is what the event provided (nil when the column was missing).
	SourceValue any
	// OutValue is the projected value (nil for dropped columns).
	OutValue any
	// Basis is the human-readable rule that justified the decision.
	Basis string
}
