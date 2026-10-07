package ontology

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
)

// EntryProof 是一条索引条目与其来源对象写入的可复核对应关系。
//
// 每条记录都能独立回答四个问题：
//  1. 哪个值（Value）映射到哪个对象（ObjectID）；
//  2. 该值来自哪一次写入（SourceLSN）、对象当时哪个版本（ObjectVer）；
//  3. 该写入属于基线范围还是增量范围（SourceRange）；
//  4. 审计在哪个时点封存（由 AuditRecord.CompletionLSN 给出）。
type EntryProof struct {
	IndexKey    string
	ObjectID    string
	Property    string
	Value       PropertyValue
	ObjectVer   int64
	SourceLSN   int64
	SourceRange string
}

// SourceRange 取值。
const (
	RangeBaseline = "baseline"
	RangeDelta    = "delta"
)

// AuditRecord 是一次索引重建产生的、可独立复核的审计记录。
//
// 边界定义（LSN 均为全局单调标识，非墙钟时间）：
//   - 发起重建时在串行点上记录围栏 fence = Store.NextLSN()：
//     LSN <= fence 的写入全部属于基线，LSN > fence 的写入全部属于增量；
//   - BaselineEndLSN 记录实际基线扫描所覆盖的末尾 LSN（= fence）；
//   - CompletionLSN 记录增量全部应用完毕、索引封存时点的末尾 LSN；
//   - 封存后到达的写入由管理器在提交点逐条以 RangeDelta 追加进
//     “存活审计（live audit）”，因此审计始终对应当前索引，
//     且每条追加与其写入在同一全局串行点发生。
type AuditRecord struct {
	IndexName        string
	ObjectType       string
	Property         string
	BaselineStartLSN int64
	BaselineEndLSN   int64
	CompletionLSN    int64
	Entries          []EntryProof
	Digest           string
}

// CanonicalEntries 返回按 (IndexKey, ObjectID) 排序后的条目副本，
// 保证摘要与展示不依赖构建时的遍历顺序。
func (a *AuditRecord) CanonicalEntries() []EntryProof {
	out := make([]EntryProof, len(a.Entries))
	copy(out, a.Entries)
	sort.Slice(out, func(i, j int) bool {
		if out[i].IndexKey != out[j].IndexKey {
			return out[i].IndexKey < out[j].IndexKey
		}
		return out[i].ObjectID < out[j].ObjectID
	})
	return out
}

// ComputeDigest 计算审计记录的防篡改摘要：范围元数据 + 每条条目的
// (值, 对象, 属性, 版本, 来源LSN, 范围)。摘要本身不被信任——
// VerifyDigest 会从内容重新计算，复核器还会独立对照对象当前状态，
// 因此摘要只用于检测审计落盘/传输过程中的意外或人为篡改。
func (a *AuditRecord) ComputeDigest() string {
	h := sha256.New()
	fmt.Fprintf(h, "index=%s|type=%s|prop=%s|bstart=%d|bend=%d|done=%d|n=%d\n",
		a.IndexName, a.ObjectType, a.Property,
		a.BaselineStartLSN, a.BaselineEndLSN, a.CompletionLSN, len(a.Entries))
	for _, e := range a.CanonicalEntries() {
		fmt.Fprintf(h, "%s\x00%s\x00%s\x00%s\x00v=%d\x00lsn=%d\x00%s\n",
			e.IndexKey, e.ObjectID, e.Property, e.Value,
			e.ObjectVer, e.SourceLSN, e.SourceRange)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Seal 计算并写入摘要，返回记录自身以便链式调用。
func (a *AuditRecord) Seal() *AuditRecord {
	a.Digest = a.ComputeDigest()
	return a
}

// VerifyDigest 依据当前内容重新计算摘要并与封存值比对。
func (a *AuditRecord) VerifyDigest() bool {
	if a == nil || a.Digest == "" {
		return false
	}
	return a.Digest == a.ComputeDigest()
}
