package ontology

// ErrorKind 是视图增量维护可报告的错误类别。
// 数值越小优先级越高；同一次维护过程中若同时触发多类错误，
// 只报告优先级最高的一类（见 MaintenanceResult.TopError）。
type ErrorKind int

const (
	// ErrLinkEndpointTypeMissing 参与聚合的链接关系两端对象类型中的任一方已不存在。
	ErrLinkEndpointTypeMissing ErrorKind = iota
	// ErrGroupingPropertyDeprecated 视图分组依据的时间类属性因对象类型版本迁移而被废弃。
	ErrGroupingPropertyDeprecated
	// ErrNoDefaultTimezoneAtWrite 对象类型在其时间类属性取值被写入时刻尚未定义默认时区。
	ErrNoDefaultTimezoneAtWrite
	// ErrMigrationValidationFailed 默认时区定义版本迁移校验失败。
	ErrMigrationValidationFailed
)

func (k ErrorKind) String() string {
	switch k {
	case ErrLinkEndpointTypeMissing:
		return "link endpoint object type missing"
	case ErrGroupingPropertyDeprecated:
		return "grouping time property deprecated"
	case ErrNoDefaultTimezoneAtWrite:
		return "no default timezone defined at write time"
	case ErrMigrationValidationFailed:
		return "timezone definition migration validation failed"
	}
	return "unknown"
}

// ViewError 描述一次被报告的错误。
type ViewError struct {
	Kind         ErrorKind
	ViewID       string
	ObjectID     string
	ObjectTypeID string
	Detail       string
}

func (e *ViewError) Error() string {
	return e.Kind.String() + ": " + e.Detail
}
