// Package audit 提供带外部锚点的哈希链审计日志校验器。
package audit

import (
	"errors"
	"fmt"
	"sync"
)

// GenesisDigest 是首条记录前摘要使用的固定创世值。
const GenesisDigest = "GENESIS"

// HashFunc 由外部提供，必须是确定性的：
// 相同的（序号，负载，前摘要）必须算出相同的摘要。
type HashFunc func(seq uint64, payload []byte, prevDigest string) string

// Record 是链上的一条审计记录。
type Record struct {
	Seq     uint64 // 序号，从 1 连续递增
	Payload []byte // 负载
	Prev    string // 前摘要（首条为 GenesisDigest）
	Digest  string // 自报摘要：HashFunc(Seq, Payload, Prev)
}

// Anchor 是发布到外部锚点存储的一条锚点。
type Anchor struct {
	Seq    uint64
	Digest string
}

// AnchorStore 是外部锚点存储的抽象：只增不改。
type AnchorStore interface {
	// Publish 原子发布一条锚点，序号必须严格大于已有最大锚点序号。
	Publish(a Anchor) error
	// Snapshot 返回按序号升序的全部锚点快照。
	Snapshot() []Anchor
}

// 追加被拒绝的可区分原因。
var (
	ErrEmptyPayload    = errors.New("audit: 负载为空")
	ErrPayloadTooLarge = errors.New("audit: 负载超过长度上限")
)

// Verifier 是哈希链审计日志校验器，追加与校验均可并发调用。
type Verifier struct {
	mu         sync.Mutex
	interval   uint64 // 锚点间隔 A
	maxPayload int    // 负载长度上限（字节）
	hash       HashFunc
	genesis    string
	records    []Record
	anchors    AnchorStore
}

// NewVerifier 创建校验器。interval（A）非正时拒绝创建。
func NewVerifier(interval int, maxPayload int, hash HashFunc, anchors AnchorStore) (*Verifier, error) {
	if interval <= 0 {
		return nil, fmt.Errorf("audit: 锚点间隔 A 必须为正，得到 %d", interval)
	}
	if hash == nil {
		return nil, errors.New("audit: 哈希函数不能为空")
	}
	if anchors == nil {
		return nil, errors.New("audit: 锚点存储不能为空")
	}
	return &Verifier{
		interval:   uint64(interval),
		maxPayload: maxPayload,
		hash:       hash,
		genesis:    GenesisDigest,
		anchors:    anchors,
	}, nil
}
