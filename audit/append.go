package audit

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
)

// SHA256Hash 是基于 SHA-256 的确定性摘要函数，可直接用作 HashFunc。
func SHA256Hash(seq uint64, payload []byte, prevDigest string) string {
	h := sha256.New()
	var seqBuf [8]byte
	binary.BigEndian.PutUint64(seqBuf[:], seq)
	h.Write(seqBuf[:])
	h.Write([]byte{0})
	h.Write(payload)
	h.Write([]byte{0})
	h.Write([]byte(prevDigest))
	return hex.EncodeToString(h.Sum(nil))
}

// Append 原子追加一条记录；序号为 A 的倍数时在同一原子操作内发布锚点。
// 负载为空或超长时按此顺序只报第一个原因，被拒绝的追加不占序号、不改链与锚点。
func (v *Verifier) Append(payload []byte) (Record, error) {
	if len(payload) == 0 {
		return Record{}, ErrEmptyPayload
	}
	if len(payload) > v.maxPayload {
		return Record{}, fmt.Errorf("%w: %d > %d", ErrPayloadTooLarge, len(payload), v.maxPayload)
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	seq := uint64(len(v.records)) + 1
	prev := v.genesis
	if len(v.records) > 0 {
		prev = v.records[len(v.records)-1].Digest
	}
	rec := Record{
		Seq:     seq,
		Payload: append([]byte(nil), payload...),
		Prev:    prev,
	}
	rec.Digest = v.hash(rec.Seq, rec.Payload, rec.Prev)
	v.records = append(v.records, rec)

	if seq%v.interval == 0 {
		if err := v.anchors.Publish(Anchor{Seq: seq, Digest: rec.Digest}); err != nil {
			return Record{}, fmt.Errorf("audit: 发布锚点失败: %w", err)
		}
	}
	return rec, nil
}

// IsEmptyPayload 报告错误原因是否为负载为空。
func IsEmptyPayload(err error) bool { return errors.Is(err, ErrEmptyPayload) }

// IsPayloadTooLarge 报告错误原因是否为负载超长。
func IsPayloadTooLarge(err error) bool { return errors.Is(err, ErrPayloadTooLarge) }

// Len 返回当前链长。
func (v *Verifier) Len() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return len(v.records)
}

// Snapshot 返回当前链的一致前缀快照。
func (v *Verifier) Snapshot() []Record {
	v.mu.Lock()
	defer v.mu.Unlock()
	return append([]Record(nil), v.records...)
}
