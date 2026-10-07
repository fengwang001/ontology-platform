package contract

import "fmt"

// ContractParams 是创建主合同的参数。
// 自动续签由三个普通条款承载，其整数取值含义为：
//   - AutoRenewClause：到期日当日有效值非 0 表示允许自动续签；
//   - PeriodClause：到期日当日有效值为续签一期长度（天），<= 0 视为不续签；
//   - NoticeClause：到期日当日有效值为不续签通知所需提前天数。
type ContractParams struct {
	Parties         [2]string   // 双方，须非空且互不相同
	Clauses         map[int]int // 条款编号 -> 主合同取值
	Locked          []int       // 锁定类条款编号（修改须法务会签）
	Start           int         // 起始日（含）
	Expiry          int         // 初始到期日（含），须 >= Start
	AutoRenewClause int         // 自动续签开关条款编号
	PeriodClause    int         // 续签期长度条款编号
	NoticeClause    int         // 不续签通知提前天数条款编号
	SignWindowDays  int         // 一方签署后对方可补签的天数窗口
}

// Source 标记某次有效值查询的来源。
type Source struct {
	Master      bool // true 表示取值来自主合同原文
	AmendmentID int  // Master 为 false 时，取值来源的补充协议编号
}

func (s Source) String() string {
	if s.Master {
		return "master"
	}
	return fmt.Sprintf("amendment#%d", s.AmendmentID)
}

// Renewal 记录一次已发生的自动续签。
type Renewal struct {
	OldExpiry  int // 触发本次判定的到期日
	NewExpiry  int // 续签后的新到期日
	Period     int // 续签期长度（取自到期日当日有效值）
	NoticeDays int // 判定时的不续签通知提前天数
}

// OpKind 枚举全部操作类别。
type OpKind int

const (
	OpCreateContract OpKind = iota
	OpCreateAmendment
	OpSign
	OpCountersign
	OpNotice
	OpTerminate
	OpQueryValue
	OpQueryInForce
	OpQueryExpiry
	OpQueryRenewals
)

func (k OpKind) String() string {
	switch k {
	case OpCreateContract:
		return "CreateContract"
	case OpCreateAmendment:
		return "CreateAmendment"
	case OpSign:
		return "Sign"
	case OpCountersign:
		return "Countersign"
	case OpNotice:
		return "NonRenewalNotice"
	case OpTerminate:
		return "Terminate"
	case OpQueryValue:
		return "QueryValue"
	case OpQueryInForce:
		return "QueryInForce"
	case OpQueryExpiry:
		return "QueryExpiry"
	case OpQueryRenewals:
		return "QueryRenewals"
	}
	return fmt.Sprintf("opkind(%d)", int(k))
}

// Op 是一次操作的完整自描述，可直接持久化用于重放。
// 不同类别使用不同字段：
//   - CreateContract：Params
//   - CreateAmendment：Contract、DeclaredEffDay、Mods 或 IsRevocation+RevokeTarget
//   - Sign：Contract、Amendment、Party、AuthFrom、AuthTo
//   - Countersign / Terminate：Contract、Amendment（Terminate 不用 Amendment）
//   - Notice：Contract、Party
//   - QueryValue：Contract、Clause、Day
//   - QueryInForce：Contract、Day
//   - QueryExpiry / QueryRenewals：Contract
type Op struct {
	Kind     OpKind
	Now      int // 操作时刻（整数日序号）
	Contract int // 合同编号（CreateContract 时忽略）

	Params ContractParams // CreateContract

	DeclaredEffDay int         // CreateAmendment：声明生效日
	Mods           map[int]int // CreateAmendment：条款修改
	IsRevocation   bool        // CreateAmendment：是否为撤销协议
	RevokeTarget   int         // CreateAmendment：被撤销协议编号

	Amendment int    // Sign / Countersign
	Party     string // Sign / Notice
	AuthFrom  int    // Sign：签署授权有效期起（含）
	AuthTo    int    // Sign：签署授权有效期止（含）

	Clause int // QueryValue
	Day    int // QueryValue / QueryInForce：查询的目标日
}

// OpResult 是一次操作的结果。Err 为 None 表示被接受。
type OpResult struct {
	Err     Category
	Message string

	ContractID  int       // CreateContract
	AmendmentID int       // CreateAmendment
	Value       int       // QueryValue
	Source      Source    // QueryValue
	InForce     bool      // QueryInForce
	Expiry      int       // QueryExpiry
	Renewals    []Renewal // QueryRenewals
}

// LogEntry 是操作日志条目：输入操作与其结果。
type LogEntry struct {
	Op     Op
	Result OpResult
}
