package ontology

type ErrorKind string

const (
	KindObjectNotFound       ErrorKind = "object_not_found"
	KindVersionStale         ErrorKind = "version_stale"
	KindTypePermissionDenied ErrorKind = "type_permission_denied"
	KindPropertyNotFound     ErrorKind = "property_identifier_not_found"
	KindPermissionRevoked    ErrorKind = "permission_revoked"
	KindAttributePermission  ErrorKind = "attribute_permission_denied"
	KindWritePolicyConflict  ErrorKind = "write_policy_conflict"
	KindInvalidInput         ErrorKind = "invalid_input"
)

type Error struct {
	Kind    ErrorKind
	Message string
}

func (e *Error) Error() string {
	return string(e.Kind) + ": " + e.Message
}

func newError(kind ErrorKind, message string) *Error {
	return &Error{Kind: kind, Message: message}
}
