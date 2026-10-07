package ontology

// BatchMode 声明批量导入的整体语义。
type BatchMode int

const (
	AllOrNothing BatchMode = iota
	BestEffort
)

// BatchItem 是批量导入中的一条输入。
type BatchItem struct {
	LinkType string
	Pair
}

// ItemResult 是批量导入中一条输入的逐条结论。
type ItemResult struct {
	Accepted bool
	Kind     ErrKind
}

// BatchResult 保持与输入列表相同顺序的完整结论清单。
type BatchResult struct {
	Results []ItemResult
	// Aborted 仅在 AllOrNothing 语义下且批次中途失败时为 true。
	Aborted bool
	// AbortIndex 是第一个导致整批中止的条目下标。
	AbortIndex int
}

// AcceptedCount 返回被接受的条目数。
func (r *BatchResult) AcceptedCount() int {
	n := 0
	for _, x := range r.Results {
		if x.Accepted {
			n++
		}
	}
	return n
}
