package ontology

import "crypto/sha256"

// AuditEntry 是索引条目与其来源对象写入的逐条对应关系。
// SourceSeq 指向使该条目取值生效的那次对象写入（单调序号，而非墙钟时间）。
type AuditEntry struct {
	Object    ObjectID `json:"object"`
	Value     Value    `json:"value"`
	SourceSeq int64    `json:"source_seq"`
	// Baseline=true 表示该条目由基线范围扫描产生，
	// 否则由增量范围（BaselineSeq < seq <= CompleteSeq）的某次写入产生。
	Baseline bool `json:"baseline"`
}

// AuditRecord 是一次重建产生的、可独立复核的完整审计记录。
// 它不依赖重建过程的执行日志：复核者仅凭本记录、对象当前状态即可判定。
type AuditRecord struct {
	RebuildID   string       `json:"rebuild_id"`
	Type        TypeID       `json:"type"`
	Attr        AttrName     `json:"attr"`
	BaselineSeq int64        `json:"baseline_seq"` // 基准点：seq <= 它构成基线范围
	CompleteSeq int64        `json:"complete_seq"` // 重建完成时点：seq <= 它均已纳入
	Entries     []AuditEntry `json:"entries"`
	Digest      string       `json:"digest"` // 对除 Digest 外全部字段的 SHA-256
}

// ComputeDigest 计算审计记录内容指纹（确定性编码由 marshalForDigest 保证）。
func (r *AuditRecord) ComputeDigest() string {
	sum := sha256.Sum256(marshalForDigest(r))
	return encodeHex(sum[:])
}

// ValidDigest 校验指纹，防止审计记录被篡改。
func (r *AuditRecord) ValidDigest() bool { return r.ComputeDigest() == r.Digest }

func encodeHex(b []byte) string {
	const hexd = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, c := range b {
		out[i*2] = hexd[c>>4]
		out[i*2+1] = hexd[c&0x0f]
	}
	return string(out)
}
