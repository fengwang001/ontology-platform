package ontology

// Reason 标识一次被整体拒绝的操作的可区分原因。
type Reason string

const (
	ReasonUnknownAsset      Reason = "unknown_asset"
	ReasonDuplicateEdge     Reason = "duplicate_edge"
	ReasonDuplicateAsset    Reason = "duplicate_asset"
	ReasonCycle             Reason = "dependency_cycle"
	ReasonBadOffset         Reason = "lo_greater_than_hi"
	ReasonBadRange          Reason = "first_greater_than_last"
	ReasonPartitionOutside  Reason = "partition_outside_range"
	ReasonAlreadyRunning    Reason = "already_running"
	ReasonInputNotReady     Reason = "input_not_ready"
	ReasonRunNotFound       Reason = "run_not_found"
	ReasonRunFinished       Reason = "run_already_finished"
	ReasonExternalOnDerived Reason = "external_write_on_non_source"
)

// Error 携带机器可读的原因与诊断细节。
type Error struct {
	Reason  Reason
	Message string
	// Details 保存原因相关的结构化信息，例如未就绪输入、成环节点。
	Details map[string]any
}

func (e *Error) Error() string {
	return string(e.Reason) + ": " + e.Message
}

func newError(reason Reason, msg string) *Error {
	return &Error{Reason: reason, Message: msg, Details: map[string]any{}}
}

// NotReadyInput 描述一个未就绪（缺失或过期）的输入分区。
type NotReadyInput struct {
	Asset     string
	Partition int
	Missing   bool
}
