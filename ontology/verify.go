package ontology

// Mismatch 是条目级不一致的精确定位。
type Mismatch struct {
	Object ObjectID `json:"object"`
	Value  Value    `json:"value,omitempty"` // 索引中（错误）的值
	Kind   string   `json:"kind"`            // wrong_value / stale_entry / missing_entry / extra_entry / audit_gap / audit_overlap / digest_bad
	Detail string   `json:"detail"`
}

// VerificationReport 是复核结论：条目级清单而非笼统结论。
type VerificationReport struct {
	Type         TypeID     `json:"type"`
	Attr         AttrName   `json:"attr"`
	Consistent   bool       `json:"consistent"`
	SnapshotSeq  int64      `json:"snapshot_seq"` // 复核线性化点的全局序号
	AuditSeqAt   int64      `json:"audit_complete_seq"`
	CheckedNow   int        `json:"checked_entries_now"`
	CellReads    int        `json:"cell_reads"`    // 读取的对象当前属性单元数
	HistoryReads int        `json:"history_reads"` // 读取的历史写入记录数（证明 O(1)）
	Mismatches   []Mismatch `json:"mismatches"`
}
