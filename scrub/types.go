// Package scrub 实现多副本块存储的后台校验巡检与修复仲裁。
//
// 模块划分：
//   - types.go      公共类型：副本、巡检结果、告警、错误
//   - arbitrate.go  纯函数仲裁：自洽判定、已提交版本推断、权威副本选取
//   - heap.go       到期块索引：支撑不按块总数线性增长的到期选取
//   - service.go    服务编排：时钟、频率限制、修复写入、告警、并发控制
//
// 错误优先级（只报次序最靠前的一类）：
// 参数非法 > 时钟回退 > 块不存在 > 过于频繁。
package scrub

import "errors"

var (
	// ErrInvalidParam 参数非法（如版本为 0、摘要为空、副本数越界、节点重复等）。
	ErrInvalidParam = errors.New("scrub: invalid parameter")
	// ErrClockRegression 时钟回退：时刻小于上一次被接受操作的时刻。
	ErrClockRegression = errors.New("scrub: clock regression")
	// ErrBlockNotFound 块不存在（从未创建或副本已降为零被移除）。
	ErrBlockNotFound = errors.New("scrub: block not found")
	// ErrTooFrequent 距上一次已记录巡检严格小于最小巡检间隔。
	ErrTooFrequent = errors.New("scrub: scrub too frequent")
)

// Replica 是某个节点上某块的一个副本。
// Stored 为写入时保存的内容摘要，Actual 为巡检时重新读出计算的摘要。
type Replica struct {
	NodeID  uint64
	Version uint64
	Stored  string
	Actual  string
}

// SelfConsistent 报告副本是否自洽（保存摘要等于实际摘要），否则为位腐。
func (r Replica) SelfConsistent() bool { return r.Stored == r.Actual }

// Outcome 是一次巡检的判定结果，六类结果相互可区分。
type Outcome int

const (
	// OutcomeConsistent 已一致，无需修复。
	OutcomeConsistent Outcome = iota
	// OutcomeRepaired 存在需修复副本且全部修复成功。
	OutcomeRepaired
	// OutcomePartialRepair 部分修复：至少一个需修复副本写失败。
	OutcomePartialRepair
	// OutcomeNoSource 不可修复：没有任何自洽副本，无可用来源。
	OutcomeNoSource
	// OutcomeCommittedDataLost 不可修复：自洽副本最大版本低于已提交版本。
	OutcomeCommittedDataLost
	// OutcomeVersionConflict 不可修复：最大版本下的自洽副本保存摘要不一致。
	OutcomeVersionConflict
)

// Unrepairable 报告该结果是否为三类不可修复情形之一。
func (o Outcome) Unrepairable() bool {
	return o == OutcomeNoSource || o == OutcomeCommittedDataLost || o == OutcomeVersionConflict
}

func (o Outcome) String() string {
	switch o {
	case OutcomeConsistent:
		return "Consistent"
	case OutcomeRepaired:
		return "Repaired"
	case OutcomePartialRepair:
		return "PartialRepair"
	case OutcomeNoSource:
		return "NoSource"
	case OutcomeCommittedDataLost:
		return "CommittedDataLost"
	case OutcomeVersionConflict:
		return "VersionConflict"
	}
	return "Unknown"
}

// ScrubResult 精确描述一次巡检"修了什么、没修什么、为什么"。
type ScrubResult struct {
	Outcome Outcome
	// CommittedVersion 按当前副本数推断出的已提交版本。
	CommittedVersion uint64
	// AuthVersion / AuthDigest 权威版本与摘要（仅可修复路径有效）。
	AuthVersion uint64
	AuthDigest  string
	// Repaired 修复写成功的节点（升序）；Failed 修复写失败的节点（升序）。
	Repaired []uint64
	Failed   []uint64
}

// Alert 是不可修复巡检追加到告警列表的一条记录，按发生次序可查询。
type Alert struct {
	Seq              int
	BlockID          uint64
	Time             int64
	Outcome          Outcome
	CommittedVersion uint64
	// HasSelfConsistent 为 false 时 MaxSelfConsistentVersion 无效。
	HasSelfConsistent        bool
	MaxSelfConsistentVersion uint64
}

// BlockInfo 是块状态的只读快照，供调用方与测试核对。
type BlockInfo struct {
	ID          uint64
	MinInterval int64
	Scrubbed    bool
	LastScrub   int64
	Replicas    []Replica // 按 NodeID 升序
}
