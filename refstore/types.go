// Package refstore 实现代码托管服务端的引用更新事务处理器：
// 对一批引用更新指令做全有或全无的裁决，并逐条给出可区分的拒绝原因。
package refstore

// Reason 是单条指令的拒绝原因。声明顺序即优先级顺序：
// 一条指令同时触发多个原因时，只报告声明在前的那个。
type Reason int

const (
	ReasonNone               Reason = iota // 通过
	ReasonInvalidArgument                  // 参数非法
	ReasonInvalidRefName                   // 引用名非法
	ReasonObjectNotFound                   // 对象不存在
	ReasonOldValueMismatch                 // 旧值不匹配
	ReasonProtectedNoCreate                // 受保护禁止创建
	ReasonProtectedNoDelete                // 受保护禁止删除
	ReasonProtectedAllowlist               // 受保护仅限名单
	ReasonTagNoUpdate                      // 标签不可更新
	ReasonProtectedNoNonFF                 // 受保护禁止非快进
	ReasonNonFastForward                   // 未带强制标志的非快进
	ReasonBatchConflict                    // 批内冲突
	ReasonNotExecuted                      // 因他项拒绝而未执行
)

var reasonNames = map[Reason]string{
	ReasonNone:               "通过",
	ReasonInvalidArgument:    "参数非法",
	ReasonInvalidRefName:     "引用名非法",
	ReasonObjectNotFound:     "对象不存在",
	ReasonOldValueMismatch:   "旧值不匹配",
	ReasonProtectedNoCreate:  "受保护禁止创建",
	ReasonProtectedNoDelete:  "受保护禁止删除",
	ReasonProtectedAllowlist: "受保护仅限名单",
	ReasonTagNoUpdate:        "标签不可更新",
	ReasonProtectedNoNonFF:   "受保护禁止非快进",
	ReasonNonFastForward:     "未带强制标志的非快进",
	ReasonBatchConflict:      "批内冲突",
	ReasonNotExecuted:        "因他项拒绝而未执行",
}

func (r Reason) String() string { return reasonNames[r] }

// Instruction 是一条引用更新指令。
// Old 为空表示要求引用当前不存在（创建）；New 为空表示删除；
// 二者都非空表示更新；二者都空为参数非法。
type Instruction struct {
	Ref   string
	Old   string
	New   string
	Force bool
}

// Verdict 是单条指令的裁决结果。OK 为 true 时 Reason 必为 ReasonNone。
type Verdict struct {
	OK     bool
	Reason Reason
}

// RefUpdate 记录一条已应用更新的前后值，写入审计记录。
type RefUpdate struct {
	Ref string
	Old string
	New string
}

// AuditRecord 是一次成功推送的审计记录。Seq 严格递增且无空洞。
type AuditRecord struct {
	Seq     uint64
	User    string
	Updates []RefUpdate
}

// BatchResult 是整批推送的应答。Applied 为 false 时所有引用保持原值；
// Verdicts 与请求的指令一一对应。
type BatchResult struct {
	Applied  bool
	Verdicts []Verdict
	AuditSeq uint64 // 仅 Applied 为 true 时有效
}
