// Package audit 提供权限决策审计回放能力：
// 版本化权限规则、追加式审计记录、历史回放、纠正记录与三态合法性查询。
package audit

import (
	"errors"
	"fmt"
	"time"
)

// Decision 是一次访问判定的结果。
type Decision bool

const (
	DecisionAllow Decision = true
	DecisionDeny  Decision = false
)

func (d Decision) String() string {
	if d {
		return "ALLOW"
	}
	return "DENY"
}

// VersionID 是权限规则版本的标识，格式为 rv-<seq>，seq 单调递增，
// 版本之间的先后关系由 seq 唯一确定。
type VersionID string

// RecordID 是审计记录的标识，格式为 ar-<seq>。
type RecordID string

// CorrectionID 是纠正记录的标识，格式为 cr-<seq>。
type CorrectionID string

// Request 描述一次访问请求的内容。
type Request struct {
	Action string            `json:"action"`
	Attrs  map[string]string `json:"attrs,omitempty"`
}

// AuditRecord 是一次实际访问判定的审计记录。记录一旦写入即不可变。
type AuditRecord struct {
	ID          RecordID  `json:"id"`
	Seq         uint64    `json:"seq"`
	Subject     string    `json:"subject"`
	Target      string    `json:"target"`
	Request     Request   `json:"request"`
	DecidedAt   time.Time `json:"decided_at"`
	Result      Decision  `json:"result"`
	VersionID   VersionID `json:"version_id"`
	VersionHash string    `json:"version_hash"` // 判定时该版本规则内容的哈希
	SelfHash    string    `json:"self_hash"`
	PrevChain   string    `json:"prev_chain"`
	ChainHash   string    `json:"chain_hash"`
}

// Correction 是一条纠正记录，作为独立新记录追加，指向被纠正的原始记录。
// 后一条纠正记录可以通过 Supersedes 针对前一条纠正记录的结论再次纠正。
type Correction struct {
	ID          CorrectionID `json:"id"`
	Seq         uint64       `json:"seq"`
	RecordID    RecordID     `json:"record_id"`    // 被纠正的原始审计记录
	Supersedes  CorrectionID `json:"supersedes"`   // 可选：针对的前一条纠正记录
	CorrectedTo Decision     `json:"corrected_to"` // 纠正后的结论
	Reason      string       `json:"reason"`
	RecordedAt  time.Time    `json:"recorded_at"`
	SelfHash    string       `json:"self_hash"`
	PrevChain   string       `json:"prev_chain"`
	ChainHash   string       `json:"chain_hash"`
}

// Legality 是对"某主体在某历史时刻的访问是否合法"的三态回答。
type Legality int

const (
	// LegalityDenyAsRecorded 依据原始审计记录判定为不合法。
	LegalityDenyAsRecorded Legality = iota
	// LegalityAllowAsRecorded 依据原始审计记录判定为合法。
	LegalityAllowAsRecorded
	// LegalityCorrected 存在至少一条未被后续纠正否定的纠正记录改变了结论。
	LegalityCorrected
)

func (l Legality) String() string {
	switch l {
	case LegalityAllowAsRecorded:
		return "ALLOW_AS_RECORDED"
	case LegalityDenyAsRecorded:
		return "DENY_AS_RECORDED"
	case LegalityCorrected:
		return "CORRECTED"
	default:
		return "UNKNOWN"
	}
}

// 错误分类。优先级固定且唯一（数值越小优先级越高）：
//  1. ErrVersionNotFound 被回放记录登记的规则版本标识不存在
//  2. ErrAuditIntegrity 原始审计记录本身已被检测到完整性破坏
//  3. ErrCorrectionTargetMissing 纠正记录试图指向不存在的原始记录
var (
	ErrVersionNotFound         = errors.New("audit: rule version not found")
	ErrAuditIntegrity          = errors.New("audit: audit record integrity violation")
	ErrCorrectionTargetMissing = errors.New("audit: correction target record not found")

	ErrRecordNotFound       = errors.New("audit: audit record not found")
	ErrRuleVersionIntegrity = errors.New("audit: rule version content integrity violation")
	ErrCorrectionNotFound   = errors.New("audit: superseded correction not found")
	ErrCorrectionChain      = errors.New("audit: correction does not extend the chain head")
)

// ErrorPriority 返回错误的固定优先级（数值越小越优先汇报）。
// 非本包分类错误返回最大优先级值。
func ErrorPriority(err error) int {
	switch {
	case errors.Is(err, ErrVersionNotFound):
		return 1
	case errors.Is(err, ErrAuditIntegrity):
		return 2
	case errors.Is(err, ErrCorrectionTargetMissing):
		return 3
	default:
		return 1 << 30
	}
}

// ReplayOutcome 区分回放结果的三种情形。
type ReplayOutcome int

const (
	// ReplayConsistent 回放结果与原审计记录一致。
	ReplayConsistent ReplayOutcome = iota
	// ReplayVersionTampered 该版本规则内容发生了不应发生的改变（完整性破坏）。
	ReplayVersionTampered
	// ReplayMisjudgment 原判定过程本身存在缺陷（历史误判）。
	ReplayMisjudgment
)

func (o ReplayOutcome) String() string {
	switch o {
	case ReplayConsistent:
		return "CONSISTENT"
	case ReplayVersionTampered:
		return "VERSION_TAMPERED"
	case ReplayMisjudgment:
		return "MISJUDGMENT"
	default:
		return "UNKNOWN"
	}
}

// ReplayResult 是一次回放的完整结论。
type ReplayResult struct {
	RecordID    RecordID      `json:"record_id"`
	Outcome     ReplayOutcome `json:"outcome"`
	Original    Decision      `json:"original"`
	Recomputed  Decision      `json:"recomputed"`
	VersionID   VersionID     `json:"version_id"`
	VersionHash string        `json:"version_hash"`
}

func (r ReplayResult) String() string {
	return fmt.Sprintf("replay(%s): %s original=%s recomputed=%s version=%s",
		r.RecordID, r.Outcome, r.Original, r.Recomputed, r.VersionID)
}
