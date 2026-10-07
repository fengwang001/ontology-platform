package ontology

// ErrorCategory 区分可机器处理的错误类别。
type ErrorCategory string

const (
	CategoryObjectTypeNotFound  ErrorCategory = "object_type_not_found"
	CategoryPrincipalNotFound   ErrorCategory = "principal_not_found"
	CategoryExportNotFound      ErrorCategory = "export_not_found"
	CategoryPropertyNotExcluded ErrorCategory = "property_not_excluded"
	CategoryInvalidArgument     ErrorCategory = "invalid_argument"
	CategoryInheritanceCycle    ErrorCategory = "inheritance_cycle"
)

// Error 是平台所有被拒绝请求的统一错误类型。
type Error struct {
	Category ErrorCategory
	Message  string
}

func (e *Error) Error() string { return string(e.Category) + ": " + e.Message }

func newError(cat ErrorCategory, msg string) *Error {
	return &Error{Category: cat, Message: msg}
}
