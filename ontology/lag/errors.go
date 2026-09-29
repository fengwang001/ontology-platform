package lag

import "fmt"

// Reason 标识一次被整体拒绝的输入的具体原因；各原因互不相同、可区分。
type Reason string

const (
	// ReasonEmptyPartition 分区名为空字符串。
	ReasonEmptyPartition Reason = "empty_partition"
	// ReasonEmptyID 行标识为空字符串。
	ReasonEmptyID Reason = "empty_id"
	// ReasonDuplicateID 插入的标识在该分区中已存在（重复标识）。
	ReasonDuplicateID Reason = "duplicate_id"
	// ReasonMissingID 删除的标识在该分区中不存在。
	ReasonMissingID Reason = "missing_id"
	// ReasonRowLimitExceeded 插入后行数超过引擎允许的上限。
	ReasonRowLimitExceeded Reason = "row_limit_exceeded"
)

// RejectError 描述一次被整体拒绝的变更。拒绝不留痕：返回该错误时
// 行、视图与已提交日志均保持调用前状态。
type RejectError struct {
	Reason Reason
	Op     string
	Part   string
	ID     string
	Detail string
}

func (e *RejectError) Error() string {
	return fmt.Sprintf("lag: op=%s partition=%q id=%q reason=%s: %s",
		e.Op, e.Part, e.ID, e.Reason, e.Detail)
}
