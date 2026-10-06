// Package sigchain 提供提交签名验证链服务：依据可信锚点、密钥有效区间
// 与背书关系，对分支从锚点到顶端的第一父提交链给出可信裁决。
package sigchain

import "errors"

// Time 为逻辑时刻，非负。
type Time int64

// Signature 表示对象上附带的签名。本题不做密码学运算。
type Signature struct {
	KeyID string
	Time  Time
}

// Commit 为提交对象：唯一标识、若干父提交、作者时刻与可选签名。
type Commit struct {
	ID         string
	Parents    []string // Parents[0] 为第一父
	AuthorTime Time
	Sig        *Signature
}

// Tag 为标签对象：指向一个提交，可带签名。
type Tag struct {
	Name     string
	CommitID string
	Sig      *Signature
}

// Endorsement 为背书：由 Endorser 在 Time 签署的「我信任此密钥」声明。
type Endorsement struct {
	Endorser string
	Time     Time
}

// Key 为登记的密钥：生效时刻、可选失效时刻、可选吊销时刻与可选背书。
type Key struct {
	ID          string
	ValidFrom   Time
	ValidUntil  *Time // 失效时刻，开区间端点
	RevokedAt   *Time // 吊销时刻，设置后不可撤销也不可提前
	Endorsement *Endorsement
}

// Policy 为链路策略开关。
type Policy struct {
	MergeExempt           bool // 合并提交豁免：合并提交自身不需签名
	RevocationRetroactive bool // 吊销追溯：吊销时刻早于验证时刻的密钥所签一律降级
}

// 调用层面的错误，按此优先级次序报告，每次调用只报最靠前的一个。
var (
	ErrInvalidParam     = errors.New("参数非法")
	ErrCommitNotFound   = errors.New("提交不存在")
	ErrTagNotFound      = errors.New("标签不存在")
	ErrKeyNotFound      = errors.New("密钥不存在")
	ErrEndorsementCycle = errors.New("背书成环")
	ErrRevokeEarlier    = errors.New("吊销提前")
)

// Reason 为签名判定与链路裁决的原因分类。
type Reason int

const (
	ReasonTrusted           Reason = iota // 签名可信 / 全链可信
	ReasonForged                          // 签名伪造
	ReasonUnknownKey                      // 密钥未知
	ReasonNotYetValid                     // 签署时未生效
	ReasonExpired                         // 签署时已失效
	ReasonRevoked                         // 签署时已吊销
	ReasonBrokenEndorsement               // 背书链断裂
	ReasonTimeInversion                   // 时刻倒置：签署时刻早于作者时刻
	ReasonFromFuture                      // 来自未来：签署时刻晚于验证时刻
	ReasonUnsigned                        // 未签名
	ReasonAnchorNotOnChain                // 锚点不在链上
)

func (r Reason) String() string {
	switch r {
	case ReasonTrusted:
		return "签名可信"
	case ReasonForged:
		return "签名伪造"
	case ReasonUnknownKey:
		return "密钥未知"
	case ReasonNotYetValid:
		return "签署时未生效"
	case ReasonExpired:
		return "签署时已失效"
	case ReasonRevoked:
		return "签署时已吊销"
	case ReasonBrokenEndorsement:
		return "背书链断裂"
	case ReasonTimeInversion:
		return "时刻倒置"
	case ReasonFromFuture:
		return "来自未来"
	case ReasonUnsigned:
		return "未签名"
	case ReasonAnchorNotOnChain:
		return "锚点不在链上"
	}
	return "未知原因"
}

// Verdict 为一次链路裁决的结果。裁决是结果而非错误。
type Verdict struct {
	Trusted  bool   // 全链可信
	CommitID string // 首个不可信提交（Trusted 或锚点不在链上时为空）
	Reason   Reason // 不可信原因；Trusted 时为 ReasonTrusted
	Checked  int    // 实际参与判定的提交数（供日志与性能验证）
}
