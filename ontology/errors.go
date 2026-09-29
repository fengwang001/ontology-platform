package ontology

import "errors"

// 写入被整体拒绝的原因。
var (
	// ErrEmptyKey 记录的键为空：校验不通过，不分配序号。
	ErrEmptyKey = errors.New("ontology: record key must not be empty")
	// ErrLogSealed 日志已结束（被密封），拒绝再写入。
	ErrLogSealed = errors.New("ontology: log is sealed and rejects writes")
)

// 导出会话操作被整体拒绝的原因。
var (
	// ErrExportNotStarted 会话尚未 Start 就调用 Next/End/Drained/Report。
	ErrExportNotStarted = errors.New("ontology: export session has not started")
	// ErrExportAlreadyStarted 会话已经 Start，再次 Start 被拒绝。
	ErrExportAlreadyStarted = errors.New("ontology: export session already started")
	// ErrExportEnded 会话已经 End，再次 Next/End/Start 被拒绝。
	ErrExportEnded = errors.New("ontology: export session has ended")
	// ErrExportLimitExceeded 未结束的导出会话数已达上限，Start 被拒绝。
	ErrExportLimitExceeded = errors.New("ontology: active export session limit exceeded")
	// ErrSeam 导出结束核验失败：快照段或增量段不满足无缝拼接不变量。
	ErrSeam = errors.New("ontology: export segments are not seamlessly contiguous")
	// ErrLogStillOpen 日志尚未密封却要求完整性核验，无法保证完整导出。
	ErrLogStillOpen = errors.New("ontology: log is still open; completeness cannot be verified")
)
