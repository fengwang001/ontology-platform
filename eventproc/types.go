package eventproc

// Event 是一个变更事件：ID 全局唯一，Priority 为非负整数（值越大优先级越高）。
type Event struct {
	ID       string
	Priority int
	Payload  string
}

// BatchItem 描述快照中的一个批次条目。
type BatchItem struct {
	ID       string
	Priority int
	Payload  string
}

// BatchView 是某一优先级批次当前处理现场的不可变视图。
type BatchView struct {
	Priority int
	Events   []BatchItem
	// Cursor 是该批次已处理事件的数量；恢复时从 Cursor 位置继续。
	Cursor int
}

// FrameView 是保存现场栈中的一帧：被打断批次的优先级与其已处理位置。
type FrameView struct {
	Priority int
	Cursor   int
}

// Snapshot 是处理器某一时刻的完整状态视图，用于日志与自检。
type Snapshot struct {
	// Queued 为尚未成为当前批次的等待事件，按优先级分组，组内 FIFO。
	Queued map[int][]BatchItem
	// Current 为当前正在推进的批次；无当前批次时为 nil。
	Current *BatchView
	// Stack 为被高优先级打断而保存的现场，栈尾为最近一次保存的现场。
	Stack []FrameView
	// Processed 为已处理事件 ID 序列。
	Processed []string
}

// 可区分的拒绝原因。
var (
	// ErrNegativePriority 表示存在优先级为负数的事件。
	ErrNegativePriority = newValidationError("negative priority is not allowed")
	// ErrDuplicateID 表示事件标识与已有（等待/当前/栈中/已处理）标识重复。
	ErrDuplicateID = newValidationError("duplicate event id")
	// ErrEmptyBatch 表示提交了零个事件。
	ErrEmptyBatch = newValidationError("empty batch is not allowed")
	// ErrNoProcessable 表示当前没有任何可处理事件。
	ErrNoProcessable = newProcessError("no processable event")
)

// ValidationError 为入队校验失败错误，携带可区分原因与冲突明细。
type ValidationError struct{ msg string }

func (e *ValidationError) Error() string { return e.msg }

// ProcessError 为推进失败错误。
type ProcessError struct{ msg string }

func (e *ProcessError) Error() string { return e.msg }

func newValidationError(msg string) error { return &ValidationError{msg: msg} }
func newProcessError(msg string) error    { return &ProcessError{msg: msg} }
