package ontology

// InvalidInputError 表示参数非法：目标实例不存在、被订正的序号不存在、
// 或被订正的序号本身就是一条订正记录。
//
// 按题目要求，参数非法的报错优先级高于「审计写入失败」。
type InvalidInputError struct{ msg string }

func (e *InvalidInputError) Error() string { return "invalid input: " + e.msg }

// AuditWriteError 表示审计记录写入失败。此时状态变更必须整体撤销，
// 且该次尝试不占用任何序号（序号严格递增无空洞）。
type AuditWriteError struct{ msg string }

func (e *AuditWriteError) Error() string { return "audit write failed: " + e.msg }

// IsInvalidInput 判断错误是否为参数非法。
func IsInvalidInput(err error) bool {
	_, ok := err.(*InvalidInputError)
	return ok
}

// IsAuditWriteFailure 判断错误是否为审计写入失败。
func IsAuditWriteFailure(err error) bool {
	_, ok := err.(*AuditWriteError)
	return ok
}
