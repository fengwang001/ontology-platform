// Package auditlog 提供带外部锚点的哈希链审计日志。
//
// 每条记录包含序号、负载、前摘要与自报摘要，摘要由外部提供的确定性
// 函数计算。追加记录时，序号为锚点间隔 A 的倍数的记录会在同一原子
// 操作内把（序号，摘要）发布到外部锚点存储。校验可发现内容篡改、
// 删除、重排与尾部截断，并报告首个问题的位置与类别。
package auditlog

import (
	"crypto/sha256"
	"errors"
	"sync"
)

// Digest 是记录摘要的定长表示。
type Digest = [sha256.Size]byte

// GenesisDigest 是首条记录前摘要的固定创世值。
var GenesisDigest = sha256.Sum256([]byte("ontology/auditlog/genesis/v1"))

// DigestFunc 由外部提供，对（序号，负载，前摘要）计算确定性摘要。
type DigestFunc func(seq uint64, payload []byte, prev Digest) Digest

// DefaultDigestFunc 是基于 SHA-256 的默认摘要函数。
func DefaultDigestFunc(seq uint64, payload []byte, prev Digest) Digest {
	h := sha256.New()
	var seqBuf [8]byte
	for i := 0; i < 8; i++ {
		seqBuf[i] = byte(seq >> (56 - 8*i))
	}
	h.Write(seqBuf[:])
	h.Write(prev[:])
	h.Write(payload)
	var d Digest
	copy(d[:], h.Sum(nil))
	return d
}

// Record 是链上的一条审计记录。
type Record struct {
	Seq     uint64 // 序号，从 1 连续
	Payload []byte // 负载
	Prev    Digest // 前摘要（上一条的自报摘要，首条为创世值）
	Digest  Digest // 自报摘要
}

// Anchor 是发布到外部锚点存储的（序号，摘要）检查点。
type Anchor struct {
	Seq    uint64
	Digest Digest
}

// 追加被拒绝的可区分原因。
var (
	ErrNonPositiveInterval = errors.New("auditlog: 锚点间隔必须为正整数")
	ErrEmptyPayload        = errors.New("auditlog: 负载为空")
	ErrPayloadTooLarge     = errors.New("auditlog: 负载超过长度上限")
)

// Log 是并发安全的哈希链审计日志。
type Log struct {
	mu         sync.RWMutex
	interval   uint64
	maxPayload int
	digestFn   DigestFunc
	records    []Record
	anchors    AnchorStore
}

// NewLog 创建审计日志。interval 为锚点间隔 A，非正时拒绝创建。
func NewLog(interval uint64, maxPayload int, fn DigestFunc, anchors AnchorStore) (*Log, error) {
	if interval == 0 {
		return nil, ErrNonPositiveInterval
	}
	if fn == nil {
		fn = DefaultDigestFunc
	}
	if anchors == nil {
		anchors = NewMemoryAnchorStore()
	}
	return &Log{
		interval:   interval,
		maxPayload: maxPayload,
		digestFn:   fn,
		anchors:    anchors,
	}, nil
}

// Append 原子地追加一条记录；序号为 A 的倍数时在同一原子操作内
// 发布锚点。被拒绝的追加不占用序号、不改变链与锚点。
//
// 拒绝原因按顺序判定且只报第一个：负载为空、负载超过长度上限。
func (l *Log) Append(payload []byte) (Record, error) {
	if len(payload) == 0 {
		return Record{}, ErrEmptyPayload
	}
	if l.maxPayload > 0 && len(payload) > l.maxPayload {
		return Record{}, ErrPayloadTooLarge
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	seq := uint64(len(l.records)) + 1
	prev := GenesisDigest
	if n := len(l.records); n > 0 {
		prev = l.records[n-1].Digest
	}
	rec := Record{
		Seq:     seq,
		Payload: append([]byte(nil), payload...),
		Prev:    prev,
		Digest:  l.digestFn(seq, payload, prev),
	}
	// 追加与锚点发布在同一临界区内完成，构成原子操作：
	// 任一时刻链长以内每个 A 的倍数序号都已有锚点。
	if seq%l.interval == 0 {
		if err := l.anchors.Publish(seq, rec.Digest); err != nil {
			return Record{}, err
		}
	}
	l.records = append(l.records, rec)
	return rec, nil
}

// Verify 校验当前链与锚点，返回首个问题的位置与类别。
// 校验在快照上进行，并发追加下看到的链恒为某个自洽前缀。
func (l *Log) Verify() Result {
	l.mu.RLock()
	records := make([]Record, len(l.records))
	copy(records, l.records)
	l.mu.RUnlock()
	return VerifyChain(records, l.anchors.List(), l.digestFn)
}

// Snapshot 返回当前链的一致快照（某个自洽前缀）。
func (l *Log) Snapshot() []Record {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]Record, len(l.records))
	copy(out, l.records)
	return out
}

// Anchors 返回当前全部锚点（按序号升序）。
func (l *Log) Anchors() []Anchor {
	return l.anchors.List()
}

// Len 返回当前链长。
func (l *Log) Len() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.records)
}
