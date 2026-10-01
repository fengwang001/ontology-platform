package ontology

// mergeStatus 是两步合并的生命周期状态。
type mergeStatus int

const (
	mergePending mergeStatus = iota
	mergeCommitted
	mergeAborted
)

// mergeState 是 BeginMerge 冻结的合并集合。
type mergeState struct {
	id     int // 句柄号
	status mergeStatus
	inputs []int       // 按 ids 次序的输入段编号
	docs   []frozenDoc // BeginMerge 时刻存活文档的冻结序列
}

// snapshotDeletions 读取各冻结文档在当前源段上的删除标记。
// 删除以被冻结的那个文档本身为准，与之后登记的同键新文档无关。
func (ms *mergeState) snapshotDeletions() []bool {
	deleted := make([]bool, len(ms.docs))
	for i, doc := range ms.docs {
		deleted[i] = doc.seg.deleted[doc.docID]
	}
	return deleted
}
